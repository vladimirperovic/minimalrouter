package accounting

import (
	"context"
	"log"
	"sort"
	"sync"
	"time"
)

// DNSLookup is one client's lookups of one name as reported by router-applyd.
// The type mirrors apply.DNSLookupCount so this package does not depend on the
// IPC package.
type DNSLookup struct {
	Client   string
	Name     string
	Count    uint32
	LastSeen int64
}

// DNSDrain is one page drained from router-applyd.
type DNSDrain struct {
	Enabled bool
	Lookups []DNSLookup
	Dropped uint64
	More    bool
}

// DNSSource converges the privileged helper on the setting and drains the
// lookups it collected.
type DNSSource interface {
	Drain(ctx context.Context, enabled bool) (DNSDrain, error)
}

// DNSSettings is the operator's DNS activity setting.
type DNSSettings struct {
	Enabled       bool `json:"enabled"`
	RetentionDays int  `json:"retention_days"`
}

// DNSRecentLookup is one entry of the in-memory recent list.
type DNSRecentLookup struct {
	At       time.Time `json:"at"`
	Address  string    `json:"address"`
	Hostname string    `json:"hostname,omitempty"`
	Name     string    `json:"name"`
	Site     string    `json:"site"`
	Category string    `json:"category,omitempty"`
	Count    uint32    `json:"count"`
}

const (
	dnsCollectInterval = time.Minute
	// Aggregates are written to SQLite every five minutes, in one
	// transaction, like the traffic accounting. A hard power loss can lose at
	// most that interval.
	dnsFlushEvery = 5
	// dnsMaxPendingPairs bounds the in-memory aggregate between flushes.
	dnsMaxPendingPairs = 20000
	dnsMaxDrainPages   = 64
	// dnsRecentCapacity bounds the memory-only recent list.
	dnsRecentCapacity = 2000
	dnsPruneInterval  = time.Hour
)

// DNSCollector folds drained lookups into daily per-device site counts. The
// recent list of full hostnames lives only in memory.
type DNSCollector struct {
	store      *Store
	source     DNSSource
	settings   func() DNSSettings
	allowWrite func() bool

	wake       chan struct{}
	mu         sync.Mutex
	writeMu    sync.Mutex
	generation uint64
	onLookups  func([]DNSLookup)
	onClear    func() error
	status     DNSCollectionStatus
	writeError string
	dropped    uint64
	observed   map[int64]bool
	daily      map[DNSDailyKey]DNSDailyCount
	hourly     map[DNSHourlyKey]uint64
	unitemized map[int64]uint64
	recent     []DNSRecentLookup
	recentNext int
	lastPrune  time.Time
}

type DNSCollectionStatus struct {
	State          string     `json:"state"`
	LastSuccess    *time.Time `json:"last_success"`
	Error          string     `json:"error,omitempty"`
	DroppedLookups uint64     `json:"dropped_lookups"`
}

// SetRiskObserver is configured once, before Run. The observer only receives
// validated lookups, and must queue work without delaying collection.
func (c *DNSCollector) SetRiskObserver(lookups func([]DNSLookup), clear func() error) {
	c.onLookups, c.onClear = lookups, clear
}

func (c *DNSCollector) Status(now time.Time) DNSCollectionStatus {
	if c == nil {
		return DNSCollectionStatus{State: "unavailable"}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.status
	if out.State == "" {
		out.State = "starting"
	}
	if out.State == "active" && (out.LastSuccess == nil || now.Sub(*out.LastSuccess) > 3*time.Minute) {
		out.State = "stale"
	}
	if c.writeError != "" {
		out.Error = c.writeError
		if out.State == "active" {
			out.State = "degraded"
		}
	}
	out.DroppedLookups = c.dropped
	return out
}

func (c *DNSCollector) collectionError(message string) {
	c.mu.Lock()
	c.status.State = "unavailable"
	c.status.Error = message
	c.mu.Unlock()
}

func NewDNSCollector(store *Store, source DNSSource, settings func() DNSSettings, allowWrite func() bool) *DNSCollector {
	if allowWrite == nil {
		allowWrite = func() bool { return true }
	}
	return &DNSCollector{store: store, source: source, settings: settings, allowWrite: allowWrite, wake: make(chan struct{}, 1)}
}

// Wake runs a collection round now, so a changed setting takes effect within
// seconds instead of at the next tick.
func (c *DNSCollector) Wake() {
	if c == nil {
		return
	}
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

func (c *DNSCollector) Run(ctx context.Context) {
	if c == nil || c.store == nil || c.source == nil || c.settings == nil {
		return
	}
	ticker := time.NewTicker(dnsCollectInterval)
	defer ticker.Stop()
	rounds := 0
	for {
		settings := c.settings()
		if settings.Enabled {
			c.collect(ctx, time.Now())
			rounds++
			if rounds%dnsFlushEvery == 0 || c.pendingPairs() >= dnsMaxPendingPairs {
				c.flush(time.Now(), settings)
			}
		} else {
			// Tell the helper every round, so query logging is switched off
			// even if the previous attempt or routerd itself failed midway.
			drainCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			if _, err := c.source.Drain(drainCtx, false); err != nil && ctx.Err() == nil {
				log.Printf("[DNS] could not disable query logging: %v", err)
			}
			cancel()
			c.disable()
			rounds = 0
		}
		select {
		case <-ctx.Done():
			if settings := c.settings(); settings.Enabled {
				c.flush(time.Now(), settings)
			}
			return
		case <-ticker.C:
		case <-c.wake:
		}
	}
}

func (c *DNSCollector) collect(ctx context.Context, now time.Time) {
	c.mu.Lock()
	generation := c.generation
	c.mu.Unlock()
	for page := 0; page < dnsMaxDrainPages; page++ {
		// Enabling can restart dnsmasq, which takes a few seconds.
		drainCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		drain, err := c.source.Drain(drainCtx, true)
		cancel()
		if err != nil {
			c.collectionError("DNS collection failed; activity may be missing")
			if ctx.Err() == nil {
				log.Printf("[DNS] could not collect DNS activity: %v", err)
			}
			return
		}
		if !drain.Enabled {
			c.collectionError("DNS query logging is not active")
			return
		}
		c.mu.Lock()
		if generation != c.generation {
			c.mu.Unlock()
			return
		}
		c.addLocked(now, drain)
		if c.onLookups != nil {
			c.onLookups(drain.Lookups)
		}
		if !drain.More {
			at := now.UTC()
			c.status = DNSCollectionStatus{State: "active", LastSuccess: &at}
			if c.observed == nil {
				c.observed = map[int64]bool{}
			}
			c.observed[now.UTC().Truncate(time.Hour).Unix()] = true
			c.mu.Unlock()
			return
		}
		c.mu.Unlock()
	}
	c.collectionError("DNS collection backlog exceeded the round limit")
}

func (c *DNSCollector) add(now time.Time, drain DNSDrain) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.addLocked(now, drain)
}

func (c *DNSCollector) addLocked(now time.Time, drain DNSDrain) {
	if c.daily == nil {
		c.daily = map[DNSDailyKey]DNSDailyCount{}
		c.hourly = map[DNSHourlyKey]uint64{}
		c.unitemized = map[int64]uint64{}
	}
	if drain.Dropped > 0 {
		c.unitemized[utcDay(now)] += drain.Dropped
		c.dropped += drain.Dropped
	}
	for _, lookup := range drain.Lookups {
		if lookup.Count == 0 || lookup.Client == "" || lookup.Name == "" {
			continue
		}
		seen := lookup.LastSeen
		if seen <= 0 || seen > now.Unix()+300 {
			seen = now.Unix()
		}
		at := time.Unix(seen, 0).UTC()
		day := utcDay(at)
		site := SiteOf(lookup.Name)
		c.hourly[DNSHourlyKey{Hour: at.Truncate(time.Hour).Unix(), Address: lookup.Client}] += uint64(lookup.Count)
		key := DNSDailyKey{Day: day, Address: lookup.Client, Site: site}
		count, exists := c.daily[key]
		if !exists && len(c.daily) >= dnsMaxPendingPairs {
			c.unitemized[day] += uint64(lookup.Count)
		} else {
			if !exists || seen < count.FirstSeen {
				count.FirstSeen = seen
			}
			if seen > count.LastSeen {
				count.LastSeen = seen
			}
			count.Lookups += uint64(lookup.Count)
			c.daily[key] = count
		}
		c.remember(DNSRecentLookup{At: at, Address: lookup.Client, Name: lookup.Name, Site: site, Count: lookup.Count})
	}
}

func (c *DNSCollector) remember(entry DNSRecentLookup) {
	if len(c.recent) < dnsRecentCapacity {
		c.recent = append(c.recent, entry)
		return
	}
	c.recent[c.recentNext] = entry
	c.recentNext = (c.recentNext + 1) % dnsRecentCapacity
}

func (c *DNSCollector) pendingPairs() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.daily)
}

func (c *DNSCollector) flush(now time.Time, settings DNSSettings) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.Lock()
	batch := DNSFlush{Daily: c.daily, Hourly: c.hourly, Unitemized: c.unitemized, ObservedHours: c.observed}
	c.daily, c.hourly, c.unitemized = nil, nil, nil
	c.observed = nil
	prune := now.Sub(c.lastPrune) >= dnsPruneInterval
	if prune {
		c.lastPrune = now
	}
	c.mu.Unlock()

	// Statistics are nonessential: under critical disk pressure the batch is
	// discarded rather than competing with configuration writes.
	if !c.allowWrite() {
		c.mu.Lock()
		c.writeError = "History writes paused by storage pressure; activity may be missing"
		c.mu.Unlock()
		if !batch.empty() {
			log.Printf("[DNS] skipped writing DNS activity under storage pressure")
		}
		return
	}
	var err error
	if !batch.empty() {
		err = c.store.RecordDNS(now, batch)
		c.mu.Lock()
		if err == nil {
			c.writeError = ""
		} else {
			c.writeError = "DNS history could not be written; activity may be missing"
		}
		c.mu.Unlock()
	}
	if err != nil {
		log.Printf("[DNS] could not record DNS activity: %v", err)
	}
	if prune {
		if err := c.store.PruneDNS(now, settings.RetentionDays); err != nil {
			log.Printf("[DNS] could not prune DNS activity: %v", err)
		}
	}
}

// disable drops everything held in memory and deletes stored history. It runs
// every round while the feature is off, so a disable followed by a restart
// still deletes and a failed delete is retried.
func (c *DNSCollector) disable() {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.Lock()
	c.generation++
	c.daily, c.hourly, c.unitemized = nil, nil, nil
	c.observed = nil
	c.status = DNSCollectionStatus{State: "disabled"}
	c.writeError, c.dropped = "", 0
	c.recent, c.recentNext = nil, 0
	c.mu.Unlock()
	if c.onClear != nil {
		if err := c.onClear(); err != nil {
			log.Printf("[DNS] could not clear risk alerts: %v", err)
		}
	}
	has, err := c.store.HasDNSHistory()
	if err != nil {
		log.Printf("[DNS] could not read DNS activity state: %v", err)
		return
	}
	if has {
		if err := c.store.ClearDNS(); err != nil {
			log.Printf("[DNS] could not delete DNS activity after disable: %v", err)
		}
	}
}

// Clear deletes stored and in-memory DNS activity at the operator's request.
func (c *DNSCollector) Clear() error {
	if c == nil {
		return nil
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.Lock()
	c.generation++
	c.daily, c.hourly, c.unitemized = nil, nil, nil
	c.observed = nil
	c.recent, c.recentNext = nil, 0
	c.writeError, c.dropped = "", 0
	c.mu.Unlock()
	if c.onClear != nil {
		if err := c.onClear(); err != nil {
			return err
		}
	}
	return c.store.ClearDNS()
}

// Recent returns the newest in-memory lookups, optionally for one device.
func (c *DNSCollector) Recent(device string, limit int) []DNSRecentLookup {
	out := []DNSRecentLookup{}
	if c == nil {
		return out
	}
	c.mu.Lock()
	for _, entry := range c.recent {
		if device == "" || entry.Address == device {
			out = append(out, entry)
		}
	}
	c.mu.Unlock()
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

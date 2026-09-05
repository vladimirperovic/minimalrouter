package accounting

import (
	"context"
	"log"
	"os/exec"
	"time"
)

// Reader returns the raw JSON of one accounting set. It is an interface so the
// collector can be tested without a kernel, and so the only production
// implementation is the narrow doas call below.
type Reader interface {
	ReadSet(ctx context.Context, name string) (string, error)
}

// CommandReader executes exactly the two argument vectors the installer grants
// routerd through doas. Any other set name is refused here as well, so a future
// caller mistake cannot widen what the helper policy allows.
type CommandReader struct{}

const commandTimeout = 5 * time.Second

func (CommandReader) ReadSet(ctx context.Context, name string) (string, error) {
	switch name {
	case AccountingSetRXName, AccountingSetTXName:
	default:
		return "", errUnsupportedSet
	}
	runCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "doas", "/usr/sbin/nft", "-j", "list", "set", "inet", "minimalrouter", name)
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(output), nil
}

// Names are duplicated here rather than imported from internal/services to keep
// this package free of a dependency on the generator. The nftables security
// test asserts the two stay in sync.
const (
	AccountingSetRXName = "acct_rx"
	AccountingSetTXName = "acct_tx"
)

type accountingError string

func (e accountingError) Error() string { return string(e) }

const errUnsupportedSet = accountingError("unsupported accounting set")

// Settings is the canonical accounting state one collection round works from.
type Settings struct {
	Enabled         bool
	RetentionMonths int
	// Generation identifies the current kernel counter set. It is the canonical
	// configuration revision: every apply recreates the nftables table, so a
	// changed revision means every counter restarted at zero.
	Generation uint64
}

// Collector periodically folds kernel counters into the monthly buckets.
type Collector struct {
	store  *Store
	reader Reader
	// settings reports the current canonical configuration. It is a function
	// because accounting can be switched on and off at runtime and the
	// collector must not hold a stale copy of the configuration.
	settings func() Settings
	interval time.Duration
}

func NewCollector(store *Store, reader Reader, settings func() Settings) *Collector {
	return &Collector{
		store:    store,
		reader:   reader,
		settings: settings,
		// Accounting is monthly aggregate telemetry, not a real-time safety
		// signal. Five minutes limits the always-on appliance to two short nft
		// reads per five minutes instead of two per minute while still staying
		// far below the kernel set timeout. A hard power loss can lose at most
		// the latest interval, which is an acceptable trade-off for optional
		// accounting on a minimal router.
		interval: 5 * time.Minute,
	}
}

func (c *Collector) Run(ctx context.Context) {
	if c == nil || c.store == nil || c.reader == nil || c.settings == nil {
		return
	}
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	c.collectOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.collectOnce(ctx)
		}
	}
}

func (c *Collector) collectOnce(ctx context.Context) {
	settings := c.settings()
	if !settings.Enabled {
		// Disabling accounting removes the per-device history rather than
		// freezing it on disk: the operator asked the router to stop measuring.
		// The "already cleared" answer is durable, so a disable followed by a
		// restart still deletes, and a failed delete is retried next tick.
		cleared, err := c.store.HistoryCleared()
		if err != nil {
			log.Printf("[ACCOUNTING] could not read disable state: %v", err)
			return
		}
		if cleared {
			return
		}
		if err := c.store.ClearHistory(); err != nil {
			log.Printf("[ACCOUNTING] could not clear history after disable: %v", err)
		}
		return
	}
	if err := c.store.MarkHistoryLive(); err != nil {
		log.Printf("[ACCOUNTING] could not record active accounting state: %v", err)
		return
	}

	rx, err := c.readCounters(ctx, AccountingSetRXName)
	if err != nil {
		return
	}
	tx, err := c.readCounters(ctx, AccountingSetTXName)
	if err != nil {
		return
	}
	if len(rx) == 0 && len(tx) == 0 {
		return
	}
	if err := c.store.Record(time.Now(), rx, tx, settings.RetentionMonths, settings.Generation); err != nil {
		log.Printf("[ACCOUNTING] could not record counters: %v", err)
	}
}

// readCounters treats a missing set as "nothing to record". The table is
// recreated on every apply, so between enabling accounting and the next apply
// the sets legitimately do not exist yet.
func (c *Collector) readCounters(ctx context.Context, name string) ([]Counter, error) {
	raw, err := c.reader.ReadSet(ctx, name)
	if err != nil {
		return nil, nil
	}
	counters, parseErr := ParseCounters(raw)
	if parseErr != nil {
		log.Printf("[ACCOUNTING] could not parse %s: %v", name, parseErr)
		return nil, nil
	}
	return counters, nil
}

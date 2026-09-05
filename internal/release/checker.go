package release

import (
	"context"
	"errors"
	"math/rand/v2"
	"sync"
	"time"
)

const (
	// DefaultInitialDelay keeps discovery off the boot path: the appliance
	// finishes reconciling and serving before it talks to GitHub.
	DefaultInitialDelay = 3 * time.Minute
	// DefaultInterval is deliberately long. A router checks for a new release
	// as a convenience, not as a safety signal.
	DefaultInterval = 6 * time.Hour
	// DefaultJitter spreads appliances so a release does not produce a
	// synchronized burst from every installation on the same schedule.
	DefaultJitter = 30 * time.Minute
	// DefaultCooldown bounds operator-triggered checks.
	DefaultCooldown = 60 * time.Second
	// DefaultStaleAfter is when a cached answer stops being presented as the
	// current state of the world.
	DefaultStaleAfter = 24 * time.Hour

	maxBackoff = 4 * time.Hour
)

// Snapshot is what the dashboard reads. It always answers immediately from
// cache: an appliance must not call GitHub while rendering a page.
type Snapshot struct {
	Channel         Channel
	CheckedAt       time.Time
	LastSuccessAt   time.Time
	Newest          *Release
	Candidate       *Release
	Error           string
	RateLimited     bool
	NextEarliest    time.Time
	NeverSucceeded  bool
	StaleAfter      time.Duration
	CooldownExpires time.Time
}

// Stale reports whether the newest successful answer is too old to present as
// current. A stale snapshot must never be shown as "up to date": not knowing
// and knowing there is nothing new are different states.
func (s Snapshot) Stale(now time.Time) bool {
	if s.LastSuccessAt.IsZero() {
		return true
	}
	return now.Sub(s.LastSuccessAt) > s.StaleAfter
}

// Checker keeps one cached view of published releases for the whole process,
// so any number of dashboard tabs share a single upstream request budget.
type Checker struct {
	catalog     Catalog
	arch        string
	channelFunc func() Channel
	now         func() time.Time

	InitialDelay time.Duration
	Interval     time.Duration
	Jitter       time.Duration
	Cooldown     time.Duration
	StaleAfter   time.Duration

	mu           sync.Mutex
	inFlight     bool
	snapshot     Snapshot
	etag         string
	lastAttempt  time.Time
	backoffUntil time.Time
	failures     int
}

// NewChecker builds a checker for one architecture and a channel that the
// operator can change at runtime.
func NewChecker(catalog Catalog, arch string, channel func() Channel) *Checker {
	return &Checker{
		catalog:      catalog,
		arch:         arch,
		channelFunc:  channel,
		now:          time.Now,
		InitialDelay: DefaultInitialDelay,
		Interval:     DefaultInterval,
		Jitter:       DefaultJitter,
		Cooldown:     DefaultCooldown,
		StaleAfter:   DefaultStaleAfter,
	}
}

// SetClock injects a clock for tests.
func (c *Checker) SetClock(now func() time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = now
}

func (c *Checker) channel() Channel {
	if c.channelFunc == nil {
		return ChannelStable
	}
	channel := c.channelFunc()
	if channel == "" {
		return ChannelStable
	}
	return channel
}

// Snapshot returns the cached view. It never performs I/O.
func (c *Checker) Snapshot() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	snapshot := c.snapshot
	snapshot.Channel = c.channel()
	snapshot.StaleAfter = c.StaleAfter
	snapshot.NeverSucceeded = c.snapshot.LastSuccessAt.IsZero()
	snapshot.NextEarliest = c.backoffUntil
	snapshot.CooldownExpires = c.lastAttempt.Add(c.Cooldown)
	return snapshot
}

// ErrCheckTooSoon reports that a manual check was refused by the cooldown.
var ErrCheckTooSoon = errors.New("a release check ran recently; try again shortly")

// ErrCheckInProgress reports that another caller is already checking. Two
// dashboard tabs pressing the button share the one result.
var ErrCheckInProgress = errors.New("a release check is already running")

// CheckNow performs an operator-triggered check, subject to the cooldown and
// to single-flight. It returns the snapshot as of after the attempt.
func (c *Checker) CheckNow(ctx context.Context) (Snapshot, error) {
	c.mu.Lock()
	now := c.now()
	switch {
	case c.inFlight:
		c.mu.Unlock()
		return c.Snapshot(), ErrCheckInProgress
	case !c.lastAttempt.IsZero() && now.Sub(c.lastAttempt) < c.Cooldown:
		c.mu.Unlock()
		return c.Snapshot(), ErrCheckTooSoon
	}
	c.inFlight = true
	c.mu.Unlock()

	err := c.check(ctx)
	c.mu.Lock()
	c.inFlight = false
	c.mu.Unlock()
	return c.Snapshot(), err
}

// check performs one discovery round and folds the outcome into the cache.
func (c *Checker) check(ctx context.Context) error {
	c.mu.Lock()
	etag := c.etag
	channel := c.channel()
	c.lastAttempt = c.now()
	c.mu.Unlock()

	result, err := c.catalog.Fetch(ctx, etag)

	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	c.snapshot.CheckedAt = now
	c.snapshot.Channel = channel

	if err != nil {
		c.failures++
		c.snapshot.RateLimited = errors.Is(err, ErrRateLimited)
		c.snapshot.Error = err.Error()
		wait := result.RetryAfter
		if wait <= 0 {
			// Exponential backoff on repeated failures, so an outage or a
			// throttled endpoint is not hammered by every appliance.
			wait = c.Interval / 6 * time.Duration(1<<min(c.failures-1, 6))
		}
		if wait > maxBackoff {
			wait = maxBackoff
		}
		c.backoffUntil = now.Add(wait)
		return err
	}

	c.failures = 0
	c.backoffUntil = time.Time{}
	c.snapshot.Error = ""
	c.snapshot.RateLimited = false
	c.snapshot.LastSuccessAt = now
	if result.NotModified {
		// The list is unchanged: the previous selection still stands.
		return nil
	}
	c.etag = result.ETag
	newest, candidate := SelectNewest(result.Releases, channel, c.arch)
	c.snapshot.Newest = newest
	c.snapshot.Candidate = candidate
	return nil
}

// Run performs the periodic checks until the context is cancelled.
func (c *Checker) Run(ctx context.Context) {
	timer := time.NewTimer(c.InitialDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}

		c.mu.Lock()
		blocked := c.now().Before(c.backoffUntil)
		running := c.inFlight
		if !blocked && !running {
			c.inFlight = true
		}
		c.mu.Unlock()

		if !blocked && !running {
			_ = c.check(ctx)
			c.mu.Lock()
			c.inFlight = false
			c.mu.Unlock()
		}

		timer.Reset(c.nextDelay())
	}
}

func (c *Checker) nextDelay() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	delay := c.Interval
	if wait := c.backoffUntil.Sub(c.now()); wait > delay {
		delay = wait
	}
	if c.Jitter > 0 {
		delay += time.Duration(rand.Int64N(int64(c.Jitter)))
	}
	return delay
}

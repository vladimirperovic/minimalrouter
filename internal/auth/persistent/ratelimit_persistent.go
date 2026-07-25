package persistent

import (
	"database/sql"
	"sync"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

// PersistentRateLimiter enforces rate limits with SQLite persistence.
type PersistentRateLimiter struct {
	mu     sync.Mutex
	store  *config.SQLiteStore
	limit  int
	window time.Duration
	cache  map[string]*ipHistory // in-memory cache for performance
}

type ipHistory struct {
	attempts  int
	firstSeen time.Time
}

// NewPersistentRateLimiter creates a persistent rate limiter instance.
func NewPersistentRateLimiter(store *config.SQLiteStore, limit int, window time.Duration) *PersistentRateLimiter {
	rl := &PersistentRateLimiter{
		store:  store,
		limit:  limit,
		window: window,
		cache:  make(map[string]*ipHistory),
	}
	// Load recent entries from SQLite (optional optimization)
	// For now, we start fresh and persist all new entries
	go rl.cleanLoop()
	return rl
}

// Allow checks if the given IP address is within rate limits.
func (rl *PersistentRateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()

	// Check in-memory cache first
	if rec, exists := rl.cache[ip]; exists {
		if now.Sub(rec.firstSeen) > rl.window {
			// Window expired, reset
			rec.attempts = 1
			rec.firstSeen = now
			return rl.store.SetRateLimitBucket(ip, 1, now) == nil
		}
		if rec.attempts >= rl.limit {
			return false
		}
		rec.attempts++
		return rl.store.SetRateLimitBucket(ip, rec.attempts, rec.firstSeen) == nil
	}

	// Not in cache - load from SQLite
	attempts, windowStart, err := rl.store.GetRateLimitBucket(ip)
	if err != nil {
		if err != sql.ErrNoRows {
			return false
		}
		// Not found in SQLite - first attempt
		rl.cache[ip] = &ipHistory{attempts: 1, firstSeen: now}
		return rl.store.SetRateLimitBucket(ip, 1, now) == nil
	}

	if now.Sub(windowStart) > rl.window {
		// Window expired in SQLite too
		rl.cache[ip] = &ipHistory{attempts: 1, firstSeen: now}
		return rl.store.SetRateLimitBucket(ip, 1, now) == nil
	}

	if attempts >= rl.limit {
		// Update cache with current state
		rl.cache[ip] = &ipHistory{attempts: attempts, firstSeen: windowStart}
		return false
	}

	// Allow and update
	rl.cache[ip] = &ipHistory{attempts: attempts + 1, firstSeen: windowStart}
	return rl.store.SetRateLimitBucket(ip, attempts+1, windowStart) == nil
}

func (rl *PersistentRateLimiter) cleanLoop() {
	ticker := time.NewTicker(2 * time.Minute)
	for range ticker.C {
		rl.mu.Lock()
		now := time.Now()
		// Clean in-memory cache
		for ip, rec := range rl.cache {
			if now.Sub(rec.firstSeen) > rl.window {
				delete(rl.cache, ip)
			}
		}
		rl.mu.Unlock()

		// Clean SQLite
		_ = rl.store.CleanExpiredRateLimitBuckets(rl.window)
	}
}

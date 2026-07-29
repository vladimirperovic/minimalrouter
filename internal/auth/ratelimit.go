package auth

import (
	"sync"
	"time"
)

type ipHistory struct {
	attempts  int
	firstSeen time.Time
}

// RateLimiter enforces 5 attempts per 60 seconds limit per source IP per SECURITY.md.
type RateLimiter struct {
	mu      sync.Mutex
	history map[string]*ipHistory
	limit   int
	window  time.Duration
}

// NewRateLimiter creates a rate limiter instance.
func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	rl := &RateLimiter{
		history: make(map[string]*ipHistory),
		limit:   limit,
		window:  window,
	}
	go rl.cleanLoop()
	return rl
}

// Allow checks if the given IP address is within rate limits.
func (rl *RateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	rec, exists := rl.history[ip]
	if !exists {
		rl.history[ip] = &ipHistory{
			attempts:  1,
			firstSeen: now,
		}
		return true
	}

	if now.Sub(rec.firstSeen) > rl.window {
		rec.attempts = 1
		rec.firstSeen = now
		return true
	}

	if rec.attempts >= rl.limit {
		return false
	}

	rec.attempts++
	return true
}

func (rl *RateLimiter) cleanLoop() {
	ticker := time.NewTicker(2 * time.Minute)
	for range ticker.C {
		rl.mu.Lock()
		now := time.Now()
		for ip, rec := range rl.history {
			if now.Sub(rec.firstSeen) > rl.window {
				delete(rl.history, ip)
			}
		}
		rl.mu.Unlock()
	}
}

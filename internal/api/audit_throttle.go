package api

import (
	"strconv"
	"sync"
	"time"
)

// Request rejections (no session, untrusted source, CSRF/Origin failures,
// login rate limiting) are audited at a rate the requester chooses. Writing
// each of them made every rejected request an fsync'd SQLite transaction and
// let any trusted-network client churn the audit log. They are therefore
// recorded at a bounded rate per event type and source, and in total; what is
// dropped is still accounted for: suppression is marked once per window when
// it starts, and the next window opens with the exact count.
const (
	auditThrottleWindow     = time.Minute
	auditThrottlePerSource  = 10
	auditThrottleGlobal     = 120
	auditThrottleMaxSources = 1024
)

type auditThrottle struct {
	mu          sync.Mutex
	windowStart time.Time
	total       int
	perSource   map[string]int
	suppressed  int
	marked      bool
}

type auditThrottleDecision struct {
	record bool
	// markSuppression is set for the first event dropped in a window.
	markSuppression bool
	// previousSuppressed/previousWindowStart summarize the window that just
	// ended, reported once when the next window opens.
	previousSuppressed  int
	previousWindowStart time.Time
}

func (t *auditThrottle) admit(key string, now time.Time) auditThrottleDecision {
	t.mu.Lock()
	defer t.mu.Unlock()

	var decision auditThrottleDecision
	if t.perSource == nil || now.Sub(t.windowStart) >= auditThrottleWindow || now.Before(t.windowStart) {
		if t.suppressed > 0 {
			decision.previousSuppressed = t.suppressed
			decision.previousWindowStart = t.windowStart
		}
		t.windowStart = now
		t.total = 0
		t.perSource = make(map[string]int)
		t.suppressed = 0
		t.marked = false
	}

	count, known := t.perSource[key]
	if t.total >= auditThrottleGlobal || count >= auditThrottlePerSource ||
		(!known && len(t.perSource) >= auditThrottleMaxSources) {
		t.suppressed++
		if !t.marked {
			t.marked = true
			decision.markSuppression = true
		}
		return decision
	}
	t.perSource[key] = count + 1
	t.total++
	decision.record = true
	return decision
}

func (s *Server) appendThrottledAudit(eventType, actor string, details map[string]string, now time.Time) {
	decision := s.auditLimiter.admit(eventType+"\x00"+actor, now)
	if decision.previousSuppressed > 0 {
		s.persistAudit("audit.suppressed", "local", map[string]string{
			"count":          strconv.Itoa(decision.previousSuppressed),
			"window_start":   decision.previousWindowStart.UTC().Format(time.RFC3339),
			"window_seconds": strconv.Itoa(int(auditThrottleWindow / time.Second)),
		})
	}
	if decision.markSuppression {
		s.persistAudit("audit.throttled", "local", map[string]string{
			"first_event":    eventType,
			"first_actor":    actor,
			"window_seconds": strconv.Itoa(int(auditThrottleWindow / time.Second)),
		})
	}
	if decision.record {
		s.persistAudit(eventType, actor, details)
	}
}

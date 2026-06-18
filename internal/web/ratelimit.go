package web

import (
	"sync"
	"time"
)

// rateLimiter is a small in-memory sliding-window limiter used to throttle
// login attempts. Keyed by client IP. Adequate
// for a single-process, ≤5-user deployment; a distributed setup would need a
// shared store, but that's out of scope.
type rateLimiter struct {
	mu     sync.Mutex
	hits   map[string][]time.Time
	max    int
	window time.Duration
}

func newRateLimiter(max int, window time.Duration) *rateLimiter {
	return &rateLimiter{hits: make(map[string][]time.Time), max: max, window: window}
}

// allow records an attempt for key and reports whether it is within the limit.
func (rl *rateLimiter) allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-rl.window)

	kept := rl.hits[key][:0]
	for _, t := range rl.hits[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	kept = append(kept, now)
	rl.hits[key] = kept
	return len(kept) <= rl.max
}

// Package ratelimit provides a small in-process fixed-window rate limiter.
//
// Why not a dependency: the only thing we need is "how many replays per minute per
// caller", and a 60-line map with lazy eviction is easier to audit than pulling in a
// full rate limiting library. It is also trivially testable with an injected clock.
//
// Scope: this protects a single process. Behind several replicas the effective limit is
// N x replicas - acceptable for a debugging tool, and documented in the README.
package ratelimit

import (
	"sync"
	"time"
)

// maxTracked is the number of keys we keep before forcing a sweep of expired windows.
const maxTracked = 4096

// Limiter counts events per key inside a rolling fixed window.
type Limiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	now    func() time.Time
	keys   map[string]bucket
}

type bucket struct {
	used    int
	resetAt time.Time
}

// New builds a limiter allowing limit events per key per window. A limit <= 0 disables
// limiting entirely (every call is allowed), which keeps configuration honest: the
// operator asked for no limit, and we should not invent one.
func New(limit int, window time.Duration, now func() time.Time) *Limiter {
	if now == nil {
		now = time.Now
	}

	return &Limiter{limit: limit, window: window, now: now, keys: make(map[string]bucket)}
}

// Allow reports whether the caller may proceed and, when not, how long it has to wait.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	if l.limit <= 0 || l.window <= 0 {
		return true, 0
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()

	w, ok := l.keys[key]
	if !ok || now.After(w.resetAt) {
		l.evict(now)

		l.keys[key] = bucket{used: 1, resetAt: now.Add(l.window)}

		return true, 0
	}

	if w.used < l.limit {
		w.used++
		l.keys[key] = w

		return true, 0
	}

	return false, w.resetAt.Sub(now)
}

// evict drops expired windows. Called under the lock and only once the map grew past
// maxTracked, so the steady-state cost is a single map lookup.
func (l *Limiter) evict(now time.Time) {
	if len(l.keys) < maxTracked {
		return
	}

	for k, w := range l.keys {
		if now.After(w.resetAt) {
			delete(l.keys, k)
		}
	}
}

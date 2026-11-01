package auth

import (
	"sync"
	"time"
)

// Limiter is a fixed-window in-memory rate limiter. It is intentionally
// process-local: single-writer deployments only.
type Limiter struct {
	mu      sync.Mutex
	entries map[string]*window
	now     func() time.Time
}

type window struct {
	count int
	reset time.Time
}

// NewLimiter creates a limiter.
func NewLimiter() *Limiter {
	return &Limiter{entries: map[string]*window{}, now: time.Now}
}

// Allow consumes one attempt and reports whether it is within limit.
func (l *Limiter) Allow(key string, limit int, dur time.Duration) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	e, ok := l.entries[key]
	if !ok || now.After(e.reset) {
		l.entries[key] = &window{count: 1, reset: now.Add(dur)}
		return true, 0
	}
	if e.count >= limit {
		return false, time.Until(e.reset)
	}
	e.count++
	return true, 0
}

// Reset clears a key after successful authentication.
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	delete(l.entries, key)
	l.mu.Unlock()
}

// Cleanup drops expired windows.
func (l *Limiter) Cleanup() {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	for k, e := range l.entries {
		if now.After(e.reset) {
			delete(l.entries, k)
		}
	}
}

// RoleAllowed reports whether role satisfies the minimum required role.
func RoleAllowed(role, required string) bool {
	return roleRank(role) >= roleRank(required)
}

func roleRank(role string) int {
	switch role {
	case "viewer":
		return 1
	case "operator":
		return 2
	case "admin":
		return 3
	}
	return 0
}

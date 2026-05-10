package auth

import (
	"sync"
	"time"
)

// LoginLimiter is a tiny in-memory per-IP sliding-window limiter for /login.
// A successful login resets the counter; consecutive failures push it up.
// Exceeding `max` within `window` returns Allowed()=false until the oldest
// failure ages out. Memory is bounded by expiring buckets in Allow.
type LoginLimiter struct {
	mu     sync.Mutex
	events map[string][]time.Time
	max    int
	window time.Duration
}

// NewLoginLimiter returns a limiter allowing `maxFailures` failed attempts per `window`.
// Sonarr-ish defaults: 5 per 15 min.
func NewLoginLimiter(maxFailures int, window time.Duration) *LoginLimiter {
	return &LoginLimiter{events: make(map[string][]time.Time), max: maxFailures, window: window}
}

// Allow returns true if the caller may attempt another login. Record a failure
// via Record(); reset on success via Reset().
func (l *LoginLimiter) Allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.gc(ip, time.Now())
	return len(l.events[ip]) < l.max
}

// Record increments the failure counter for ip.
func (l *LoginLimiter) Record(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	l.gc(ip, now)
	l.events[ip] = append(l.events[ip], now)
}

// Reset clears the failure counter for ip (call on successful login).
func (l *LoginLimiter) Reset(ip string) {
	l.mu.Lock()
	delete(l.events, ip)
	l.mu.Unlock()
}

// gc expires events older than the window for one ip; caller holds l.mu.
func (l *LoginLimiter) gc(ip string, now time.Time) {
	cutoff := now.Add(-l.window)
	events := l.events[ip]
	i := 0
	for ; i < len(events); i++ {
		if events[i].After(cutoff) {
			break
		}
	}
	if i == len(events) {
		delete(l.events, ip)
		return
	}
	l.events[ip] = events[i:]
}

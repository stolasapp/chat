// Package ws provides WebSocket HTTP handlers and middleware.
package ws

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// IPLimiter tracks per-IP rate limiters.
type IPLimiter struct {
	mu       sync.Mutex
	limiters map[string]*entry
	rate     rate.Limit
	burst    int
}

type entry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// NewIPLimiter creates a limiter that allows r events per second
// with the given burst size per IP.
func NewIPLimiter(r rate.Limit, burst int) *IPLimiter {
	return &IPLimiter{
		limiters: make(map[string]*entry),
		rate:     r,
		burst:    burst,
	}
}

// Allow reports whether an event from addr is permitted.
func (ipl *IPLimiter) Allow(addr string) bool {
	ipl.mu.Lock()
	defer ipl.mu.Unlock()

	ent, ok := ipl.limiters[addr]
	if !ok {
		ent = &entry{limiter: rate.NewLimiter(ipl.rate, ipl.burst)}
		ipl.limiters[addr] = ent
	}
	ent.lastSeen = time.Now()
	return ent.limiter.Allow()
}

// Cleanup removes entries that have been idle longer than maxAge.
func (ipl *IPLimiter) Cleanup(maxAge time.Duration) {
	ipl.mu.Lock()
	defer ipl.mu.Unlock()

	cutoff := time.Now().Add(-maxAge)
	for ip, ent := range ipl.limiters {
		if ent.lastSeen.Before(cutoff) {
			delete(ipl.limiters, ip)
		}
	}
}

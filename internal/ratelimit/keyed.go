// Package ratelimit provides generic keyed rate limiters.
package ratelimit

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

type entry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// Keyed tracks per-key rate limiters. Keys are created lazily on
// first access and share the configured rate and burst.
type Keyed[K comparable] struct {
	mu      sync.Mutex
	entries map[K]*entry // +checklocks:mu
	rate    rate.Limit   // +checklocks:mu
	burst   int          // +checklocks:mu
}

// NewKeyed creates a limiter that allows r events per second with
// the given burst size per key.
func NewKeyed[K comparable](r rate.Limit, burst int) *Keyed[K] {
	return &Keyed[K]{
		entries: make(map[K]*entry),
		rate:    r,
		burst:   burst,
	}
}

// Allow reports whether an event for the given key is permitted.
func (kl *Keyed[K]) Allow(key K) bool {
	kl.mu.Lock()
	defer kl.mu.Unlock()

	ent, ok := kl.entries[key]
	if !ok {
		ent = &entry{limiter: rate.NewLimiter(kl.rate, kl.burst)}
		kl.entries[key] = ent
	}
	ent.lastSeen = time.Now()
	return ent.limiter.Allow()
}

// Remove deletes the limiter for the given key.
func (kl *Keyed[K]) Remove(key K) {
	kl.mu.Lock()
	defer kl.mu.Unlock()
	delete(kl.entries, key)
}

// Cleanup removes entries that have been idle longer than maxAge.
func (kl *Keyed[K]) Cleanup(maxAge time.Duration) {
	kl.mu.Lock()
	defer kl.mu.Unlock()

	cutoff := time.Now().Add(-maxAge)
	for key, ent := range kl.entries {
		if ent.lastSeen.Before(cutoff) {
			delete(kl.entries, key)
		}
	}
}

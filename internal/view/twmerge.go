package view

import (
	"sync"

	twmerge "github.com/Oudwins/tailwind-merge-go"
	"github.com/Oudwins/tailwind-merge-go/pkg/cache"
	twpkg "github.com/Oudwins/tailwind-merge-go/pkg/twmerge"
)

func init() { //nolint:gochecknoinits // must replace global before concurrent use
	// tailwind-merge-go's global Merge function uses a
	// non-thread-safe LRU cache and lazy initialization.
	// Replace it with a thread-safe cache and eagerly
	// initialize to prevent races during concurrent
	// templ rendering.
	safeMerge := twpkg.CreateTwMerge(nil, &safeCache{
		data: make(map[string]string),
	})
	// Trigger the lazy init path before any concurrent access.
	_ = safeMerge("_warmup_")
	twmerge.Merge = safeMerge
}

// safeCache implements cache.ICache with a sync.RWMutex.
type safeCache struct {
	mu   sync.RWMutex
	data map[string]string // +checklocks:mu
}

var _ cache.ICache = (*safeCache)(nil)

func (c *safeCache) Get(key string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.data[key]
}

func (c *safeCache) Set(key, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[key] = value
}

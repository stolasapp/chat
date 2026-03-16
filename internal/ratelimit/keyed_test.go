package ratelimit

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
)

func TestKeyed_BurstExhaustion(t *testing.T) {
	t.Parallel()

	limiter := NewKeyed[string](rate.Limit(0.1), 3)

	for range 3 {
		require.True(t, limiter.Allow("a"), "should allow within burst")
	}
	assert.False(t, limiter.Allow("a"), "should deny after burst exhausted")
}

func TestKeyed_IndependentKeys(t *testing.T) {
	t.Parallel()

	limiter := NewKeyed[string](rate.Limit(0.1), 1)

	require.True(t, limiter.Allow("a"))
	assert.False(t, limiter.Allow("a"), "first key exhausted")
	assert.True(t, limiter.Allow("b"), "second key independent")
}

func TestKeyed_Remove(t *testing.T) {
	t.Parallel()

	limiter := NewKeyed[string](rate.Limit(0.1), 2)

	require.True(t, limiter.Allow("a"))
	require.True(t, limiter.Allow("a"))
	assert.False(t, limiter.Allow("a"))

	limiter.Remove("a")

	assert.True(t, limiter.Allow("a"), "should allow after remove")
}

func TestKeyed_Cleanup(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		limiter := NewKeyed[string](rate.Limit(1), 1)
		limiter.Allow("stale")

		time.Sleep(20 * time.Millisecond)
		limiter.Allow("fresh")

		limiter.Cleanup(10 * time.Millisecond)

		limiter.mu.Lock()
		_, hasStale := limiter.entries["stale"]
		_, hasFresh := limiter.entries["fresh"]
		limiter.mu.Unlock()

		assert.False(t, hasStale, "stale entry should be cleaned up")
		assert.True(t, hasFresh, "fresh entry should remain")
	})
}

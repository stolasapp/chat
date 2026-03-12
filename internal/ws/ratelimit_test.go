package ws

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
)

func TestIPLimiter_BurstExhaustion(t *testing.T) {
	t.Parallel()

	limiter := NewIPLimiter(rate.Limit(0.1), 3)
	addr := "10.0.0.1"

	for range 3 {
		require.True(t, limiter.Allow(addr), "should allow within burst")
	}
	assert.False(t, limiter.Allow(addr), "should deny after burst exhausted")
}

func TestIPLimiter_IndependentIPs(t *testing.T) {
	t.Parallel()

	limiter := NewIPLimiter(rate.Limit(0.1), 1)

	require.True(t, limiter.Allow("10.0.0.1"))
	assert.False(t, limiter.Allow("10.0.0.1"), "first IP exhausted")
	assert.True(t, limiter.Allow("10.0.0.2"), "second IP independent")
}

func TestIPLimiter_Cleanup(t *testing.T) {
	t.Parallel()

	limiter := NewIPLimiter(rate.Limit(1), 1)
	limiter.Allow("stale")

	time.Sleep(20 * time.Millisecond)
	limiter.Allow("fresh")

	limiter.Cleanup(10 * time.Millisecond)

	assert.True(t, limiter.Allow("stale"), "stale entry should be evicted and re-created")
	assert.False(t, limiter.Allow("fresh"), "fresh entry should be retained")
}

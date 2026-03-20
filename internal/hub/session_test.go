package hub

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stolasapp/chat/internal/match"
)

func testSessionConfig() sessionConfig {
	return sessionConfig{
		idleTimeout: 500 * time.Millisecond,
		idleWarning: 150 * time.Millisecond,
	}
}

func TestSessionManager_CreateAndInSession(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	sessMgr := NewSessionManager(ctx, testSessionConfig())
	defer sessMgr.Shutdown()

	ended := make(chan struct{})
	clientA := testClient("a")
	clientB := testClient("b")

	sessMgr.Create("a", "b", clientA, clientB, func(_, _ match.Token) {
		close(ended)
	})

	assert.True(t, sessMgr.InSession("a"))
	assert.True(t, sessMgr.InSession("b"))
	assert.False(t, sessMgr.InSession("c"))
	assert.Equal(t, match.Token("b"), sessMgr.Partner("a"))
	assert.Equal(t, match.Token("a"), sessMgr.Partner("b"))
}

func TestSessionManager_Leave(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	sessMgr := NewSessionManager(ctx, testSessionConfig())
	defer sessMgr.Shutdown()

	ended := make(chan struct{})
	clientA := testClient("a")
	clientB := testClient("b")

	sessMgr.Create("a", "b", clientA, clientB, func(_, _ match.Token) {
		close(ended)
	})

	// send leave from client A via the sink
	env, err := NewEnvelope(LeaveMessage{})
	require.NoError(t, err)
	(*clientA.sink.Load())(ctx, clientA, env)

	select {
	case <-ended:
	case <-time.After(time.Second):
		t.Fatal("session did not end after leave")
	}

	// session should be cleaned up
	require.Eventually(t, func() bool {
		return !sessMgr.InSession("a") && !sessMgr.InSession("b")
	}, time.Second, 10*time.Millisecond)
}

func TestSessionManager_GraceExpired(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	sessMgr := NewSessionManager(ctx, testSessionConfig())
	defer sessMgr.Shutdown()

	ended := make(chan struct{})
	clientA := testClient("a")
	clientB := testClient("b")

	sessMgr.Create("a", "b", clientA, clientB, func(_, _ match.Token) {
		close(ended)
	})

	sessMgr.End("a")

	select {
	case <-ended:
	case <-time.After(time.Second):
		t.Fatal("session did not end after grace expired")
	}
}

func TestSessionManager_Shutdown(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	sessMgr := NewSessionManager(ctx, testSessionConfig())

	clientA := testClient("a")
	clientB := testClient("b")

	sessMgr.Create("a", "b", clientA, clientB, func(_, _ match.Token) {})

	assert.True(t, sessMgr.InSession("a"))

	sessMgr.Shutdown()

	// after shutdown, sessions should be cleaned up
	assert.False(t, sessMgr.InSession("a"))
	assert.False(t, sessMgr.InSession("b"))
}

func TestSessionManager_PartnerNotInSession(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	sessMgr := NewSessionManager(ctx, testSessionConfig())
	defer sessMgr.Shutdown()

	assert.Equal(t, match.Token(""), sessMgr.Partner("nonexistent"))
}

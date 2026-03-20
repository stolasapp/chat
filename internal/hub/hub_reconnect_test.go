package hub

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stolasapp/chat/internal/match"
	"github.com/stolasapp/chat/internal/view"
)

// matchTwoWithTokens matches two clients and returns their
// connections, collectors, and session tokens for reconnect testing.
func matchTwoWithTokens(t *testing.T, wsHub *Hub, srv *httptest.Server) (
	connA, connB *websocket.Conn,
	collB *messageCollector,
	tokenA, tokenB string,
) {
	t.Helper()
	connA, tokenA = dialHubWithToken(t, srv)
	connB, tokenB = dialHubWithToken(t, srv)

	collA := newCollector(t, connA)
	collB = newCollector(t, connB)

	require.Eventually(t, func() bool {
		return wsHub.Len() == 2
	}, time.Second, 10*time.Millisecond)

	sendJSON(t, connA, MessageTypeFindMatch, findMatchPayload())
	sendJSON(t, connB, MessageTypeFindMatch, findMatchPayload())

	require.True(t, collA.waitFor("matched", 3*time.Second), "A should be matched")
	require.True(t, collB.waitFor("matched", 3*time.Second), "B should be matched")

	return connA, connB, collB, tokenA, tokenB
}

func TestHub_ReconnectWithinGracePeriod(t *testing.T) {
	t.Parallel()

	wsHub := testHub()
	go wsHub.Run(t.Context())

	srv := simpleHandler(wsHub)
	defer srv.Close()

	connA, connB, collB, tokenA, _ := matchTwoWithTokens(t, wsHub, srv)
	defer func() { _ = connB.Close() }()

	// A sends a message before disconnecting
	sendJSON(t, connA, MessageTypeMessage, map[string]any{"text": "before disconnect"})
	require.True(t, collB.waitFor("before disconnect", 2*time.Second))

	// A disconnects (enters detached state)
	_ = connA.Close()

	// Len should stay at 2 (1 active + 1 detached)
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, 2, wsHub.Len(), "count should include detached client")

	// A reconnects via query param
	connA2, _ := dialHubReconnect(t, srv, tokenA)
	defer func() { _ = connA2.Close() }()
	collA2 := newCollector(t, connA2)

	// should receive partner profile (MatchedNotify renders "matched with")
	assert.True(t, collA2.waitFor("matched with", 3*time.Second), "should see partner profile on reconnect")

	// session should still work: B sends to A
	sendJSON(t, connB, MessageTypeMessage, map[string]any{"text": "welcome back"})
	assert.True(t, collA2.waitFor("welcome back", 2*time.Second), "should receive message after reconnect")

	// A sends to B
	sendJSON(t, connA2, MessageTypeMessage, map[string]any{"text": "im back"})
	assert.True(t, collB.waitFor("im back", 2*time.Second), "partner should receive message from reconnected client")
}

func TestHub_ReconnectAfterGraceExpired(t *testing.T) {
	t.Parallel()

	wsHub := testHub()
	go wsHub.Run(t.Context())

	srv := simpleHandler(wsHub)
	defer srv.Close()

	connA, connB, collB, tokenA, _ := matchTwoWithTokens(t, wsHub, srv)
	defer func() { _ = connB.Close() }()

	// A disconnects
	_ = connA.Close()

	// wait for grace period to expire
	assert.True(t, collB.waitFor(view.MsgPartnerLeft, 2*time.Second), "B should see partner left after grace expiry")

	// A tries to reconnect after grace expired: gets fresh identity
	connA2, tokenA2 := dialHubReconnect(t, srv, tokenA)
	defer func() { _ = connA2.Close() }()

	// token should be fresh (not the old one)
	assert.NotEqual(t, tokenA, tokenA2, "should get fresh identity after grace expiry")
}

func TestHub_ReconnectPartnerNotified(t *testing.T) {
	t.Parallel()

	wsHub := testHub()
	go wsHub.Run(t.Context())

	srv := simpleHandler(wsHub)
	defer srv.Close()

	connA, connB, collB, tokenA, _ := matchTwoWithTokens(t, wsHub, srv)
	defer func() { _ = connB.Close() }()

	// A disconnects
	_ = connA.Close()

	// B should see "reconnecting" after ~2s delay
	assert.True(t, collB.waitFor("reconnecting", 5*time.Second), "B should see reconnecting indicator")

	// A reconnects within grace period via query param
	connA2, _ := dialHubReconnect(t, srv, tokenA)
	defer func() { _ = connA2.Close() }()
	collA2 := newCollector(t, connA2)

	require.True(t, collA2.waitFor("Reconnected", 3*time.Second))

	// B should see the reconnecting indicator cleared (hidden)
	assert.True(t, collB.waitFor("hidden", 3*time.Second), "B should see reconnecting indicator cleared")
}

func TestHub_BothDisconnectAndReconnect(t *testing.T) {
	t.Parallel()

	wsHub := testHub()
	go wsHub.Run(t.Context())

	srv := simpleHandler(wsHub)
	defer srv.Close()

	connA, connB, _, tokenA, tokenB := matchTwoWithTokens(t, wsHub, srv)

	// both disconnect
	_ = connA.Close()
	_ = connB.Close()
	time.Sleep(200 * time.Millisecond)

	// Len should stay at 2 (both detached)
	assert.Equal(t, 2, wsHub.Len(), "both clients detached, count should be 2")

	// A reconnects via query param
	connA2, _ := dialHubReconnect(t, srv, tokenA)
	defer func() { _ = connA2.Close() }()
	collA2 := newCollector(t, connA2)
	require.True(t, collA2.waitFor("Reconnected", 3*time.Second), "A should reconnect")

	// B reconnects via query param
	connB2, _ := dialHubReconnect(t, srv, tokenB)
	defer func() { _ = connB2.Close() }()
	collB2 := newCollector(t, connB2)
	require.True(t, collB2.waitFor("Reconnected", 3*time.Second), "B should reconnect")

	// session should work: A sends to B
	sendJSON(t, connA2, MessageTypeMessage, map[string]any{"text": "both back"})
	assert.True(t, collB2.waitFor("both back", 2*time.Second), "B should receive message after both reconnect")
}

func TestHub_ReconnectClientCountStability(t *testing.T) {
	t.Parallel()

	wsHub := testHub()
	go wsHub.Run(t.Context())

	srv := simpleHandler(wsHub)
	defer srv.Close()

	connA, connB, _, tokenA, _ := matchTwoWithTokens(t, wsHub, srv)
	defer func() { _ = connB.Close() }()

	// before disconnect: 2
	assert.Equal(t, 2, wsHub.Len())

	// A disconnects (detached)
	_ = connA.Close()
	time.Sleep(100 * time.Millisecond)

	// still 2 (1 active + 1 detached)
	assert.Equal(t, 2, wsHub.Len())

	// A reconnects via query param
	connA2, _ := dialHubReconnect(t, srv, tokenA)
	defer func() { _ = connA2.Close() }()
	collA2 := newCollector(t, connA2)
	require.True(t, collA2.waitFor("Reconnected", 3*time.Second))

	require.Eventually(t, func() bool {
		return wsHub.Len() == 2
	}, time.Second, 10*time.Millisecond, "count should remain stable after reconnect")
}

func TestHub_GracefulShutdownDuringGracePeriod(t *testing.T) {
	t.Parallel()

	wsHub := testHub()
	ctx, cancel := context.WithCancel(context.Background())
	go wsHub.Run(ctx)

	srv := simpleHandler(wsHub)
	defer srv.Close()

	connA, connB, collB, _, _ := matchTwoWithTokens(t, wsHub, srv)
	defer func() { _ = connB.Close() }()

	// A disconnects (enters detached state)
	_ = connA.Close()
	time.Sleep(100 * time.Millisecond)

	// trigger shutdown while A is detached
	cancel()

	// B should see shutdown message (detached timers cleaned up)
	assert.True(t, collB.waitFor(view.MsgServerReset, 5*time.Second), "B should see reset message")
}

func TestHub_RefreshFromLandingPageThenMatch(t *testing.T) {
	t.Parallel()

	wsHub := testHub()
	go wsHub.Run(t.Context())

	srv := simpleHandler(wsHub)
	defer srv.Close()

	// A connects (landing page, no session)
	connA, tokenA := dialHubWithToken(t, srv)

	// A refreshes: old WS closes, new WS opens with token
	_ = connA.Close()
	time.Sleep(100 * time.Millisecond)

	connA2, _ := dialHubReconnect(t, srv, tokenA)
	defer func() { _ = connA2.Close() }()
	collA2 := newCollector(t, connA2)

	// B connects fresh
	connB := dialHub(t, srv)
	defer func() { _ = connB.Close() }()
	collB := newCollector(t, connB)

	require.Eventually(t, func() bool {
		return wsHub.Len() == 2
	}, time.Second, 10*time.Millisecond)

	// both search for a match
	sendJSON(t, connA2, MessageTypeFindMatch, findMatchPayload())
	sendJSON(t, connB, MessageTypeFindMatch, findMatchPayload())

	// should match normally
	assert.True(t, collA2.waitFor("matched", 3*time.Second), "A should match after refresh")
	assert.True(t, collB.waitFor("matched", 3*time.Second), "B should match")
}

func TestHub_RapidRefreshPreservesSession(t *testing.T) {
	t.Parallel()

	wsHub := testHub()
	go wsHub.Run(t.Context())

	srv := simpleHandler(wsHub)
	defer srv.Close()

	connA, connB, collB, tokenA, _ := matchTwoWithTokens(t, wsHub, srv)
	defer func() { _ = connB.Close() }()

	// rapid triple-refresh: close and reconnect 3 times
	for range 3 {
		_ = connA.Close()
		var newToken string
		connA, newToken = dialHubReconnect(t, srv, tokenA)
		// token should be preserved (reconnect succeeded)
		assert.Equal(t, tokenA, newToken, "token identity should be stable across rapid refreshes")
	}
	defer func() { _ = connA.Close() }()
	collA := newCollector(t, connA)

	// verify session still works
	assert.True(t, collA.waitFor("Reconnected", 3*time.Second), "should see reconnected after rapid refreshes")

	sendJSON(t, connA, MessageTypeMessage, map[string]any{"text": "survived rapid refresh"})
	assert.True(t, collB.waitFor("survived rapid refresh", 2*time.Second),
		"partner should receive message after rapid refreshes")
}

func TestHub_RefreshDuringSearchPreservesQueue(t *testing.T) {
	t.Parallel()

	wsHub := testHub()
	go wsHub.Run(t.Context())

	srv := simpleHandler(wsHub)
	defer srv.Close()

	// A connects and starts searching
	connA, tokenA := dialHubWithToken(t, srv)
	collA := newCollector(t, connA)

	require.Eventually(t, func() bool {
		return wsHub.Len() == 1
	}, time.Second, 10*time.Millisecond)

	sendJSON(t, connA, MessageTypeFindMatch, findMatchPayload())
	require.True(t, collA.waitFor("Searching", 2*time.Second))

	// A refreshes while searching
	_ = connA.Close()
	time.Sleep(100 * time.Millisecond)

	connA2, _ := dialHubReconnect(t, srv, tokenA)
	defer func() { _ = connA2.Close() }()
	collA2 := newCollector(t, connA2)

	// should see "Searching" recovery
	assert.True(t, collA2.waitFor("Searching", 3*time.Second), "should restore searching state")

	// B connects and searches; should match with A
	connB := dialHub(t, srv)
	defer func() { _ = connB.Close() }()
	collB := newCollector(t, connB)

	require.Eventually(t, func() bool {
		return wsHub.Len() == 2
	}, time.Second, 10*time.Millisecond)

	sendJSON(t, connB, MessageTypeFindMatch, findMatchPayload())

	assert.True(t, collA2.waitFor("matched", 3*time.Second), "A should match after refresh during search")
	assert.True(t, collB.waitFor("matched", 3*time.Second), "B should match with reconnected A")
}

func TestHub_SecondTabDoesNotStealSession(t *testing.T) {
	t.Parallel()

	wsHub := testHub()
	go wsHub.Run(t.Context())

	srv := simpleHandler(wsHub)
	defer srv.Close()

	connA, connB, collB, _, _ := matchTwoWithTokens(t, wsHub, srv)
	defer func() { _ = connA.Close() }()
	defer func() { _ = connB.Close() }()

	// "second tab" connects with no reconnect token (fresh sessionStorage)
	connA2 := dialHub(t, srv)
	defer func() { _ = connA2.Close() }()

	require.Eventually(t, func() bool {
		return wsHub.Len() == 3
	}, time.Second, 10*time.Millisecond)

	// first tab's session should still work
	sendJSON(t, connA, MessageTypeMessage, map[string]any{"text": "still here"})
	assert.True(t, collB.waitFor("still here", 2*time.Second), "first tab session should be intact")
}

// TestHub_StaleReconnectShowsReset verifies that reconnecting with
// a token the server doesn't recognize (e.g. after server restart)
// shows the reset message with action buttons instead of leaving
// the client in a stale UI state.
func TestHub_StaleReconnectShowsReset(t *testing.T) {
	t.Parallel()

	wsHub := testHub()
	go wsHub.Run(t.Context())

	srv := simpleHandler(wsHub)
	defer srv.Close()

	// connect with a fabricated stale token (server never issued it)
	conn, _ := dialHubReconnect(t, srv, "stale-token-from-old-server")
	defer func() { _ = conn.Close() }()
	coll := newCollector(t, conn)

	// TokenMessage + SessionEndComponents arrive as two frames;
	// the second contains both the reset message and action buttons
	assert.True(t, coll.waitFor(view.MsgServerReset, 2*time.Second),
		"stale reconnect should see server reset message with action buttons")
}

// TestHub_StaleReconnectNotifyIgnored verifies that
// handleReconnectNotify discards events for clients that have
// already reattached. This prevents a permanently visible
// "reconnecting" indicator when the notify timer fires after
// the client has reconnected.
func TestHub_StaleReconnectNotifyIgnored(t *testing.T) {
	t.Parallel()

	wsHub := testHub()
	go wsHub.Run(t.Context())

	srv := simpleHandler(wsHub)
	defer srv.Close()

	connA, connB, collB, tokenA, _ := matchTwoWithTokens(t, wsHub, srv)
	defer func() { _ = connB.Close() }()

	// A disconnects and reconnects within the notify delay
	_ = connA.Close()
	time.Sleep(50 * time.Millisecond)
	connA2, _ := dialHubReconnect(t, srv, tokenA)
	defer func() { _ = connA2.Close() }()
	collA2 := newCollector(t, connA2)
	require.True(t, collA2.waitFor("Reconnected", 3*time.Second))

	// push a stale notify directly into the hub's channel;
	// this simulates the timer firing after the client
	// reattached (the race that's hard to hit via timing)
	client := wsHub.ClientByToken(match.Token(tokenA))
	require.NotNil(t, client)
	wsHub.reconnectNotify <- client

	// give the hub time to process the stale event
	time.Sleep(100 * time.Millisecond)

	// A sends a message as a sentinel to bound B's read loop
	sendJSON(t, connA2, MessageTypeMessage, map[string]any{"text": "sentinel"})

	// read B's messages until the sentinel, checking whether
	// an active (non-hidden) reconnecting indicator leaked
	// through from the stale notify
	deadline := time.After(3 * time.Second)
	sawActiveIndicator := false
	for {
		select {
		case msg, ok := <-collB.msgs:
			if !ok {
				t.Fatal("connection closed before sentinel")
			}
			if strings.Contains(msg, "sentinel") {
				goto done
			}
			if strings.Contains(msg, "Partner reconnecting") &&
				!strings.Contains(msg, "hidden") {
				sawActiveIndicator = true
			}
		case <-deadline:
			t.Fatal("timed out waiting for sentinel")
		}
	}
done:
	assert.False(t, sawActiveIndicator,
		"stale notify should not show active reconnecting indicator")
}

func TestHub_LandingPageRefreshGetsFreshIdentity(t *testing.T) {
	t.Parallel()

	wsHub := testHub()
	go wsHub.Run(t.Context())

	srv := simpleHandler(wsHub)
	defer srv.Close()

	// connect, get token, close (no match/search)
	conn1, token1 := dialHubWithToken(t, srv)
	_ = conn1.Close()
	time.Sleep(50 * time.Millisecond)

	// reconnect with stale token; should get a fresh identity
	// since the old client had no session or search to preserve
	conn2, token2 := dialHubReconnect(t, srv, token1)
	defer func() { _ = conn2.Close() }()

	assert.NotEqual(t, token1, token2, "landing-page refresh should get fresh identity")
}

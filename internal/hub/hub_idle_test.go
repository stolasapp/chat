package hub

import (
	"fmt"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stolasapp/chat/internal/view"
)

func idleHub() *Hub {
	wsHub := testHub()
	wsHub.IdleTimeout = time.Second
	wsHub.IdleWarning = 300 * time.Millisecond
	return wsHub
}

func TestHub_IdleTimeout_WarningThenDisconnect(t *testing.T) {
	t.Parallel()

	wsHub := idleHub()
	go wsHub.Run(t.Context())

	srv := simpleHandler(wsHub)
	defer srv.Close()

	connA, connB, collA, _ := matchTwo(t, wsHub, srv)
	defer func() { _ = connA.Close() }()
	defer func() { _ = connB.Close() }()

	// neither sends a message; A should see warning then disconnect
	assert.True(t, collA.waitFor(view.MsgIdleKicked, 3*time.Second),
		"should see warning then disconnect")
}

func TestHub_IdleTimeout_ResetOnMessage(t *testing.T) {
	t.Parallel()

	wsHub := idleHub()
	go wsHub.Run(t.Context())

	srv := simpleHandler(wsHub)
	defer srv.Close()

	connA, connB, collA, collB := matchTwo(t, wsHub, srv)
	defer func() { _ = connA.Close() }()
	defer func() { _ = connB.Close() }()

	// send a message before warning fires (within 300ms)
	time.Sleep(200 * time.Millisecond)
	sendJSON(t, connA, MessageTypeMessage, map[string]any{"text": "still here"})
	require.True(t, collB.waitFor("still here", 2*time.Second))

	// should not see inactivity warning for a while (timer reset)
	assert.False(t, collA.waitFor(view.MsgIdleKicked, 400*time.Millisecond),
		"should not see warning after message reset")
}

func TestHub_IdleTimeout_ResetOnTyping(t *testing.T) {
	t.Parallel()

	wsHub := idleHub()
	go wsHub.Run(t.Context())

	srv := simpleHandler(wsHub)
	defer srv.Close()

	connA, connB, collA, _ := matchTwo(t, wsHub, srv)
	defer func() { _ = connA.Close() }()
	defer func() { _ = connB.Close() }()

	// send typing before warning fires
	time.Sleep(200 * time.Millisecond)
	sendJSON(t, connA, MessageTypeTyping, map[string]any{"active": true})

	// should not see inactivity warning for a while (timer reset)
	assert.False(t, collA.waitFor(view.MsgIdleKicked, 400*time.Millisecond),
		"should not see warning after typing reset")
}

func TestHub_IdleTimeout_WarningThenReset(t *testing.T) {
	t.Parallel()

	wsHub := idleHub()
	go wsHub.Run(t.Context())

	srv := simpleHandler(wsHub)
	defer srv.Close()

	connA, connB, collA, collB := matchTwo(t, wsHub, srv)
	defer func() { _ = connA.Close() }()
	defer func() { _ = connB.Close() }()

	// wait for warning
	warnMsg := fmt.Sprintf("disconnected for inactivity in %d seconds", int(wsHub.IdleWarning.Seconds()))
	require.True(t, collA.waitFor(warnMsg, 2*time.Second), "should see warning")

	// send a message to reset the timer
	sendJSON(t, connA, MessageTypeMessage, map[string]any{"text": "woke up"})
	require.True(t, collB.waitFor("woke up", 2*time.Second))

	// should not be disconnected (timer was reset)
	assert.False(t, collA.waitFor(view.MsgIdleKicked, 400*time.Millisecond),
		"should not disconnect after message during warning")
}

func TestHub_IdleTimeout_NotDuringSearch(t *testing.T) {
	t.Parallel()

	wsHub := idleHub()
	go wsHub.Run(t.Context())

	srv := simpleHandler(wsHub)
	defer srv.Close()

	conn := dialHub(t, srv)
	defer func() { _ = conn.Close() }()
	coll := newCollector(t, conn)

	require.Eventually(t, func() bool {
		return wsHub.Len() == 1
	}, time.Second, 10*time.Millisecond)

	sendJSON(t, conn, MessageTypeFindMatch, findMatchPayload())
	require.True(t, coll.waitFor("Searching", 2*time.Second))

	// wait past the idle timeout; should NOT be disconnected
	// (idle only applies during active sessions)
	assert.False(t, coll.waitFor(view.MsgIdleKicked, time.Second),
		"should not see idle timeout while searching")
}

func TestHub_IdleTimeout_ReconnectResetsTimer(t *testing.T) {
	t.Parallel()

	wsHub := idleHub()
	go wsHub.Run(t.Context())

	srv := simpleHandler(wsHub)
	defer srv.Close()

	connA, connB, _, tokenA, _ := matchTwoWithTokens(t, wsHub, srv)
	defer func() { _ = connB.Close() }()

	// A disconnects and reconnects
	_ = connA.Close()
	time.Sleep(100 * time.Millisecond)

	connA2, _ := dialHubReconnect(t, srv, tokenA)
	defer func() { _ = connA2.Close() }()
	collA2 := newCollector(t, connA2)

	require.True(t, collA2.waitFor("Reconnected", 3*time.Second))

	// idle timer should have been restarted on reconnect;
	// should not see warning for a while
	assert.False(t, collA2.waitFor(view.MsgIdleKicked, 200*time.Millisecond),
		"reconnect should reset idle timer")
}

func TestHub_IdleTimeout_PartnerSeesLeft(t *testing.T) {
	t.Parallel()

	wsHub := idleHub()
	go wsHub.Run(t.Context())

	srv := simpleHandler(wsHub)
	defer srv.Close()

	connA, connB, _, collB := matchTwo(t, wsHub, srv)
	defer func() { _ = connA.Close() }()
	defer func() { _ = connB.Close() }()

	// B sends periodic typing to stay active; only A is idle.
	// When A's idle timer fires, B should see "Your partner has left."
	typingMsg := []byte(`{"type":"typing","payload":{"active":true}}`)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				_ = connB.WriteMessage(websocket.TextMessage, typingMsg)
			case <-stop:
				return
			}
		}
	}()

	assert.True(t, collB.waitFor(view.MsgPartnerLeft, 5*time.Second),
		"partner should see standard left message on idle disconnect")
}

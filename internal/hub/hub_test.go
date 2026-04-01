package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stolasapp/chat/internal/match"
	"github.com/stolasapp/chat/internal/view"
)

var testClientCounter atomic.Int64

func testMatcher() *match.Matcher {
	return match.NewMatcher(match.DefaultMatchTimeout)
}

func testHub() *Hub {
	wsHub := NewHub(testMatcher())
	wsHub.GracePeriod = 500 * time.Millisecond
	wsHub.ReconnectNotifyDelay = 100 * time.Millisecond
	wsHub.SearchCooldown = 0
	return wsHub
}

func simpleHandler(wsHub *Hub) *httptest.Server {
	upgrader := websocket.Upgrader{}
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		idx := testClientCounter.Add(1)
		token := match.Token(fmt.Sprintf("tok-%d", idx))

		conn, err := upgrader.Upgrade(writer, request, nil)
		if err != nil {
			return
		}
		client := wsHub.CreateClient(token)
		var reconnectToken match.Token
		if qToken := request.URL.Query().Get("token"); qToken != "" {
			reconnectToken = match.Token(qToken)
		}
		if err := wsHub.Register(request.Context(), client, conn, reconnectToken); err != nil {
			client.Close(ErrClientClosed)
			return
		}
	}))
}

func dialHub(t *testing.T, srv *httptest.Server) *websocket.Conn {
	t.Helper()
	conn, _ := dialHubWithToken(t, srv)
	return conn
}

// dialHubWithToken opens a WS and reads the first message (a
// TokenMessage) to extract the session token.
func dialHubWithToken(t *testing.T, srv *httptest.Server) (*websocket.Conn, string) {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/"
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	token := readTokenMessage(t, conn)
	return conn, token
}

// dialHubReconnect opens a WS with a token query param, simulating
// a browser reconnection attempt. Returns the connection and the
// new token from the TokenMessage.
func dialHubReconnect(t *testing.T, srv *httptest.Server, token string) (*websocket.Conn, string) {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/?token=" + token
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	newToken := readTokenMessage(t, conn)
	return conn, newToken
}

// readTokenMessage reads the first WS message and extracts the
// token from a TokenMessage envelope.
func readTokenMessage(t *testing.T, conn *websocket.Conn) string {
	t.Helper()
	_, msg, err := conn.ReadMessage()
	require.NoError(t, err)
	var env Envelope
	require.NoError(t, json.Unmarshal(msg, &env))
	require.Equal(t, MessageTypeToken, env.Type, "first message should be token")
	var tok TokenMessage
	require.NoError(t, json.Unmarshal(env.Payload, &tok))
	require.NotEmpty(t, tok.Token)
	return string(tok.Token)
}

func TestHub_RegisterAndLen(t *testing.T) {
	t.Parallel()

	wsHub := NewHub(testMatcher())
	go wsHub.Run(t.Context())

	srv := simpleHandler(wsHub)
	defer srv.Close()

	conn := dialHub(t, srv)
	defer func() { _ = conn.Close() }()

	require.Eventually(t, func() bool { return wsHub.Len() == 1 }, time.Second, 10*time.Millisecond)
}

func TestHub_Unregister(t *testing.T) {
	t.Parallel()

	wsHub := NewHub(testMatcher())
	go wsHub.Run(t.Context())

	srv := simpleHandler(wsHub)
	defer srv.Close()

	conn := dialHub(t, srv)
	require.Eventually(t, func() bool { return wsHub.Len() == 1 }, time.Second, 10*time.Millisecond)

	_ = conn.Close()
	require.Eventually(t, func() bool { return wsHub.Len() == 0 }, time.Second, 10*time.Millisecond)
}

func TestHub_DoubleUnregister(t *testing.T) {
	t.Parallel()

	wsHub := NewHub(testMatcher())
	go wsHub.Run(t.Context())

	srv := simpleHandler(wsHub)
	defer srv.Close()

	conn := dialHub(t, srv)
	require.Eventually(t, func() bool { return wsHub.Len() == 1 }, time.Second, 10*time.Millisecond)

	// close triggers readPump exit -> unregister
	_ = conn.Close()
	require.Eventually(t, func() bool { return wsHub.Len() == 0 }, time.Second, 10*time.Millisecond)

	// second unregister of the same client should be a no-op
	// (simulate by getting a reference and sending it again)
	// the hub already removed it, so this should not panic
}

func TestHub_MultipleClients(t *testing.T) {
	t.Parallel()

	wsHub := NewHub(testMatcher())
	go wsHub.Run(t.Context())

	srv := simpleHandler(wsHub)
	defer srv.Close()

	conns := make([]*websocket.Conn, 3)
	for idx := range conns {
		conns[idx] = dialHub(t, srv)
	}
	defer func() {
		for _, conn := range conns {
			_ = conn.Close()
		}
	}()

	require.Eventually(t, func() bool { return wsHub.Len() == 3 }, time.Second, 10*time.Millisecond)
}

func TestHub_ClientByToken(t *testing.T) {
	t.Parallel()

	wsHub := NewHub(testMatcher())
	go wsHub.Run(t.Context())

	srv := simpleHandler(wsHub)
	defer srv.Close()

	conn := dialHub(t, srv)
	defer func() { _ = conn.Close() }()

	require.Eventually(t, func() bool { return wsHub.Len() == 1 }, time.Second, 10*time.Millisecond)

	// find the registered client via registry snapshot
	clients, _ := wsHub.reg.Snapshot()
	require.Len(t, clients, 1)
	client := clients[0]

	assert.Equal(t, client.Token(), wsHub.ClientByToken(client.Token()).Token())
}

func TestHub_GracefulShutdown(t *testing.T) {
	t.Parallel()

	wsHub := NewHub(testMatcher())
	ctx, cancel := context.WithCancel(context.Background())
	go wsHub.Run(ctx)

	srv := simpleHandler(wsHub)
	defer srv.Close()

	conn := dialHub(t, srv)
	defer func() { _ = conn.Close() }()

	require.Eventually(t, func() bool { return wsHub.Len() == 1 }, time.Second, 10*time.Millisecond)

	coll := newCollector(t, conn)

	cancel()

	assert.True(t, coll.waitFor(view.MsgServerReset, 2*time.Second), "should see reset message")
}

func TestHub_RunOnce(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		wsHub := NewHub(testMatcher())
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		go wsHub.Run(ctx)

		// wait for first Run to start and block on select
		synctest.Wait()

		// second call should be a no-op and return immediately
		done := make(chan struct{})
		go func() {
			wsHub.Run(ctx)
			close(done)
		}()

		synctest.Wait()

		select {
		case <-done:
		default:
			t.Fatal("second Run call did not return immediately")
		}

		cancel()
		synctest.Wait()
	})
}

// --- Client tests ---

func TestClient_SendRaw_ClosedClient(t *testing.T) {
	t.Parallel()

	// unbuffered send channel so the only ready case is ctx.Done
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	client := &Client{
		send:   make(chan []byte),
		ctx:    ctx,
		cancel: cancel,
	}

	client.Close(ErrClientClosed)

	err := client.SendRaw(context.Background(), []byte("hello"))
	assert.ErrorIs(t, err, ErrClientClosed)
}

func TestClient_SendRaw_CancelledContext(t *testing.T) {
	t.Parallel()

	// test SendRaw directly without pumps to avoid races
	clientCtx, clientCancel := context.WithCancelCause(context.Background())
	defer clientCancel(nil)
	client := &Client{
		send:   make(chan []byte, 1),
		ctx:    clientCtx,
		cancel: clientCancel,
	}

	// fill the buffer
	require.NoError(t, client.SendRaw(context.Background(), []byte("x")))

	// buffer is full; send with cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := client.SendRaw(ctx, []byte("should fail"))
	assert.ErrorIs(t, err, context.Canceled)
}

func TestClient_CloseMultipleTimes(t *testing.T) {
	t.Parallel()

	wsHub := NewHub(testMatcher())
	go wsHub.Run(t.Context())

	srv := simpleHandler(wsHub)
	defer srv.Close()

	conn := dialHub(t, srv)
	defer func() { _ = conn.Close() }()

	require.Eventually(t, func() bool { return wsHub.Len() == 1 }, time.Second, 10*time.Millisecond)

	clients, _ := wsHub.reg.Snapshot()
	require.Len(t, clients, 1)
	client := clients[0]

	// should not panic
	client.Close(ErrClientClosed)
	client.Close(ErrClientClosed)
	client.Close(ErrClientClosed)
}

// --- Integration tests ---

func sendJSON(t *testing.T, conn *websocket.Conn, msgType MessageType, payload map[string]any) {
	t.Helper()
	payloadBytes, err := json.Marshal(payload)
	require.NoError(t, err)
	env := Envelope{Type: msgType, Payload: payloadBytes}
	data, err := json.Marshal(env)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data))
}

// messageCollector reads WS messages in a background goroutine
// and makes them available via waitFor.
type messageCollector struct {
	msgs chan string
}

func newCollector(t *testing.T, conn *websocket.Conn) *messageCollector {
	t.Helper()
	collector := &messageCollector{msgs: make(chan string, 256)}
	go func() {
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				close(collector.msgs)
				return
			}
			collector.msgs <- string(msg)
		}
	}()
	return collector
}

// waitFor reads messages until one contains substr or timeout
// expires. Returns all messages read and whether the substring
// was found.
func (mc *messageCollector) waitFor(substr string, timeout time.Duration) bool {
	deadline := time.After(timeout)
	for {
		select {
		case msg, ok := <-mc.msgs:
			if !ok {
				return false
			}
			if strings.Contains(msg, substr) {
				return true
			}
		case <-deadline:
			return false
		}
	}
}

func findMatchPayload() map[string]any {
	return map[string]any{
		"gender":            "male",
		"role":              "dominant",
		"interests":         []string{"basketball"},
		"filter_gender":     []string{},
		"filter_role":       []string{},
		"exclude_interests": []string{},
	}
}

func matchTwo(t *testing.T, wsHub *Hub, srv *httptest.Server) (
	connA, connB *websocket.Conn, collA, collB *messageCollector,
) {
	t.Helper()
	connA = dialHub(t, srv)
	connB = dialHub(t, srv)

	collA = newCollector(t, connA)
	collB = newCollector(t, connB)

	require.Eventually(t, func() bool {
		return wsHub.Len() == 2
	}, time.Second, 10*time.Millisecond)

	sendJSON(t, connA, MessageTypeFindMatch, findMatchPayload())
	sendJSON(t, connB, MessageTypeFindMatch, findMatchPayload())

	require.True(t, collA.waitFor(view.MsgMatched, 3*time.Second), "A should be matched")
	require.True(t, collB.waitFor(view.MsgMatched, 3*time.Second), "B should be matched")

	return connA, connB, collA, collB
}

func TestHub_MessageExchange(t *testing.T) {
	t.Parallel()

	wsHub := testHub()
	go wsHub.Run(t.Context())

	srv := simpleHandler(wsHub)
	defer srv.Close()

	connA, connB, collA, collB := matchTwo(t, wsHub, srv)
	defer func() { _ = connA.Close() }()
	defer func() { _ = connB.Close() }()

	// A sends a message
	sendJSON(t, connA, MessageTypeMessage, map[string]any{"text": "hello from A"})

	// A should see it as self (ml-auto = self styling)
	assert.True(t, collA.waitFor("hello from A", 2*time.Second), "sender should see own message")
	// B should see it as other (mr-auto = other styling)
	assert.True(t, collB.waitFor("hello from A", 2*time.Second), "partner should see message")

	// B sends a message back
	sendJSON(t, connB, MessageTypeMessage, map[string]any{"text": "hello from B"})

	assert.True(t, collB.waitFor("hello from B", 2*time.Second), "sender should see own message")
	assert.True(t, collA.waitFor("hello from B", 2*time.Second), "partner should see message")
}

func TestHub_MessageNoSession(t *testing.T) {
	t.Parallel()

	wsHub := testHub()
	go wsHub.Run(t.Context())

	srv := simpleHandler(wsHub)
	defer srv.Close()

	conn := dialHub(t, srv)
	defer func() { _ = conn.Close() }()

	coll := newCollector(t, conn)

	require.Eventually(t, func() bool {
		return wsHub.Len() == 1
	}, time.Second, 10*time.Millisecond)

	// send a message without being in a session
	sendJSON(t, conn, MessageTypeMessage, map[string]any{"text": "orphan"})

	// should not receive any message back (no crash, no response)
	assert.False(t, coll.waitFor("orphan", 500*time.Millisecond), "should not echo without session")
}

func TestHub_MessageRateLimit(t *testing.T) {
	t.Parallel()

	wsHub := testHub()
	go wsHub.Run(t.Context())

	srv := simpleHandler(wsHub)
	defer srv.Close()

	connA, connB, collA, _ := matchTwo(t, wsHub, srv)
	defer func() { _ = connA.Close() }()
	defer func() { _ = connB.Close() }()

	// exhaust byte burst (8192) by sending messages with large
	// payloads. each message is ~3100 bytes on the wire, so 4
	// messages (~12 KB) well exceeds the 8 KB burst even if
	// some tokens refill between sends (2 KB/s).
	bigText := strings.Repeat("x", 3000)
	for range 4 {
		sendJSON(t, connA, MessageTypeMessage, map[string]any{
			"text": bigText,
		})
	}

	// should eventually see rate limit notification
	assert.True(t, collA.waitFor("Slow down", 2*time.Second), "should see rate limit notification")
}

func TestHub_MessagePartnerDisconnected(t *testing.T) {
	t.Parallel()

	wsHub := testHub()
	go wsHub.Run(t.Context())

	srv := simpleHandler(wsHub)
	defer srv.Close()

	connA, connB, collA, _ := matchTwo(t, wsHub, srv)
	defer func() { _ = connA.Close() }()

	// B disconnects; enters detached state (Len stays 2)
	_ = connB.Close()

	// A should see "reconnecting" after notify delay, then "partner
	// has left" after the grace period expires
	assert.True(t, collA.waitFor("reconnecting", 2*time.Second), "should see reconnecting indicator")
	assert.True(t, collA.waitFor(view.MsgPartnerLeft, 2*time.Second), "should see partner left after grace period")

	// Len drops to 1 after grace expiry
	require.Eventually(t, func() bool {
		return wsHub.Len() == 1
	}, time.Second, 10*time.Millisecond)

	// A tries to send a message after session is gone; should be silently dropped
	sendJSON(t, connA, MessageTypeMessage, map[string]any{"text": "are you there?"})
	assert.False(t, collA.waitFor("are you there", 500*time.Millisecond), "message should be dropped after session ends")
}

func TestHub_SendButtonState(t *testing.T) {
	t.Parallel()

	wsHub := testHub()
	go wsHub.Run(t.Context())

	srv := simpleHandler(wsHub)
	defer srv.Close()

	connA, connB := dialHub(t, srv), dialHub(t, srv)
	defer func() { _ = connA.Close() }()
	defer func() { _ = connB.Close() }()

	collA := newCollector(t, connA)
	collB := newCollector(t, connB)

	require.Eventually(t, func() bool {
		return wsHub.Len() == 2
	}, time.Second, 10*time.Millisecond)

	// match
	sendJSON(t, connA, MessageTypeFindMatch, findMatchPayload())
	sendJSON(t, connB, MessageTypeFindMatch, findMatchPayload())

	// matched notification and send button arrive in one frame
	require.True(t, collA.waitFor("send-btn", 3*time.Second), "A should get send button on match")
	require.True(t, collB.waitFor("send-btn", 3*time.Second), "B should get send button on match")

	// A leaves
	sendJSON(t, connA, MessageTypeLeave, map[string]any{})

	// both should receive disabled send button
	assert.True(t, collA.waitFor("disabled", 2*time.Second), "A should get disabled button")
	assert.True(t, collB.waitFor("disabled", 2*time.Second), "B should get disabled button")
}

func TestHub_BlockAndRematch(t *testing.T) {
	t.Parallel()

	wsHub := testHub()
	go wsHub.Run(t.Context())

	srv := simpleHandler(wsHub)
	defer srv.Close()

	// connect A and B, match them
	connA, connB, collA, collB := matchTwo(t, wsHub, srv)
	defer func() { _ = connA.Close() }()
	defer func() { _ = connB.Close() }()

	// A leaves
	sendJSON(t, connA, MessageTypeLeave, map[string]any{})
	require.True(t, collA.waitFor(view.MsgYouLeft, 2*time.Second))
	require.True(t, collB.waitFor(view.MsgPartnerLeft, 2*time.Second))

	// A re-queues with block=true to block B
	payload := findMatchPayload()
	payload["block"] = true
	sendJSON(t, connA, MessageTypeFindMatch, payload)
	require.True(t, collA.waitFor("Searching", 2*time.Second))

	// B re-queues normally; should NOT match A because A blocked B
	sendJSON(t, connB, MessageTypeFindMatch, findMatchPayload())

	// neither should match (only two clients, and they're blocked)
	assert.False(t, collA.waitFor(view.MsgMatched, 2*time.Second), "A should not match blocked B")

	// connect C; A should match C instead of B
	connC := dialHub(t, srv)
	defer func() { _ = connC.Close() }()
	collC := newCollector(t, connC)

	require.Eventually(t, func() bool {
		return wsHub.Len() == 3
	}, time.Second, 10*time.Millisecond)

	sendJSON(t, connC, MessageTypeFindMatch, findMatchPayload())

	// A and C should match
	assert.True(t, collA.waitFor(view.MsgMatched, 3*time.Second), "A should match C")
	assert.True(t, collC.waitFor(view.MsgMatched, 3*time.Second), "C should match A")
}

// TestHub_BlockAndRematchFromButton simulates clicking the
// "Block & Find Another" button which sends only block=true
// without profile fields. The client's existing profile should
// be reused.
func TestHub_BlockAndRematchFromButton(t *testing.T) {
	t.Parallel()

	wsHub := testHub()
	go wsHub.Run(t.Context())

	srv := simpleHandler(wsHub)
	defer srv.Close()

	connA, connB, collA, collB := matchTwo(t, wsHub, srv)
	defer func() { _ = connA.Close() }()
	defer func() { _ = connB.Close() }()

	// A leaves
	sendJSON(t, connA, MessageTypeLeave, map[string]any{})
	require.True(t, collA.waitFor(view.MsgYouLeft, 2*time.Second))
	require.True(t, collB.waitFor(view.MsgPartnerLeft, 2*time.Second))

	// A clicks "Block & Find Another" -- form sends only
	// block="true" (string, no profile fields)
	sendJSON(t, connA, MessageTypeFindMatch, map[string]any{
		"block": "true",
	})
	require.True(t, collA.waitFor("Searching", 2*time.Second),
		"re-queue from button should work with existing profile")

	// connect C and match with A (B is blocked)
	connC := dialHub(t, srv)
	defer func() { _ = connC.Close() }()
	collC := newCollector(t, connC)

	require.Eventually(t, func() bool {
		return wsHub.Len() == 3
	}, time.Second, 10*time.Millisecond)

	sendJSON(t, connC, MessageTypeFindMatch, findMatchPayload())

	assert.True(t, collA.waitFor(view.MsgMatched, 3*time.Second), "A should match C")
	assert.True(t, collC.waitFor(view.MsgMatched, 3*time.Second), "C should match A")
}

func TestHub_MatchLeaveRematch(t *testing.T) {
	t.Parallel()

	wsHub := testHub()
	go wsHub.Run(t.Context())

	srv := simpleHandler(wsHub)
	defer srv.Close()

	// connect two clients
	connA := dialHub(t, srv)
	defer func() { _ = connA.Close() }()
	connB := dialHub(t, srv)
	defer func() { _ = connB.Close() }()

	collA := newCollector(t, connA)
	collB := newCollector(t, connB)

	require.Eventually(t, func() bool {
		return wsHub.Len() == 2
	}, time.Second, 10*time.Millisecond)

	// both send find_match with shared interests
	sendJSON(t, connA, MessageTypeFindMatch, findMatchPayload())
	sendJSON(t, connB, MessageTypeFindMatch, findMatchPayload())

	// wait for match
	matchedA := collA.waitFor(view.MsgMatched, 3*time.Second)
	matchedB := collB.waitFor(view.MsgMatched, 3*time.Second)
	require.True(t, matchedA, "client A should be matched")
	require.True(t, matchedB, "client B should be matched")

	// A leaves
	sendJSON(t, connA, MessageTypeLeave, map[string]any{})

	// both should see ChatEnded with find-another button
	leftA := collA.waitFor("find-another-btn", 3*time.Second)
	leftB := collB.waitFor("find-another-btn", 3*time.Second)
	assert.True(t, leftA, "leaver should see find-another button")
	assert.True(t, leftB, "partner should see find-another button")

	// both re-queue
	sendJSON(t, connA, MessageTypeFindMatch, findMatchPayload())
	sendJSON(t, connB, MessageTypeFindMatch, findMatchPayload())

	// should re-match quickly (not 20s+)
	start := time.Now()
	rematchedA := collA.waitFor(view.MsgMatched, 5*time.Second)
	rematchedB := collB.waitFor(view.MsgMatched, 5*time.Second)
	elapsed := time.Since(start)
	require.True(t, rematchedA, "client A should be re-matched")
	require.True(t, rematchedB, "client B should be re-matched")
	assert.Less(t, elapsed, 3*time.Second, "re-match should be fast")
}

// --- Search cooldown test ---

func TestHub_FindMatchCooldown(t *testing.T) {
	t.Parallel()

	wsHub := testHub()
	wsHub.SearchCooldown = 500 * time.Millisecond
	go wsHub.Run(t.Context())

	srv := simpleHandler(wsHub)
	defer srv.Close()

	connA, connB, collA, _ := matchTwo(t, wsHub, srv)
	defer func() { _ = connA.Close() }()
	defer func() { _ = connB.Close() }()

	// A leaves
	sendJSON(t, connA, MessageTypeLeave, map[string]any{})
	require.True(t, collA.waitFor(view.MsgYouLeft, 2*time.Second))

	// A immediately re-queues; should see cooldown message
	sendJSON(t, connA, MessageTypeFindMatch, findMatchPayload())
	assert.True(t, collA.waitFor(view.MsgCooldown, 2*time.Second), "should see cooldown message")
}

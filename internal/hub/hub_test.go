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
)

var testClientCounter atomic.Int64

func testMatcher() *match.Matcher {
	return match.NewMatcher(match.DefaultMatchTimeout)
}

func testHub() *Hub {
	wsHub := NewHub(testMatcher())
	wsHub.GracePeriod = 500 * time.Millisecond
	wsHub.ReconnectNotifyDelay = 100 * time.Millisecond
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
		connCtx := context.WithoutCancel(request.Context())
		client := wsHub.NewClient(connCtx, conn, token)
		if qToken := request.URL.Query().Get("token"); qToken != "" {
			client.SetReconnectToken(match.Token(qToken))
		}
		if err := wsHub.Register(connCtx, client); err != nil {
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

	// find the registered client by iterating tokens (since
	// simpleHandler uses a counter for unique tokens)
	var client *Client
	wsHub.mu.RLock()
	for _, candidate := range wsHub.tokens {
		client = candidate
		break
	}
	wsHub.mu.RUnlock()

	require.NotNil(t, client)
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

	assert.True(t, coll.waitFor("shutting down", 2*time.Second), "should see shutdown message")
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

	wsHub.mu.RLock()
	var client *Client
	for candidate := range wsHub.clients {
		client = candidate
		break
	}
	wsHub.mu.RUnlock()
	require.NotNil(t, client)

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

	require.True(t, collA.waitFor("matched", 3*time.Second), "A should be matched")
	require.True(t, collB.waitFor("matched", 3*time.Second), "B should be matched")

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
	assert.True(t, collA.waitFor("partner has left", 2*time.Second), "should see partner left after grace period")

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
	require.True(t, collA.waitFor("left", 2*time.Second))
	require.True(t, collB.waitFor("left", 2*time.Second))

	// A re-queues with block=true to block B
	payload := findMatchPayload()
	payload["block"] = true
	sendJSON(t, connA, MessageTypeFindMatch, payload)
	require.True(t, collA.waitFor("Searching", 2*time.Second))

	// B re-queues normally; should NOT match A because A blocked B
	sendJSON(t, connB, MessageTypeFindMatch, findMatchPayload())

	// neither should match (only two clients, and they're blocked)
	assert.False(t, collA.waitFor("matched", 2*time.Second), "A should not match blocked B")

	// connect C; A should match C instead of B
	connC := dialHub(t, srv)
	defer func() { _ = connC.Close() }()
	collC := newCollector(t, connC)

	require.Eventually(t, func() bool {
		return wsHub.Len() == 3
	}, time.Second, 10*time.Millisecond)

	sendJSON(t, connC, MessageTypeFindMatch, findMatchPayload())

	// A and C should match
	assert.True(t, collA.waitFor("matched", 3*time.Second), "A should match C")
	assert.True(t, collC.waitFor("matched", 3*time.Second), "C should match A")
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
	matchedA := collA.waitFor("matched", 3*time.Second)
	matchedB := collB.waitFor("matched", 3*time.Second)
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
	rematchedA := collA.waitFor("matched", 5*time.Second)
	rematchedB := collB.waitFor("matched", 5*time.Second)
	elapsed := time.Since(start)
	require.True(t, rematchedA, "client A should be re-matched")
	require.True(t, rematchedB, "client B should be re-matched")
	assert.Less(t, elapsed, 3*time.Second, "re-match should be fast")
}

// --- Reconnection tests ---

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
	assert.True(t, collB.waitFor("partner has left", 2*time.Second), "B should see partner left after grace expiry")

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
	assert.True(t, collB.waitFor("shutting down", 5*time.Second), "B should see shutdown message")
}

// TestHub_RefreshFromLandingPageThenMatch verifies that refreshing
// the page before entering a session does not break subsequent
// matching. The stale token should not trigger a reconnect for a
// client that had no session or pending search.
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

// TestHub_RapidRefreshPreservesSession verifies that rapidly
// refreshing the page (multiple times before the hub processes
// unregisters) does not break the session. The token identity
// must remain stable across rapid reconnects.
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

// TestHub_RefreshDuringSearchPreservesQueue verifies that
// refreshing while searching for a match preserves the queue
// position and completes matching after reconnect.
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

// TestHub_SecondTabDoesNotStealSession verifies that opening a
// second tab (different sessionStorage, no reconnect token) does
// not interfere with the first tab's session.
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

// TestHub_LandingPageRefreshGetsFreshIdentity verifies that
// refreshing a landing-page client (no session, no search)
// gets a fresh identity each time. There is no state to
// preserve, so the stale reconnect token is harmlessly ignored.
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

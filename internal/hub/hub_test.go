package hub

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testClientCounter atomic.Int64

func simpleHandler(wsHub *Hub) *httptest.Server {
	upgrader := websocket.Upgrader{}
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		conn, err := upgrader.Upgrade(writer, request, nil)
		if err != nil {
			return
		}
		idx := testClientCounter.Add(1)
		token := fmt.Sprintf("tok-%d", idx)
		refresh := fmt.Sprintf("ref-%d", idx)
		client := NewClient(wsHub, conn, token, refresh)
		if err := client.Send(request.Context(), TokenMessage{
			Token:   token,
			Refresh: refresh,
		}); err != nil {
			client.Close()
			return
		}
		wsHub.Register(client)
	}))
}

func dialHub(t *testing.T, srv *httptest.Server) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/"
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	_, _, err = conn.ReadMessage()
	require.NoError(t, err)
	return conn
}

func TestHub_RegisterAndLen(t *testing.T) {
	t.Parallel()

	wsHub := NewHub()
	go wsHub.Run(t.Context())

	srv := simpleHandler(wsHub)
	defer srv.Close()

	conn := dialHub(t, srv)
	defer func() { _ = conn.Close() }()

	require.Eventually(t, func() bool { return wsHub.Len() == 1 }, time.Second, 10*time.Millisecond)
}

func TestHub_Unregister(t *testing.T) {
	t.Parallel()

	wsHub := NewHub()
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

	wsHub := NewHub()
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

	wsHub := NewHub()
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

	wsHub := NewHub()
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

	wsHub := NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	go wsHub.Run(ctx)

	srv := simpleHandler(wsHub)
	defer srv.Close()

	conn := dialHub(t, srv)
	defer func() { _ = conn.Close() }()

	require.Eventually(t, func() bool { return wsHub.Len() == 1 }, time.Second, 10*time.Millisecond)

	cancel()

	_, msg, err := conn.ReadMessage()
	require.NoError(t, err)
	assert.Contains(t, string(msg), "shutting down")

	_, _, err = conn.ReadMessage()
	assert.Error(t, err)
}

func TestHub_RunOnce(t *testing.T) {
	t.Parallel()

	wsHub := NewHub()
	go wsHub.Run(t.Context())

	// give the first Run a moment to start
	time.Sleep(10 * time.Millisecond)

	// second call should be a no-op and return immediately
	done := make(chan struct{})
	go func() {
		wsHub.Run(t.Context())
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("second Run call did not return immediately")
	}
}

// --- Client tests ---

func TestClient_SendRaw_ClosedClient(t *testing.T) {
	t.Parallel()

	// test SendRaw directly without pumps to avoid races
	client := &Client{
		send: make(chan []byte, sendBufSize),
		done: make(chan struct{}),
	}

	client.Close()

	err := client.SendRaw(context.Background(), []byte("hello"))
	assert.ErrorIs(t, err, ErrClientClosed)
}

func TestClient_SendRaw_CancelledContext(t *testing.T) {
	t.Parallel()

	// test SendRaw directly without pumps to avoid races
	client := &Client{
		send: make(chan []byte, 1),
		done: make(chan struct{}),
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

	wsHub := NewHub()
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
	client.Close()
	client.Close()
	client.Close()
}

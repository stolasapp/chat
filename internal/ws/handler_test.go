package ws

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"

	"github.com/stolasapp/chat/internal/hub"
)

func dialTestServer(t *testing.T, limiter *IPLimiter) (*hub.Hub, *websocket.Conn, func()) {
	t.Helper()

	wsHub := hub.NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	go wsHub.Run(ctx)

	srv := httptest.NewServer(NewHandler(wsHub, limiter, nil))
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/"

	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}

	cleanup := func() {
		_ = conn.Close()
		cancel()
		srv.Close()
	}
	return wsHub, conn, cleanup
}

func readTokenMessage(t *testing.T, conn *websocket.Conn) hub.TokenMessage {
	t.Helper()
	_, msg, err := conn.ReadMessage()
	require.NoError(t, err)

	var env hub.Envelope
	require.NoError(t, json.Unmarshal(msg, &env))
	require.Equal(t, hub.MessageTypeToken, env.Type)

	var token hub.TokenMessage
	require.NoError(t, json.Unmarshal(env.Payload, &token))
	return token
}

func TestHandler_TokenOnConnect(t *testing.T) {
	t.Parallel()

	limiter := NewIPLimiter(rate.Limit(10), 10)
	_, conn, cleanup := dialTestServer(t, limiter)
	defer cleanup()

	token := readTokenMessage(t, conn)

	assert.Len(t, token.Token, 16, "token should be 16 hex chars")
	assert.Len(t, token.Refresh, 16, "refresh should be 16 hex chars")
	assert.NotEqual(t, token.Token, token.Refresh, "token and refresh should differ")
}

func TestHandler_ClientRegistered(t *testing.T) {
	t.Parallel()

	limiter := NewIPLimiter(rate.Limit(10), 10)
	wsHub, conn, cleanup := dialTestServer(t, limiter)
	defer cleanup()

	readTokenMessage(t, conn)

	require.Eventually(t, func() bool { return wsHub.Len() == 1 }, time.Second, 10*time.Millisecond)
}

func TestHandler_UniqueTokens(t *testing.T) {
	t.Parallel()

	limiter := NewIPLimiter(rate.Limit(100), 100)
	wsHub := hub.NewHub()
	go wsHub.Run(t.Context())

	srv := httptest.NewServer(NewHandler(wsHub, limiter, nil))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/"

	tokens := make(map[string]struct{})
	conns := make([]*websocket.Conn, 5)
	for idx := range conns {
		conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
		require.NoError(t, err)
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		conns[idx] = conn

		tok := readTokenMessage(t, conn)
		tokens[tok.Token] = struct{}{}
		tokens[tok.Refresh] = struct{}{}
	}
	for _, conn := range conns {
		_ = conn.Close()
	}

	assert.Len(t, tokens, 10, "all tokens should be unique")
}

func TestHandler_RateLimited(t *testing.T) {
	t.Parallel()

	limiter := NewIPLimiter(rate.Limit(0.1), 1)
	wsHub := hub.NewHub()
	go wsHub.Run(t.Context())

	srv := httptest.NewServer(NewHandler(wsHub, limiter, nil))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/"

	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	defer func() { _ = conn.Close() }()

	conn2, resp2, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err, "upgrade should succeed even when rate limited")
	if resp2 != nil && resp2.Body != nil {
		_ = resp2.Body.Close()
	}
	defer func() { _ = conn2.Close() }()

	_, msg, err := conn2.ReadMessage()
	require.NoError(t, err)

	var env hub.Envelope
	require.NoError(t, json.Unmarshal(msg, &env))
	assert.Equal(t, hub.MessageTypeRateLimited, env.Type)

	var rateLimited hub.RateLimitedMessage
	require.NoError(t, json.Unmarshal(env.Payload, &rateLimited))
	assert.Greater(t, rateLimited.RetryAfter, time.Duration(0))

	_, _, err = conn2.ReadMessage()
	assert.Error(t, err)
}

func TestHandler_CheckOrigin_AllowedList(t *testing.T) {
	t.Parallel()

	limiter := NewIPLimiter(rate.Limit(100), 100)
	wsHub := hub.NewHub()
	go wsHub.Run(t.Context())

	allowed := []string{"http://example.com", "https://chat.example.com"}
	srv := httptest.NewServer(NewHandler(wsHub, limiter, allowed))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/"

	header := http.Header{"Origin": []string{"http://example.com"}}
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, header)
	require.NoError(t, err)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	_ = conn.Close()

	header = http.Header{"Origin": []string{"http://evil.com"}}
	_, resp, err = websocket.DefaultDialer.Dial(wsURL, header)
	require.Error(t, err)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestHandler_CheckOrigin_SameHost(t *testing.T) {
	t.Parallel()

	limiter := NewIPLimiter(rate.Limit(100), 100)
	wsHub := hub.NewHub()
	go wsHub.Run(t.Context())

	srv := httptest.NewServer(NewHandler(wsHub, limiter, nil))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/"

	srvHost := strings.TrimPrefix(srv.URL, "http://")
	header := http.Header{"Origin": []string{"http://" + srvHost}}
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, header)
	require.NoError(t, err)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	_ = conn.Close()

	header = http.Header{"Origin": []string{"http://evil.com"}}
	_, resp, err = websocket.DefaultDialer.Dial(wsURL, header)
	require.Error(t, err)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestRealIP(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		xff      string
		xri      string
		remote   string
		expected string
	}{
		{
			name:     "x-forwarded-for single",
			xff:      "1.2.3.4",
			expected: "1.2.3.4",
		},
		{
			name:     "x-forwarded-for multiple",
			xff:      "1.2.3.4, 5.6.7.8",
			expected: "1.2.3.4",
		},
		{
			name:     "x-real-ip",
			xri:      "9.8.7.6",
			expected: "9.8.7.6",
		},
		{
			name:     "xff takes precedence over xri",
			xff:      "1.1.1.1",
			xri:      "2.2.2.2",
			expected: "1.1.1.1",
		},
		{
			name:     "remote addr with port",
			remote:   "10.0.0.1:12345",
			expected: "10.0.0.1",
		},
		{
			name:     "remote addr without port",
			remote:   "10.0.0.1",
			expected: "10.0.0.1",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := &http.Request{
				RemoteAddr: test.remote,
				Header:     http.Header{},
			}
			if test.xff != "" {
				request.Header.Set("X-Forwarded-For", test.xff)
			}
			if test.xri != "" {
				request.Header.Set("X-Real-Ip", test.xri)
			}
			assert.Equal(t, test.expected, RealIP(request))
		})
	}
}

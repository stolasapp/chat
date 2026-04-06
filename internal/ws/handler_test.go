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

	"github.com/stolasapp/chat/internal/hub"
	"github.com/stolasapp/chat/internal/match"
)

func dialTestServer(t *testing.T) (*hub.Hub, string, func()) {
	t.Helper()

	wsHub := hub.NewHub(match.NewMatcher(match.DefaultMatchTimeout))
	ctx, cancel := context.WithCancel(context.Background())
	go wsHub.Run(ctx)

	srv := httptest.NewServer(NewHandler(wsHub, nil))
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"

	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}

	// read the first message which should be a TokenMessage
	_, msg, err := conn.ReadMessage()
	require.NoError(t, err)
	var env hub.Envelope
	require.NoError(t, json.Unmarshal(msg, &env))
	require.Equal(t, hub.MessageTypeToken, env.Type)
	var tok hub.TokenMessage
	require.NoError(t, json.Unmarshal(env.Payload, &tok))
	require.NotEmpty(t, tok.Token)

	cleanup := func() {
		_ = conn.Close()
		cancel()
		srv.Close()
	}
	return wsHub, string(tok.Token), cleanup
}

func TestHandler_TokenOnConnect(t *testing.T) {
	t.Parallel()

	_, token, cleanup := dialTestServer(t)
	defer cleanup()

	assert.Len(t, token, 32, "session token should be 32 hex chars")
}

func TestHandler_ClientRegistered(t *testing.T) {
	t.Parallel()

	wsHub, _, cleanup := dialTestServer(t)
	defer cleanup()

	assert.Equal(t, 1, wsHub.Len())
}

func TestHandler_UniqueTokens(t *testing.T) {
	t.Parallel()

	wsHub := hub.NewHub(match.NewMatcher(match.DefaultMatchTimeout))
	go wsHub.Run(t.Context())

	srv := httptest.NewServer(NewHandler(wsHub, nil))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"

	// open and close connections sequentially so each gets a
	// unique token before the next connects
	tokens := make(map[string]struct{})
	for range 5 {
		conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
		require.NoError(t, err)
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		// read TokenMessage to get the token
		_, msg, err := conn.ReadMessage()
		require.NoError(t, err)
		var env hub.Envelope
		require.NoError(t, json.Unmarshal(msg, &env))
		var tok hub.TokenMessage
		require.NoError(t, json.Unmarshal(env.Payload, &tok))
		tokens[string(tok.Token)] = struct{}{}
		_ = conn.Close()
	}

	assert.Len(t, tokens, 5, "all session tokens should be unique")
}

func TestHandler_RateLimited(t *testing.T) {
	t.Parallel()

	wsHub := hub.NewHub(match.NewMatcher(match.DefaultMatchTimeout))
	go wsHub.Run(t.Context())

	handler := NewHandler(wsHub, nil)
	srv := httptest.NewServer(handler)
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"

	// exhaust the burst (ipBurst = 5)
	conns := make([]*websocket.Conn, ipBurst)
	for idx := range conns {
		conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
		require.NoError(t, err)
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		conns[idx] = conn
	}
	defer func() {
		for _, conn := range conns {
			_ = conn.Close()
		}
	}()

	// next connection should be rate limited
	rateLimitedConn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err, "upgrade should succeed even when rate limited")
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	defer func() { _ = rateLimitedConn.Close() }()

	_, msg, err := rateLimitedConn.ReadMessage()
	require.NoError(t, err)

	var env hub.Envelope
	require.NoError(t, json.Unmarshal(msg, &env))
	assert.Equal(t, hub.MessageTypeRateLimited, env.Type)

	var rateLimited hub.RateLimitedMessage
	require.NoError(t, json.Unmarshal(env.Payload, &rateLimited))
	assert.Greater(t, rateLimited.RetryAfter, time.Duration(0))

	_, _, err = rateLimitedConn.ReadMessage()
	assert.Error(t, err)
}

func TestHandler_CheckOrigin_AllowedList(t *testing.T) {
	t.Parallel()

	wsHub := hub.NewHub(match.NewMatcher(match.DefaultMatchTimeout))
	go wsHub.Run(t.Context())

	allowed := []string{"http://example.com", "https://chat.example.com"}
	srv := httptest.NewServer(NewHandler(wsHub, allowed))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"

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

	wsHub := hub.NewHub(match.NewMatcher(match.DefaultMatchTimeout))
	go wsHub.Run(t.Context())

	srv := httptest.NewServer(NewHandler(wsHub, nil))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"

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
			name:     "x-real-ip trusted",
			xri:      "9.8.7.6",
			expected: "9.8.7.6",
		},
		{
			name:     "xff ignored in favor of xri",
			xff:      "1.1.1.1",
			xri:      "2.2.2.2",
			expected: "2.2.2.2",
		},
		{
			name:     "xff alone is ignored",
			xff:      "1.2.3.4",
			remote:   "10.0.0.1:12345",
			expected: "10.0.0.1",
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

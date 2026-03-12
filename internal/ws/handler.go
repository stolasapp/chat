package ws

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/stolasapp/chat/internal/hub"
)

const (
	tokenBytes       = 8 // 64 bits per token
	wsReadBufSize    = 1024
	wsWriteBufSize   = 1024
	rateLimitBackoff = 30 * time.Second
)

// NewHandler returns an http.Handler that upgrades connections to
// WebSocket and registers them with the Hub. allowedOrigins
// specifies which Origin header values are permitted. If empty,
// the handler falls back to same-host checking (Origin host must
// match the Host header).
func NewHandler(wsHub *hub.Hub, limiter *IPLimiter, allowedOrigins []string) http.Handler {
	upgrader := websocket.Upgrader{
		ReadBufferSize:  wsReadBufSize,
		WriteBufferSize: wsWriteBufSize,
		CheckOrigin:     checkOrigin(allowedOrigins),
	}

	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		ipAddr := RealIP(request)

		conn, err := upgrader.Upgrade(writer, request, nil)
		if err != nil {
			slog.Warn("ws upgrade failed", "error", err)
			return
		}

		if !limiter.Allow(ipAddr) {
			if err := writeEnvelope(conn, &hub.RateLimitedMessage{
				RetryAfter: rateLimitBackoff,
			}); err != nil {
				slog.Warn("failed to send rate limit message", "error", err)
			}
			closeConn(conn, websocket.ClosePolicyViolation, "rate limited")
			return
		}

		token := generateToken()
		refresh := generateToken()

		client := hub.NewClient(wsHub, conn, token, refresh)

		// send token before registering to avoid a race between
		// Send writing to client.send and the hub receiving the
		// client pointer from the register channel
		if err := client.Send(request.Context(), hub.TokenMessage{
			Token:   token,
			Refresh: refresh,
		}); err != nil {
			slog.Warn("failed to send token message", "error", err)
			client.Close()
			closeConn(conn, websocket.CloseInternalServerErr, "internal error")
			return
		}

		wsHub.Register(client)
	})
}

// RealIP extracts the client IP from the request, checking
// X-Forwarded-For and X-Real-IP headers before falling back to
// RemoteAddr. The port is stripped.
func RealIP(request *http.Request) string {
	if xff := request.Header.Get("X-Forwarded-For"); xff != "" {
		// first entry is the original client
		if ipAddr, _, ok := strings.Cut(xff, ","); ok {
			return strings.TrimSpace(ipAddr)
		}
		return strings.TrimSpace(xff)
	}
	if xri := request.Header.Get("X-Real-Ip"); xri != "" {
		return strings.TrimSpace(xri)
	}
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		return request.RemoteAddr
	}
	return host
}

// checkOrigin returns a function that validates the Origin header
// against a list of allowed origins. If the list is empty, it falls
// back to same-host checking: the Origin's host must match the
// request's Host header.
func checkOrigin(allowedOrigins []string) func(*http.Request) bool {
	normalize := func(origin string) string {
		return strings.ToLower(strings.TrimRight(origin, "/"))
	}

	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		allowed[normalize(origin)] = struct{}{}
	}

	return func(request *http.Request) bool {
		origin := request.Header.Get("Origin")
		if origin == "" {
			return true
		}

		if len(allowed) > 0 {
			_, ok := allowed[normalize(origin)]
			return ok
		}

		// fall back to same-host check
		parsed, err := url.Parse(origin)
		if err != nil {
			return false
		}
		return strings.EqualFold(parsed.Host, request.Host)
	}
}

func generateToken() string {
	var buf [tokenBytes]byte
	_, _ = rand.Read(buf[:])
	return hex.EncodeToString(buf[:])
}

// writeEnvelope marshals a Message and writes it to the connection
// as a text frame. All gorilla WriteMessage errors are terminal:
// once a write fails the connection's internal writeErr is latched
// and all subsequent writes return immediately. Callers should close
// the connection on error.
func writeEnvelope(conn *websocket.Conn, msg hub.Message) error {
	data, err := hub.MarshalMessage(msg)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		return fmt.Errorf("write ws frame: %w", err)
	}
	return nil
}

// closeConn attempts a graceful WebSocket close handshake with the
// given code and reason, then tears down the TCP connection
// regardless of whether the handshake succeeded.
func closeConn(conn *websocket.Conn, code int, reason string) {
	closeMsg := websocket.FormatCloseMessage(code, reason)
	if err := conn.WriteMessage(websocket.CloseMessage, closeMsg); err != nil {
		slog.Warn("ws close handshake failed", "error", err)
	}
	_ = conn.Close()
}

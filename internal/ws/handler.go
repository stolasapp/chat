// Package ws provides WebSocket HTTP handlers and middleware.
package ws

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/time/rate"

	"github.com/stolasapp/chat/internal/hub"
	"github.com/stolasapp/chat/internal/match"
	"github.com/stolasapp/chat/internal/ratelimit"
)

const (
	wsReadBufSize    = 1024
	wsWriteBufSize   = 1024
	ipRate           = rate.Limit(1)
	ipBurst          = 5
	rateLimitBackoff = 30 * time.Second
)

// Handler upgrades HTTP connections to WebSocket and registers
// them with the Hub. It manages its own per-IP rate limiter.
// Per-IP connection limits are deferred to the reverse proxy.
type Handler struct {
	hub      *hub.Hub
	limiter  *ratelimit.Keyed[string]
	upgrader websocket.Upgrader
}

// NewHandler creates a Handler. allowedOrigins specifies which
// Origin header values are permitted. If empty, the handler falls
// back to same-host checking (Origin host must match the Host
// header).
func NewHandler(wsHub *hub.Hub, allowedOrigins []string) *Handler {
	return &Handler{
		hub:     wsHub,
		limiter: ratelimit.NewKeyed[string](ipRate, ipBurst),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  wsReadBufSize,
			WriteBufferSize: wsWriteBufSize,
			CheckOrigin:     checkOrigin(allowedOrigins),
		},
	}
}

// Cleanup removes rate limiter entries idle longer than maxAge.
func (h *Handler) Cleanup(maxAge time.Duration) {
	h.limiter.Cleanup(maxAge)
}

func (h *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	ipAddr := RealIP(request)

	conn, err := h.upgrader.Upgrade(writer, request, nil)
	if err != nil {
		slog.Warn("ws upgrade failed", slog.Any("error", err))
		return
	}

	if !h.limiter.Allow(ipAddr) {
		if err := writeEnvelope(conn, &hub.RateLimitedMessage{
			RetryAfter: rateLimitBackoff,
		}); err != nil {
			slog.Warn("failed to send rate limit message", slog.Any("error", err))
		}
		closeConn(conn, websocket.ClosePolicyViolation, "rate limited")
		return
	}

	token := match.NewToken()
	client := h.hub.CreateClient(token)

	var reconnectToken match.Token
	if qToken := request.URL.Query().Get("token"); qToken != "" {
		reconnectToken = match.Token(qToken)
	}

	if err := h.hub.Register(request.Context(), client, conn, reconnectToken); err != nil {
		slog.Warn("failed to register client", slog.Any("error", err))
		client.Close(hub.ErrClientClosed)
		return
	}
}

// RealIP extracts the client IP from the request. It trusts only
// X-Real-IP, which must be set by the reverse proxy (nginx).
// X-Forwarded-For is ignored because clients can spoof it.
// Falls back to RemoteAddr when the header is absent.
func RealIP(request *http.Request) string {
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
		slog.Warn("ws close handshake failed", slog.Any("error", err))
	}
	_ = conn.Close()
}

// Package server wires HTTP routes and manages the listener lifecycle.
package server

import (
	"context"
	"net/http"
	"time"

	"github.com/a-h/templ"

	"github.com/stolasapp/chat/internal/hub"
	"github.com/stolasapp/chat/internal/static"
	"github.com/stolasapp/chat/internal/view"
	"github.com/stolasapp/chat/internal/ws"
)

const (
	// readHeaderTimeout bounds how long the server waits for
	// request headers after accepting a connection (slowloris
	// defense). ReadTimeout and WriteTimeout are intentionally
	// omitted: they apply to the full connection lifetime and
	// would kill long-lived WebSocket connections. The WS pumps
	// manage their own per-message deadlines instead.
	readHeaderTimeout = 10 * time.Second

	// idleTimeout controls how long keep-alive HTTP connections
	// (non-upgraded) sit idle before being closed.
	idleTimeout = 120 * time.Second

	// maxHeaderBytes caps the size of request headers to prevent
	// memory exhaustion from oversized headers.
	maxHeaderBytes = 1 << 20 // 1 MiB
)

// Config holds the dependencies needed to construct a Server.
type Config struct {
	Addr           string
	Hub            *hub.Hub
	IPLimiter      *ws.IPLimiter
	AllowedOrigins []string
}

// Server wraps an http.Server with configured routes.
type Server struct {
	http *http.Server
}

// New creates a Server with all routes registered.
func New(cfg Config) *Server {
	mux := http.NewServeMux()

	mux.Handle("GET /", templ.Handler(view.LandingPage()))
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(static.FS))))
	mux.Handle("GET /ws", ws.NewHandler(cfg.Hub, cfg.IPLimiter, cfg.AllowedOrigins))

	return &Server{
		http: &http.Server{
			Addr:              cfg.Addr,
			Handler:           mux,
			ReadHeaderTimeout: readHeaderTimeout,
			IdleTimeout:       idleTimeout,
			MaxHeaderBytes:    maxHeaderBytes,
		},
	}
}

// ListenAndServe starts the HTTP server.
func (s *Server) ListenAndServe() error {
	return s.http.ListenAndServe()
}

// Shutdown gracefully stops the HTTP server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.http.Shutdown(ctx)
}

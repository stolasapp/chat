// Package server wires HTTP routes and manages the listener lifecycle.
package server

import (
	"context"
	"errors"
	"log/slog"
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

	shutdownTimeout = 10 * time.Second

	limiterCleanup = 5 * time.Minute

	limiterMaxAge = 10 * time.Minute
)

// Config holds the dependencies needed to construct a Server.
type Config struct {
	Addr           string
	Hub            *hub.Hub
	AllowedOrigins []string
}

// Server wraps an http.Server with configured routes and the
// WebSocket handler for lifecycle management.
type Server struct {
	http      *http.Server
	wsHandler *ws.Handler
}

// New creates a Server with all routes registered.
func New(cfg Config) *Server {
	wsHandler := ws.NewHandler(cfg.Hub, cfg.AllowedOrigins)

	mux := http.NewServeMux()
	mux.Handle("GET /", templ.Handler(view.LandingPage()))
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(static.FS))))
	mux.Handle("GET /ws", wsHandler)

	return &Server{
		wsHandler: wsHandler,
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
func (s *Server) ListenAndServe(ctx context.Context) {
	ctx, cancel := context.WithCancelCause(ctx)
	go func() {
		defer cancel(nil)
		slog.Info("server starting", slog.String("addr", s.http.Addr))
		if err := s.http.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			cancel(err)
			slog.Error("server failed", slog.Any("error", err))
		}
	}()
	go func() {
		ticker := time.NewTicker(limiterCleanup)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.wsHandler.Cleanup(limiterMaxAge)
			case <-ctx.Done():
				return
			}
		}
	}()

	<-ctx.Done()
	slog.Info("server shutting down", slog.Any("cause", context.Cause(ctx)))
	shutdownCtx, shutdownCancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer shutdownCancel()
	if err := s.http.Shutdown(shutdownCtx); err != nil {
		slog.Error("http shutdown failed", slog.Any("error", err))
	}
}

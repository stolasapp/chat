// Package server wires HTTP routes and manages the listener lifecycle.
package server

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/a-h/templ"

	"github.com/stolasapp/chat/internal/hub"
	"github.com/stolasapp/chat/internal/static"
	"github.com/stolasapp/chat/internal/view"
	"github.com/stolasapp/chat/internal/ws"
)

const (
	readHeaderTimeout = 10 * time.Second
	idleTimeout       = 120 * time.Second
	maxHeaderBytes    = 1 << 20 // 1 MiB
	shutdownTimeout   = 10 * time.Second
	limiterCleanup    = 5 * time.Minute
	limiterMaxAge     = 10 * time.Minute
)

// Config holds the dependencies needed to construct a Server.
type Config struct {
	Addr           string
	Domain         string
	Hub            *hub.Hub
	AllowedOrigins []string
	HSTS           bool
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
	mux.Handle("GET /static/", cacheControl(
		http.StripPrefix("/static/", http.FileServer(http.FS(static.Assets))),
	))
	mux.Handle("GET /ws", wsHandler)
	mux.HandleFunc("GET /robots.txt", robotsTxtHandler)
	mux.HandleFunc("/", notFoundHandler)

	handler := domainMiddleware(
		cspMiddleware(
			securityHeaders(mux, cfg.HSTS),
			cfg.Domain,
		),
		cfg.Domain,
	)

	return &Server{
		wsHandler: wsHandler,
		http: &http.Server{
			Addr:              cfg.Addr,
			Handler:           handler,
			ReadHeaderTimeout: readHeaderTimeout,
			IdleTimeout:       idleTimeout,
			MaxHeaderBytes:    maxHeaderBytes,
		},
	}
}

// ListenAndServe starts the HTTP server.
func (s *Server) ListenAndServe(ctx context.Context) {
	ctx, cancel := context.WithCancelCause(ctx)
	go s.serve(ctx, cancel)
	go s.limiterCleaner(ctx)

	// block until the server exits or the context is cancelled
	<-ctx.Done()
	slog.Info("server shutting down", slog.Any("cause", context.Cause(ctx)))
	shutdownCtx, shutdownCancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer shutdownCancel()
	if err := s.http.Shutdown(shutdownCtx); err != nil {
		slog.Error("http shutdown failed", slog.Any("error", err))
	}
}

func (s *Server) serve(ctx context.Context, cancel context.CancelCauseFunc) {
	defer cancel(nil)
	lis, err := (&net.ListenConfig{}).Listen(ctx, "tcp", s.http.Addr)
	if err != nil {
		cancel(err)
		slog.Error("server failed to listen", slog.Any("error", err))
		return
	}
	slog.Info("server starting", slog.String("address", lis.Addr().String()))
	if err = s.http.Serve(lis); err != nil && !errors.Is(err, http.ErrServerClosed) {
		cancel(err)
		slog.Error("server failed", slog.Any("error", err))
	}
}

func (s *Server) limiterCleaner(ctx context.Context) {
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
}

// cacheControl wraps a handler to add immutable cache headers.
// Used for versioned static assets.
func cacheControl(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		next.ServeHTTP(writer, request)
	})
}

const robotsTxt = "User-agent: *\nAllow: /\n"

func robotsTxtHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(robotsTxt))
}

func notFoundHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
	_ = view.ErrorPage(http.StatusNotFound, "Page not found.").Render(r.Context(), w)
}

// Package uitest provides end-to-end browser tests using Rod.
package uitest

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/a-h/templ"
	"github.com/stolasapp/chat/internal/hub"
	"github.com/stolasapp/chat/internal/match"
	"github.com/stolasapp/chat/internal/static"
	"github.com/stolasapp/chat/internal/view"
	"github.com/stolasapp/chat/internal/ws"
)

// Server is a test server running the full chat app.
type Server struct {
	baseURL string
	hub     *hub.Hub
	http    *http.Server
	cancel  context.CancelFunc
	done    chan struct{}
}

// newTestServer creates and starts a chat server on a random port.
// Timeouts are shortened for fast test execution.
func newTestServer() *Server {
	ctx, cancel := context.WithCancel(context.Background())

	matcher := match.NewMatcher(match.DefaultMatchTimeout)
	wsHub := hub.NewHub(matcher)
	wsHub.GracePeriod = 2 * time.Second
	wsHub.SearchCooldown = 0
	wsHub.IdleTimeout = 30 * time.Second
	wsHub.IdleWarning = 5 * time.Second

	wsHandler := ws.NewHandler(wsHub, nil)

	mux := http.NewServeMux()
	mux.Handle("GET /", templ.Handler(view.LandingPage()))
	mux.Handle("GET /static/",
		http.StripPrefix("/static/", http.FileServer(http.FS(static.FS))))
	mux.Handle("GET /ws", wsHandler)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		cancel()
		panic(fmt.Sprintf("uitest: failed to listen: %v", err))
	}

	srv := &http.Server{Handler: mux}
	done := make(chan struct{})

	go func() {
		defer close(done)
		wsHub.Run(ctx)
	}()
	go func() {
		if err := srv.Serve(listener); err != nil &&
			err != http.ErrServerClosed {
			panic(fmt.Sprintf("uitest: server failed: %v", err))
		}
	}()

	return &Server{
		baseURL: "http://" + listener.Addr().String(),
		hub:     wsHub,
		http:    srv,
		cancel:  cancel,
		done:    done,
	}
}

// URL returns a full URL for the given path.
func (s *Server) URL(path string) string {
	return s.baseURL + path
}

// Close shuts down the server and waits for the hub to drain.
func (s *Server) Close() {
	shutdownCtx, shutdownCancel := context.WithTimeout(
		context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = s.http.Shutdown(shutdownCtx)
	s.cancel()
	<-s.done
}

// Package main implements the chat server entry point.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/time/rate"

	"github.com/stolasapp/chat/internal/hub"
	"github.com/stolasapp/chat/internal/server"
	"github.com/stolasapp/chat/internal/ws"
)

const (
	ipRateBurst     = 5
	limiterCleanup  = 5 * time.Minute
	limiterMaxAge   = 10 * time.Minute
	shutdownTimeout = 10 * time.Second
)

func main() {
	addr := flag.String("addr", envOr("ADDR", ":8080"), "listen address")
	flag.Parse()

	wsHub := hub.NewHub()
	limiter := ws.NewIPLimiter(rate.Limit(1), ipRateBurst)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go wsHub.Run(ctx)
	go limiterCleanupLoop(ctx, limiter)

	srv := server.New(server.Config{
		Addr:      *addr,
		Hub:       wsHub,
		IPLimiter: limiter,
	})

	done := make(chan os.Signal, 1)
	signal.Notify(done, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		slog.Info("server starting", "addr", *addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	<-done
	slog.Info("shutting down")

	// stop accepting new connections first, then stop the hub
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("http shutdown failed", "error", err)
	}
	cancel()

	slog.Info("server stopped")
}

func limiterCleanupLoop(ctx context.Context, limiter *ws.IPLimiter) {
	ticker := time.NewTicker(limiterCleanup)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			limiter.Cleanup(limiterMaxAge)
		case <-ctx.Done():
			return
		}
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

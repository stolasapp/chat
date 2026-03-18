// Package main implements the chat server entry point.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/stolasapp/chat/internal/hub"
	"github.com/stolasapp/chat/internal/match"
	"github.com/stolasapp/chat/internal/server"
)

func main() {
	addr := flag.String("addr", envOr("ADDR", ":8080"), "listen address")
	debug := flag.Bool("debug", false, "enable debug logging")
	hsts := flag.Bool("hsts", false, "enable HSTS header")
	flag.Parse()

	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	hubCtx, hubCancel := context.WithCancel(context.Background())
	defer hubCancel()

	matcher := match.NewMatcher(match.DefaultMatchTimeout)
	wsHub := hub.NewHub(matcher)

	go func() {
		defer hubCancel()
		sigCtx, stop := signal.NotifyContext(hubCtx, syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		server.New(server.Config{
			Addr: *addr,
			Hub:  wsHub,
			HSTS: *hsts,
		}).ListenAndServe(sigCtx)
	}()

	// blocks until hub context is canceled and all clients are drained
	wsHub.Run(hubCtx)

	slog.Info("server stopped")
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

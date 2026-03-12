package hub

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/stolasapp/chat/internal/view"
)

const shutdownTimeout = 10 * time.Second

// Hub maintains the set of active clients and relays messages.
// All client map mutations happen in the Run goroutine.
type Hub struct {
	clients    map[*Client]struct{}
	tokens     map[string]*Client
	register   chan *Client
	unregister chan *Client
	mu         sync.RWMutex
	running    atomic.Bool
}

// NewHub creates a Hub ready to Run.
func NewHub() *Hub {
	return &Hub{
		clients:    make(map[*Client]struct{}),
		tokens:     make(map[string]*Client),
		register:   make(chan *Client, sendBufSize),
		unregister: make(chan *Client, sendBufSize),
	}
}

// Register enqueues a client for registration.
func (h *Hub) Register(client *Client) {
	h.register <- client
}

// ClientByToken looks up a client by session token. Safe for
// concurrent use.
func (h *Hub) ClientByToken(token string) *Client {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.tokens[token]
}

// Len returns the number of connected clients. Safe for concurrent
// use.
func (h *Hub) Len() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// Run processes register/unregister events until ctx is cancelled.
// It must be called exactly once, in its own goroutine. Subsequent
// calls are no-ops.
func (h *Hub) Run(ctx context.Context) {
	if !h.running.CompareAndSwap(false, true) {
		return
	}
	h.run(ctx)
}

func (h *Hub) run(ctx context.Context) {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = struct{}{}
			h.tokens[client.token] = client
			count := len(h.clients)
			h.mu.Unlock()
			slog.Info("client registered", "clients", count)

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				client.Close()
				// TODO(phase6): retain token mapping with a TTL
				// for reconnect grace period instead of deleting
				// immediately.
				delete(h.tokens, client.token)
				count := len(h.clients)
				h.mu.Unlock()
				slog.Info("client unregistered", "clients", count)
			} else {
				h.mu.Unlock()
			}

		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(
				context.WithoutCancel(ctx), shutdownTimeout,
			)
			h.shutdown(shutdownCtx)
			cancel()
			return
		}
	}
}

func (h *Hub) shutdown(ctx context.Context) {
	// drain pending registrations and unregistrations so their
	// goroutines don't block on the channel after Run exits
	h.drainChannels()

	notification := view.Notify("Server is shutting down.")

	h.mu.Lock()
	defer h.mu.Unlock()

	// attempt to deliver the shutdown notification to each client,
	// respecting the shutdown context's timeout. once the context
	// expires, skip notification for remaining clients.
	notifyCtxDone := false
	for client := range h.clients {
		if !notifyCtxDone {
			if err := client.SendComponent(ctx, notification); err != nil {
				notifyCtxDone = true
			}
		}
		client.Close()
		delete(h.clients, client)
	}

	slog.Info("hub shut down")
}

// drainChannels empties the register and unregister channel buffers,
// closing any clients found. This prevents goroutines from blocking
// on channel sends after Run exits.
func (h *Hub) drainChannels() {
	for {
		select {
		case client := <-h.register:
			client.Close()
		case client := <-h.unregister:
			client.Close()
		default:
			return
		}
	}
}

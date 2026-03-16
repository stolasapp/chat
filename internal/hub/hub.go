package hub

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/stolasapp/chat/internal/catalog"
	"github.com/stolasapp/chat/internal/match"
	"github.com/stolasapp/chat/internal/view"
)

const shutdownTimeout = 10 * time.Second

// clientMessage pairs a received envelope with the client that
// sent it.
type clientMessage struct {
	client   *Client
	envelope Envelope
}

// Hub maintains the set of active clients, dispatches messages,
// and coordinates matchmaking. All client map mutations happen in
// the Run goroutine.
type Hub struct {
	clients    map[*Client]struct{}           // +checklocks:mu
	tokens     map[match.Token]*Client        // +checklocks:mu
	sessions   map[match.Token]*match.Session // +checklocks:mu
	register   chan *Client
	unregister chan *Client
	incoming   chan clientMessage
	matcher    *match.Matcher
	mu         sync.RWMutex
	running    atomic.Bool
	clientWG   sync.WaitGroup
}

// NewHub creates a Hub ready to Run.
func NewHub(matcher *match.Matcher) *Hub {
	return &Hub{
		clients:    make(map[*Client]struct{}),
		tokens:     make(map[match.Token]*Client),
		sessions:   make(map[match.Token]*match.Session),
		register:   make(chan *Client, sendBufSize),
		unregister: make(chan *Client, sendBufSize),
		incoming:   make(chan clientMessage, sendBufSize),
		matcher:    matcher,
	}
}

// Register enqueues a client for registration. Returns
// ErrClientClosed if the client shuts down before the send
// completes.
func (h *Hub) Register(ctx context.Context, client *Client) error {
	select {
	case h.register <- client:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Unregister enqueues a client for removal. The send is
// non-blocking: if the hub has exited and the buffer is full,
// the client is simply dropped.
func (h *Hub) Unregister(client *Client) {
	select {
	case h.unregister <- client:
	default:
	}
}

// Incoming enqueues a message from a client for dispatch.
func (h *Hub) Incoming(ctx context.Context, client *Client, env Envelope) {
	select {
	case h.incoming <- clientMessage{client: client, envelope: env}:
	case <-ctx.Done():
	}
}

// ClientByToken looks up a client by session token. Safe for
// concurrent use.
func (h *Hub) ClientByToken(token match.Token) *Client {
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

// Run processes register/unregister events until ctx is canceled.
// It must be called exactly once; later calls are no-ops.
// Blocks until all clients have been drained and closed.
func (h *Hub) Run(ctx context.Context) {
	if !h.running.CompareAndSwap(false, true) {
		return
	}
	matcherCtx, matcherCancel := context.WithCancel(ctx)
	go h.matcher.Run(matcherCtx)
	h.run(ctx, matcherCancel)
	h.clientWG.Wait()
}

func (h *Hub) run(ctx context.Context, matcherCancel context.CancelFunc) {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = struct{}{}
			h.tokens[client.token] = client
			count := len(h.clients)
			h.mu.Unlock()
			slog.Info("client registered", slog.Int("clients", count))

		case client := <-h.unregister:
			h.handleUnregister(ctx, client)

		case msg := <-h.incoming:
			h.dispatch(ctx, msg)

		case result := <-h.matcher.Matched():
			h.handleMatched(ctx, result)

		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(
				context.WithoutCancel(ctx), shutdownTimeout,
			)
			h.shutdown(shutdownCtx, matcherCancel)
			cancel()
			return
		}
	}
}

func (h *Hub) handleUnregister(ctx context.Context, client *Client) {
	h.mu.Lock()
	_, registered := h.clients[client]
	if !registered {
		h.mu.Unlock()
		return
	}

	delete(h.clients, client)
	wasSearching := client.attempt != ""
	client.attempt = ""
	client.Close(ErrClientClosed)
	// TODO(phase6): retain token mapping with a TTL
	// for reconnect grace period instead of deleting
	// immediately.
	delete(h.tokens, client.token)
	count := len(h.clients)
	partnerClient := h.endSession(client.token)
	h.mu.Unlock()

	slog.Info("client unregistered", slog.Int("clients", count))

	if partnerClient != nil {
		_ = partnerClient.SendComponent(ctx, view.SendButton(false), view.ChatEnded("Your partner has left.", true))
	}

	if wasSearching {
		h.matcher.Leave(ctx, client.token)
	}
}

func (h *Hub) dispatch(ctx context.Context, incoming clientMessage) {
	parsed, err := incoming.envelope.Parse()
	if err != nil {
		slog.Warn("failed to parse message", slog.Any("error", err))
		return
	}

	switch msg := parsed.(type) {
	case FindMatchMessage:
		h.handleFindMatch(ctx, incoming.client, msg)
	case ChatMessage:
		h.handleMessage(ctx, incoming.client, msg)
	case LeaveMessage:
		h.handleLeave(ctx, incoming.client)
	default:
		slog.Warn("unhandled message type", slog.String("type", string(incoming.envelope.Type)))
	}
}

func (h *Hub) handleFindMatch(ctx context.Context, client *Client, msg FindMatchMessage) {
	// if already in a session, ignore
	h.mu.RLock()
	_, inSession := h.sessions[client.token]
	h.mu.RUnlock()
	if inSession {
		return
	}

	profile := &msg.Profile

	// preserve block list from previous profile if any
	if old := client.profile; old != nil {
		profile.BlockedTokens = old.BlockedTokens
	}
	if profile.BlockedTokens == nil {
		profile.BlockedTokens = make(catalog.Set[match.Token])
	}

	// block last partner if requested
	if msg.Block && client.lastPartner != "" {
		profile.BlockedTokens.Add(client.lastPartner)
	}

	client.profile = profile

	// send a chat view with a searching notification
	_ = client.SendComponent(ctx, view.ChatView(), view.Notify("Searching for a match..."))

	attempt := h.matcher.Enqueue(ctx, client.token, profile)
	if attempt == "" {
		// context was cancelled before the entry was accepted
		_ = client.SendComponent(ctx, view.LandingContent())
		return
	}
	client.attempt = attempt
}

func (h *Hub) handleLeave(ctx context.Context, client *Client) {
	// invalidate any pending match attempt
	client.attempt = ""

	h.mu.Lock()
	partnerClient := h.endSession(client.token)
	h.mu.Unlock()

	if partnerClient != nil {
		_ = partnerClient.SendComponent(ctx, view.SendButton(false), view.ChatEnded("Your partner has left.", true))
		_ = client.SendComponent(ctx, view.SendButton(false), view.ChatEnded("You left the chat.", true))
		return
	}

	// not in a session; cancel searching and return to landing
	h.matcher.Leave(ctx, client.token)
	_ = client.SendComponent(ctx, view.LandingContent())
}

func (h *Hub) handleMessage(ctx context.Context, client *Client, msg ChatMessage) {
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return
	}

	h.mu.RLock()
	session, inSession := h.sessions[client.token]
	var partnerClient *Client
	if inSession {
		partner := session.Partner(client.token)
		partnerClient = h.tokens[partner]
	}
	h.mu.RUnlock()

	if !inSession {
		return
	}

	_ = client.SendComponent(ctx, view.ChatMessage(text, true))
	if partnerClient != nil {
		_ = partnerClient.SendComponent(ctx, view.ChatMessage(text, false))
	}
}

// endSession tears down the session for the given token and
// returns the partner's Client (if any). Must be called with
// h.mu held.
//
// +checklocks:h.mu
func (h *Hub) endSession(token match.Token) *Client {
	session, ok := h.sessions[token]
	if !ok {
		return nil
	}
	partner := session.Partner(token)
	delete(h.sessions, token)
	if partner == "" {
		return nil
	}
	delete(h.sessions, partner)
	partnerClient, ok := h.tokens[partner]
	if !ok {
		return nil
	}
	return partnerClient
}

func (h *Hub) handleMatched(ctx context.Context, result match.Result) {
	h.mu.RLock()
	clientA, okA := h.tokens[result.A.Token]
	clientB, okB := h.tokens[result.B.Token]
	h.mu.RUnlock()

	// verify both clients' current attempts match the result.
	// a mismatch means the client left or re-queued since this
	// match was computed.
	staleA := !okA || clientA.attempt != result.A.Attempt
	staleB := !okB || clientB.attempt != result.B.Attempt

	if staleA && staleB {
		return
	}
	if staleA {
		// re-queue B with a fresh attempt
		clientB.attempt = h.matcher.Enqueue(ctx, result.B.Token, clientB.profile)
		return
	}
	if staleB {
		// re-queue A with a fresh attempt
		clientA.attempt = h.matcher.Enqueue(ctx, result.A.Token, clientA.profile)
		return
	}

	session := match.NewSession(result.A.Token, result.B.Token)

	h.mu.Lock()
	h.sessions[result.A.Token] = session
	h.sessions[result.B.Token] = session
	clientA.lastPartner = result.B.Token
	clientB.lastPartner = result.A.Token
	h.mu.Unlock()

	_ = clientA.SendComponent(ctx, view.Notify("You have been matched!"), view.SendButton(true))
	_ = clientB.SendComponent(ctx, view.Notify("You have been matched!"), view.SendButton(true))
}

func (h *Hub) shutdown(ctx context.Context, matcherCancel context.CancelFunc) {
	// 1. stop matchmaking so no new sessions are created
	matcherCancel()
	slog.Info("matcher stopped")

	// 2. drain pending channel operations so their goroutines
	//    don't block after Run exits
	h.drainChannels()

	// 3. collect all clients and clear maps under lock
	h.mu.Lock()
	clients := make([]*Client, 0, len(h.clients))
	for client := range h.clients {
		clients = append(clients, client)
		delete(h.clients, client)
		delete(h.tokens, client.token)
	}
	h.mu.Unlock()

	// 4. notify and close all clients concurrently, waiting
	//    for each write pump to drain
	var closeWG sync.WaitGroup
	for _, client := range clients {
		closeWG.Go(func() {
			_ = client.SendComponent(ctx,
				view.SendButton(false),
				view.ChatEnded("Server is shutting down.", true),
			)
			client.Close(ErrClientClosed)
			client.Wait(ctx)
		})
	}
	closeWG.Wait()

	slog.Info("hub shut down")
}

// drainChannels empties the register and unregister channel buffers,
// closing any clients found. This prevents goroutines from blocking
// on channel sends after Run exits.
func (h *Hub) drainChannels() {
	for {
		select {
		case client := <-h.register:
			client.Close(ErrClientClosed)
		case client := <-h.unregister:
			client.Close(ErrClientClosed)
		default:
			return
		}
	}
}

package hub

import (
	"bytes"
	"context"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/a-h/templ"

	"github.com/stolasapp/chat/internal/catalog"
	"github.com/stolasapp/chat/internal/match"
	"github.com/stolasapp/chat/internal/view"
)

const (
	shutdownTimeout             = 10 * time.Second
	defaultGracePeriod          = 15 * time.Second
	defaultReconnectNotifyDelay = 2 * time.Second
)

// excessiveNewlines matches 3 or more consecutive newlines
// (with optional carriage returns). Used to clamp whitespace
// in chat messages.
var excessiveNewlines = regexp.MustCompile(`(\r?\n){3,}`)

// clientMessage pairs a received envelope with the client that
// sent it.
type clientMessage struct {
	client   *Client
	envelope Envelope
}

// detachedClient holds state for a client that disconnected but
// may reconnect within the grace period. Session entries and
// matcher queue position are preserved until the timer expires.
type detachedClient struct {
	token           match.Token
	profile         *match.Profile
	lastPartner     match.Token
	attempt         match.Token // non-empty if was searching
	graceTimer      *time.Timer // fires after gracePeriod
	notifyTimer     *time.Timer // fires after reconnectNotifyDelay
	notifiedPartner bool        // whether reconnecting indicator was sent
}

// Hub maintains the set of active clients, dispatches messages,
// and coordinates matchmaking. All client map mutations happen in
// the Run goroutine.
type Hub struct {
	clients      map[*Client]struct{}            // +checklocks:mu
	tokens       map[match.Token]*Client         // +checklocks:mu
	sessions     map[match.Token]*match.Session  // +checklocks:mu
	detached     map[match.Token]*detachedClient // +checklocks:mu
	register     chan *Client
	unregister   chan *Client
	incoming     chan clientMessage
	graceExpired chan match.Token
	matcher      *match.Matcher
	mu           sync.RWMutex
	running      atomic.Bool
	clientWG     sync.WaitGroup

	// GracePeriod is how long a detached client's session is
	// preserved before teardown. Defaults to 15s. Set before
	// calling Run; read-only thereafter.
	GracePeriod time.Duration // +checklocksignore: read-only after init

	// ReconnectNotifyDelay is how long to wait before showing
	// the "reconnecting" indicator to the partner. Defaults to 2s.
	// Set before calling Run; read-only thereafter.
	ReconnectNotifyDelay time.Duration // +checklocksignore: read-only after init
}

// NewHub creates a Hub ready to Run.
func NewHub(matcher *match.Matcher) *Hub {
	return &Hub{
		clients:              make(map[*Client]struct{}),
		tokens:               make(map[match.Token]*Client),
		sessions:             make(map[match.Token]*match.Session),
		detached:             make(map[match.Token]*detachedClient),
		register:             make(chan *Client, sendBufSize),
		unregister:           make(chan *Client, sendBufSize),
		incoming:             make(chan clientMessage, sendBufSize),
		graceExpired:         make(chan match.Token, sendBufSize),
		matcher:              matcher,
		GracePeriod:          defaultGracePeriod,
		ReconnectNotifyDelay: defaultReconnectNotifyDelay,
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

// Len returns the number of connected and detached clients. Safe
// for concurrent use. Detached clients are included so the count
// does not drop during brief disconnects.
func (h *Hub) Len() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients) + len(h.detached)
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

const clientCountInterval = 10 * time.Second

func (h *Hub) run(ctx context.Context, matcherCancel context.CancelFunc) {
	countTicker := time.NewTicker(clientCountInterval)
	defer countTicker.Stop()
	countDirty := false

	for {
		select {
		case client := <-h.register:
			h.handleRegister(ctx, client)
			countDirty = true

		case client := <-h.unregister:
			h.handleUnregister(ctx, client)
			countDirty = true

		case token := <-h.graceExpired:
			h.handleGraceExpired(ctx, token)
			countDirty = true

		case msg := <-h.incoming:
			h.dispatch(ctx, msg)

		case result := <-h.matcher.Matched():
			h.handleMatched(ctx, result)

		case <-countTicker.C:
			if countDirty {
				h.broadcastClientCount(ctx)
				countDirty = false
			}

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

func (h *Hub) broadcastClientCount(ctx context.Context) {
	h.mu.RLock()
	count := len(h.clients) + len(h.detached)
	clients := make([]*Client, 0, len(h.clients))
	for client := range h.clients {
		clients = append(clients, client)
	}
	h.mu.RUnlock()

	var buf bytes.Buffer
	if err := view.ClientCount(count).Render(ctx, &buf); err != nil {
		slog.Warn("failed to render client count", slog.Any("error", err))
		return
	}
	data := buf.Bytes()
	for _, client := range clients {
		_ = client.SendRaw(ctx, data)
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
	client.Close(ErrClientClosed)

	_, inSession := h.sessions[client.token]
	wasSearching := client.attempt != ""

	if inSession || wasSearching {
		// detach: preserve session and queue position for
		// reconnection within the grace period
		delete(h.tokens, client.token)
		entry := &detachedClient{
			token:       client.token,
			profile:     client.profile,
			lastPartner: client.lastPartner,
			attempt:     client.attempt,
		}
		entry.graceTimer = time.AfterFunc(h.GracePeriod, func() {
			select {
			case h.graceExpired <- entry.token:
			default:
			}
		})
		if inSession {
			partnerToken := h.sessions[client.token].Partner(client.token)
			entry.notifyTimer = time.AfterFunc(h.ReconnectNotifyDelay, func() {
				h.mu.Lock()
				entry.notifiedPartner = true
				partner := h.tokens[partnerToken]
				h.mu.Unlock()
				if partner != nil {
					_ = partner.SendComponent(ctx, view.ReconnectingIndicator(true))
				}
			})
		}
		h.detached[client.token] = entry
		count := len(h.clients) + len(h.detached)
		h.mu.Unlock()

		slog.Info("client detached", slog.Int("clients", count))
		return
	}

	// no session or search: immediate cleanup
	delete(h.tokens, client.token)
	count := len(h.clients) + len(h.detached)
	h.mu.Unlock()

	slog.Info("client unregistered", slog.Int("clients", count))
}

// reconnectState holds data extracted under lock for
// post-unlock reconnect recovery.
type reconnectState struct {
	reconnected     bool
	inSession       bool
	count           int
	partner         *Client
	partnerProfile  *match.Profile
	notifiedPartner bool
	attempt         match.Token
	oldClient       *Client // non-nil for reconnectFromRegistered
}

func (h *Hub) handleRegister(ctx context.Context, client *Client) {
	h.mu.Lock()
	h.clients[client] = struct{}{}
	h.tokens[client.token] = client

	var state reconnectState

	// attempt session resumption if a reconnect token was
	// provided via query param on the WS upgrade request.
	// sessionStorage is per-tab, so cross-tab conflicts are
	// not possible. Transferring empty state from a landing-
	// page client is harmless (preserves token identity).
	if client.reconnectToken != "" {
		if entry, ok := h.detached[client.reconnectToken]; ok {
			state = h.reconnectFromDetached(client, entry)
		} else if old, ok := h.tokens[client.reconnectToken]; ok && old != client {
			state = h.reconnectFromRegistered(client, old)
		}
	}

	if !state.reconnected {
		state.count = len(h.clients) + len(h.detached)
	}
	h.mu.Unlock()

	if state.reconnected {
		if state.oldClient != nil {
			state.oldClient.Close(ErrClientClosed)
		}
		h.sendReconnectRecovery(ctx, client, state)
		return
	}
	_ = client.Send(ctx, TokenMessage{Token: client.token})
	slog.Info("client registered", slog.Int("clients", state.count))
}

// reconnectFromDetached reattaches a new client to a detached
// entry. Must be called with h.mu held.
//
// +checklocks:h.mu
func (h *Hub) reconnectFromDetached(client *Client, entry *detachedClient) reconnectState {
	entry.graceTimer.Stop()
	if entry.notifyTimer != nil {
		entry.notifyTimer.Stop()
	}
	delete(h.detached, entry.token)

	state := h.transferIdentity(client, entry.token, entry.profile, entry.lastPartner, entry.attempt)
	state.notifiedPartner = entry.notifiedPartner
	return state
}

// reconnectFromRegistered transfers state from an old client
// that is still registered (unregister not yet processed) to a
// new client. Must be called with h.mu held.
//
// +checklocks:h.mu
func (h *Hub) reconnectFromRegistered(client *Client, oldClient *Client) reconnectState {
	delete(h.clients, oldClient)
	delete(h.tokens, oldClient.token)

	state := h.transferIdentity(client, oldClient.token, oldClient.profile, oldClient.lastPartner, oldClient.attempt)
	state.oldClient = oldClient
	return state
}

// transferIdentity swaps a newly registered client's token to
// oldToken, copies the given state fields, and builds a
// reconnectState with session/partner info. Must be called with
// h.mu held.
//
// +checklocks:h.mu
func (h *Hub) transferIdentity(
	client *Client,
	oldToken match.Token,
	profile *match.Profile,
	lastPartner match.Token,
	attempt match.Token,
) reconnectState {
	delete(h.tokens, client.token)
	client.token = oldToken
	client.profile = profile
	client.lastPartner = lastPartner
	client.attempt = attempt
	h.tokens[oldToken] = client

	state := reconnectState{
		reconnected: true,
		attempt:     attempt,
		count:       len(h.clients) + len(h.detached),
	}
	if session := h.sessions[oldToken]; session != nil {
		state.inSession = true
		partnerToken := session.Partner(oldToken)
		state.partner = h.tokens[partnerToken]
		if state.partner != nil {
			state.partnerProfile = state.partner.profile
		} else if detachedPartner, ok := h.detached[partnerToken]; ok {
			state.partnerProfile = detachedPartner.profile
		}
	}
	return state
}

// sendReconnectRecovery sends state recovery HTML to a
// reconnected client.
func (h *Hub) sendReconnectRecovery(ctx context.Context, client *Client, state reconnectState) {
	_ = client.Send(ctx, TokenMessage{Token: client.token})
	if state.inSession {
		components := []templ.Component{
			view.ChatView(),
			view.ClientCount(state.count),
			view.Notify("Reconnected."),
			view.SendButton(true),
		}
		if state.partnerProfile != nil {
			matched := view.NewMatchedProfile(state.partnerProfile, client.profile.Interests)
			components = append(components, view.MatchedNotify(matched))
		}
		_ = client.SendComponent(ctx, components...)
		if state.notifiedPartner && state.partner != nil {
			_ = state.partner.SendComponent(ctx, view.ReconnectingIndicator(false))
		}
	} else if state.attempt != "" {
		_ = client.SendComponent(ctx,
			view.ChatView(),
			view.ClientCount(state.count),
			view.Notify("Searching for a match..."),
		)
	}

	slog.Info("client reconnected", slog.Int("clients", state.count))
}

func (h *Hub) handleGraceExpired(ctx context.Context, token match.Token) {
	h.mu.Lock()
	entry, ok := h.detached[token]
	if !ok {
		// already reconnected
		h.mu.Unlock()
		return
	}

	if entry.notifyTimer != nil {
		entry.notifyTimer.Stop()
	}
	delete(h.detached, token)

	partnerClient := h.endSession(token)
	count := len(h.clients) + len(h.detached)
	h.mu.Unlock()

	if partnerClient != nil {
		_ = partnerClient.SendComponent(ctx, view.SessionEndComponents("Your partner has left.", true)...)
	}

	if entry.attempt != "" {
		h.matcher.Leave(ctx, token)
	}

	slog.Info("grace period expired", slog.Int("clients", count))
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
	case TypingMessage:
		h.handleTyping(ctx, incoming.client, msg)
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
	count := len(h.clients) + len(h.detached)
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
	_ = client.SendComponent(ctx,
		view.ChatView(),
		view.ClientCount(count),
		view.Notify("Searching for a match..."),
	)

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
		_ = partnerClient.SendComponent(ctx, view.SessionEndComponents("Your partner has left.", true)...)
		_ = client.SendComponent(ctx, view.SessionEndComponents("You left the chat.", true)...)
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
	text = excessiveNewlines.ReplaceAllString(text, "\n\n")

	partner := h.sessionPartner(client.token)
	if partner == nil {
		return
	}

	_ = client.SendComponent(ctx, view.ChatMessage(text, true, msg.Seq))
	_ = partner.SendComponent(ctx, view.TypingIndicator(false), view.ChatMessage(text, false, 0))
}

func (h *Hub) handleTyping(ctx context.Context, client *Client, msg TypingMessage) {
	partner := h.sessionPartner(client.token)
	if partner == nil {
		return
	}
	_ = partner.SendComponent(ctx, view.TypingIndicator(msg.Active))
}

// sessionPartner returns the partner's Client for the given
// token, or nil if not in a session or the partner is gone.
func (h *Hub) sessionPartner(token match.Token) *Client {
	h.mu.RLock()
	defer h.mu.RUnlock()
	session, ok := h.sessions[token]
	if !ok {
		return nil
	}
	return h.tokens[session.Partner(token)]
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

	profileForA := view.NewMatchedProfile(clientB.profile, clientA.profile.Interests)
	profileForB := view.NewMatchedProfile(clientA.profile, clientB.profile.Interests)
	_ = clientA.SendComponent(ctx, view.MatchedNotify(profileForA), view.SendButton(true))
	_ = clientB.SendComponent(ctx, view.MatchedNotify(profileForB), view.SendButton(true))
}

func (h *Hub) shutdown(ctx context.Context, matcherCancel context.CancelFunc) {
	// 1. stop matchmaking so no new sessions are created
	matcherCancel()
	slog.Info("matcher stopped")

	// 2. drain pending channel operations so their goroutines
	//    don't block after Run exits
	h.drainChannels()

	// 3. cancel all detached timers and end their sessions
	h.mu.Lock()
	for token, entry := range h.detached {
		entry.graceTimer.Stop()
		if entry.notifyTimer != nil {
			entry.notifyTimer.Stop()
		}
		h.endSession(token)
		delete(h.detached, token)
	}

	// 4. collect all clients and clear maps under lock
	clients := make([]*Client, 0, len(h.clients))
	for client := range h.clients {
		clients = append(clients, client)
		delete(h.clients, client)
		delete(h.tokens, client.token)
	}
	h.mu.Unlock()

	// 5. notify and close all clients concurrently, waiting
	//    for each write pump to drain
	var closeWG sync.WaitGroup
	for _, client := range clients {
		closeWG.Go(func() {
			_ = client.SendComponent(ctx, view.SessionEndComponents("Server is shutting down.", true)...)
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

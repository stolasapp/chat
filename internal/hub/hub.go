package hub

import (
	"bytes"
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/a-h/templ"
	"github.com/gorilla/websocket"

	"github.com/stolasapp/chat/internal/catalog"
	"github.com/stolasapp/chat/internal/match"
	"github.com/stolasapp/chat/internal/view"
)

const (
	shutdownTimeout       = 10 * time.Second
	defaultGracePeriod    = 15 * time.Second
	defaultIdleTimeout    = 5 * time.Minute
	defaultIdleWarning    = 30 * time.Second
	defaultSearchCooldown = 2 * time.Second

	defaultReconnectNotifyDelay = 2 * time.Second

	hubChanBuf = 256
)

// MatchService provides matchmaking operations.
type MatchService interface {
	Enqueue(ctx context.Context, token match.Token,
		profile *match.Profile) match.Token
	Leave(ctx context.Context, token match.Token)
	Matched() <-chan match.Result
	Run(ctx context.Context)
}

// Compile-time assertion that *match.Matcher satisfies MatchService.
var _ MatchService = (*match.Matcher)(nil)

// clientMessage pairs a received envelope with the client that
// sent it.
type clientMessage struct {
	client   *Client
	envelope Envelope
}

// registerRequest bundles the data needed to register a client.
type registerRequest struct {
	client         *Client
	conn           *websocket.Conn
	reconnectToken match.Token
}

// Hub maintains the set of active clients, dispatches messages,
// and coordinates matchmaking. Session lifecycle is delegated to
// the SessionService; the hub handles registration, matching,
// grace periods, and shutdown.
type Hub struct {
	reg             ClientRegistry
	sess            SessionService
	matcher         MatchService
	register        chan registerRequest
	unregister      chan *Client
	incoming        chan clientMessage
	graceExpired    chan *Client
	reconnectNotify chan *Client
	sessionEnded    chan struct{}
	running         atomic.Bool
	clientWG        sync.WaitGroup

	hubSink *MessageSink // default sink for unmatched clients

	// GracePeriod is how long a detached client is preserved
	// before teardown. Defaults to 15s. Set before calling Run;
	// read-only thereafter.
	GracePeriod time.Duration // +checklocksignore: read-only after init

	// SearchCooldown is the minimum time between find_match
	// requests. Defaults to 2s. Set before calling Run;
	// read-only thereafter.
	SearchCooldown time.Duration // +checklocksignore: read-only after init

	// IdleTimeout is how long a client can be idle in a session
	// before being disconnected. Defaults to 5m. Set before
	// calling Run; read-only thereafter.
	IdleTimeout time.Duration // +checklocksignore: read-only after init

	// IdleWarning is the duration of the warning period before
	// idle disconnect. Defaults to 30s. Set before calling Run;
	// read-only thereafter.
	IdleWarning time.Duration // +checklocksignore: read-only after init

	// ReconnectNotifyDelay is how long to wait before showing
	// the "reconnecting" indicator to the partner. Defaults to
	// 2s. Set before calling Run; read-only thereafter.
	ReconnectNotifyDelay time.Duration // +checklocksignore: read-only after init
}

// newHubSink creates a MessageSink that routes messages to the
// hub's incoming channel.
func newHubSink(incoming chan clientMessage) *MessageSink {
	sink := MessageSink(func(ctx context.Context, c *Client, env Envelope) {
		select {
		case incoming <- clientMessage{client: c, envelope: env}:
		case <-ctx.Done():
		}
	})
	return &sink
}

// NewHub creates a Hub ready to Run.
func NewHub(matcher MatchService) *Hub {
	incoming := make(chan clientMessage, hubChanBuf)
	return &Hub{
		reg:                  NewRegistry(),
		matcher:              matcher,
		register:             make(chan registerRequest, hubChanBuf),
		unregister:           make(chan *Client, hubChanBuf),
		incoming:             incoming,
		graceExpired:         make(chan *Client, hubChanBuf),
		reconnectNotify:      make(chan *Client, hubChanBuf),
		sessionEnded:         make(chan struct{}, hubChanBuf),
		hubSink:              newHubSink(incoming),
		GracePeriod:          defaultGracePeriod,
		ReconnectNotifyDelay: defaultReconnectNotifyDelay,
		IdleTimeout:          defaultIdleTimeout,
		IdleWarning:          defaultIdleWarning,
		SearchCooldown:       defaultSearchCooldown,
	}
}

// CreateClient creates a persistent client with no connection,
// wired to the hub's default message sink and pump WaitGroup.
func (h *Hub) CreateClient(token match.Token) *Client {
	return NewClient(token, h.hubSink, &h.clientWG)
}

// Register enqueues a client for registration. The conn will be
// attached to either the new client (fresh connection) or an
// existing client (reconnect). If reconnectToken is non-empty,
// the hub attempts to resume a prior session.
func (h *Hub) Register(
	ctx context.Context,
	client *Client,
	conn *websocket.Conn,
	reconnectToken match.Token,
) error {
	select {
	case h.register <- registerRequest{
		client:         client,
		conn:           conn,
		reconnectToken: reconnectToken,
	}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Unregister enqueues a client for removal. Non-blocking.
func (h *Hub) Unregister(client *Client) {
	select {
	case h.unregister <- client:
	default:
	}
}

// ClientByToken looks up a client by session token.
func (h *Hub) ClientByToken(token match.Token) *Client {
	return h.reg.ByToken(token)
}

// Len returns the number of clients (including detached).
func (h *Hub) Len() int {
	return h.reg.Len()
}

// Run processes events until ctx is canceled. Must be called
// exactly once.
func (h *Hub) Run(ctx context.Context) {
	if !h.running.CompareAndSwap(false, true) {
		return
	}

	h.sess = NewSessionManager(ctx, sessionConfig{
		idleTimeout: h.IdleTimeout,
		idleWarning: h.IdleWarning,
	})

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
		case req := <-h.register:
			h.handleRegister(ctx, req)
			countDirty = true

		case client := <-h.unregister:
			h.handleUnregister(ctx, client)
			countDirty = true

		case client := <-h.graceExpired:
			h.handleGraceExpired(ctx, client)
			countDirty = true

		case client := <-h.reconnectNotify:
			h.handleReconnectNotify(ctx, client)

		case <-h.sessionEnded:
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
	clients, count := h.reg.Snapshot()

	var buf bytes.Buffer
	if err := view.ClientCount(count).Render(ctx, &buf); err != nil {
		slog.Warn("failed to render client count", slog.Any("error", err))
		return
	}
	data := buf.Bytes()
	for _, client := range clients {
		client.TrySendRaw(data)
	}
}

func (h *Hub) handleRegister(ctx context.Context, req registerRequest) {
	client := req.client
	conn := req.conn

	if req.reconnectToken != "" {
		if existing := h.reg.ByToken(req.reconnectToken); existing != nil {
			// reconnect: attach conn to existing persistent client
			pending := existing.Attach(conn, h.Unregister)
			client.Close(ErrClientClosed)
			h.sendReconnectRecovery(ctx, existing, pending)
			return
		}
	}

	// new client or stale reconnect token
	_ = client.Attach(conn, h.Unregister)
	count := h.reg.Add(client)
	_ = client.Send(ctx, TokenMessage{Token: client.Token()})
	if req.reconnectToken != "" {
		_ = client.SendComponent(ctx, view.SessionEndComponents(view.MsgServerReset, true)...)
	}
	slog.Info("client registered", slog.Int("clients", count))
}

func (h *Hub) handleUnregister(ctx context.Context, client *Client) {
	if h.reg.ByToken(client.Token()) != client {
		return
	}
	// stale event: client was already reattached
	if !client.isDetached() {
		return
	}

	if !client.HasProfile() {
		// no profile: immediate cleanup
		client.Close(ErrClientClosed)
		_, count := h.reg.Remove(client)
		if client.IsSearching() {
			h.matcher.Leave(ctx, client.Token())
		}
		slog.Info("client unregistered", slog.Int("clients", count))
		return
	}

	// profiled client: detach with grace period
	client.Detach(
		h.GracePeriod, func(c *Client) {
			select {
			case h.graceExpired <- c:
			case <-c.Done():
			}
		},
		h.ReconnectNotifyDelay, func(c *Client) {
			select {
			case h.reconnectNotify <- c:
			case <-c.Done():
			}
		},
	)
	slog.Info("client detached", slog.Int("clients", h.reg.Len()))
}

func (h *Hub) sendReconnectRecovery(ctx context.Context, client *Client, pending [][]byte) {
	_ = client.Send(ctx, TokenMessage{Token: client.Token()})

	count := h.reg.Len()
	if h.sess.InSession(client.Token()) {
		partnerToken := h.sess.Partner(client.Token())
		partner := h.reg.ByToken(partnerToken)

		components := []templ.Component{
			view.ChatView(),
			view.ClientCount(count),
			view.Notify("Reconnected."),
			view.SendButton(true),
		}
		if partner != nil && partner.HasProfile() {
			matched := view.NewMatchedProfile(partner.Profile(), client.Profile().Interests)
			components = append(components, view.MatchedNotify(matched))
		}
		_ = client.SendComponent(ctx, components...)

		// replay messages buffered during the disconnect gap
		for _, data := range pending {
			_ = client.SendRaw(ctx, data)
		}

		// clear reconnecting indicator on partner if it was shown
		if partner != nil {
			_ = partner.SendComponent(ctx, view.ReconnectingIndicator(false))
		}
	} else if client.IsSearching() {
		_ = client.SendComponent(ctx,
			view.ChatView(),
			view.ClientCount(count),
			view.Notify("Searching for a match..."),
		)
	}

	slog.Info("client reconnected", slog.Int("clients", count))
}

func (h *Hub) handleGraceExpired(ctx context.Context, client *Client) {
	if !client.isDetached() {
		return // stale: client already reattached
	}
	client.Close(ErrClientClosed)
	h.reg.Remove(client)

	if h.sess.InSession(client.Token()) {
		h.sess.End(client.Token())
	}
	if client.IsSearching() {
		h.matcher.Leave(ctx, client.Token())
	}

	slog.Info("grace period expired", slog.Int("clients", h.reg.Len()))
}

func (h *Hub) handleReconnectNotify(ctx context.Context, client *Client) {
	if !client.isDetached() {
		return // stale: client already reattached
	}
	if !h.sess.InSession(client.Token()) {
		return
	}
	partnerToken := h.sess.Partner(client.Token())
	partner := h.reg.ByToken(partnerToken)
	if partner == nil {
		return
	}
	_ = partner.SendComponent(ctx, view.ReconnectingIndicator(true))
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
	case LeaveMessage:
		h.handleLeave(ctx, incoming.client)
	case ChatMessage:
		// only arrives here if client is not in a session
		// (in-session messages go to the session sink)
		_ = msg
	case TypingMessage:
		_ = msg
	default:
		slog.Warn("unhandled message type",
			slog.String("type", string(incoming.envelope.Type)))
	}
}

func (h *Hub) handleFindMatch(ctx context.Context, client *Client, msg FindMatchMessage) {
	if h.sess.InSession(client.Token()) {
		return
	}

	if client.SearchOnCooldown(h.SearchCooldown) {
		_ = client.SendComponent(ctx, view.Notify(view.MsgCooldown))
		return
	}

	// when re-queuing from "Find Another" / "Block & Find
	// Another", the form only sends block; profile fields are
	// absent. Reuse the existing profile in that case.
	profile := client.Profile()
	if msg.Gender != "" {
		profile = &msg.Profile
	}
	if profile == nil {
		return
	}

	if profile.BlockedTokens == nil {
		profile.BlockedTokens = make(catalog.Set[match.Token])
	}

	if msg.Block && client.LastPartner() != "" {
		profile.BlockedTokens.Add(client.LastPartner())
	}

	client.SetProfile(profile)
	count := h.reg.Len()
	_ = client.SendComponent(ctx,
		view.ChatView(),
		view.ClientCount(count),
		view.Notify("Searching for a match..."),
	)

	attempt := h.matcher.Enqueue(ctx, client.Token(), profile)
	if attempt == "" {
		_ = client.SendComponent(ctx, view.LandingContent())
		return
	}
	client.BeginSearch(attempt)
}

func (h *Hub) handleLeave(ctx context.Context, client *Client) {
	// leave only reaches the hub sink when the client is not in
	// a session (in-session leave is handled by the session
	// goroutine). Cancel any pending search.
	if client.IsSearching() {
		h.matcher.Leave(ctx, client.Token())
	}
	client.ClearSearch()
	_ = client.SendComponent(ctx, view.LandingContent())
}

func (h *Hub) handleMatched(ctx context.Context, result match.Result) {
	clientA := h.reg.ByToken(result.A.Token)
	clientB := h.reg.ByToken(result.B.Token)

	staleA := clientA == nil || clientA.Attempt() != result.A.Attempt
	staleB := clientB == nil || clientB.Attempt() != result.B.Attempt

	if staleA && staleB {
		return
	}
	if staleA {
		clientB.BeginSearch(h.matcher.Enqueue(ctx, result.B.Token, clientB.Profile()))
		return
	}
	if staleB {
		clientA.BeginSearch(h.matcher.Enqueue(ctx, result.A.Token, clientA.Profile()))
		return
	}

	clientA.SetLastPartner(result.B.Token)
	clientB.SetLastPartner(result.A.Token)

	h.sess.Create(
		result.A.Token, result.B.Token,
		clientA, clientB,
		func(_, _ match.Token) {
			select {
			case h.sessionEnded <- struct{}{}:
			default:
			}
		},
	)

	profileForA := view.NewMatchedProfile(clientB.Profile(), clientA.Profile().Interests)
	profileForB := view.NewMatchedProfile(clientA.Profile(), clientB.Profile().Interests)
	_ = clientA.SendComponent(ctx, view.MatchedNotify(profileForA), view.SendButton(true))
	_ = clientB.SendComponent(ctx, view.MatchedNotify(profileForB), view.SendButton(true))
}

func (h *Hub) shutdown(ctx context.Context, matcherCancel context.CancelFunc) {
	matcherCancel()
	slog.Info("matcher stopped")

	h.drainChannels()

	h.sess.Shutdown()

	clients, _ := h.reg.Snapshot()
	h.reg.Clear()

	var closeWG sync.WaitGroup
	for _, client := range clients {
		closeWG.Go(func() {
			_ = client.SendComponent(ctx, view.SessionEndComponents(view.MsgServerReset, true)...)
			client.Close(ErrClientClosed)
			client.Wait(ctx)
		})
	}
	closeWG.Wait()

	slog.Info("hub shut down")
}

func (h *Hub) drainChannels() {
	for {
		select {
		case req := <-h.register:
			req.client.Close(ErrClientClosed)
			if req.conn != nil {
				_ = req.conn.Close()
			}
		case client := <-h.unregister:
			client.Close(ErrClientClosed)
		case <-h.graceExpired:
		case <-h.reconnectNotify:
		default:
			return
		}
	}
}

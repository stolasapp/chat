package hub

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/stolasapp/chat/internal/match"
	"github.com/stolasapp/chat/internal/view"
)

// sessionExcessiveNewlines matches 3+ consecutive newlines. Used
// to clamp whitespace in chat messages relayed by sessions.
var sessionExcessiveNewlines = regexp.MustCompile(`(\r?\n){3,}`)

// SessionService manages active chat sessions.
type SessionService interface {
	Create(tokenA, tokenB match.Token,
		clientA, clientB *Client,
		onEnd func(match.Token, match.Token))
	InSession(token match.Token) bool
	Partner(token match.Token) match.Token
	End(token match.Token)
	Shutdown()
}

// sessionConfig holds timing parameters for sessions.
type sessionConfig struct {
	idleTimeout time.Duration
	idleWarning time.Duration
}

// SessionManager tracks active sessions and their goroutines.
//
//nolint:containedctx // ctx is intentionally stored for session lifetime management
type SessionManager struct {
	active map[match.Token]*activeSession // +checklocks:mu
	mu     sync.RWMutex
	wg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc
	cfg    sessionConfig
}

var _ SessionService = (*SessionManager)(nil)

// NewSessionManager creates a SessionManager. The parent context
// is used to derive per-session contexts; cancelling it stops all
// sessions.
func NewSessionManager(parent context.Context, cfg sessionConfig) *SessionManager {
	ctx, cancel := context.WithCancel(parent)
	return &SessionManager{
		active: make(map[match.Token]*activeSession),
		ctx:    ctx,
		cancel: cancel,
		cfg:    cfg,
	}
}

// Create starts a new session between two clients.
func (m *SessionManager) Create(
	tokenA, tokenB match.Token,
	clientA, clientB *Client,
	onEnd func(match.Token, match.Token),
) {
	sess := &activeSession{
		tokenA:  tokenA,
		tokenB:  tokenB,
		clientA: clientA,
		clientB: clientB,
		events:  make(chan sessionEvent, 8), //nolint:mnd // small buffer
		onEnd:   onEnd,
		hubSink: clientA.sink.Load(),
		cfg:     m.cfg,
	}

	sink := sess.sessionSink()
	clientA.SetSink(sink)
	clientB.SetSink(sink)

	m.mu.Lock()
	m.active[tokenA] = sess
	m.active[tokenB] = sess
	m.mu.Unlock()

	m.wg.Go(func() {
		defer func() {
			m.mu.Lock()
			delete(m.active, tokenA)
			delete(m.active, tokenB)
			m.mu.Unlock()
			sess.restoreRouting()
		}()
		sess.run(m.ctx)
	})
}

// InSession reports whether the token belongs to an active session.
func (m *SessionManager) InSession(token match.Token) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.active[token]
	return ok
}

// Partner returns the partner token for the given token's session.
func (m *SessionManager) Partner(token match.Token) match.Token {
	m.mu.RLock()
	defer m.mu.RUnlock()
	sess, ok := m.active[token]
	if !ok {
		return ""
	}
	return sess.partner(token)
}

// End signals the session for the given token to end immediately.
func (m *SessionManager) End(token match.Token) {
	m.mu.RLock()
	sess, ok := m.active[token]
	m.mu.RUnlock()
	if !ok {
		return
	}
	select {
	case sess.events <- graceExpiredEvent{token: token}:
	case <-m.ctx.Done():
	}
}

// Shutdown cancels all sessions and waits for them to finish.
func (m *SessionManager) Shutdown() {
	m.cancel()
	m.wg.Wait()
}

// --- session events ---

type sessionEvent interface{ sessionEvent() }

type msgEvent struct {
	client *Client
	env    Envelope
}

type graceExpiredEvent struct {
	token match.Token
}

type idleWarnEvent struct {
	token match.Token
}

type idleKickEvent struct {
	token match.Token
}

func (msgEvent) sessionEvent()          {}
func (graceExpiredEvent) sessionEvent() {}
func (idleWarnEvent) sessionEvent()     {}
func (idleKickEvent) sessionEvent()     {}

// --- activeSession ---

// activeSession holds two persistent clients that are always
// non-nil for the lifetime of the session. Clients survive
// connection drops, so there is no need for nil guards on
// clientA/clientB.
type activeSession struct {
	tokenA, tokenB   match.Token
	clientA, clientB *Client
	events           chan sessionEvent
	idleA, idleB     *time.Timer
	warnA, warnB     *time.Timer
	ended            bool // true after a session-ending event
	onEnd            func(match.Token, match.Token)
	hubSink          *MessageSink // saved for routing restore
	cfg              sessionConfig
}

func (s *activeSession) run(ctx context.Context) {
	s.idleA = s.newIdleTimer(s.tokenA)
	s.idleB = s.newIdleTimer(s.tokenB)

	for {
		select {
		case ev := <-s.events:
			if s.processEvent(ctx, ev) {
				s.drainPending(ctx)
				return
			}
		case <-ctx.Done():
			s.stopTimers()
			return
		}
	}
}

// processEvent handles one event and returns true if the session
// should end.
func (s *activeSession) processEvent(ctx context.Context, raw sessionEvent) bool {
	switch event := raw.(type) {
	case msgEvent:
		return s.handleMessage(ctx, event)
	case graceExpiredEvent:
		s.handleGraceExpired(ctx, event)
		return true
	case idleWarnEvent:
		s.handleIdleWarn(ctx, event)
	case idleKickEvent:
		s.handleIdleKick(ctx, event)
		return s.ended
	}
	return false
}

// drainPending processes any buffered events remaining after the
// session has ended. This ensures both clients receive their final
// messages (e.g., both idle kick events when both fire
// simultaneously).
func (s *activeSession) drainPending(ctx context.Context) {
	for {
		select {
		case ev := <-s.events:
			s.processEvent(ctx, ev)
		default:
			return
		}
	}
}

func (s *activeSession) partner(token match.Token) match.Token {
	if token == s.tokenA {
		return s.tokenB
	}
	return s.tokenA
}

func (s *activeSession) clientFor(token match.Token) *Client {
	if token == s.tokenA {
		return s.clientA
	}
	return s.clientB
}

func (s *activeSession) partnerClient(token match.Token) *Client {
	if token == s.tokenA {
		return s.clientB
	}
	return s.clientA
}

// handleMessage returns true if the session should end (leave).
func (s *activeSession) handleMessage(ctx context.Context, event msgEvent) bool {
	parsed, err := event.env.Parse()
	if err != nil {
		slog.Warn("session: failed to parse message", slog.Any("error", err))
		return false
	}

	switch msg := parsed.(type) {
	case ChatMessage:
		s.relayChat(ctx, event.client, msg)
	case TypingMessage:
		s.relayTyping(ctx, event.client, msg)
	case LeaveMessage:
		s.handleLeave(ctx, event.client)
		return true
	case FindMatchMessage:
		// find_match while in session: ignore (client will
		// re-send after session routing is restored)
		_ = msg
	default:
		slog.Warn("session: unhandled message type",
			slog.String("type", string(event.env.Type)))
	}
	return false
}

func (s *activeSession) relayChat(ctx context.Context, sender *Client, msg ChatMessage) {
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return
	}
	text = sessionExcessiveNewlines.ReplaceAllString(text, "\n\n")

	partner := s.partnerClient(sender.Token())
	s.resetIdleTimer(sender.Token())

	_ = sender.SendComponent(ctx, view.ChatMessage(text, true, msg.Seq))
	_ = partner.SendComponent(ctx, view.TypingIndicator(false), view.ChatMessage(text, false, 0))
}

func (s *activeSession) relayTyping(ctx context.Context, sender *Client, msg TypingMessage) {
	partner := s.partnerClient(sender.Token())
	s.resetIdleTimer(sender.Token())
	_ = partner.SendComponent(ctx, view.TypingIndicator(msg.Active))
}

func (s *activeSession) handleLeave(ctx context.Context, sender *Client) {
	if s.ended {
		return
	}
	s.ended = true

	sender.ClearSearch()

	partner := s.partnerClient(sender.Token())
	_ = partner.SendComponent(ctx, view.SessionEndComponents(view.MsgPartnerLeft, true)...)
	_ = sender.SendComponent(ctx, view.SessionEndComponents(view.MsgYouLeft, true)...)

	s.stopTimers()
	s.onEnd(s.tokenA, s.tokenB)
}

func (s *activeSession) handleGraceExpired(ctx context.Context, event graceExpiredEvent) {
	if s.ended {
		return
	}
	s.ended = true

	partner := s.partnerClient(event.token)
	_ = partner.SendComponent(ctx, view.SessionEndComponents(view.MsgPartnerLeft, true)...)

	s.stopTimers()
	s.onEnd(s.tokenA, s.tokenB)
}

func (s *activeSession) handleIdleWarn(ctx context.Context, event idleWarnEvent) {
	if s.ended {
		return
	}
	client := s.clientFor(event.token)

	// guard: if this token already has a kick timer pending,
	// this is a duplicate warn delivery
	if s.warnTimerFor(event.token) != nil {
		return
	}

	msg := fmt.Sprintf(
		"You will be disconnected for inactivity in %d seconds.",
		int(s.cfg.idleWarning.Seconds()),
	)
	_ = client.SendComponent(ctx, view.Notify(msg))

	s.setWarnTimer(event.token, time.AfterFunc(s.cfg.idleWarning, func() {
		select {
		case s.events <- idleKickEvent(event):
		default:
		}
	}))
}

func (s *activeSession) handleIdleKick(ctx context.Context, event idleKickEvent) {
	client := s.clientFor(event.token)

	if s.ended {
		// session already ending from the other client's kick;
		// still send the idle message so this client sees it
		_ = client.SendComponent(ctx, view.SessionEndComponents(view.MsgIdleKicked, true)...)
		return
	}
	s.ended = true

	partner := s.partnerClient(event.token)
	_ = partner.SendComponent(ctx, view.SessionEndComponents(view.MsgPartnerLeft, true)...)
	_ = client.SendComponent(ctx, view.SessionEndComponents(view.MsgIdleKicked, true)...)

	slog.Info("client idle disconnected")

	s.stopTimers()
	s.onEnd(s.tokenA, s.tokenB)
}

// --- idle timer helpers ---

func (s *activeSession) warnAfter() time.Duration {
	return s.cfg.idleTimeout - s.cfg.idleWarning
}

func (s *activeSession) newIdleTimer(token match.Token) *time.Timer {
	return time.AfterFunc(s.warnAfter(), func() {
		select {
		case s.events <- idleWarnEvent{token: token}:
		default:
		}
	})
}

func (s *activeSession) idleTimerFor(token match.Token) *time.Timer {
	if token == s.tokenA {
		return s.idleA
	}
	return s.idleB
}

func (s *activeSession) warnTimerFor(token match.Token) *time.Timer {
	if token == s.tokenA {
		return s.warnA
	}
	return s.warnB
}

func (s *activeSession) setWarnTimer(token match.Token, t *time.Timer) {
	if token == s.tokenA {
		s.warnA = t
	} else {
		s.warnB = t
	}
}

func (s *activeSession) resetIdleTimer(token match.Token) {
	if t := s.idleTimerFor(token); t != nil {
		t.Stop()
	}
	if t := s.warnTimerFor(token); t != nil {
		t.Stop()
		s.setWarnTimer(token, nil)
	}

	if token == s.tokenA {
		s.idleA = s.newIdleTimer(token)
	} else {
		s.idleB = s.newIdleTimer(token)
	}
}

func (s *activeSession) stopIdleTimer(token match.Token) {
	if t := s.idleTimerFor(token); t != nil {
		t.Stop()
	}
	if t := s.warnTimerFor(token); t != nil {
		t.Stop()
		s.setWarnTimer(token, nil)
	}
	if token == s.tokenA {
		s.idleA = nil
	} else {
		s.idleB = nil
	}
}

func (s *activeSession) stopTimers() {
	s.stopIdleTimer(s.tokenA)
	s.stopIdleTimer(s.tokenB)
}

// sessionSink returns a MessageSink that routes messages into
// the session's event channel.
func (s *activeSession) sessionSink() *MessageSink {
	sink := MessageSink(func(ctx context.Context, c *Client, env Envelope) {
		select {
		case s.events <- msgEvent{client: c, env: env}:
		case <-ctx.Done():
		}
	})
	return &sink
}

// restoreRouting swaps both clients back to the hub sink.
func (s *activeSession) restoreRouting() {
	s.clientA.SetSink(s.hubSink)
	s.clientB.SetSink(s.hubSink)
}

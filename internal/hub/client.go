package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/a-h/templ"
	"github.com/gorilla/websocket"
	"golang.org/x/time/rate"

	"github.com/stolasapp/chat/internal/match"
	"github.com/stolasapp/chat/internal/view"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = (pongWait * 9) / 10 //nolint:mnd // 90% of pongWait
	maxMessageSize = 4096
	// sendBufSize is the per-client message buffer. Messages
	// accumulate here during disconnects and are drained by
	// Attach on reconnect. 64 slots covers several seconds of
	// partner chat during a typical grace period.
	sendBufSize = 64

	// per-client byte rate limit: 2 KB/s sustained with an 8 KB
	// burst, controlling total throughput rather than message count.
	clientByteRate  rate.Limit = 2048
	clientByteBurst int        = 8192
)

// ErrClientClosed is returned by Send methods when the client has
// been closed.
var ErrClientClosed = errors.New("client closed")

// MessageSink receives messages from a client's readPump. The hub
// sets itself as the default sink; sessions swap in a closure over
// their event channel.
type MessageSink func(context.Context, *Client, Envelope)

// Client is a long-lived object tied to a token. WebSocket
// connections attach and detach underneath it. The send channel
// survives across connections so messages buffer during
// disconnects and drain on reconnect.
//
//nolint:containedctx // ctx is the client-scoped context, cancelled on Close
type Client struct {
	// identity (set once at creation or via mutators)
	token        match.Token
	profile      *match.Profile
	lastPartner  match.Token
	attempt      match.Token
	lastSearchAt time.Time

	// long-lived (cancelled only by Close)
	ctx    context.Context
	cancel context.CancelCauseFunc
	send   chan []byte
	sink   atomic.Pointer[MessageSink]
	pumpWG *sync.WaitGroup

	// connection-scoped (cancelled by Detach or Close)
	conn       *websocket.Conn
	connCtx    context.Context
	connCancel context.CancelCauseFunc
	connDone   chan struct{} // closed when writePump exits
	readDone   chan struct{} // closed when readPump exits
	limiter    *rate.Limiter

	// timers (started by Detach, stopped by Attach/Close)
	graceTimer  *time.Timer
	notifyTimer *time.Timer

	// pump exit callback (set per-Attach, called by readPump)
	onReadExit func(*Client)
}

// NewClient creates a persistent client with no connection.
// Call Attach to start pumps on a WebSocket connection. The
// pumpWG tracks writePump goroutines for graceful shutdown.
func NewClient(
	token match.Token,
	defaultSink *MessageSink,
	pumpWG *sync.WaitGroup,
) *Client {
	ctx, cancel := context.WithCancelCause(context.Background())
	client := &Client{
		token:  token,
		send:   make(chan []byte, sendBufSize),
		ctx:    ctx,
		cancel: cancel,
		pumpWG: pumpWG,
	}
	client.sink.Store(defaultSink)
	return client
}

// --- identity accessors ---

// Token returns the client's unique session token.
func (c *Client) Token() match.Token { return c.token }

// Profile returns the client's match profile, or nil if none.
func (c *Client) Profile() *match.Profile { return c.profile }

// HasProfile reports whether the client has set a profile.
func (c *Client) HasProfile() bool { return c.profile != nil }

// SetProfile sets the client's match profile.
func (c *Client) SetProfile(p *match.Profile) { c.profile = p }

// Attempt returns the current search attempt token.
func (c *Client) Attempt() match.Token { return c.attempt }

// IsSearching reports whether the client has an active search.
func (c *Client) IsSearching() bool { return c.attempt != "" }

// BeginSearch records a new search attempt and marks the
// search timestamp for cooldown tracking.
func (c *Client) BeginSearch(attempt match.Token) {
	c.attempt = attempt
	c.lastSearchAt = time.Now()
}

// ClearSearch cancels the current search attempt and marks the
// timestamp for cooldown tracking.
func (c *Client) ClearSearch() {
	c.attempt = ""
	c.lastSearchAt = time.Now()
}

// SearchOnCooldown reports whether a new search would violate
// the given cooldown duration.
func (c *Client) SearchOnCooldown(cooldown time.Duration) bool {
	return !c.lastSearchAt.IsZero() && time.Since(c.lastSearchAt) < cooldown
}

// LastPartner returns the token of the client's most recent
// session partner.
func (c *Client) LastPartner() match.Token { return c.lastPartner }

// SetLastPartner records the token of the client's most recent
// session partner.
func (c *Client) SetLastPartner(token match.Token) { c.lastPartner = token }

// --- connection lifecycle ---

// Attach binds a WebSocket connection and starts read/write
// pumps. If already attached, cancels the old connection and
// waits for its pumps to exit before starting new ones. Stops
// any grace/notify timers from a prior Detach.
//
// Returns any messages that were buffered on the send channel
// during the disconnect gap. The caller can re-enqueue them
// after sending recovery state.
func (c *Client) Attach(conn *websocket.Conn, onReadExit func(*Client)) [][]byte {
	if c.connCancel != nil {
		c.connCancel(ErrClientClosed)
		<-c.connDone
	}

	// drain messages buffered during the disconnect gap.
	// safe because Attach is called from the hub's single event
	// loop goroutine, and the old pumps have fully exited
	// (connDone closed above).
	var pending [][]byte
	for len(c.send) > 0 {
		pending = append(pending, <-c.send)
	}

	c.conn = conn
	c.connCtx, c.connCancel = context.WithCancelCause(c.ctx)
	c.connDone = make(chan struct{})
	c.readDone = make(chan struct{})
	c.limiter = rate.NewLimiter(clientByteRate, clientByteBurst)
	c.onReadExit = onReadExit

	c.stopTimers()

	go c.readPump()
	c.pumpWG.Go(c.writePump)
	return pending
}

// Detach starts grace and notify timers after the connection is
// lost. Called by the hub after the readPump exits. The pumps
// self-terminate via connCtx cancellation; Detach does not stop
// them. Attach stops the timers if reconnect happens before
// expiry.
func (c *Client) Detach(
	gracePeriod time.Duration, onGraceExpired func(*Client),
	notifyDelay time.Duration, onNotify func(*Client),
) {
	c.graceTimer = time.AfterFunc(gracePeriod, func() {
		onGraceExpired(c)
	})
	c.notifyTimer = time.AfterFunc(notifyDelay, func() {
		onNotify(c)
	})
}

// SetSink atomically swaps the message routing destination.
func (c *Client) SetSink(sink *MessageSink) {
	c.sink.Store(sink)
}

// --- send methods ---

// Send marshals a Message into a JSON Envelope and enqueues it for
// sending. Used for protocol messages (token, key_exchange, etc.)
// that the client JS handles directly. Blocks until queued or ctx
// is canceled.
func (c *Client) Send(ctx context.Context, msg Message) error {
	data, err := MarshalMessage(msg)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}
	return c.SendRaw(ctx, data)
}

// SendComponent renders one or more templ Components into a single
// WS frame and enqueues it for sending. Multiple components are
// concatenated so HTMX processes all OOB swaps atomically. Blocks
// until queued or ctx is cancelled.
func (c *Client) SendComponent(ctx context.Context, components ...templ.Component) error {
	var buf bytes.Buffer
	for _, component := range components {
		if err := component.Render(ctx, &buf); err != nil {
			return fmt.Errorf("render component: %w", err)
		}
	}
	return c.SendRaw(ctx, buf.Bytes())
}

// SendRaw enqueues pre-rendered bytes for sending as a text frame.
// Blocks until queued or ctx is cancelled. Selects on the client
// lifetime context (not connection), so sends buffer during
// disconnects and drain on reconnect.
func (c *Client) SendRaw(ctx context.Context, data []byte) error {
	select {
	case c.send <- data:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.ctx.Done():
		return context.Cause(c.ctx) //nolint:contextcheck // reading cause, not creating context
	}
}

// TrySendRaw is a non-blocking variant of SendRaw. Returns false
// if the send buffer is full. Useful for best-effort broadcasts
// where dropping a message is preferable to stalling the caller.
func (c *Client) TrySendRaw(data []byte) bool {
	select {
	case c.send <- data:
		return true
	default:
		return false
	}
}

// --- lifecycle ---

// Done returns a channel that is closed when the client is
// permanently closed. Safe to call from any goroutine.
func (c *Client) Done() <-chan struct{} { return c.ctx.Done() }

// Close signals permanent client shutdown with the given cause.
// Stops grace/notify timers and cancels the client context (which
// also cancels any active connection). Does not block; use Wait to
// block until the write pump has drained. Safe to call multiple
// times.
func (c *Client) Close(cause error) {
	c.stopTimers()
	c.cancel(cause)
}

// Wait blocks until the client is permanently closed and its
// connection has fully drained, or until ctx expires.
func (c *Client) Wait(ctx context.Context) {
	select {
	case <-c.ctx.Done():
	case <-ctx.Done():
		return
	}
	if c.connDone != nil {
		select {
		case <-c.connDone:
		case <-ctx.Done():
		}
	}
}

// isDetached reports whether the client has no active connection.
// Only safe to call from the hub's event loop goroutine.
func (c *Client) isDetached() bool {
	return c.connCtx == nil || c.connCtx.Err() != nil
}

func (c *Client) stopTimers() {
	if c.graceTimer != nil {
		c.graceTimer.Stop()
		c.graceTimer = nil
	}
	if c.notifyTimer != nil {
		c.notifyTimer.Stop()
		c.notifyTimer = nil
	}
}

// --- pumps ---

func (c *Client) readPump() {
	defer func() {
		c.connCancel(ErrClientClosed)
		c.onReadExit(c)
		close(c.readDone)
	}()

	c.conn.SetReadLimit(maxMessageSize)
	if err := c.conn.SetReadDeadline(time.Now().Add(pongWait)); err != nil {
		slog.Warn("failed to set initial read deadline", slog.Any("error", err))
		return
	}
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	// when the connection context is canceled, expire the read
	// deadline to unblock ReadMessage. SetReadDeadline on the
	// underlying net.Conn is safe to call concurrently with a
	// blocked Read. The pong handler runs synchronously inside
	// ReadMessage so it cannot race.
	stop := context.AfterFunc(c.connCtx, func() {
		_ = c.conn.SetReadDeadline(time.Now())
	})
	defer stop()

	for {
		msgType, msg, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(
				err,
				websocket.CloseGoingAway,
				websocket.CloseNormalClosure,
			) {
				slog.Warn("unexpected ws close", slog.Any("error", err))
			}
			return
		}

		if !c.limiter.AllowN(time.Now(), len(msg)) {
			_ = c.SendComponent(c.ctx, view.Notify("Slow down, you are sending too fast."))
			continue
		}

		if msgType != websocket.TextMessage {
			slog.Warn("unexpected ws message type", slog.Int("type", msgType))
			continue
		}

		var env Envelope
		if err := json.Unmarshal(msg, &env); err != nil {
			slog.Warn("invalid envelope", slog.Any("error", err))
			continue
		}
		slog.Debug("ws recv", slog.String("type", string(env.Type)))

		(*c.sink.Load())(c.ctx, c, env)
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.connCancel(ErrClientClosed)
		<-c.readDone
		// only drain to the connection on permanent close;
		// on disconnect, messages stay buffered for reconnect
		if c.ctx.Err() != nil {
			c.drainAndClose()
		}
		if c.conn != nil {
			if err := c.conn.Close(); err != nil {
				slog.Warn("ws conn close failed", slog.Any("error", err))
			}
		}
		close(c.connDone)
	}()

	for {
		select {
		case msg := <-c.send:
			if err := c.write(websocket.TextMessage, msg); err != nil {
				slog.Warn("ws write failed", slog.Any("error", err))
				return
			}
		case <-c.connCtx.Done():
			return
		case <-ticker.C:
			if err := c.write(websocket.PingMessage, nil); err != nil {
				slog.Warn("ws ping failed", slog.Any("error", err))
				return
			}
		}
	}
}

func (c *Client) write(msgType int, data []byte) error {
	if err := c.conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
		return err
	}
	return c.conn.WriteMessage(msgType, data)
}

// drainAndClose flushes remaining buffered messages. No close
// frame is sent so the HTMX ws extension sees an unclean close
// and auto-reconnects.
func (c *Client) drainAndClose() {
	for {
		select {
		case msg := <-c.send:
			if err := c.write(websocket.TextMessage, msg); err != nil {
				slog.Warn("drain: write failed", slog.Any("error", err))
				return
			}
		default:
			return
		}
	}
}

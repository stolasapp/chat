package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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
	sendBufSize    = 256

	// per-client message rate limit: 0.5 messages/sec with a
	// burst of 5, allowing ~30 messages/min sustained.
	clientRate  rate.Limit = 0.5
	clientBurst int        = 5
)

// ErrClientClosed is returned by Send methods when the client has
// been closed.
var ErrClientClosed = errors.New("client closed")

// Client bridges a single WebSocket connection to the Hub.
//
//nolint:containedctx // ctx is the connection-scoped context, cancelled on Close
type Client struct {
	hub          *Hub
	conn         *websocket.Conn
	send         chan []byte
	ctx          context.Context
	cancel       context.CancelCauseFunc
	readStopped  chan struct{}
	done         chan struct{}
	token        match.Token
	refreshToken match.Token
	limiter      *rate.Limiter

	// profile, lastPartner, and attempt are only accessed from
	// the hub's Run goroutine. Do not read or write from other
	// goroutines.
	profile     *match.Profile
	lastPartner match.Token
	attempt     match.Token
}

// NewClient creates a client and starts its read/write pumps.
// The context is used for the client's lifetime and cancelled
// on Close.
func (h *Hub) NewClient(ctx context.Context, conn *websocket.Conn, token, refreshToken match.Token) *Client {
	ctx, cancel := context.WithCancelCause(ctx)
	client := &Client{
		hub:          h,
		conn:         conn,
		send:         make(chan []byte, sendBufSize),
		ctx:          ctx,
		cancel:       cancel,
		readStopped:  make(chan struct{}),
		done:         make(chan struct{}),
		token:        token,
		refreshToken: refreshToken,
		limiter:      rate.NewLimiter(clientRate, clientBurst),
	}
	h.clientWG.Go(client.writePump)
	go client.readPump()
	return client
}

// Token returns the client's session token.
func (c *Client) Token() match.Token { return c.token }

// RefreshToken returns the client's refresh token.
func (c *Client) RefreshToken() match.Token { return c.refreshToken }

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
// Blocks until queued or ctx is cancelled. If the client has been
// closed, returns the cause passed to Close.
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

// Close signals the client to shut down with the given cause.
// Does not block; use Wait to block until the write pump has
// drained. Safe to call multiple times.
func (c *Client) Close(cause error) {
	c.cancel(cause)
}

// Wait blocks until the write pump has finished draining buffered
// messages and closed the connection, or until ctx expires.
func (c *Client) Wait(ctx context.Context) {
	select {
	case <-c.done:
	case <-ctx.Done():
	}
}

func (c *Client) readPump() {
	defer close(c.readStopped)
	defer c.hub.Unregister(c)

	c.conn.SetReadLimit(maxMessageSize)
	if err := c.conn.SetReadDeadline(time.Now().Add(pongWait)); err != nil {
		slog.Warn("failed to set initial read deadline", slog.Any("error", err))
		return
	}
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	// when the client context is canceled, expire the read
	// deadline to unblock ReadMessage. SetReadDeadline on the
	// underlying net.Conn is safe to call concurrently with a
	// blocked Read. The pong handler runs synchronously inside
	// ReadMessage so it cannot race.
	stop := context.AfterFunc(c.ctx, func() {
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

		if !c.limiter.Allow() {
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

		c.hub.Incoming(c.ctx, c, env)
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		// 1. stop the read pump
		c.cancel(ErrClientClosed)
		<-c.readStopped
		// 2. drain buffered messages
		c.drainAndClose()
		// 3. close the connection
		if err := c.conn.Close(); err != nil {
			slog.Warn("ws conn close failed", slog.Any("error", err))
		}
		close(c.done)
	}()

	for {
		select {
		case msg := <-c.send:
			if err := c.write(websocket.TextMessage, msg); err != nil {
				slog.Warn("ws write failed", slog.Any("error", err))
				return
			}
		case <-c.ctx.Done():
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

package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/a-h/templ"
	"github.com/gorilla/websocket"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = (pongWait * 9) / 10 //nolint:mnd // 90% of pongWait
	maxMessageSize = 4096
	sendBufSize    = 256
)

// ErrClientClosed is returned by Send methods when the client has
// been closed.
var ErrClientClosed = errors.New("client closed")

// Client bridges a single WebSocket connection to the Hub.
type Client struct {
	hub          *Hub
	conn         *websocket.Conn
	send         chan []byte
	done         chan struct{}
	closeOnce    sync.Once
	token        string
	refreshToken string
}

// NewClient creates a client and starts its read/write pumps.
func NewClient(hub *Hub, conn *websocket.Conn, token, refreshToken string) *Client {
	client := &Client{
		hub:          hub,
		conn:         conn,
		send:         make(chan []byte, sendBufSize),
		done:         make(chan struct{}),
		token:        token,
		refreshToken: refreshToken,
	}
	go client.writePump()
	go client.readPump()
	return client
}

// Token returns the client's session token.
func (c *Client) Token() string { return c.token }

// RefreshToken returns the client's refresh token.
func (c *Client) RefreshToken() string { return c.refreshToken }

// Send marshals a Message into a JSON Envelope and enqueues it for
// sending. Used for protocol messages (token, key_exchange, etc.)
// that the client JS handles directly. Blocks until queued or ctx
// is cancelled.
func (c *Client) Send(ctx context.Context, msg Message) error {
	data, err := MarshalMessage(msg)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}
	return c.SendRaw(ctx, data)
}

// SendComponent renders a templ Component and enqueues the resulting
// HTML fragment for sending. Used for server-rendered HTML that HTMX
// swaps into the DOM. Blocks until queued or ctx is cancelled.
func (c *Client) SendComponent(ctx context.Context, component templ.Component) error {
	var buf bytes.Buffer
	if err := component.Render(ctx, &buf); err != nil {
		return fmt.Errorf("render component: %w", err)
	}
	return c.SendRaw(ctx, buf.Bytes())
}

// SendRaw enqueues pre-rendered bytes for sending as a text frame.
// Blocks until queued or ctx is cancelled. Returns ErrClientClosed
// if the client has been closed.
func (c *Client) SendRaw(ctx context.Context, data []byte) error {
	select {
	case c.send <- data:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return ErrClientClosed
	}
}

// Close signals the client to shut down. The write pump will drain
// any remaining buffered messages, send a WS close frame, and tear
// down the TCP connection. Safe to call multiple times.
func (c *Client) Close() {
	c.closeOnce.Do(func() {
		close(c.done)
	})
}

func (c *Client) readPump() {
	defer func() {
		// select on done to avoid blocking if the hub's Run loop
		// has already exited and is no longer draining unregister
		select {
		case c.hub.unregister <- c:
		case <-c.done:
		}
	}()

	c.conn.SetReadLimit(maxMessageSize)
	if err := c.conn.SetReadDeadline(time.Now().Add(pongWait)); err != nil {
		slog.Warn("failed to set initial read deadline", "error", err)
		return
	}
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	for {
		_, msg, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(
				err,
				websocket.CloseGoingAway,
				websocket.CloseNormalClosure,
			) {
				slog.Warn("unexpected ws close", "error", err)
			}
			return
		}

		var env Envelope
		if err := json.Unmarshal(msg, &env); err != nil {
			slog.Warn("invalid envelope", "error", err)
			continue
		}
		slog.Debug("ws recv", "type", env.Type)
		// Phase 2: discard; later phases dispatch by type.
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		if err := c.conn.Close(); err != nil {
			slog.Warn("ws conn close failed", "error", err)
		}
	}()

	for {
		select {
		case msg := <-c.send:
			if err := c.writeMsg(msg); err != nil {
				slog.Warn("ws write failed", "error", err)
				return
			}
		case <-c.done:
			c.drainAndClose()
			return
		case <-ticker.C:
			if err := c.writePing(); err != nil {
				slog.Warn("ws ping failed", "error", err)
				return
			}
		}
	}
}

// writeMsg writes a text message and drains currently buffered
// messages from the send channel.
func (c *Client) writeMsg(msg []byte) error {
	if err := c.conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
		return err
	}
	if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
		return err
	}
	for range len(c.send) {
		if err := c.conn.WriteMessage(websocket.TextMessage, <-c.send); err != nil {
			return err
		}
	}
	return nil
}

// drainAndClose flushes remaining buffered messages and sends a WS
// close frame.
func (c *Client) drainAndClose() {
	for {
		select {
		case msg := <-c.send:
			if err := c.conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
				slog.Warn("drain: set deadline failed", "error", err)
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				slog.Warn("drain: write failed", "error", err)
				return
			}
		default:
			if err := c.conn.WriteMessage(websocket.CloseMessage, nil); err != nil {
				slog.Warn("drain: close frame failed", "error", err)
			}
			return
		}
	}
}

func (c *Client) writePing() error {
	if err := c.conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
		return err
	}
	return c.conn.WriteMessage(websocket.PingMessage, nil)
}

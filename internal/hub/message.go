// Package hub manages WebSocket client lifecycle and message routing.
package hub

import (
	"encoding/json"
	"time"
)

// MessageType identifies the kind of message in a WebSocket envelope.
type MessageType string

// WebSocket message types.
const (
	MessageTypeToken       MessageType = "token"
	MessageTypeFindMatch   MessageType = "find_match"
	MessageTypeCancelMatch MessageType = "cancel_match"
	MessageTypeMessage     MessageType = "message"
	MessageTypeKeyExchange MessageType = "key_exchange"
	MessageTypeLeave       MessageType = "leave"
	MessageTypeReconnect   MessageType = "reconnect"
	MessageTypeRateLimited MessageType = "rate_limited"
)

// Message is implemented by all typed WebSocket payloads. Each
// concrete type maps to exactly one MessageType. The method is
// exported so callers outside the hub package can construct messages.
type Message interface {
	MessageType() MessageType
}

// Envelope is the JSON wire format for all WebSocket messages.
type Envelope struct {
	Type    MessageType     `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// NewEnvelope creates an Envelope from a typed message, marshaling
// the payload.
func NewEnvelope(msg Message) (Envelope, error) {
	payload, err := json.Marshal(msg)
	if err != nil {
		return Envelope{}, err
	}
	return Envelope{
		Type:    msg.MessageType(),
		Payload: payload,
	}, nil
}

// MarshalMessage creates an Envelope from a Message and returns its
// JSON encoding. This is the common path for both channel-based
// sends (Client.Send) and direct writes (writeEnvelope).
func MarshalMessage(msg Message) ([]byte, error) {
	env, err := NewEnvelope(msg)
	if err != nil {
		return nil, err
	}
	return json.Marshal(env)
}

// TokenMessage is sent to the client on initial connection with
// session and refresh tokens.
type TokenMessage struct {
	Token   string `json:"token"`
	Refresh string `json:"refresh"`
}

// MessageType implements Message.
func (TokenMessage) MessageType() MessageType { return MessageTypeToken }

// RateLimitedMessage is sent when a client exceeds the connection
// rate limit, indicating how long to wait before retrying.
type RateLimitedMessage struct {
	RetryAfter time.Duration `json:"-"`
}

// MessageType implements Message.
func (*RateLimitedMessage) MessageType() MessageType { return MessageTypeRateLimited }

// MarshalJSON encodes RetryAfter as seconds.
func (m RateLimitedMessage) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		RetryAfter float64 `json:"retry_after"`
	}{
		RetryAfter: m.RetryAfter.Seconds(),
	})
}

// UnmarshalJSON decodes retry_after from seconds.
func (m *RateLimitedMessage) UnmarshalJSON(data []byte) error {
	var raw struct {
		RetryAfter float64 `json:"retry_after"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	m.RetryAfter = time.Duration(raw.RetryAfter * float64(time.Second))
	return nil
}

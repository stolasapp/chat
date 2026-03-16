// Package hub manages WebSocket client lifecycle and message routing.
package hub

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/stolasapp/chat/internal/match"
)

// MessageType identifies the kind of message in a WebSocket envelope.
type MessageType string

// WebSocket message types.
const (
	MessageTypeToken       MessageType = "token"
	MessageTypeFindMatch   MessageType = "find_match"
	MessageTypeLeave       MessageType = "leave"
	MessageTypeMessage     MessageType = "message"
	MessageTypeRateLimited MessageType = "rate_limited"
)

// Message is implemented by all typed WebSocket payloads. Each
// concrete type maps to exactly one MessageType.
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

// Parse unmarshals the envelope payload into the appropriate
// typed message based on Type.
func (e Envelope) Parse() (Message, error) {
	switch e.Type {
	case MessageTypeToken:
		return unmarshalPayload[TokenMessage](e.Payload)
	case MessageTypeFindMatch:
		return unmarshalPayload[FindMatchMessage](e.Payload)
	case MessageTypeLeave:
		return LeaveMessage{}, nil
	case MessageTypeMessage:
		return unmarshalPayload[ChatMessage](e.Payload)
	case MessageTypeRateLimited:
		return unmarshalPayload[RateLimitedMessage](e.Payload)
	default:
		return nil, fmt.Errorf("unknown message type: %q", e.Type)
	}
}

func unmarshalPayload[T Message](payload json.RawMessage) (T, error) {
	var msg T
	if err := json.Unmarshal(payload, &msg); err != nil {
		var zero T
		return zero, fmt.Errorf("unmarshal %T: %w", msg, err)
	}
	return msg, nil
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
	Token   match.Token `json:"token"`
	Refresh match.Token `json:"refresh"`
}

// MessageType implements Message.
func (TokenMessage) MessageType() MessageType { return MessageTypeToken }

// FindMatchMessage is sent by the client to enter the match queue.
// When Block is true, the client's last partner is added to the
// block list before re-queuing.
type FindMatchMessage struct {
	match.Profile

	Block bool `json:"block,omitempty"`
}

// MessageType implements Message.
func (FindMatchMessage) MessageType() MessageType { return MessageTypeFindMatch }

// LeaveMessage is sent by the client to leave the current chat.
type LeaveMessage struct{}

// MessageType implements Message.
func (LeaveMessage) MessageType() MessageType { return MessageTypeLeave }

// ChatMessage is sent by the client to relay a text message
// to their partner.
type ChatMessage struct {
	Text string `json:"text"`
}

// MessageType implements Message.
func (ChatMessage) MessageType() MessageType { return MessageTypeMessage }

// RateLimitedMessage is sent when a client exceeds the connection
// rate limit, indicating how long to wait before retrying.
//
//nolint:recvcheck // MarshalJSON requires value receiver, UnmarshalJSON requires pointer
type RateLimitedMessage struct {
	RetryAfter time.Duration `json:"-"`
}

// MessageType implements Message.
func (RateLimitedMessage) MessageType() MessageType { return MessageTypeRateLimited }

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

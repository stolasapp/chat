package hub

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMarshalMessage_TokenMessage(t *testing.T) {
	t.Parallel()

	data, err := MarshalMessage(TokenMessage{
		Token:   "abc123",
		Refresh: "def456",
	})
	require.NoError(t, err)

	var env Envelope
	require.NoError(t, json.Unmarshal(data, &env))

	assert.Equal(t, MessageTypeToken, env.Type)

	var token TokenMessage
	require.NoError(t, json.Unmarshal(env.Payload, &token))
	assert.Equal(t, "abc123", token.Token)
	assert.Equal(t, "def456", token.Refresh)
}

func TestMarshalMessage_RateLimitedMessage(t *testing.T) {
	t.Parallel()

	data, err := MarshalMessage(&RateLimitedMessage{
		RetryAfter: 30 * time.Second,
	})
	require.NoError(t, err)

	var env Envelope
	require.NoError(t, json.Unmarshal(data, &env))

	assert.Equal(t, MessageTypeRateLimited, env.Type)

	// verify wire format uses seconds
	var raw map[string]any
	require.NoError(t, json.Unmarshal(env.Payload, &raw))
	assert.InDelta(t, 30.0, raw["retry_after"], 0.001)

	// verify round-trip through typed struct
	var rateLimited RateLimitedMessage
	require.NoError(t, json.Unmarshal(env.Payload, &rateLimited))
	assert.Equal(t, 30*time.Second, rateLimited.RetryAfter)
}

func TestMarshalMessage_AllTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		msg          Message
		expectedType MessageType
	}{
		{
			name:         "token",
			msg:          TokenMessage{Token: "t", Refresh: "r"},
			expectedType: MessageTypeToken,
		},
		{
			name:         "rate_limited",
			msg:          &RateLimitedMessage{RetryAfter: time.Second},
			expectedType: MessageTypeRateLimited,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			data, err := MarshalMessage(test.msg)
			require.NoError(t, err)

			var env Envelope
			require.NoError(t, json.Unmarshal(data, &env))
			assert.Equal(t, test.expectedType, env.Type)
			assert.NotEmpty(t, env.Payload)
		})
	}
}

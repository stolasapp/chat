package hub

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stolasapp/chat/internal/catalog"
	"github.com/stolasapp/chat/internal/match"
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
	assert.Equal(t, match.Token("abc123"), token.Token)
	assert.Equal(t, match.Token("def456"), token.Refresh)
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
			name:         "message",
			msg:          ChatMessage{Text: "hello"},
			expectedType: MessageTypeMessage,
		},
		{
			name:         "typing",
			msg:          TypingMessage{Active: true},
			expectedType: MessageTypeTyping,
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

func TestEnvelope_Parse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		env     Envelope
		wantErr bool
	}{
		{
			name: "valid find_match minimal",
			env: Envelope{
				Type: MessageTypeFindMatch,
				Payload: json.RawMessage(`{
					"gender": "male",
					"role": "dominant",
					"interests": [],
					"filter_gender": [],
					"filter_role": [],
					"exclude_interests": []
				}`),
			},
		},
		{
			name: "valid find_match with interests",
			env: Envelope{
				Type: MessageTypeFindMatch,
				Payload: json.RawMessage(`{
					"gender": "female",
					"role": "submissive",
					"interests": ["basketball", "tennis"],
					"filter_gender": ["male"],
					"filter_role": ["dominant"],
					"exclude_interests": ["team-sports"]
				}`),
			},
		},
		{
			name: "valid message",
			env: Envelope{
				Type:    MessageTypeMessage,
				Payload: json.RawMessage(`{"text":"hello world"}`),
			},
		},
		{
			name: "valid message empty text",
			env: Envelope{
				Type:    MessageTypeMessage,
				Payload: json.RawMessage(`{"text":""}`),
			},
		},
		{
			name: "valid leave",
			env:  Envelope{Type: MessageTypeLeave},
		},
		{
			name: "valid typing",
			env: Envelope{
				Type:    MessageTypeTyping,
				Payload: json.RawMessage(`{"active":true}`),
			},
		},
		{
			name: "valid find_match with block",
			env: Envelope{
				Type: MessageTypeFindMatch,
				Payload: json.RawMessage(`{
					"gender": "male",
					"role": "dominant",
					"interests": [],
					"filter_gender": [],
					"filter_role": [],
					"exclude_interests": [],
					"block": true
				}`),
			},
		},
		{
			name: "invalid gender",
			env: Envelope{
				Type:    MessageTypeFindMatch,
				Payload: json.RawMessage(`{"gender": "unknown", "role": "dominant"}`),
			},
			wantErr: true,
		},
		{
			name: "invalid json",
			env: Envelope{
				Type:    MessageTypeFindMatch,
				Payload: json.RawMessage(`{invalid}`),
			},
			wantErr: true,
		},
		{
			name:    "unknown type",
			env:     Envelope{Type: "nosuchtype"},
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			msg, err := test.env.Parse()
			if test.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, msg)
			assert.Equal(t, test.env.Type, msg.MessageType())
		})
	}
}

func TestEnvelope_Parse_FindMatchRoundTrip(t *testing.T) {
	t.Parallel()

	env := Envelope{
		Type: MessageTypeFindMatch,
		Payload: json.RawMessage(`{
			"gender": "female",
			"role": "switch",
			"interests": ["basketball", "team-sports"],
			"filter_gender": ["male", "nonbinary"],
			"filter_role": ["dominant"],
			"exclude_interests": ["individual-sports"]
		}`),
	}

	msg, err := env.Parse()
	require.NoError(t, err)

	findMatch, ok := msg.(FindMatchMessage)
	require.True(t, ok)

	assert.Equal(t, catalog.GenderFemale, findMatch.Gender)
	assert.Equal(t, catalog.RoleSwitch, findMatch.Role)
	assert.True(t, findMatch.Interests.Contains(catalog.InterestBasketball))
	assert.True(t, findMatch.Interests.Contains(catalog.InterestTeamSports))
	assert.Equal(t, 2, findMatch.Interests.Len())
	assert.True(t, findMatch.FilterGender.Contains(catalog.GenderMale))
	assert.True(t, findMatch.FilterGender.Contains(catalog.GenderNonBinary))
	assert.True(t, findMatch.FilterRole.Contains(catalog.RoleDominant))
	assert.True(t, findMatch.ExcludeInterests.Contains(catalog.InterestIndividualSports))
}

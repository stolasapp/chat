package hub

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stolasapp/chat/internal/catalog"
)

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
			msg:          TokenMessage{Token: "abc123"},
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
			name: "token is server-only, not parseable",
			env: Envelope{
				Type:    MessageTypeToken,
				Payload: json.RawMessage(`{"token":"abc123"}`),
			},
			wantErr: true,
		},
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
					"exclude_interests": ["golf"]
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
			name: "valid find_match with block as string",
			env: Envelope{
				Type: MessageTypeFindMatch,
				Payload: json.RawMessage(`{
					"gender": "male",
					"role": "dominant",
					"interests": [],
					"filter_gender": [],
					"filter_role": [],
					"exclude_interests": [],
					"block": "true"
				}`),
			},
		},
		{
			name: "valid find_match block-only re-queue",
			env: Envelope{
				Type:    MessageTypeFindMatch,
				Payload: json.RawMessage(`{"block":"true"}`),
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
			"species": "wolf",
			"interests": ["basketball", "tennis"],
			"filter_gender": ["male", "non_binary"],
			"filter_role": ["dominant"],
			"exclude_interests": ["golf"]
		}`),
	}

	msg, err := env.Parse()
	require.NoError(t, err)

	findMatch, ok := msg.(FindMatchMessage)
	require.True(t, ok)

	assert.Equal(t, catalog.GenderFemale, findMatch.Gender)
	assert.Equal(t, catalog.RoleSwitch, findMatch.Role)
	assert.Equal(t, catalog.Species("Wolf"), findMatch.Species)
	assert.True(t, findMatch.Interests.Contains(catalog.Interest("Basketball")))
	assert.True(t, findMatch.Interests.Contains(catalog.Interest("Tennis")))
	assert.Equal(t, 2, findMatch.Interests.Len())
	assert.True(t, findMatch.FilterGender.Contains(catalog.GenderMale))
	assert.True(t, findMatch.FilterGender.Contains(catalog.GenderNonBinary))
	assert.True(t, findMatch.FilterRole.Contains(catalog.RoleDominant))
	assert.True(t, findMatch.ExcludeInterests.Contains(catalog.Interest("Golf")))
}

func TestFindMatchMessage_BlockFieldParsing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		payload string
		want    bool
	}{
		{"bool true", `{"block":true}`, true},
		{"bool false", `{"block":false}`, false},
		{"string true", `{"block":"true"}`, true},
		{"string false", `{"block":"false"}`, false},
		{"absent", `{}`, false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			env := Envelope{
				Type:    MessageTypeFindMatch,
				Payload: json.RawMessage(test.payload),
			}
			msg, err := env.Parse()
			require.NoError(t, err)
			findMatch, ok := msg.(FindMatchMessage)
			require.True(t, ok)
			assert.Equal(t, test.want, findMatch.Block)
		})
	}
}

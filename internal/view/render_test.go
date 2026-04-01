package view

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChatView_NoMatchedSubstring(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	require.NoError(t, ChatView().Render(context.Background(), &buf))
	assert.False(t, strings.Contains(buf.String(), MsgMatched),
		"ChatView should not contain MsgMatched (breaks hub test assertions)")
}

func BenchmarkChatView(b *testing.B) {
	ctx := context.Background()
	var buf bytes.Buffer
	for b.Loop() {
		buf.Reset()
		_ = ChatView().Render(ctx, &buf)
	}
}

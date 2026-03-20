package view

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLinkifyHTML(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		text     string
		self     bool
		wantURL  bool
		wantSafe bool
	}{
		{
			name: "plain text escaped",
			text: "hello <script>alert(1)</script>",
		},
		{
			name:    "single url becomes link",
			text:    "https://example.com",
			wantURL: true,
		},
		{
			name:    "url in sentence",
			text:    "check https://example.com for details",
			wantURL: true,
		},
		{
			name:    "url with path and query",
			text:    "see https://example.com/path?q=1&r=2#frag ok",
			wantURL: true,
		},
		{
			name: "no scheme not linked",
			text: "go to example.com now",
		},
		{
			name:     "xss in url rejected",
			text:     `<a href="javascript:alert(1)">click</a>`,
			wantSafe: true,
		},
		{
			name: "empty string",
			text: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result := linkPolicy.Sanitize(linkifyHTML(test.text, false))

			if test.wantURL {
				assert.Contains(t, result, "<a ")
				assert.Contains(t, result, `rel="nofollow noreferrer`)
				assert.Contains(t, result, `target="_blank"`)
			}

			if test.wantSafe {
				assert.NotContains(t, result, `href="javascript:`)
				assert.NotContains(t, result, "<script")
			}

			assert.NotContains(t, result, "<script>")
		})
	}
}

func TestLinkifyHTML_SelfStyling(t *testing.T) {
	t.Parallel()

	selfResult := linkPolicy.Sanitize(linkifyHTML("https://example.com", true))
	otherResult := linkPolicy.Sanitize(linkifyHTML("https://example.com", false))

	assert.Contains(t, selfResult, "text-amber-200")
	assert.Contains(t, otherResult, "text-amber-700")
}

func TestLinkifyHTML_MultipleURLs(t *testing.T) {
	t.Parallel()

	result := linkPolicy.Sanitize(
		linkifyHTML("try https://a.com and http://b.com too", false),
	)

	assert.Contains(t, result, `href="https://a.com"`)
	assert.Contains(t, result, `href="http://b.com"`)
	assert.Contains(t, result, "try ")
	assert.Contains(t, result, " and ")
	assert.Contains(t, result, " too")
}

func TestLinkifyHTML_ParenthesizedURL(t *testing.T) {
	t.Parallel()

	result := linkPolicy.Sanitize(
		linkifyHTML("see https://en.wikipedia.org/wiki/Foo_(bar) ok", false),
	)
	assert.Contains(t, result, `href="https://en.wikipedia.org/wiki/Foo_(bar)"`)
}

func TestLinkifyMessage_Newlines(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	err := linkifyMessage("line one\nline two\nline three", false).Render(t.Context(), &buf)
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "line one<br>line two<br>line three")
}

func TestLinkifyHTML_ClassRestriction(t *testing.T) {
	t.Parallel()

	injected := `<a href="https://example.com" class="evil-class">x</a>`
	result := linkPolicy.Sanitize(injected)
	assert.NotContains(t, result, "evil-class")
}

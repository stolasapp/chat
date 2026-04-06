package view

import (
	"context"
	"encoding/json"
	"html"
	"io"

	"github.com/a-h/templ"
)

// Element IDs referenced by HTMX OOB swaps, ws-send forms, and
// client-side JS. The elementIDs component exposes these to JS
// as window.IDs.
const (
	ElementContent      = "content"
	ElementMessages     = "messages"
	ElementSendButton   = "send-btn"
	ElementProfileForm  = "profile-form"
	ElementChatForm     = "chat-form"
	ElementMessageInput = "message-input"
	ElementFindMatchBtn = "find-match-btn"
	ElementFindAnother  = "find-another-btn"
	ElementBlockBtn     = "block-btn"
	ElementClientCount  = "client-count"
	ElementTyping       = "typing-indicator"
	ElementCharCount    = "char-count"
	ElementWSContainer  = "ws-container"

	// MaxMessageLength is the maximum allowed chat message length
	// in characters. Shared with JS via window.IDs.
	MaxMessageLength = 2000
)

// nonceScript returns a templ component that emits a <script> tag
// with the CSP nonce from the request context and the given src.
func nonceScript(src string) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		nonce := Nonce(ctx)
		tag := `<script src="` + html.EscapeString(src) + `"`
		if nonce != "" {
			tag += ` nonce="` + html.EscapeString(nonce) + `"`
		}
		tag += " defer></script>"
		_, err := io.WriteString(w, tag)
		return err
	})
}

// elementIDs returns a templ component that emits a script block
// attaching the element ID constants to window.IDs. The CSP nonce
// is read from the request context.
func elementIDs() templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		ids := map[string]any{
			"content":          ElementContent,
			"messages":         ElementMessages,
			"sendBtn":          ElementSendButton,
			"profileForm":      ElementProfileForm,
			"chatForm":         ElementChatForm,
			"messageInput":     ElementMessageInput,
			"findMatchBtn":     ElementFindMatchBtn,
			"charCount":        ElementCharCount,
			"wsContainer":      ElementWSContainer,
			"maxMessageLength": MaxMessageLength,
		}
		data, err := json.Marshal(ids)
		if err != nil {
			return err
		}
		nonce := Nonce(ctx)
		tag := "<script"
		if nonce != "" {
			tag += ` nonce="` + html.EscapeString(nonce) + `"`
		}
		tag += ">window.IDs=Object.freeze(" + string(data) + ")</script>"
		_, err = io.WriteString(w, tag)
		return err
	})
}

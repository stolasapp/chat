package view

import (
	"context"
	"encoding/json"
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
)

// elementIDs returns a templ component that emits a script block
// attaching the element ID constants to window.IDs.
func elementIDs() templ.Component {
	return templ.ComponentFunc(func(_ context.Context, w io.Writer) error {
		ids := map[string]string{
			"content":      ElementContent,
			"messages":     ElementMessages,
			"sendBtn":      ElementSendButton,
			"profileForm":  ElementProfileForm,
			"chatForm":     ElementChatForm,
			"messageInput": ElementMessageInput,
			"findMatchBtn": ElementFindMatchBtn,
		}
		data, err := json.Marshal(ids)
		if err != nil {
			return err
		}
		_, err = io.WriteString(w, "<script>window.IDs=Object.freeze("+string(data)+")</script>")
		return err
	})
}

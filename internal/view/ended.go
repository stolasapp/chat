package view

import "github.com/a-h/templ"

// Common session-end messages used across hub handlers.
const (
	MsgPartnerLeft = "Your partner has left."
	MsgYouLeft     = "You left the chat."
	MsgIdleKicked  = "Disconnected for inactivity."
	MsgServerReset = "The server was reset. Find a new match to continue."
	MsgCooldown    = "Please wait before searching again."
)

// SessionEndComponents returns the standard set of components
// sent when a chat session ends: hide typing indicator, disable
// send button, and show the ended notification.
func SessionEndComponents(message string, showActions bool) []templ.Component {
	return []templ.Component{
		TypingIndicator(false),
		SendButton(false),
		ChatEnded(message, showActions),
	}
}

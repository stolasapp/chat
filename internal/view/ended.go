package view

import "github.com/a-h/templ"

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

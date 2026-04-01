package uitest

import "github.com/stolasapp/chat/internal/view"

// CSS selectors derived from element ID constants.
var (
	SelectorContent      = "#" + view.ElementContent
	SelectorMessages     = "#" + view.ElementMessages
	SelectorProfileForm  = "#" + view.ElementProfileForm
	SelectorChatForm     = "#" + view.ElementChatForm
	SelectorMessageInput = "#" + view.ElementMessageInput
	SelectorFindMatchBtn = "#" + view.ElementFindMatchBtn
	SelectorFindAnother  = "#" + view.ElementFindAnother
	SelectorSendButton   = "#" + view.ElementSendButton
	SelectorCharCount    = "#" + view.ElementCharCount
	SelectorWSContainer  = "#" + view.ElementWSContainer
)

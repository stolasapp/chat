package uitest

import (
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
)

const (
	defaultTimeout = 10 * time.Second
	stableTimeout  = 5 * time.Second
)

// testPage wraps a rod.Page with consistent timeout handling
// and HTMX/WebSocket-aware helpers.
type testPage struct {
	*rod.Page
	t *testing.T
}

// el finds a single element with the default timeout.
func (p *testPage) el(selector string) *rod.Element {
	return p.Page.Timeout(defaultTimeout).MustElement(selector)
}

// els finds multiple elements with the default timeout.
func (p *testPage) els(selector string) rod.Elements {
	els, _ := p.Page.Timeout(defaultTimeout).Elements(selector)
	return els
}

// elMaybe finds an element or returns nil if not found.
func (p *testPage) elMaybe(selector string) *rod.Element {
	el, err := p.Page.Timeout(defaultTimeout).Element(selector)
	if err != nil {
		return nil
	}
	return el
}

// waitStable waits for the page DOM to stop changing.
func (p *testPage) waitStable() {
	p.Page.Timeout(stableTimeout).MustWaitStable()
}

// js runs JavaScript in the page and returns the JSON result.
func (p *testPage) js(expression string) string {
	return p.Page.Timeout(defaultTimeout).MustEval(expression).Str()
}

// selectBoxSelect sets a selectbox value by clicking the matching
// item in the dropdown. The name parameter matches the hidden
// input's name attribute.
func (p *testPage) selectBoxSelect(name, value string) {
	trigger := p.el(
		`button.select-trigger:has(input[name="` + name + `"])`)
	trigger.MustClick()
	p.waitStable()

	container := trigger.MustParent().MustParent()
	item := container.Timeout(defaultTimeout).MustElement(
		`[data-tui-selectbox-value="` + value + `"]`)
	item.MustClick()
	p.waitStable()
}

// typeInto clears a textarea/input and types text into it.
func (p *testPage) typeInto(selector, text string) {
	el := p.el(selector)
	el.MustClick()
	el.MustSelectAllText()
	el.MustInput(text)
	p.waitStable()
}

// pressEnter sends the Enter key to the focused element.
func (p *testPage) pressEnter() {
	p.Page.Keyboard.MustType(input.Enter)
}

// reload reloads the page and waits for stability.
func (p *testPage) reload() {
	p.Page.Timeout(defaultTimeout).MustReload()
	p.waitStable()
}

// messageTexts returns the text content of all child divs in the
// messages area.
func (p *testPage) messageTexts() []string {
	msgs := p.els(SelectorMessages + " > div")
	texts := make([]string, 0, len(msgs))
	for _, msg := range msgs {
		texts = append(texts, msg.MustText())
	}
	return texts
}

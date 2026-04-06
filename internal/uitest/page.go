package uitest

import (
	"testing"
	"time"

	"github.com/go-rod/rod"
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

// js runs JavaScript in the page and returns the string result.
// The expression should be a JS function like `() => expr`.
func (p *testPage) js(expression string) string {
	return p.Page.Timeout(defaultTimeout).MustEval(expression).Str()
}

// selectBoxSelect sets a selectbox value by programmatically
// setting the hidden input and dispatching events. This avoids
// issues with popover positioning in headless mode.
func (p *testPage) selectBoxSelect(name, value string) {
	p.js(`() => {
		const input = document.querySelector(
			'input[type="hidden"][name="` + name + `"]');
		if (!input) throw new Error("no input for " + "` + name + `");
		input.value = "` + value + `";
		input.dispatchEvent(new Event("input", {bubbles: true}));
		input.dispatchEvent(new Event("change", {bubbles: true}));
	}`)
	p.waitStable()
}

// typeInto sets the value of a textarea/input via JS.
func (p *testPage) typeInto(selector, text string) {
	p.js(`() => {
		const el = document.querySelector('` + selector + `');
		el.focus();
		el.value = '` + text + `';
		el.dispatchEvent(new Event('input', {bubbles: true}));
	}`)
	p.waitStable()
}

// reload reloads the page and waits for stability.
func (p *testPage) reload() {
	p.Page.Timeout(defaultTimeout).MustReload()
	p.waitStable()
}

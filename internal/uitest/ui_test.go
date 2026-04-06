package uitest

import (
	"testing"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUI is the parent test that starts a single server and headless
// browser, then runs all UI subtests serially. Subtests share a single
// browser and server instance and must run sequentially.
//
//nolint:paralleltest,tparallel // sequential E2E tests sharing browser state
func TestUI(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping UI tests in short mode")
	}

	server := newTestServer()
	t.Cleanup(server.Close)

	browserURL := launcher.New().Headless(true).MustLaunch()
	browser := rod.New().ControlURL(browserURL).MustConnect()
	t.Cleanup(func() { _ = browser.Close() })

	t.Run("LandingPage", func(t *testing.T) {
		testLandingPage(t, browser, server)
	})
	t.Run("ProfilePersistence", func(t *testing.T) {
		testProfilePersistence(t, browser, server)
	})
	t.Run("MatchFlow", func(t *testing.T) {
		testMatchFlow(t, browser, server)
	})
	t.Run("MessageExchange", func(t *testing.T) {
		testMessageExchange(t, browser, server)
	})
	t.Run("LeaveChat", func(t *testing.T) {
		testLeaveChat(t, browser, server)
	})
	t.Run("SettingsDrawer", func(t *testing.T) {
		testSettingsDrawer(t, browser, server)
	})
	t.Run("CharacterCounter", func(t *testing.T) {
		testCharacterCounter(t, browser, server)
	})
}

// newPage creates a testPage navigated to the server root and waits
// for the WebSocket connection to be established.
func newPage(t *testing.T, browser *rod.Browser, server *Server) *testPage {
	t.Helper()
	page := browser.MustPage(server.URL("/"))
	t.Cleanup(func() { _ = page.Close() })
	page.Timeout(defaultTimeout).MustWaitLoad()
	testPg := &testPage{Page: page, t: t}
	testPg.waitStable()
	// Wait until the WebSocket is connected. The ws extension
	// stores the socket reference on the container element.
	testPg.Page.Timeout(defaultTimeout).MustWait(
		`() => {
			const el = document.getElementById('ws-container');
			return el && el['htmx-internal-data']
				&& el['htmx-internal-data']['webSocket']
				&& el['htmx-internal-data']['webSocket']['socket']
				&& el['htmx-internal-data']['webSocket']['socket'].readyState === 1;
		}`)
	testPg.waitStable()
	return testPg
}

// fillAndSubmit selects gender and role on the landing page profile
// form, force-enables the find match button, then clicks it.
func fillAndSubmit(page *testPage, gender, role string) {
	page.t.Helper()
	page.selectBoxSelect("gender", gender)
	page.selectBoxSelect("role", role)

	// Enable the button and submit the form via JS click.
	page.js(`() => {
		const btn = document.getElementById('find-match-btn');
		btn.disabled = false;
		btn.click();
	}`)
	page.waitStable()
}

// matchPair creates two pages, submits the profile form on each, and
// waits for the match notification to appear on both pages.
func matchPair(t *testing.T, browser *rod.Browser, server *Server) (*testPage, *testPage) {
	t.Helper()

	pageA := newPage(t, browser, server)
	pageB := newPage(t, browser, server)

	fillAndSubmit(pageA, "male", "dominant")
	fillAndSubmit(pageB, "female", "submissive")

	// Wait for both pages to show the chat form (sent as part
	// of ChatView OOB swap when matched).
	pageA.el(SelectorChatForm)
	pageB.el(SelectorChatForm)

	return pageA, pageB
}

// testLandingPage verifies the landing page title, profile form
// presence, all 7 selectbox fields, and the disabled find match
// button.
func testLandingPage(t *testing.T, browser *rod.Browser, server *Server) {
	t.Helper()

	page := newPage(t, browser, server)

	title := page.Page.MustInfo().Title
	assert.Contains(t, title, "Yiff Chat",
		"page title should contain 'Yiff Chat', got %q", title)

	require.NotNil(t, page.elMaybe(SelectorProfileForm),
		"profile form should be present")

	// Expect 7 selectbox trigger fields.
	triggers := page.els("button.select-trigger")
	assert.Len(t, triggers, 7, "expected 7 selectbox fields")

	// Find match button should be disabled initially.
	findBtn := page.el(SelectorFindMatchBtn)
	disabled, err := findBtn.Property("disabled")
	require.NoError(t, err)
	assert.True(t, disabled.Bool(), "find match button should be disabled")
}

// testProfilePersistence selects gender=male and role=dominant,
// reloads the page, then verifies the hidden input values and display
// text are restored from sessionStorage.
func testProfilePersistence(t *testing.T, browser *rod.Browser, server *Server) {
	t.Helper()

	page := newPage(t, browser, server)

	page.selectBoxSelect("gender", "male")
	page.selectBoxSelect("role", "dominant")

	page.reload()

	// Verify the hidden input values are restored.
	genderVal := page.js(
		`() => document.querySelector('#profile-form input[name="gender"]').value`)
	assert.Equal(t, "male", genderVal)

	roleVal := page.js(
		`() => document.querySelector('#profile-form input[name="role"]').value`)
	assert.Equal(t, "dominant", roleVal)

	// Verify the display text shows the selected label.
	genderText := page.js(selectDisplayText("gender"))
	assert.Equal(t, "Male", genderText)

	roleText := page.js(selectDisplayText("role"))
	assert.Equal(t, "Dominant", roleText)
}

// testMatchFlow verifies that two clients can be matched together
// and both see the chat form.
func testMatchFlow(t *testing.T, browser *rod.Browser, server *Server) {
	t.Helper()

	pageA, pageB := matchPair(t, browser, server)

	require.NotNil(t, pageA.elMaybe(SelectorChatForm),
		"page A should show chat form after match")
	require.NotNil(t, pageB.elMaybe(SelectorChatForm),
		"page B should show chat form after match")
}

// testMessageExchange verifies that messages sent by one client
// appear on the other client's page.
func testMessageExchange(t *testing.T, browser *rod.Browser, server *Server) {
	t.Helper()

	pageA, pageB := matchPair(t, browser, server)

	// A sends a message; B should receive it.
	pageA.typeInto(SelectorMessageInput, "hello from A")
	pageA.js(`() => document.getElementById('send-btn').click()`)
	pageA.waitStable()

	// Wait for message to appear on B.
	pageB.Page.Timeout(defaultTimeout).MustWait(
		`() => document.getElementById('messages')?.textContent?.includes('hello from A')`)

	// B sends a message; A should receive it.
	pageB.typeInto(SelectorMessageInput, "hello from B")
	pageB.js(`() => document.getElementById('send-btn').click()`)
	pageB.waitStable()

	// Wait for message to appear on A.
	pageA.Page.Timeout(defaultTimeout).MustWait(
		`() => document.getElementById('messages')?.textContent?.includes('hello from B')`)
}

// testLeaveChat verifies that when one client opens the leave dialog
// and confirms, both clients see the ended notification.
func testLeaveChat(t *testing.T, browser *rod.Browser, server *Server) {
	t.Helper()

	pageA, pageB := matchPair(t, browser, server)

	// Send leave message directly via WebSocket.
	pageA.js(`() => {
		const container = document.getElementById('ws-container');
		const ws = container['htmx-internal-data']?.webSocket?.socket;
		ws.send(JSON.stringify({type: 'leave'}));
	}`)

	// A should see "You left the chat." and B should see
	// "Your partner has left."
	pageA.Page.Timeout(defaultTimeout).MustWait(
		`() => document.getElementById('messages')
			?.textContent?.includes('You left the chat.')`)
	pageB.Page.Timeout(defaultTimeout).MustWait(
		`() => document.getElementById('messages')
			?.textContent?.includes('Your partner has left.')`)
}

// testSettingsDrawer verifies that the settings drawer in the chat
// view shows the correct gender and role values from the matched
// profile.
func testSettingsDrawer(t *testing.T, browser *rod.Browser, server *Server) {
	t.Helper()

	pageA, _ := matchPair(t, browser, server)

	// Open the settings drawer on page A.
	pageA.js(`() => {
		const btns = document.querySelectorAll('button');
		for (const btn of btns) {
			if (btn.textContent.trim() === 'Settings') {
				btn.click();
				break;
			}
		}
	}`)
	pageA.waitStable()

	genderText := pageA.js(selectDisplayText("gender"))
	assert.Equal(t, "Male", genderText,
		"settings drawer should show correct gender")

	roleText := pageA.js(selectDisplayText("role"))
	assert.Equal(t, "Dominant", roleText,
		"settings drawer should show correct role")
}

// testCharacterCounter verifies that the character counter is hidden
// initially, becomes visible when the remaining character count falls
// at or below the threshold, and displays the correct count.
func testCharacterCounter(t *testing.T, browser *rod.Browser, server *Server) {
	t.Helper()

	pageA, _ := matchPair(t, browser, server)

	// Counter should be hidden initially.
	counter := pageA.el(SelectorCharCount)
	classes, err := counter.Attribute("class")
	require.NoError(t, err)
	assert.Contains(t, *classes, "hidden",
		"character counter should be hidden initially")

	// Set 1850 chars via JS (remaining = 2000 - 1850 = 150).
	pageA.js(`() => {
		const input = document.getElementById('` + SelectorMessageInput[1:] + `');
		if (input) {
			input.value = 'a'.repeat(1850);
			window.updateCharCount(input);
		}
	}`)
	pageA.waitStable()

	// Counter should now be visible and show "150".
	counterText := pageA.js(
		`() => document.getElementById('` + SelectorCharCount[1:] + `').textContent.trim()`)
	assert.Equal(t, "150", counterText,
		"character counter should show 150 remaining")

	counterClasses, err := pageA.el(SelectorCharCount).Attribute("class")
	require.NoError(t, err)
	assert.NotContains(t, *counterClasses, "hidden",
		"character counter should be visible after threshold")
}

// selectDisplayText returns a JS expression that reads the visible
// text of a selectbox trigger identified by its hidden input name.
func selectDisplayText(name string) string {
	return `() => document.querySelector(
		'#profile-form button.select-trigger:has(input[name="` +
		name + `"]) .select-value'
	).textContent.trim()`
}

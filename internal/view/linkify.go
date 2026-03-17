package view

import (
	"html"
	"regexp"
	"strings"

	"github.com/a-h/templ"
	"github.com/microcosm-cc/bluemonday"
	"mvdan.cc/xurls/v2"
)

// urlPattern matches URLs with http/https schemes using the
// xurls library, which handles parenthesized URLs, international
// domains, and other edge cases better than a naive regex.
var urlPattern = xurls.Strict()

// allowedLinkClasses restricts the class attribute on <a> tags
// to only classes we generate. Prevents user-injected classes.
var allowedLinkClasses = regexp.MustCompile(
	`^(underline|text-indigo-[\w-]+|hover:text-[\w-]+|dark:text-indigo-[\w-]+|dark:hover:text-[\w-]+)` +
		`(\s+(underline|text-indigo-[\w-]+|hover:text-[\w-]+|dark:text-indigo-[\w-]+|dark:hover:text-[\w-]+))*$`,
)

// linkPolicy sanitizes linkified HTML, allowing only <a> with
// href and our specific link classes, plus <br> for newlines.
// Enforces noreferrer, nofollow, and target=_blank on all links.
var linkPolicy = func() *bluemonday.Policy {
	policy := bluemonday.NewPolicy()
	policy.AllowStandardURLs()
	policy.AllowAttrs("href").OnElements("a")
	policy.AllowAttrs("class").Matching(allowedLinkClasses).OnElements("a")
	policy.AllowElements("br")
	policy.RequireNoFollowOnLinks(true)
	policy.RequireNoReferrerOnLinks(true)
	policy.AddTargetBlankToFullyQualifiedLinks(true)
	return policy
}()

// linkifyMessage converts URLs in plain text to safe, clickable
// <a> tags and newlines to <br>. All other content is HTML-escaped.
// The result is sanitized by bluemonday and safe to render as raw
// HTML.
func linkifyMessage(text string, self bool) templ.Component {
	raw := linkifyHTML(text, self)
	raw = strings.ReplaceAll(raw, "\n", "<br>")
	sanitized := linkPolicy.Sanitize(raw)
	return templ.Raw(sanitized)
}

// linkifyHTML converts URLs to <a> tags with appropriate styling.
// Non-URL text is HTML-escaped.
func linkifyHTML(text string, self bool) string {
	matches := urlPattern.FindAllStringIndex(text, -1)
	if len(matches) == 0 {
		return html.EscapeString(text)
	}

	linkClass := "underline text-indigo-600 hover:text-indigo-800" +
		" dark:text-indigo-400 dark:hover:text-indigo-200"
	if self {
		linkClass = "underline text-indigo-200 hover:text-white"
	}

	var buf strings.Builder
	prev := 0
	for _, match := range matches {
		start, end := match[0], match[1]

		buf.WriteString(html.EscapeString(text[prev:start]))
		escapedURL := html.EscapeString(text[start:end])
		buf.WriteString(`<a href="`)
		buf.WriteString(escapedURL)
		buf.WriteString(`" class="`)
		buf.WriteString(linkClass)
		buf.WriteString(`">`)
		buf.WriteString(escapedURL)
		buf.WriteString(`</a>`)
		prev = end
	}
	buf.WriteString(html.EscapeString(text[prev:]))
	return buf.String()
}

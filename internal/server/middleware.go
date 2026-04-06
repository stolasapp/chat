package server

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/stolasapp/chat/internal/view"
)

// securityHeaders wraps a handler to set HTTP security headers on
// every response. When hsts is true, Strict-Transport-Security is
// included (enable only when TLS is guaranteed by the infrastructure).
func securityHeaders(next http.Handler, hsts bool) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		header := writer.Header()
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("X-Frame-Options", "DENY")
		header.Set("Referrer-Policy", "no-referrer")
		header.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		if hsts {
			header.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(writer, request)
	})
}

// cspMiddleware generates a per-request CSP nonce, injects it into
// the request context, and sets the Content-Security-Policy header.
// The domain is used to restrict connect-src to the app's own
// WebSocket endpoints.
func cspMiddleware(next http.Handler, domain string) http.Handler {
	connectSrc := buildConnectSrc(domain)

	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		nonce := view.GenerateNonce()
		ctx := view.WithNonce(request.Context(), nonce)

		header := writer.Header()
		header.Set("Content-Security-Policy", strings.Join([]string{
			"default-src 'none'",
			"script-src 'nonce-" + nonce + "'",
			"style-src 'self'",
			"img-src 'self'",
			connectSrc,
			"font-src 'self'",
			"base-uri 'none'",
			"form-action 'none'",
			"frame-ancestors 'none'",
		}, "; "))

		next.ServeHTTP(writer, request.WithContext(ctx))
	})
}

// domainMiddleware injects the canonical domain URL into the
// request context so templates can read it.
func domainMiddleware(next http.Handler, domain string) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		ctx := view.WithDomain(request.Context(), domain)
		next.ServeHTTP(writer, request.WithContext(ctx))
	})
}

// buildConnectSrc constructs the connect-src directive allowing
// both ws:// and wss:// to the domain host.
func buildConnectSrc(domain string) string {
	parsed, err := url.Parse(domain)
	if err != nil || parsed.Host == "" {
		return "connect-src 'self'"
	}
	host := parsed.Host
	return "connect-src 'self' ws://" + host + " wss://" + host
}

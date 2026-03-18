package server

import "net/http"

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

// Package static embeds the static assets served by the HTTP server.
package static

import "embed"

// FS contains the embedded static assets.
//
//go:embed *.js
var FS embed.FS

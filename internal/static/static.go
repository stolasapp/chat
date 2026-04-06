// Package static embeds the static assets served by the HTTP server.
package static

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
)

// Assets contains the embedded static assets.
//
//go:embed *.js *.svg *.css
var Assets embed.FS

const hashChars = 8 // hex characters from the SHA-256 hash

// CSSPath returns the versioned URL for the CSS bundle.
func CSSPath() string { return cssPath }

// JSPath returns the versioned URL for the JS bundle.
func JSPath() string { return jsPath }

var (
	cssPath = versionedPath("output.css")
	jsPath  = versionedPath("output.js")
)

func versionedPath(name string) string {
	data, err := Assets.ReadFile(name)
	if err != nil {
		panic(fmt.Sprintf("static: missing embedded file %s: %v", name, err))
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])[:hashChars]
	return "/static/" + name + "?v=" + hash
}

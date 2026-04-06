package view

import (
	"context"
	"crypto/rand"
	"encoding/base64"
)

type nonceKey struct{}

const nonceBytes = 16

// GenerateNonce returns a cryptographically random base64 string
// suitable for a CSP nonce attribute.
func GenerateNonce() string {
	var buf [nonceBytes]byte
	if _, err := rand.Read(buf[:]); err != nil {
		panic("view: failed to generate nonce: " + err.Error())
	}
	return base64.StdEncoding.EncodeToString(buf[:])
}

// WithNonce returns a context carrying the given CSP nonce.
func WithNonce(ctx context.Context, nonce string) context.Context {
	return context.WithValue(ctx, nonceKey{}, nonce)
}

// Nonce extracts the CSP nonce from the context. Returns an empty
// string if no nonce is present.
func Nonce(ctx context.Context) string {
	if v, ok := ctx.Value(nonceKey{}).(string); ok {
		return v
	}
	return ""
}

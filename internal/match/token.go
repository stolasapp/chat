package match

import (
	"crypto/rand"
	"encoding/hex"
)

const tokenBytes = 8 // 64 bits per token

// Token is a session token identifying a connected client.
type Token string

// NewToken generates a cryptographically random session token.
// Panics if the system entropy source is unavailable.
func NewToken() Token {
	var buf [tokenBytes]byte
	if _, err := rand.Read(buf[:]); err != nil {
		panic("match: failed to generate token: " + err.Error())
	}
	return Token(hex.EncodeToString(buf[:]))
}

package match

// Session represents an active chat session between two matched
// users, identified by their tokens.
type Session struct {
	a, b Token
}

// NewSession creates a session pairing two distinct tokens.
// Panics if either token is empty or if both are equal.
func NewSession(tokenA, tokenB Token) *Session {
	if tokenA == "" || tokenB == "" {
		panic("match: session tokens must not be empty")
	}
	if tokenA == tokenB {
		panic("match: session tokens must be distinct")
	}
	return &Session{a: tokenA, b: tokenB}
}

// Partner returns the other participant's token. Returns empty
// if the token is not part of this session.
func (s *Session) Partner(token Token) Token {
	switch token {
	case s.a:
		return s.b
	case s.b:
		return s.a
	default:
		return ""
	}
}

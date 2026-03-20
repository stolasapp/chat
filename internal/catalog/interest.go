package catalog

import "fmt"

// Interest identifies an individual interest. The underlying string
// is the display label; the wire format is derived (lowercase,
// spaces to underscores).
//
//nolint:recvcheck // MarshalText requires value receiver, UnmarshalText requires pointer
type Interest string

// Label returns the human-readable label.
func (i Interest) Label() string { return string(i) }

// String returns the wire format.
func (i Interest) String() string { return toWire(string(i)) }

// MarshalText implements encoding.TextMarshaler.
func (i Interest) MarshalText() ([]byte, error) {
	if !interestSet[i] {
		return nil, fmt.Errorf("invalid interest: %q", i)
	}
	return []byte(i.String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (i *Interest) UnmarshalText(text []byte) error {
	parsed, ok := interestLookup[string(text)]
	if !ok {
		return fmt.Errorf("unknown interest: %q", text)
	}
	*i = parsed
	return nil
}

// AllInterests returns all interest values sorted by label.
func AllInterests() []Interest { return interests }

// interests is the canonical list. Keep sorted by label.
var interests = []Interest{
	"Baseball",
	"Basketball",
	"Cycling",
	"Golf",
	"Hockey",
	"Soccer",
	"Swimming",
	"Tennis",
	"Track",
	"Volleyball",
}

var interestSet = buildSet(interests)
var interestLookup = buildLookup(interests)

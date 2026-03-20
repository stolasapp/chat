package catalog

import "fmt"

// Gender identifies a user's gender. The underlying string is the
// display label; the wire format is derived.
//
//nolint:recvcheck // MarshalText requires value receiver, UnmarshalText requires pointer
type Gender string

// Gender values.
const (
	GenderMale      Gender = "Male"
	GenderFemale    Gender = "Female"
	GenderFluid     Gender = "Gender Fluid"
	GenderNonBinary Gender = "Non-Binary"
	GenderAgender   Gender = "Agender"
	GenderOther     Gender = "Other"
)

// Label returns the human-readable label.
func (g Gender) Label() string { return string(g) }

// String returns the wire format.
func (g Gender) String() string { return toWire(string(g)) }

// MarshalText implements encoding.TextMarshaler.
func (g Gender) MarshalText() ([]byte, error) {
	if !genderSet[g] {
		return nil, fmt.Errorf("invalid gender: %q", g)
	}
	return []byte(g.String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (g *Gender) UnmarshalText(text []byte) error {
	parsed, ok := genderLookup[string(text)]
	if !ok {
		return fmt.Errorf("unknown gender: %q", text)
	}
	*g = parsed
	return nil
}

// Genders returns all valid Gender values in display order.
func Genders() []Gender { return genders }

var genders = []Gender{
	GenderMale,
	GenderFemale,
	GenderFluid,
	GenderNonBinary,
	GenderAgender,
	GenderOther,
}

var genderSet = buildSet(genders)
var genderLookup = buildLookup(genders)

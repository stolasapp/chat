package catalog

import (
	"fmt"
	"strings"
)

// Gender identifies a user's gender.
//
//nolint:recvcheck // MarshalText requires value receiver, UnmarshalText requires pointer
type Gender uint8

// Gender values.
const (
	GenderMale Gender = iota + 1
	GenderFemale
	GenderFluid
	GenderNonBinary
	GenderAgender
	GenderOther
)

var genderStrings = map[Gender]string{
	GenderMale:      "male",
	GenderFemale:    "female",
	GenderFluid:     "gender-fluid",
	GenderNonBinary: "nonbinary",
	GenderAgender:   "agender",
	GenderOther:     "other",
}

var genderLabels = map[Gender]string{
	GenderMale:      "Male",
	GenderFemale:    "Female",
	GenderFluid:     "Gender Fluid",
	GenderNonBinary: "Non-Binary",
	GenderAgender:   "Agender",
	GenderOther:     "Other",
}

var genderLookup = func() map[string]Gender {
	lookup := make(map[string]Gender, len(genderStrings))
	for gender, str := range genderStrings {
		lookup[str] = gender
	}
	return lookup
}()

func (g Gender) String() string {
	if str, ok := genderStrings[g]; ok {
		return str
	}
	return fmt.Sprintf("Gender(%d)", g)
}

// Label returns the human-readable label for the gender.
func (g Gender) Label() string {
	if label, ok := genderLabels[g]; ok {
		return label
	}
	return g.String()
}

// MarshalText implements encoding.TextMarshaler.
func (g Gender) MarshalText() ([]byte, error) {
	if _, ok := genderStrings[g]; !ok {
		return nil, fmt.Errorf("invalid gender: %d", g)
	}
	return []byte(g.String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (g *Gender) UnmarshalText(text []byte) error {
	parsed, ok := genderLookup[strings.ToLower(string(text))]
	if !ok {
		return fmt.Errorf("unknown gender: %q", text)
	}
	*g = parsed
	return nil
}

// Genders returns all valid Gender values in order.
func Genders() []Gender {
	return []Gender{
		GenderMale, GenderFemale, GenderFluid,
		GenderNonBinary, GenderAgender, GenderOther,
	}
}

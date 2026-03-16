package catalog

import (
	"fmt"
	"strings"
)

// Role identifies a user's role preference.
//
//nolint:recvcheck // MarshalText requires value receiver, UnmarshalText requires pointer
type Role uint8

// Role values.
const (
	RoleDominant Role = iota + 1
	RoleSubmissive
	RoleSwitch
)

var roleStrings = map[Role]string{
	RoleDominant:   "dominant",
	RoleSubmissive: "submissive",
	RoleSwitch:     "switch",
}

var roleLabels = map[Role]string{
	RoleDominant:   "Dominant",
	RoleSubmissive: "Submissive",
	RoleSwitch:     "Switch",
}

var roleLookup = func() map[string]Role {
	lookup := make(map[string]Role, len(roleStrings))
	for role, str := range roleStrings {
		lookup[str] = role
	}
	return lookup
}()

func (r Role) String() string {
	if str, ok := roleStrings[r]; ok {
		return str
	}
	return fmt.Sprintf("Role(%d)", r)
}

// Label returns the human-readable label for the role.
func (r Role) Label() string {
	if label, ok := roleLabels[r]; ok {
		return label
	}
	return r.String()
}

// MarshalText implements encoding.TextMarshaler.
func (r Role) MarshalText() ([]byte, error) {
	if _, ok := roleStrings[r]; !ok {
		return nil, fmt.Errorf("invalid role: %d", r)
	}
	return []byte(r.String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (r *Role) UnmarshalText(text []byte) error {
	parsed, ok := roleLookup[strings.ToLower(string(text))]
	if !ok {
		return fmt.Errorf("unknown role: %q", text)
	}
	*r = parsed
	return nil
}

// Roles returns all valid Role values in order.
func Roles() []Role {
	return []Role{RoleDominant, RoleSubmissive, RoleSwitch}
}

// RoleMatchesFilter reports whether a user with the given role
// satisfies a role filter. Filter semantics:
//   - filter=dominant accepts dominant and switch
//   - filter=submissive accepts submissive and switch
//   - filter=switch accepts only switch
func RoleMatchesFilter(role, filter Role) bool {
	if role == filter {
		return true
	}
	return role == RoleSwitch && (filter == RoleDominant || filter == RoleSubmissive)
}

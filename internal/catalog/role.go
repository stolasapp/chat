package catalog

import "fmt"

// Role identifies a user's role preference. The underlying string
// is the display label; the wire format is derived.
//
//nolint:recvcheck // MarshalText requires value receiver, UnmarshalText requires pointer
type Role string

// Role values.
const (
	RoleDominant   Role = "Dominant"
	RoleSubmissive Role = "Submissive"
	RoleSwitch     Role = "Switch"
)

// Label returns the human-readable label.
func (r Role) Label() string { return string(r) }

// String returns the wire format.
func (r Role) String() string { return toWire(string(r)) }

// MarshalText implements encoding.TextMarshaler.
func (r Role) MarshalText() ([]byte, error) {
	if !roleSet[r] {
		return nil, fmt.Errorf("invalid role: %q", r)
	}
	return []byte(r.String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (r *Role) UnmarshalText(text []byte) error {
	parsed, ok := roleLookup[string(text)]
	if !ok {
		return fmt.Errorf("unknown role: %q", text)
	}
	*r = parsed
	return nil
}

// Roles returns all valid Role values in display order.
func Roles() []Role { return roles }

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

var roles = []Role{
	RoleDominant,
	RoleSubmissive,
	RoleSwitch,
}

var roleSet = buildSet(roles)
var roleLookup = buildLookup(roles)

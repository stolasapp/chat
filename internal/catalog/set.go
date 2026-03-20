package catalog

import "encoding/json"

// Set is a generic set backed by a map. It marshals to/from JSON
// arrays.
type Set[T comparable] map[T]struct{}

// NewSet creates a Set from a slice of values.
func NewSet[T comparable](values ...T) Set[T] {
	set := make(Set[T], len(values))
	for _, val := range values {
		set[val] = struct{}{}
	}
	return set
}

// Add inserts a value into the set.
func (s Set[T]) Add(val T) {
	s[val] = struct{}{}
}

// Contains reports whether the set contains the value.
func (s Set[T]) Contains(val T) bool {
	_, ok := s[val]
	return ok
}

// Values returns the set contents as a slice. Order is
// nondeterministic.
func (s Set[T]) Values() []T {
	vals := make([]T, 0, len(s))
	for val := range s {
		vals = append(vals, val)
	}
	return vals
}

// Len returns the number of elements in the set.
func (s Set[T]) Len() int {
	return len(s)
}

// MarshalJSON encodes the set as a JSON array.
func (s Set[T]) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.Values())
}

// UnmarshalJSON decodes a JSON array into the set. Invalid or
// unrecognized values are silently dropped.
func (s *Set[T]) UnmarshalJSON(data []byte) error {
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	result := make(Set[T], len(raw))
	for _, elem := range raw {
		var val T
		if err := json.Unmarshal(elem, &val); err == nil {
			result[val] = struct{}{}
		}
	}
	*s = result
	return nil
}

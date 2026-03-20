package catalog

import "strings"

// toWire converts a display label to wire format: lowercase with
// spaces and hyphens replaced by underscores.
func toWire(label string) string {
	r := strings.NewReplacer(" ", "_", "-", "_")
	return strings.ToLower(r.Replace(label))
}

// buildSet builds a validity set from a slice of labeled values.
func buildSet[T ~string](items []T) map[T]bool {
	set := make(map[T]bool, len(items))
	for _, item := range items {
		set[item] = true
	}
	return set
}

// buildLookup builds a wire-format-to-value lookup from a slice
// of labeled values.
func buildLookup[T ~string](items []T) map[string]T {
	lookup := make(map[string]T, len(items))
	for _, item := range items {
		lookup[toWire(string(item))] = item
	}
	return lookup
}

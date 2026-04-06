package view

import "context"

type domainKey struct{}

// WithDomain returns a context carrying the canonical domain URL.
func WithDomain(ctx context.Context, domain string) context.Context {
	return context.WithValue(ctx, domainKey{}, domain)
}

// Domain extracts the canonical domain URL from the context,
// falling back to the given default.
func Domain(ctx context.Context, fallback string) string {
	if v, ok := ctx.Value(domainKey{}).(string); ok && v != "" {
		return v
	}
	return fallback
}

// Ported from packages/coding-agent/src/core/provider-matching.ts (pi v1.5.2).

package core

import (
	"strings"

	"github.com/keejkrej/pi-go/ai"
)

// FindMatchingProviders matches provider IDs and display names exactly,
// ignoring case and surrounding whitespace in providerRef, preserving input order.
func FindMatchingProviders(providers []*ai.Provider, providerRef string) []*ai.Provider {
	return FindMatchingProvidersBy(providers, providerRef, func(p *ai.Provider) (string, string) {
		if p == nil {
			return "", ""
		}
		return p.Id, p.Name
	})
}

// FindMatchingProvidersBy matches arbitrary provider items whose (id, name) are
// supplied by idAndName against providerRef, ignoring case and surrounding whitespace.
func FindMatchingProvidersBy[T any](providers []T, providerRef string, idAndName func(T) (string, string)) []T {
	normalized := strings.ToLower(strings.TrimSpace(providerRef))
	if normalized == "" || idAndName == nil {
		return nil
	}
	var out []T
	for _, provider := range providers {
		id, name := idAndName(provider)
		if strings.ToLower(id) == normalized || strings.ToLower(name) == normalized {
			out = append(out, provider)
		}
	}
	return out
}

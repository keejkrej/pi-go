// Ported from packages/coding-agent/src/core/preferred-providers.ts (pi v1.5.2).

package core

import (
	"cmp"
	"slices"
	"strings"

	"github.com/keejkrej/pi-go/ai"
)

// PreferredProviderIds lists preferred partner providers, most preferred first.
var PreferredProviderIds = []string{RadiusProviderId, "openrouter"}

// ProviderRef identifies a provider by ID and display name for sorting and matching.
type ProviderRef struct {
	Id   string `json:"id"`
	Name string `json:"name"`
}

// IsPreferredProvider reports whether providerId is a preferred partner.
func IsPreferredProvider(providerId string) bool {
	return slices.Contains(PreferredProviderIds, providerId)
}

// CompareProviders orders providers with preferred partners first, in their order,
// then all others alphabetically by display name.
func CompareProviders(a, b *ai.Provider) int {
	if a == nil && b == nil {
		return 0
	}
	if a == nil {
		return 1
	}
	if b == nil {
		return -1
	}
	return CompareProviderKey(a.Id, a.Name, b.Id, b.Name)
}

// CompareProviderRefs orders ProviderRef values using CompareProviderKey.
func CompareProviderRefs(a, b ProviderRef) int {
	return CompareProviderKey(a.Id, a.Name, b.Id, b.Name)
}

// CompareProviderKey orders two (id, name) pairs with preferred partners first,
// then all others alphabetically by name matching JS localeCompare order.
func CompareProviderKey(idA, nameA, idB, nameB string) int {
	rankA := pprPreferredRank(idA)
	rankB := pprPreferredRank(idB)
	if rankA != rankB {
		return cmp.Compare(rankA, rankB)
	}
	return pprCompareLocaleStrings(nameA, nameB)
}

func pprPreferredRank(providerId string) int {
	idx := slices.Index(PreferredProviderIds, providerId)
	if idx == -1 {
		return len(PreferredProviderIds)
	}
	return idx
}

func pprCompareLocaleStrings(a, b string) int {
	if c := cmp.Compare(strings.ToLower(a), strings.ToLower(b)); c != 0 {
		return c
	}
	if a == b {
		return 0
	}
	// Match en-US localeCompare where lowercase sorts before uppercase on case-folded ties.
	if a > b {
		return -1
	}
	return 1
}

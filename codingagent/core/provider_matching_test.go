// Ported from packages/coding-agent/test/provider-matching.test.ts (pi v1.5.2).

package core

import (
	"testing"

	"github.com/keejkrej/pi-go/ai"
)

type tpmtEntry struct {
	id       string
	name     string
	authType string
}

func TestFindMatchingProvidersBy(t *testing.T) {
	providers := []tpmtEntry{
		{id: "first", name: "Shared Name", authType: "oauth"},
		{id: "first", name: "Shared Name", authType: "api_key"},
		{id: "other", name: "FIRST", authType: "api_key"},
		{id: "second", name: "Second Provider", authType: "oauth"},
	}
	idAndName := func(e tpmtEntry) (string, string) { return e.id, e.name }

	// Matches IDs and display names case-insensitively with surrounding whitespace.
	m1 := FindMatchingProvidersBy(providers, "  sEcOnD  ", idAndName)
	if len(m1) != 1 || m1[0] != providers[3] {
		t.Fatalf("trimmed id match = %+v, want [%+v]", m1, providers[3])
	}
	m2 := FindMatchingProvidersBy(providers, "\tSECOND PROVIDER\n", idAndName)
	if len(m2) != 1 || m2[0] != providers[3] {
		t.Fatalf("trimmed name match = %+v, want [%+v]", m2, providers[3])
	}

	// Keeps all matching providers and auth methods in input order.
	matched := FindMatchingProvidersBy(providers, "first", idAndName)
	if len(matched) != 3 || matched[0] != providers[0] || matched[1] != providers[1] || matched[2] != providers[2] {
		t.Fatalf("matched = %+v, want first 3 providers in order", matched)
	}
	shared := FindMatchingProvidersBy(providers, "shared name", idAndName)
	if len(shared) != 2 || shared[0] != providers[0] || shared[1] != providers[1] {
		t.Fatalf("shared name matches = %+v, want first 2 entries", shared)
	}

	// Does not fuzzy-match unknown or partial references.
	if got := FindMatchingProvidersBy(providers, "unknown", idAndName); len(got) != 0 {
		t.Fatalf("unknown matched %+v", got)
	}
	if got := FindMatchingProvidersBy(providers, "sec", idAndName); len(got) != 0 {
		t.Fatalf("partial matched %+v", got)
	}
	if got := FindMatchingProvidersBy([]tpmtEntry{}, "first", idAndName); len(got) != 0 {
		t.Fatalf("empty slice matched %+v", got)
	}

	// Never matches an empty reference, even against empty display names.
	if got := FindMatchingProvidersBy([]tpmtEntry{{id: "unnamed", name: ""}}, "", idAndName); len(got) != 0 {
		t.Fatalf("empty ref matched %+v", got)
	}
	if got := FindMatchingProvidersBy(providers, " \t ", idAndName); len(got) != 0 {
		t.Fatalf("whitespace ref matched %+v", got)
	}
}

func TestFindMatchingProvidersAiProvider(t *testing.T) {
	p1 := &ai.Provider{Id: "openai", Name: "OpenAI"}
	p2 := &ai.Provider{Id: "custom", Name: "OpenAI"}
	got := FindMatchingProviders([]*ai.Provider{nil, p1, p2}, " openai ")
	if len(got) != 2 || got[0] != p1 || got[1] != p2 {
		t.Fatalf("FindMatchingProviders = %+v, want [p1, p2]", got)
	}
}

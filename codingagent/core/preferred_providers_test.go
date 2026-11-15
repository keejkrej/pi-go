// Ported from packages/coding-agent/test/preferred-providers.test.ts (pi v1.5.2).

package core

import (
	"slices"
	"testing"

	"github.com/keejkrej/pi-go/ai"
)

func TestCompareProviders(t *testing.T) {
	expectedPreferred := []string{"radius", "openrouter"}
	if !slices.Equal(PreferredProviderIds, expectedPreferred) {
		t.Fatalf("PreferredProviderIds = %v, want %v", PreferredProviderIds, expectedPreferred)
	}

	providers := []*ai.Provider{
		{Id: "zai", Name: "Z.AI"},
		{Id: "openrouter", Name: "OpenRouter"},
		{Id: "anthropic", Name: "Anthropic"},
		{Id: "radius", Name: "Radius"},
		{Id: "google", Name: "Google"},
	}

	slices.SortStableFunc(providers, CompareProviders)

	got := make([]string, len(providers))
	for i, p := range providers {
		got[i] = p.Id
	}
	want := []string{"radius", "openrouter", "anthropic", "google", "zai"}
	if !slices.Equal(got, want) {
		t.Fatalf("sorted provider IDs = %v, want %v", got, want)
	}
}

func TestCompareProviderRefsAndNil(t *testing.T) {
	refs := []ProviderRef{
		{Id: "zai", Name: "Z.AI"},
		{Id: "openrouter", Name: "OpenRouter"},
		{Id: "anthropic", Name: "Anthropic"},
		{Id: "radius", Name: "Radius"},
		{Id: "alpha", Name: "Alpha"},
		{Id: "alpha-lower", Name: "alpha"},
	}
	slices.SortStableFunc(refs, CompareProviderRefs)

	got := make([]string, len(refs))
	for i, r := range refs {
		got[i] = r.Id
	}
	want := []string{"radius", "openrouter", "alpha-lower", "alpha", "anthropic", "zai"}
	if !slices.Equal(got, want) {
		t.Fatalf("sorted ProviderRefs = %v, want %v", got, want)
	}

	if CompareProviders(nil, nil) != 0 {
		t.Fatalf("CompareProviders(nil, nil) != 0")
	}
	if CompareProviders(nil, &ai.Provider{Id: "radius", Name: "Radius"}) <= 0 {
		t.Fatalf("expected nil provider to sort after non-nil provider")
	}
	if CompareProviders(&ai.Provider{Id: "radius", Name: "Radius"}, nil) >= 0 {
		t.Fatalf("expected non-nil provider to sort before nil provider")
	}
}

func TestIsPreferredProvider(t *testing.T) {
	if !IsPreferredProvider("radius") {
		t.Fatalf("expected radius to be preferred")
	}
	if !IsPreferredProvider("openrouter") {
		t.Fatalf("expected openrouter to be preferred")
	}
	if IsPreferredProvider("anthropic") {
		t.Fatalf("did not expect anthropic to be preferred")
	}
	if IsPreferredProvider("") {
		t.Fatalf("did not expect empty string to be preferred")
	}
}

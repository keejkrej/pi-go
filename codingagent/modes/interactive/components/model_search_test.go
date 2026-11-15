// Ported from packages/coding-agent/test/model-search.test.ts (pi v1.5.2).

package components

import (
	"slices"
	"sort"
	"testing"

	"github.com/keejkrej/pi-go/tui"
)

func tmseaStrPtr(s string) *string { return &s }

func TestGetDistinctModelName(t *testing.T) {
	t.Run("hides names that restate the ID", func(t *testing.T) {
		cases := []ModelSearchItem{
			{Id: "claude-sonnet-4-5", Name: tmseaStrPtr("Claude Sonnet 4.5")},
			{Id: "openai/gpt-oss-120b", Name: tmseaStrPtr("OpenAI: gpt-oss-120b")},
			{Id: "eu.anthropic.claude-sonnet-4-5-20250929-v1:0", Name: tmseaStrPtr("Claude Sonnet 4.5 (EU)")},
			{Id: "gpt-5", Name: tmseaStrPtr("")},
			{Id: "gpt-5"},
		}
		for _, tc := range cases {
			if got := GetDistinctModelName(tc); got != nil {
				t.Fatalf("GetDistinctModelName(%+v) = %q, want nil", tc, *got)
			}
		}
	})

	t.Run("shows names with words the ID lacks", func(t *testing.T) {
		name := "MiniMax M3 [rasmus@openrouter, parasail]"
		got := GetDistinctModelName(ModelSearchItem{Id: "minimax/minimax-m3", Name: &name})
		if got == nil || *got != name {
			t.Fatalf("got %v, want %q", got, name)
		}

		got2 := GetDistinctModelName(ModelSearchItem{Id: "gemini-3.1-flash-lite-image", Name: tmseaStrPtr("Nano Banana 2 Lite")})
		if got2 == nil || *got2 != "Nano Banana 2 Lite" {
			t.Fatalf("got %v, want %q", got2, "Nano Banana 2 Lite")
		}
	})

	t.Run("requires single-character words to match a whole ID token", func(t *testing.T) {
		got := GetDistinctModelName(ModelSearchItem{Id: "mistral-small-2603", Name: tmseaStrPtr("Mistral Small 4")})
		if got == nil || *got != "Mistral Small 4" {
			t.Fatalf("got %v, want %q", got, "Mistral Small 4")
		}
	})
}

func TestModelSelectorSearchRanking(t *testing.T) {
	t.Run("ranks models containing the query above scattered matches", func(t *testing.T) {
		models := []ModelSearchItem{
			{Provider: "openrouter", Id: "openai/gpt-oss-120b", Name: tmseaStrPtr("OpenAI: gpt-oss-120b")},
			{Provider: "openrouter", Id: "anthropic/claude-sonnet-4.5", Name: tmseaStrPtr("Anthropic: Claude Sonnet 4.5")},
			{Provider: "openrouter", Id: "anthropic/claude-opus-4.1", Name: tmseaStrPtr("Anthropic: Claude Opus 4.1")},
			{Provider: "anthropic", Id: "claude-sonnet-4-5", Name: tmseaStrPtr("Claude Sonnet 4.5")},
			{Provider: "anthropic", Id: "claude-opus-4-1", Name: tmseaStrPtr("Claude Opus 4.1")},
		}

		filtered := tui.FuzzyFilter(models, "opus", GetModelSelectorSearchText)
		if len(filtered) < 2 {
			t.Fatalf("expected at least 2 matches, got %d", len(filtered))
		}
		top2 := []string{filtered[0].Id, filtered[1].Id}
		sort.Strings(top2)
		want := []string{"anthropic/claude-opus-4.1", "claude-opus-4-1"}
		if !slices.Equal(top2, want) {
			t.Fatalf("top2 = %v, want %v", top2, want)
		}
	})

	t.Run("ranks the typed model above models sharing a long provider prefix", func(t *testing.T) {
		models := []ModelSearchItem{
			{Provider: "fireworks", Id: "accounts/fireworks/models/deepseek-v4-flash-0731"},
			{Provider: "fireworks", Id: "accounts/fireworks/models/kimi-k3"},
			{Provider: "fireworks", Id: "accounts/fireworks/routers/firerouter", Name: tmseaStrPtr("FireRouter")},
		}

		filtered := tui.FuzzyFilter(models, "firerouter", GetModelSelectorSearchText)
		if len(filtered) == 0 || filtered[0].Id != "accounts/fireworks/routers/firerouter" {
			t.Fatalf("filtered[0] = %+v, want firerouter", filtered)
		}
	})
}

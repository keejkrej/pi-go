package tui

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func tassDesc(s string) *string { return &s }

func tassSkillCommands() []AutocompleteCommand {
	return []AutocompleteCommand{
		&SlashCommand{Name: "skill:deep-research", Description: tassDesc("Multi-agent deep research")},
		&SlashCommand{Name: "skill:research-idea", Description: tassDesc("Refine a raw idea into a falsifiable seed")},
		&SlashCommand{Name: "skill:to-sidecar", Description: tassDesc("Route work to a sidecar")},
		&SlashCommand{Name: "skill:brainstorm", Description: tassDesc("Generate ideas")},
		&SlashCommand{Name: "model", Description: tassDesc("Select the active model")},
	}
}

func TestAutocompleteSkillSlashUnit_ApplyCompletionAndTriggers(t *testing.T) {
	provider := NewCombinedAutocompleteProvider([]AutocompleteCommand{&SlashCommand{Name: "model"}}, t.TempDir(), nil)
	for _, tc := range []struct {
		line     string
		prefix   string
		expected string
	}{
		{" /", "/", " /model "},
		{"  /mod", "/mod", "  /model "},
		{"\t/mod", "/mod", "\t/model "},
	} {
		applied := provider.ApplyCompletion([]string{tc.line}, 0, len(tc.line), AutocompleteItem{Value: "model", Label: "model"}, tc.prefix)
		if len(applied.Lines) != 1 || applied.Lines[0] != tc.expected || applied.CursorCol != len(tc.expected) {
			t.Fatalf("ApplyCompletion(%q) = %#v, want %q", tc.line, applied, tc.expected)
		}
	}

	line := "  /model son"
	appliedArg := provider.ApplyCompletion([]string{line}, 0, len(line), AutocompleteItem{Value: "sonnet", Label: "sonnet"}, "son")
	if appliedArg.Lines[0] != "  /model sonnet" {
		t.Fatalf("ApplyCompletion arg = %q, want %q", appliedArg.Lines[0], "  /model sonnet")
	}
}

func TestAutocompleteSkillSlash_RankingAndWhitespace(t *testing.T) {
	commands := tassSkillCommands()
	suggestionsFor := func(t *testing.T, prefix string) []string {
		t.Helper()
		provider := NewCombinedAutocompleteProvider(commands, t.TempDir(), nil)
		line := "/" + prefix
		res, err := provider.GetSuggestions(context.Background(), []string{line}, 0, len(line), nil)
		if err != nil {
			t.Fatalf("GetSuggestions(/%s) err = %v", prefix, err)
		}
		if res == nil {
			t.Fatalf("expected suggestions for /%s", prefix)
		}
		values := make([]string, len(res.Items))
		for i, item := range res.Items {
			values[i] = item.Value
		}
		return values
	}

	t.Run("ranks skill:research-idea first for query idea", func(t *testing.T) {
		items := suggestionsFor(t, "idea")
		if len(items) == 0 || items[0] != "skill:research-idea" {
			t.Fatalf("items = %#v, want first skill:research-idea", items)
		}
		idxDeep := slices.Index(items, "skill:deep-research")
		idxIdea := slices.Index(items, "skill:research-idea")
		if idxDeep <= idxIdea {
			t.Fatalf("expected skill:deep-research after skill:research-idea: %#v", items)
		}
	})

	t.Run("keeps ordinary slash commands matching", func(t *testing.T) {
		if !slices.Contains(suggestionsFor(t, "mod"), "model") {
			t.Fatal("expected model in /mod suggestions")
		}
	})

	t.Run("completes commands after leading whitespace", func(t *testing.T) {
		provider := NewCombinedAutocompleteProvider([]AutocompleteCommand{&SlashCommand{Name: "model"}}, t.TempDir(), nil)
		for _, tc := range [][2]string{
			{" /", " /model "},
			{"  /mod", "  /model "},
			{"\t/mod", "\t/model "},
		} {
			res, err := provider.GetSuggestions(context.Background(), []string{tc[0]}, 0, len(tc[0]), nil)
			if err != nil || res == nil {
				t.Fatalf("GetSuggestions(%q) = %#v, err=%v", tc[0], res, err)
			}
			if res.Prefix != strings.TrimLeft(tc[0], " \t") {
				t.Errorf("prefix = %q, want %q", res.Prefix, strings.TrimLeft(tc[0], " \t"))
			}
			if len(res.Items) != 1 || res.Items[0].Value != "model" {
				t.Fatalf("items = %#v, want [model]", res.Items)
			}
			applied := provider.ApplyCompletion([]string{tc[0]}, 0, len(tc[0]), res.Items[0], res.Prefix)
			if applied.Lines[0] != tc[1] || applied.CursorCol != len(tc[1]) {
				t.Errorf("applied = %#v, want line %q", applied, tc[1])
			}
		}
	})

	t.Run("completes command arguments after leading whitespace", func(t *testing.T) {
		provider := NewCombinedAutocompleteProvider([]AutocompleteCommand{
			&SlashCommand{
				Name: "model",
				GetArgumentCompletions: func(prefix string) ([]AutocompleteItem, error) {
					if prefix != "son" {
						t.Errorf("prefix = %q, want son", prefix)
					}
					return []AutocompleteItem{{Value: "sonnet", Label: "sonnet"}}, nil
				},
			},
		}, t.TempDir(), nil)
		line := "  /model son"
		res, err := provider.GetSuggestions(context.Background(), []string{line}, 0, len(line), nil)
		if err != nil || res == nil || res.Prefix != "son" || len(res.Items) != 1 {
			t.Fatalf("unexpected arg completion: %#v err=%v", res, err)
		}
		applied := provider.ApplyCompletion([]string{line}, 0, len(line), res.Items[0], res.Prefix)
		if applied.Lines[0] != "  /model sonnet" {
			t.Fatalf("applied = %q, want %q", applied.Lines[0], "  /model sonnet")
		}
	})

	t.Run("explicit skill: queries, #9944 skill prefix listing, and shorthand", func(t *testing.T) {
		if !slices.Contains(suggestionsFor(t, "skill:side"), "skill:to-sidecar") {
			t.Fatal("expected skill:to-sidecar")
		}
		items := suggestionsFor(t, "skill")
		var gotSkills []string
		for _, v := range items {
			if strings.HasPrefix(v, "skill:") {
				gotSkills = append(gotSkills, v)
			}
		}
		wantSkills := []string{"skill:deep-research", "skill:research-idea", "skill:to-sidecar", "skill:brainstorm"}
		if !reflect.DeepEqual(gotSkills, wantSkills) {
			t.Fatalf("gotSkills = %#v, want %#v", gotSkills, wantSkills)
		}
		if !slices.Contains(suggestionsFor(t, "skbra"), "skill:brainstorm") {
			t.Fatal("expected skill:brainstorm for skbra")
		}
	})
}

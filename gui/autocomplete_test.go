package gui

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestFuzzyMatchAndFilter(t *testing.T) {
	if !FuzzyMatch("", "anything").Matches {
		t.Fatal("empty query must match")
	}
	exact := FuzzyMatch("help", "help")
	middle := FuzzyMatch("help", "unhelpful")
	if !exact.Matches || !middle.Matches || exact.Score >= middle.Score {
		t.Fatalf("expected boundary/exact match to beat middle match: exact=%+v middle=%+v", exact, middle)
	}
	if !FuzzyMatch("1a", "foo_a1_bar").Matches {
		t.Fatal("expected adjacent alpha-digit swap tolerance to match")
	}
	items := []string{"foo_bar", "src/help.go", "help"}
	got := FuzzyFilter(items, "help", func(s string) string { return s })
	if len(got) != 2 || got[0] != "help" {
		t.Fatalf("unexpected FuzzyFilter result: %v", got)
	}
}

func TestCombinedAutocompleteProvider(t *testing.T) {
	root := t.TempDir()
	mustWrite := func(rel string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("ok\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("README.md")
	mustWrite("src/main.go")
	mustWrite("src/editor view.go")
	mustWrite(".git/HEAD")
	mustWrite("node_modules/pkg/index.js")

	prov := NewCombinedAutocompleteProvider([]SlashCommand{
		{Name: "help", Description: "Show help"},
		{
			Name:        "model",
			Description: "Switch model",
			GetArgumentCompletions: func(arg string) []AutocompleteItem {
				return FuzzyFilter([]AutocompleteItem{
					{Value: "claude-sonnet", Label: "claude-sonnet", Description: "Anthropic"},
					{Value: "gpt-4o", Label: "gpt-4o", Description: "OpenAI"},
				}, arg, func(it AutocompleteItem) string { return it.Label })
			},
		},
	}, root)

	// Slash command matching
	sugg, err := prov.GetSuggestions(context.Background(), []string{"/he"}, 0, 3, false)
	if err != nil || sugg == nil || len(sugg.Items) != 1 || sugg.Items[0].Label != "help" {
		t.Fatalf("expected /help suggestion, got %+v err=%v", sugg, err)
	}
	applied := prov.ApplyCompletion([]string{"/he"}, 0, 3, sugg.Items[0], sugg.Prefix)
	if applied.Lines[0] != "/help " || applied.CursorCol != 6 {
		t.Fatalf("unexpected ApplyCompletion for slash cmd: %+v", applied)
	}

	// Argument completions
	argSugg, err := prov.GetSuggestions(context.Background(), []string{"/model son"}, 0, 10, false)
	if err != nil || argSugg == nil || len(argSugg.Items) != 1 || argSugg.Items[0].Value != "claude-sonnet" {
		t.Fatalf("expected model arg suggestion, got %+v err=%v", argSugg, err)
	}

	// @file workspace fuzzy completion skipping .git & node_modules
	fileSugg, err := prov.GetSuggestions(context.Background(), []string{"check @main"}, 0, 11, false)
	if err != nil || fileSugg == nil || len(fileSugg.Items) == 0 {
		t.Fatalf("expected @main file suggestion, got %+v err=%v", fileSugg, err)
	}
	if fileSugg.Items[0].Value != "@src/main.go" {
		t.Fatalf("expected @src/main.go first, got %+v", fileSugg.Items[0])
	}
	for _, it := range fileSugg.Items {
		if it.Description == ".git/HEAD" || it.Description == "node_modules/pkg/index.js" {
			t.Fatalf("unexpected ignored file in suggestions: %+v", it)
		}
	}

	// File with spaces quotes value cleanly
	spaceSugg, err := prov.GetSuggestions(context.Background(), []string{"@editor"}, 0, 7, false)
	if err != nil || spaceSugg == nil || len(spaceSugg.Items) == 0 {
		t.Fatalf("expected spaced file suggestion, got %+v err=%v", spaceSugg, err)
	}
	if spaceSugg.Items[0].Value != `@"src/editor view.go"` {
		t.Fatalf("expected quoted spaced path, got %q", spaceSugg.Items[0].Value)
	}

	appliedFile := prov.ApplyCompletion([]string{"check @main"}, 0, 11, fileSugg.Items[0], fileSugg.Prefix)
	if appliedFile.Lines[0] != "check @src/main.go " {
		t.Fatalf("unexpected applied file text: %q", appliedFile.Lines[0])
	}
}

func TestAutocompletePopupWidget(t *testing.T) {
	pop := NewAutocompletePopup()
	var chosen AutocompleteItem
	pop.SetOnSelect(func(it AutocompleteItem) {
		chosen = it
	})
	pop.SetItems("/h", []AutocompleteItem{
		{Value: "help", Label: "help", Description: "Show commands"},
		{Value: "history", Label: "history", Description: "Show prompt history"},
	})

	tt := ui.NewTester(func(c *ui.Context) {
		ui.Box(c).Fill().Padding(10).Children(func() {
			pop.View(c)
		})
	}, 400, 240)

	if !tt.HasText("help") || !tt.HasText("Show commands") {
		t.Fatalf("expected popup items rendered, texts=%v", tt.Texts())
	}
	pop.MoveDown()
	it, ok := pop.SelectedItem()
	if !ok || it.Value != "history" {
		t.Fatalf("expected history after MoveDown, got %+v (%v)", it, ok)
	}
	if err := tt.Click("history"); err != nil {
		t.Fatalf("click history failed: %v", err)
	}
	if chosen.Value != "history" {
		t.Fatalf("expected OnSelect(history), got %+v", chosen)
	}
}

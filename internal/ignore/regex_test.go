package ignore

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestRegexSources(t *testing.T) {
	raw, err := os.ReadFile("testdata/regex.json")
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Rules []struct {
			Pattern  string `json:"pattern"`
			Source   string `json:"source"`
			Negative bool   `json:"negative"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, r := range file.Rules {
		lines = append(lines, r.Pattern)
	}
	g := New()
	g.Add(lines...)
	if len(g.rules) != len(file.Rules) {
		t.Fatalf("rules: got %d want %d", len(g.rules), len(file.Rules))
	}
	for i, want := range file.Rules {
		got := g.rules[i]
		if got.pattern != want.Pattern || got.negative != want.Negative || got.src != want.Source {
			t.Errorf("%d %s\n%s", i, want.Pattern, cmp.Diff(
				map[string]any{"pattern": want.Pattern, "negative": want.Negative, "source": want.Source},
				map[string]any{"pattern": got.pattern, "negative": got.negative, "source": got.src},
			))
		}
	}
}

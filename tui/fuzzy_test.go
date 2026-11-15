// Ported from packages/tui/test/fuzzy.test.ts (pi v1.5.2).

package tui_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/keejkrej/pi-go/tui"
)

func TestFuzzyMatch(t *testing.T) {
	t.Run("empty query matches everything with score 0", func(t *testing.T) {
		result := tui.NewFuzzyMatch("", "anything")
		if !result.Matches || result.Score != 0 {
			t.Fatalf("got %+v", result)
		}
	})

	t.Run("query longer than text does not match", func(t *testing.T) {
		result := tui.NewFuzzyMatch("longquery", "short")
		if result.Matches {
			t.Fatalf("expected no match, got %+v", result)
		}
	})

	t.Run("exact match has good score", func(t *testing.T) {
		result := tui.NewFuzzyMatch("test", "test")
		if !result.Matches || result.Score >= 0 {
			t.Fatalf("got %+v", result)
		}
	})

	t.Run("characters must appear in order", func(t *testing.T) {
		if !tui.NewFuzzyMatch("abc", "aXbXc").Matches {
			t.Fatal("expected match in order")
		}
		if tui.NewFuzzyMatch("abc", "cba").Matches {
			t.Fatal("expected no match out of order")
		}
	})

	t.Run("case insensitive matching", func(t *testing.T) {
		if !tui.NewFuzzyMatch("ABC", "abc").Matches || !tui.NewFuzzyMatch("abc", "ABC").Matches {
			t.Fatal("expected case insensitive matches")
		}
	})

	t.Run("consecutive matches score better than scattered matches", func(t *testing.T) {
		consecutive := tui.NewFuzzyMatch("foo", "foobar")
		scattered := tui.NewFuzzyMatch("foo", "f_o_o_bar")
		if !consecutive.Matches || !scattered.Matches || consecutive.Score >= scattered.Score {
			t.Fatalf("consecutive=%+v scattered=%+v", consecutive, scattered)
		}
	})

	t.Run("word boundary matches score better", func(t *testing.T) {
		atBoundary := tui.NewFuzzyMatch("fb", "foo-bar")
		notAtBoundary := tui.NewFuzzyMatch("fb", "afbx")
		if !atBoundary.Matches || !notAtBoundary.Matches || atBoundary.Score >= notAtBoundary.Score {
			t.Fatalf("atBoundary=%+v notAtBoundary=%+v", atBoundary, notAtBoundary)
		}
	})

	t.Run("scores a contiguous occurrence instead of an earlier scattered alignment", func(t *testing.T) {
		opus := tui.NewFuzzyMatch("opus", "openrouter openrouter/anthropic/claude-opus-4.1")
		sonnet := tui.NewFuzzyMatch("opus", "openrouter openrouter/anthropic/claude-sonnet-4.5")
		gptOss := tui.NewFuzzyMatch("opus", "openrouter openrouter/openai/gpt-oss-120b")
		if opus.Score >= sonnet.Score || opus.Score >= gptOss.Score {
			t.Fatalf("opus=%+v sonnet=%+v gptOss=%+v", opus, sonnet, gptOss)
		}
	})

	t.Run("uses the best contiguous occurrence, not the first", func(t *testing.T) {
		embeddedFirst := tui.NewFuzzyMatch("opus", "xxopus opus")
		boundaryOnly := tui.NewFuzzyMatch("opus", "xxxxxx opus")
		if embeddedFirst.Score != boundaryOnly.Score {
			t.Fatalf("embeddedFirst=%+v boundaryOnly=%+v", embeddedFirst, boundaryOnly)
		}
	})

	t.Run("matches swapped alpha numeric tokens", func(t *testing.T) {
		if !tui.NewFuzzyMatch("codex52", "gpt-5.2-codex").Matches {
			t.Fatal("expected swapped alphanumeric match")
		}
	})
}

func TestFuzzyFilter(t *testing.T) {
	id := func(x string) string { return x }

	t.Run("empty query returns all items unchanged", func(t *testing.T) {
		items := []string{"apple", "banana", "cherry"}
		if diff := cmp.Diff(items, tui.FuzzyFilter(items, "", id)); diff != "" {
			t.Fatalf("mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("filters out non-matching items", func(t *testing.T) {
		items := []string{"apple", "banana", "cherry"}
		if diff := cmp.Diff([]string{"banana"}, tui.FuzzyFilter(items, "an", id)); diff != "" {
			t.Fatalf("mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("sorts results by match quality", func(t *testing.T) {
		items := []string{"a_p_p", "app", "application"}
		res := tui.FuzzyFilter(items, "app", id)
		if len(res) == 0 || res[0] != "app" {
			t.Fatalf("got %v", res)
		}
	})

	t.Run("prioritizes exact matches over longer prefix matches", func(t *testing.T) {
		items := []string{"clone", "cl"}
		if diff := cmp.Diff([]string{"cl", "clone"}, tui.FuzzyFilter(items, "cl", id)); diff != "" {
			t.Fatalf("mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("works with custom getText function", func(t *testing.T) {
		type item struct {
			name string
			id   int
		}
		items := []item{{name: "foo", id: 1}, {name: "bar", id: 2}, {name: "foobar", id: 3}}
		res := tui.FuzzyFilter(items, "foo", func(x item) string { return x.name })
		if len(res) != 2 || res[0].name != "foo" || res[1].name != "foobar" {
			t.Fatalf("got %+v", res)
		}
	})

	t.Run("ranks a model containing the query above models matching a shared provider prefix", func(t *testing.T) {
		items := []string{
			"radius radius/gpt-5.4 radius gpt-5.4 GPT 5.4",
			"radius radius/agentic radius agentic Agentic",
			"radius radius/gpt-6-sol radius gpt-6-sol GPT 6 Sol",
			"radius radius/gpt-6-astra radius gpt-6-astra GPT 6 Astra",
		}
		res := tui.FuzzyFilter(items, "astra", id)
		if len(res) == 0 || res[0] != items[3] {
			t.Fatalf("got %v", res)
		}
	})

	t.Run("matches slash-separated provider/model queries against reordered text", func(t *testing.T) {
		type model struct {
			id       string
			provider string
		}
		item := model{id: "gpt-5.5", provider: "openai-codex"}
		res := tui.FuzzyFilter([]model{item}, "openai-codex/gpt-5.5", func(m model) string {
			return m.id + " " + m.provider
		})
		if diff := cmp.Diff([]model{item}, res, cmp.AllowUnexported(model{})); diff != "" {
			t.Fatalf("mismatch (-want +got):\n%s", diff)
		}
	})
}

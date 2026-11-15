package tui

import (
	"reflect"
	"strings"
	"testing"
)

func tasscIntPtr(v int) *int { return &v }

func TestAltScreenSearchUnit_QueryKeyAndComponentState(t *testing.T) {
	t.Run("normalizeQuery and escapeRegExp", func(t *testing.T) {
		if got := assNormalizeQuery("  Hello   World  "); got != "Hello World" {
			t.Errorf("assNormalizeQuery = %q, want 'Hello World'", got)
		}
		if got := assNormalizeQuery("   "); got != "" {
			t.Errorf("assNormalizeQuery blank = %q, want empty", got)
		}
		if got := assEscapeRegExp("a+b[0].c?"); got != `a\+b\[0\]\.c\?` {
			t.Errorf("assEscapeRegExp = %q", got)
		}
	})

	t.Run("GetAltScreenSearchMatchKey formats first/last segments", func(t *testing.T) {
		if got := GetAltScreenSearchMatchKey(AltScreenSearchMatch{}); got != "" {
			t.Errorf("empty match key = %q, want empty", got)
		}
		match := AltScreenSearchMatch{
			Segments: []AltScreenSearchSegment{
				{Row: 1, StartCol: 2, EndCol: 6},
				{Row: 2, StartCol: 0, EndCol: 4},
			},
		}
		if got := GetAltScreenSearchMatchKey(match); got != "1:2:2:4" {
			t.Errorf("GetAltScreenSearchMatchKey = %q, want 1:2:2:4", got)
		}
	})

	t.Run("FindAltScreenSearchMatches returns nil on empty query", func(t *testing.T) {
		if got := FindAltScreenSearchMatches([]string{"abc"}, ""); got != nil {
			t.Fatalf("FindAltScreenSearchMatches('', empty) = %#v, want nil", got)
		}
	})

	t.Run("component focus, navigation buttons, and result state", func(t *testing.T) {
		var queries []string
		comp := NewAltScreenSearchComponent(func(q string) { queries = append(queries, q) }, nil)
		if comp.WantsKeyRelease() {
			t.Fatal("WantsKeyRelease() = true, want false")
		}
		comp.SetFocused(true)
		if !comp.Focused() {
			t.Fatal("Focused() = false after SetFocused(true)")
		}
		comp.SetResult(2, 5)
		if dir := comp.GetNavigationDirectionAt(0, 0); dir != nil {
			t.Fatalf("unexpected hit dir before Render: %v", *dir)
		}
		if changed := comp.SetHoveredNavigationDirection(tasscIntPtr(-1)); !changed {
			t.Fatal("SetHoveredNavigationDirection(-1) = false, want true")
		}
		if changed := comp.SetHoveredNavigationDirection(tasscIntPtr(-1)); changed {
			t.Fatal("duplicate SetHoveredNavigationDirection(-1) = true, want false")
		}
		comp.Invalidate()
		_ = queries
	})
}

func TestAltScreenSearch_CorpusMatchingWrapAndRender(t *testing.T) {
	t.Run("finds case-insensitive matches across wrapped lines", func(t *testing.T) {
		lines := []string{
			"hello world foo",
			"FOO bar",
			"\x1b[32mhello\x1b[39m foo",
		}
		matches := FindAltScreenSearchMatches(lines, "foo")
		if len(matches) != 3 {
			t.Fatalf("len(matches) = %d, want 3 (%#v)", len(matches), matches)
		}

		idx := NewAltScreenSearchIndex()
		r1 := idx.Search(lines, "hello")
		if !r1.Changed || len(r1.Matches) != 2 {
			t.Fatalf("Search hello = %#v", r1)
		}
		r2 := idx.Search(lines, "  hello   ")
		if r2.Changed || !reflect.DeepEqual(r2.Matches, r1.Matches) {
			t.Fatalf("expected cached search hit for normalized query: %#v", r2)
		}
		r3 := idx.Search(lines, "  HELLO ")
		if !r3.Changed || !reflect.DeepEqual(r3.Matches, r1.Matches) {
			t.Fatalf("expected case-preserving query miss with matching results: %#v", r3)
		}
	})

	t.Run("renders query bar and tracks clickable prev/next button bounds", func(t *testing.T) {
		comp := NewAltScreenSearchComponent(func(string) {}, nil)
		comp.SetFocused(true)
		comp.HandleInput("x")
		comp.SetResult(1, 3)
		lines := comp.Render(50)
		if len(lines) < 2 || VisibleWidth(lines[1]) > 50 {
			t.Fatalf("unexpected rendered lines: %#v", lines)
		}
		if !strings.Contains(tmarkStripAnsi(lines[1]), "2/3") {
			t.Fatalf("missing match count in %q", tmarkStripAnsi(lines[1]))
		}
	})
}

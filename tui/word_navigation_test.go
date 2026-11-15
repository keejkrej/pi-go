package tui_test

import (
	"testing"

	"github.com/keejkrej/pi-go/tui"
)

func TestWordNavigation(t *testing.T) {
	t.Run("FindWordBackward basic and boundaries", func(t *testing.T) {
		if got := tui.FindWordBackward("", 0, nil); got != 0 {
			t.Fatalf("empty = %d, want 0", got)
		}
		if got := tui.FindWordBackward("foo", 0, nil); got != 0 {
			t.Fatalf("start = %d, want 0", got)
		}
		if got := tui.FindWordBackward("   ", 3, nil); got != 0 {
			t.Fatalf("spaces = %d, want 0", got)
		}
		if got := tui.FindWordBackward("foo bar... baz", 14, nil); got != 11 {
			t.Fatalf("baz = %d, want 11", got)
		}
		if got := tui.FindWordBackward("foo bar... baz", 11, nil); got != 7 {
			t.Fatalf("dots = %d, want 7", got)
		}
		if got := tui.FindWordBackward("foo bar... baz", 7, nil); got != 4 {
			t.Fatalf("bar = %d, want 4", got)
		}
		if got := tui.FindWordBackward("foo.bar baz", 7, nil); got != 4 {
			t.Fatalf("foo.bar @7 = %d, want 4", got)
		}
		if got := tui.FindWordBackward("foo.bar baz", 4, nil); got != 3 {
			t.Fatalf("foo.bar @4 = %d, want 3", got)
		}
	})

	t.Run("FindWordForward basic and boundaries", func(t *testing.T) {
		if got := tui.FindWordForward("   foo bar", 0, nil); got != 6 {
			t.Fatalf("leading spaces = %d, want 6", got)
		}
		if got := tui.FindWordForward("foo bar... baz", 4, nil); got != 7 {
			t.Fatalf("over bar = %d, want 7", got)
		}
		if got := tui.FindWordForward("foo bar... baz", 7, nil); got != 10 {
			t.Fatalf("over dots = %d, want 10", got)
		}
		if got := tui.FindWordForward("foo bar... baz", 10, nil); got != 14 {
			t.Fatalf("over space+baz = %d, want 14", got)
		}
		if got := tui.FindWordForward("foo   ", 3, nil); got != 6 {
			t.Fatalf("trailing spaces = %d, want 6", got)
		}
	})

	t.Run("emoji UTF-16 and atomic paste marker segments", func(t *testing.T) {
		text := "foo 😀 bar" // UTF-16: "foo " (4) + "😀" (2) + " " (1) + "bar" (3) = 10
		if got := tui.FindWordBackward(text, 10, nil); got != 7 {
			t.Fatalf("back from 10 = %d, want 7", got)
		}
		if got := tui.FindWordBackward(text, 7, nil); got != 4 {
			t.Fatalf("back from 7 = %d, want 4", got)
		}
		if got := tui.FindWordForward(text, 4, nil); got != 6 {
			t.Fatalf("fwd over emoji = %d, want 6", got)
		}

		// Atomic segment support (paste markers).
		opts := &tui.WordNavigationOptions{
			Segment: func(in string) []tui.SegmentData {
				wordTrue := true
				return []tui.SegmentData{
					{Segment: "foo", Index: 0, Input: in, IsWordLike: &wordTrue},
					{Segment: " ", Index: 3, Input: in},
					{Segment: "[paste #1]", Index: 4, Input: in, IsWordLike: &wordTrue},
					{Segment: "bar", Index: 14, Input: in, IsWordLike: &wordTrue},
				}
			},
			IsAtomicSegment: func(seg string) bool {
				return seg == "[paste #1]"
			},
		}
		sample := "foo [paste #1]bar"
		if got := tui.FindWordBackward(sample, 17, opts); got != 14 {
			t.Fatalf("atomic back bar = %d, want 14", got)
		}
		if got := tui.FindWordBackward(sample, 14, opts); got != 4 {
			t.Fatalf("atomic back marker = %d, want 4", got)
		}
		if got := tui.FindWordForward(sample, 4, opts); got != 14 {
			t.Fatalf("atomic fwd marker = %d, want 14", got)
		}
	})
}

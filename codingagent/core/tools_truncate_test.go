package core

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSliceHeadTailAndTruncateLine_SurrogateAndRuneSafe(t *testing.T) {
	const emoji = "\U0001F600" // 2 UTF-16 code units

	t.Run("TruncateLine does not split emoji at limit", func(t *testing.T) {
		line := strings.Repeat("a", 499) + emoji + "tail"
		res := TruncateLine(line, 500)
		if !res.WasTruncated {
			t.Fatalf("expected WasTruncated=true")
		}
		want := strings.Repeat("a", 499) + "... [truncated]"
		if res.Text != want {
			t.Fatalf("got %q, want %q", res.Text, want)
		}
		if !utf8.ValidString(res.Text) {
			t.Fatalf("expected valid UTF-8")
		}
	})

	t.Run("TruncateLine keeps emoji ending exactly at limit", func(t *testing.T) {
		line := strings.Repeat("a", 498) + emoji + "tail"
		res := TruncateLine(line, 500)
		want := strings.Repeat("a", 498) + emoji + "... [truncated]"
		if res.Text != want {
			t.Fatalf("got %q, want %q", res.Text, want)
		}
	})

	t.Run("SliceHead and SliceTail never split rune or surrogate pair", func(t *testing.T) {
		text := emoji + "x" + emoji
		for units := 0; units <= 7; units++ {
			head := SliceHead(text, units)
			tail := SliceTail(text, units)
			if !utf8.ValidString(head) || !utf8.ValidString(tail) {
				t.Fatalf("units=%d produced invalid UTF-8: head=%q tail=%q", units, head, tail)
			}
			if !strings.HasPrefix(text, head) {
				t.Fatalf("units=%d head %q not prefix of %q", units, head, text)
			}
			if !strings.HasSuffix(text, tail) {
				t.Fatalf("units=%d tail %q not suffix of %q", units, tail, text)
			}
		}
	})

	t.Run("SliceHead and SliceTail basic slices", func(t *testing.T) {
		if got := SliceHead("abcdef", 3); got != "abc" {
			t.Fatalf("got %q", got)
		}
		if got := SliceTail("abcdef", 3); got != "def" {
			t.Fatalf("got %q", got)
		}
		if got := SliceHead("abc", 10); got != "abc" {
			t.Fatalf("got %q", got)
		}
		if got := SliceTail("abc", 10); got != "abc" {
			t.Fatalf("got %q", got)
		}
		if got := SliceHead("abc", 0); got != "" {
			t.Fatalf("got %q", got)
		}
		if got := SliceTail("abc", 0); got != "" {
			t.Fatalf("got %q", got)
		}
	})
}

func TestTruncateHeadAndTail(t *testing.T) {
	if got := FormatSize(512); got != "512B" {
		t.Fatalf("FormatSize(512)=%q", got)
	}
	if got := FormatSize(1536); got != "1.5KB" {
		t.Fatalf("FormatSize(1536)=%q", got)
	}

	maxLines := 2
	maxBytes := 20
	head := TruncateHead("a\nb\nc\nd", &TruncationOptions{MaxLines: &maxLines, MaxBytes: &maxBytes})
	if !head.Truncated || head.TruncatedBy.OrElse("") != "lines" || head.Content != "a\nb" {
		t.Fatalf("unexpected TruncateHead result: %+v", head)
	}

	maxBytes = 5
	headFirst := TruncateHead("abcdefghij\nx", &TruncationOptions{MaxLines: &maxLines, MaxBytes: &maxBytes})
	if !headFirst.Truncated || !headFirst.FirstLineExceedsLimit || headFirst.Content != "" {
		t.Fatalf("unexpected FirstLineExceedsLimit result: %+v", headFirst)
	}

	maxLines = 2
	maxBytes = 100
	tail := TruncateTail("a\nb\nc\nd", &TruncationOptions{MaxLines: &maxLines, MaxBytes: &maxBytes})
	if !tail.Truncated || tail.TruncatedBy.OrElse("") != "lines" || tail.Content != "c\nd" {
		t.Fatalf("unexpected TruncateTail result: %+v", tail)
	}

	maxBytes = 3
	tailPartial := TruncateTail("abcdef", &TruncationOptions{MaxLines: &maxLines, MaxBytes: &maxBytes})
	if !tailPartial.Truncated || !tailPartial.LastLinePartial || tailPartial.Content != "def" {
		t.Fatalf("unexpected LastLinePartial result: %+v", tailPartial)
	}
}

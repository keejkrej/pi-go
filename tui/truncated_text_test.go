package tui

import (
	"strings"
	"testing"
)

func TestTruncatedTextUnit_ConstructAndInvalidate(t *testing.T) {
	tt := NewTruncatedText("Hello world", 1, 2)
	if tt == nil {
		t.Fatal("NewTruncatedText returned nil")
	}
	if tt.text != "Hello world" || tt.paddingX != 1 || tt.paddingY != 2 {
		t.Fatalf("unexpected fields: text=%q px=%d py=%d", tt.text, tt.paddingX, tt.paddingY)
	}
	tt.Invalidate()
}

func TestTruncatedText_Render(t *testing.T) {
	t.Run("pads output lines to exactly match width", func(t *testing.T) {
		text := NewTruncatedText("Hello world", 1, 0)
		lines := text.Render(50)
		if len(lines) != 1 {
			t.Fatalf("len(lines) = %d, want 1", len(lines))
		}
		if got := VisibleWidth(lines[0]); got != 50 {
			t.Fatalf("VisibleWidth = %d, want 50", got)
		}
	})

	t.Run("pads output with vertical padding lines to width", func(t *testing.T) {
		text := NewTruncatedText("Hello", 0, 2)
		lines := text.Render(40)
		if len(lines) != 5 {
			t.Fatalf("len(lines) = %d, want 5", len(lines))
		}
		for i, line := range lines {
			if got := VisibleWidth(line); got != 40 {
				t.Errorf("line %d VisibleWidth = %d, want 40", i, got)
			}
		}
	})

	t.Run("truncates long text and pads to width", func(t *testing.T) {
		longText := "This is a very long piece of text that will definitely exceed the available width"
		text := NewTruncatedText(longText, 1, 0)
		lines := text.Render(30)
		if len(lines) != 1 {
			t.Fatalf("len(lines) = %d, want 1", len(lines))
		}
		if got := VisibleWidth(lines[0]); got != 30 {
			t.Fatalf("VisibleWidth = %d, want 30", got)
		}
		if !strings.Contains(tmarkStripAnsi(lines[0]), "...") {
			t.Fatalf("expected ellipsis in %q", lines[0])
		}
	})

	t.Run("preserves ANSI codes and adds reset before ellipsis", func(t *testing.T) {
		longStyled := "\x1b[31mThis is a very long red text that will be truncated\x1b[39m"
		text := NewTruncatedText(longStyled, 1, 0)
		lines := text.Render(20)
		if len(lines) != 1 {
			t.Fatalf("len(lines) = %d, want 1", len(lines))
		}
		if got := VisibleWidth(lines[0]); got != 20 {
			t.Fatalf("VisibleWidth = %d, want 20", got)
		}
		if !strings.Contains(lines[0], "\x1b[0m...") {
			t.Fatalf("expected reset+ellipsis in %q", lines[0])
		}
	})

	t.Run("handles text that fits, empty text, and newlines", func(t *testing.T) {
		fit := NewTruncatedText("Hello world", 1, 0).Render(30)
		if len(fit) != 1 || VisibleWidth(fit[0]) != 30 || strings.Contains(tmarkStripAnsi(fit[0]), "...") {
			t.Fatalf("fit check failed: %#v", fit)
		}

		empty := NewTruncatedText("", 1, 0).Render(30)
		if len(empty) != 1 || VisibleWidth(empty[0]) != 30 {
			t.Fatalf("empty check failed: %#v", empty)
		}

		multi := NewTruncatedText("First line\nSecond line\nThird line", 1, 0).Render(40)
		if len(multi) != 1 || VisibleWidth(multi[0]) != 40 {
			t.Fatalf("multiline check failed: %#v", multi)
		}
		stripped := strings.TrimSpace(tmarkStripAnsi(multi[0]))
		if !strings.Contains(stripped, "First line") || strings.Contains(stripped, "Second line") {
			t.Fatalf("multiline stripped = %q", stripped)
		}

		longMulti := NewTruncatedText("This is a very long first line that needs truncation\nSecond line", 1, 0).Render(25)
		if len(longMulti) != 1 || VisibleWidth(longMulti[0]) != 25 || strings.Contains(longMulti[0], "Second line") {
			t.Fatalf("long multiline check failed: %#v", longMulti)
		}
	})
}

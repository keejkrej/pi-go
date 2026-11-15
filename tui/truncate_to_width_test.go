package tui_test

import (
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/tui"
)

func TestTruncateToWidth(t *testing.T) {
	t.Run("no-op and padding when text fits", func(t *testing.T) {
		if got := tui.TruncateToWidth("hello", 10, "...", false); got != "hello" {
			t.Fatalf("got %q, want hello", got)
		}
		if got := tui.TruncateToWidth("hello", 8, "...", true); got != "hello   " {
			t.Fatalf("padded = %q, want %q", got, "hello   ")
		}
	})

	t.Run("truncates ASCII and preserves ANSI reset before ellipsis", func(t *testing.T) {
		if got := tui.TruncateToWidth("Hello World", 8, "...", false); got != "Hello\x1b[0m...\x1b[0m" {
			t.Fatalf("got %q, want %q", got, "Hello\x1b[0m...\x1b[0m")
		}
		if got := tui.TruncateToWidth("Hello World", 5, "", false); got != "Hello\x1b[0m" {
			t.Fatalf("empty ellipsis = %q, want %q", got, "Hello\x1b[0m")
		}
		styled := tui.TruncateToWidth("\x1b[31m"+strings.Repeat("hello ", 1000)+"\x1b[0m", 20, "…", false)
		if tui.VisibleWidth(styled) > 20 || !strings.Contains(styled, "\x1b[31m") || !strings.HasSuffix(styled, "\x1b[0m…\x1b[0m") {
			t.Fatalf("styled = %q, width = %d", styled, tui.VisibleWidth(styled))
		}
		noEllipsis := tui.TruncateToWidth("\x1b[31m"+strings.Repeat("hello", 100), 10, "", false)
		if tui.VisibleWidth(noEllipsis) > 10 || !strings.HasSuffix(noEllipsis, "\x1b[0m") {
			t.Fatalf("noEllipsis = %q, width = %d", noEllipsis, tui.VisibleWidth(noEllipsis))
		}
		openOsc := "\x1b]8;;https://example.com\x07"
		closeOsc := "\x1b]8;;\x07"
		gotOsc := tui.TruncateToWidth(openOsc+"some-longer-label-here"+closeOsc, 15, "...", false)
		wantOsc := openOsc + "some-longer-" + closeOsc + "\x1b[0m...\x1b[0m"
		if gotOsc != wantOsc {
			t.Fatalf("osc truncate = %q, want %q", gotOsc, wantOsc)
		}
	})

	t.Run("maxWidth smaller than or equal to ellipsis", func(t *testing.T) {
		if got := tui.TruncateToWidth("abcdef", 3, "...", false); got != "\x1b[0m...\x1b[0m" {
			t.Fatalf("width=3 = %q", got)
		}
		if got := tui.TruncateToWidth("abcdef", 2, "...", false); got != "\x1b[0m..\x1b[0m" {
			t.Fatalf("width=2 = %q", got)
		}
		if got := tui.TruncateToWidth("abcdef", 0, "...", false); got != "" {
			t.Fatalf("width=0 = %q", got)
		}
		if got := tui.TruncateToWidth("abcdef", 1, "🙂", false); got != "" {
			t.Fatalf("wide ellipsis width=1 = %q, want empty", got)
		}
		if got := tui.TruncateToWidth("abcdef", 2, "🙂", false); got != "\x1b[0m🙂\x1b[0m" {
			t.Fatalf("wide ellipsis width=2 = %q", got)
		}
		if got := tui.TruncateToWidth("a", 2, "🙂", false); got != "a" {
			t.Fatalf("fits width=2 with wide ellipsis = %q", got)
		}
		if got := tui.TruncateToWidth("界", 2, "🙂", false); got != "界" {
			t.Fatalf("fits CJK width=2 with wide ellipsis = %q", got)
		}
	})

	t.Run("wide characters, emojis, malformed escapes, contiguous prefix, and Indic conjuncts", func(t *testing.T) {
		if got := tui.TruncateToWidth("你好世界", 7, "...", false); got != "你好\x1b[0m...\x1b[0m" {
			t.Fatalf("CJK odd width = %q, want %q", got, "你好\x1b[0m...\x1b[0m")
		}
		if got := tui.TruncateToWidth("你好世界", 6, "...", false); got != "你\x1b[0m...\x1b[0m" {
			t.Fatalf("CJK even width = %q, want %q", got, "你\x1b[0m...\x1b[0m")
		}
		if got := tui.TruncateToWidth("😀😀😀", 5, "...", false); got != "😀\x1b[0m...\x1b[0m" {
			t.Fatalf("emoji = %q, want %q", got, "😀\x1b[0m...\x1b[0m")
		}
		large := tui.TruncateToWidth(strings.Repeat("🙂界", 5000), 40, "…", false)
		if tui.VisibleWidth(large) > 40 || !strings.HasSuffix(large, "…\x1b[0m") {
			t.Fatalf("large = width %d suffix %q", tui.VisibleWidth(large), large)
		}
		malformed := tui.TruncateToWidth("abc\x1bnot-ansi "+strings.Repeat("🙂", 1000), 20, "…", false)
		if tui.VisibleWidth(malformed) > 20 {
			t.Fatalf("malformed = width %d > 20", tui.VisibleWidth(malformed))
		}
		indic := tui.TruncateToWidth("सर्वाधिकार सुरक्षित", 10, "...", false)
		if tui.VisibleWidth(indic) > 10 {
			t.Fatalf("indic width = %d > 10 (%q)", tui.VisibleWidth(indic), indic)
		}
		padded := tui.TruncateToWidth("🙂界🙂界🙂界", 8, "…", true)
		if tui.VisibleWidth(padded) != 8 {
			t.Fatalf("padded wide width = %d, want 8", tui.VisibleWidth(padded))
		}
		contiguous := tui.TruncateToWidth("🙂\t界 \x1b_abc\x07", 7, "…", true)
		if contiguous != "🙂\t\x1b[0m…\x1b[0m " {
			t.Fatalf("contiguous prefix = %q, want %q", contiguous, "🙂\t\x1b[0m…\x1b[0m ")
		}
	})
}

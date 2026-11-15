package tui_test

import (
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/tui"
)

func TestVisibleWidth(t *testing.T) {
	t.Run("ASCII and ANSI", func(t *testing.T) {
		if got := tui.VisibleWidth("hello"); got != 5 {
			t.Fatalf("got %d, want 5", got)
		}
		if got := tui.VisibleWidth("\x1b[31mhello\x1b[0m"); got != 5 {
			t.Fatalf("ANSI hello = %d, want 5", got)
		}
		if got := tui.VisibleWidth("\x1b]8;;https://example.com\x07link\x1b]8;;\x07"); got != 4 {
			t.Fatalf("OSC8 link = %d, want 4", got)
		}
	})

	t.Run("tabs and control characters", func(t *testing.T) {
		if got := tui.VisibleWidth("\t"); got != 3 {
			t.Fatalf("tab = %d, want 3", got)
		}
		if got := tui.VisibleWidth("a\tb"); got != 5 {
			t.Fatalf("a\\tb = %d, want 5", got)
		}
		if got := tui.VisibleWidth("\x00\x07\n"); got != 0 {
			t.Fatalf("controls = %d, want 0", got)
		}
	})

	t.Run("malformed escapes", func(t *testing.T) {
		if got := tui.VisibleWidth("\x1b[31"); got != 3 {
			t.Fatalf("unterminated CSI = %d, want 3", got)
		}
		if got := tui.VisibleWidth("a\x1b"); got != 1 {
			t.Fatalf("trailing ESC = %d, want 1", got)
		}
	})

	t.Run("CJK, emoji, combining marks, Indic conjuncts", func(t *testing.T) {
		cases := []struct {
			in   string
			want int
		}{
			{"你好", 4},
			{"😀", 2},
			{"👨‍👩‍👧‍👦", 2},
			// Ordinary combining marks stay zero-width
			{"e\u0301", 1},
			{"čřžůú", 5},
			{"שָׁ", 1},
			{"بّ", 1},
			{"རྐ", 1},
			{"ᜠ᜴", 1},
			{"가〮", 2},
			{"가〯", 2},
			// Indic conjuncts count spacing code points within grapheme clusters
			{"र्क", 2},
			{"नेटवर्क", 5},
			{"सर्वाधिकार सुरक्षित। ऑर्डर पर क्लिक करें", 33},
			{"র্ক", 2},
			{"ર્ક", 2},
			{"ର୍କ", 2},
			{"ర్క", 2},
			{"ര്‍ക", 2},
			// CJK & Japanese
			{"网络", 4},
			{"ネットワーク", 12},
			{"が", 2},
			{"か\u3099", 2},
			// Myanmar spacing vs combining marks
			{"ကာ", 2},
			{"ကေ", 2},
			{"က်", 2},
			{"ကျ", 2},
			{"ကြ", 2},
			{"ကဳ", 2},
			{"ကဴ", 2},
			{"ကဵ", 2},
			{"ကး", 2},
			{"ကို", 1},
			{"က္", 1},
			// Thai & Lao AM clusters
			{"ำ", 1},
			{"ຳ", 1},
			{"กำ", 2},
			{"ກຳ", 2},
		}
		for _, tc := range cases {
			if got := tui.VisibleWidth(tc.in); got != tc.want {
				t.Errorf("VisibleWidth(%q) = %d, want %d", tc.in, got, tc.want)
			}
		}
	})

	t.Run("normalizes Thai and Lao AM vowels only for terminal output", func(t *testing.T) {
		if got := tui.NormalizeTerminalOutput("ำ"); got != "\u0e4d\u0e32" {
			t.Fatalf("Thai AM normalized = %q, want %q", got, "\u0e4d\u0e32")
		}
		if got := tui.NormalizeTerminalOutput("ຳ"); got != "\u0ecd\u0eb2" {
			t.Fatalf("Lao AM normalized = %q, want %q", got, "\u0ecd\u0eb2")
		}
		if got, want := tui.VisibleWidth(tui.NormalizeTerminalOutput("ำabc")), tui.VisibleWidth("ำabc"); got != want {
			t.Fatalf("VisibleWidth(normalized Thai AM) = %d, want %d", got, want)
		}
		if got, want := tui.VisibleWidth(tui.NormalizeTerminalOutput("ຳabc")), tui.VisibleWidth("ຳabc"); got != want {
			t.Fatalf("VisibleWidth(normalized Lao AM) = %d, want %d", got, want)
		}
	})

	t.Run("handles repeated calls cleanly", func(t *testing.T) {
		long := strings.Repeat("a", 2000)
		if got := tui.VisibleWidth(long); got != 2000 {
			t.Fatalf("long = %d, want 2000", got)
		}
	})
}

func TestRegressionRegionalIndicatorWidth(t *testing.T) {
	if got := tui.VisibleWidth("🇨"); got != 2 {
		t.Fatalf("singleton RI = %d, want 2", got)
	}
	if got := tui.VisibleWidth("🇨🇦"); got != 2 {
		t.Fatalf("flag RI pair = %d, want 2", got)
	}
	if got := tui.VisibleWidth("🇨🇦🇧🇪"); got != 4 {
		t.Fatalf("two flags = %d, want 4", got)
	}
	if got := tui.VisibleWidth("hello 🇨 world"); got != 14 {
		t.Fatalf("hello RI world = %d, want 14", got)
	}
}

func TestRegressionSliceByColumnAnsiOrder(t *testing.T) {
	// Does not re-open a foreground color that was closed before the slice starts (#regression-slice-by-column-ansi-order)
	fgClosed := tui.SliceByColumn("\x1b[32mfoo\x1b[39m bar", 3, 4, true)
	if fgClosed != "\x1b[32m\x1b[39m bar" {
		t.Fatalf("SliceByColumn fg-closed = %q, want %q", fgClosed, "\x1b[32m\x1b[39m bar")
	}

	line := "\x1b[1m\x1b[31mhello world\x1b[0m"
	sliced := tui.SliceByColumn(line, 6, 5, true)
	if tui.StripTerminalSequences(sliced) != "world" {
		t.Fatalf("stripped sliced = %q, want world", tui.StripTerminalSequences(sliced))
	}
	if sliced != "\x1b[1m\x1b[31mworld" {
		t.Fatalf("sliced = %q, want %q", sliced, "\x1b[1m\x1b[31mworld")
	}
	if tui.VisibleWidth(sliced) != 5 {
		t.Fatalf("VisibleWidth(sliced) = %d, want 5", tui.VisibleWidth(sliced))
	}
}

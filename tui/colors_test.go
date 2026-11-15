// Ported from packages/tui/test/colors.test.ts (pi v1.5.2).

package tui_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/keejkrej/pi-go/tui"
)

func boolPtr(b bool) *bool { return &b }

func mustParseColor(t *testing.T, v any) tui.Color {
	t.Helper()
	c, err := tui.ParseColor(v)
	if err != nil {
		t.Fatalf("ParseColor(%v) failed: %v", v, err)
	}
	return c
}

func TestColors(t *testing.T) {
	t.Run("parses hex and OKLCH colors and rejects everything else", func(t *testing.T) {
		c1 := mustParseColor(t, "#abc")
		want1 := &tui.RgbColorValue{Kind: "rgb", R: 170, G: 187, B: 204}
		if diff := cmp.Diff(want1, c1); diff != "" {
			t.Fatalf("mismatch (-want +got):\n%s", diff)
		}

		c2 := mustParseColor(t, "oklch(62% 0.1 200)")
		want2 := &tui.OklchColorValue{Kind: "oklch", L: 0.62, C: 0.1, H: 200}
		if diff := cmp.Diff(want2, c2); diff != "" {
			t.Fatalf("mismatch (-want +got):\n%s", diff)
		}

		for _, bad := range []string{"", "red"} {
			_, err := tui.ParseColor(bad)
			if err == nil || !strings.Contains(err.Error(), "Invalid color value") {
				t.Fatalf("ParseColor(%q) expected Invalid color value error, got %v", bad, err)
			}
		}
	})

	t.Run("gamut-maps OKLCH to sRGB, including the lightness limits", func(t *testing.T) {
		c1, _ := tui.OklchColor(0.627955, 0.257683, 29.2339)
		if diff := cmp.Diff(tui.RgbColor{R: 255, G: 0, B: 0}, tui.ColorToRgb(c1)); diff != "" {
			t.Fatalf("red mismatch (-want +got):\n%s", diff)
		}
		c2, _ := tui.OklchColor(1, 0.3, 150)
		if diff := cmp.Diff(tui.RgbColor{R: 255, G: 255, B: 255}, tui.ColorToRgb(c2)); diff != "" {
			t.Fatalf("white mismatch (-want +got):\n%s", diff)
		}
		c3, _ := tui.OklchColor(0, 0.3, 150)
		if diff := cmp.Diff(tui.RgbColor{R: 0, G: 0, B: 0}, tui.ColorToRgb(c3)); diff != "" {
			t.Fatalf("black mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("parses OKHSL colors and round-trips them", func(t *testing.T) {
		wantRed, _ := tui.NewRgbColor(255, 0, 0)
		if diff := cmp.Diff(wantRed, mustParseColor(t, "okhsl(29.23 100% 56.8%)")); diff != "" {
			t.Fatalf("red okhsl mismatch (-want +got):\n%s", diff)
		}
		wantPurple, _ := tui.OkhslColor(250, 0.6, 0.55)
		if diff := cmp.Diff(wantPurple, mustParseColor(t, "OKHSL(250deg 60% 55%)")); diff != "" {
			t.Fatalf("purple okhsl mismatch (-want +got):\n%s", diff)
		}
		_, err := tui.ParseColor("okhsl(250 160% 55%)")
		if err == nil || !strings.Contains(err.Error(), "s must be between 0 and 1") {
			t.Fatalf("expected saturation range error, got %v", err)
		}
		for _, hex := range []string{"#4f8eb3", "#20242a", "#f8f9fa"} {
			ch := tui.ColorToOkhsl(mustParseColor(t, hex))
			round, err := tui.OkhslColor(ch.H, ch.S, ch.L)
			if err != nil {
				t.Fatalf("OkhslColor failed: %v", err)
			}
			if got := tui.ColorToHex(round); got != hex {
				t.Fatalf("roundtrip %s -> %s", hex, got)
			}
		}
	})

	t.Run("styles text and closes sequences in reverse order", func(t *testing.T) {
		fg, _ := tui.NewRgbColor(18, 52, 86)
		bg, _ := tui.NewIndexedColor(9)
		got := tui.StyleText("Ready", tui.TextStyle{
			TextAttributes: tui.TextAttributes{Bold: boolPtr(true), Italic: boolPtr(true)},
			Fg:             fg,
			Bg:             bg,
		}, tui.TerminalColorModeTruecolor)
		want := "\x1b[38;2;18;52;86m\x1b[48;5;9m\x1b[1m\x1b[3mReady\x1b[23m\x1b[22m\x1b[49m\x1b[39m"
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}

		got256 := tui.StyleText("Ready", tui.TextStyle{Fg: fg}, tui.TerminalColorMode256color)
		re := regexp.MustCompile(`^\x1b\[38;5;\d+mReady\x1b\[39m$`)
		if !re.MatchString(got256) {
			t.Fatalf("256color mismatch: %q", got256)
		}
	})
}

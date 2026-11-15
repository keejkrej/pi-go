// Ported from packages/tui/test/keys.test.ts (pi v1.5.2).

package tui_test

import (
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/tui"
)

func keysTestAssertStrPtr(t *testing.T, got *string, want string) {
	t.Helper()
	if got == nil {
		t.Fatalf("got nil, want %q", want)
	}
	if *got != want {
		t.Fatalf("got %q, want %q", *got, want)
	}
}

func TestKeysKittyProtocolMatching(t *testing.T) {
	t.Run("matches alternate keys on non-Latin layouts", func(t *testing.T) {
		defer tui.SetKittyProtocolActive(false)
		tui.SetKittyProtocolActive(true)

		// Cyrillic Ctrl+С (codepoint 1089) with base layout key 'c' (codepoint 99)
		cyrillicCtrlC := "\x1b[1089::99;5u"
		if !tui.MatchesKey(cyrillicCtrlC, "ctrl+c") {
			t.Fatalf("expected Cyrillic Ctrl+C to match ctrl+c")
		}
		keysTestAssertStrPtr(t, tui.ParseKey(cyrillicCtrlC), "ctrl+c")

		// Cyrillic Ctrl+Shift+С with shifted key and base layout key
		cyrillicCtrlShiftC := "\x1b[1089:1057:99;6u"
		if !tui.MatchesKey(cyrillicCtrlShiftC, "ctrl+shift+c") {
			t.Fatalf("expected Cyrillic Ctrl+Shift+C to match ctrl+shift+c")
		}
		keysTestAssertStrPtr(t, tui.ParseKey(cyrillicCtrlShiftC), "shift+ctrl+c")
	})

	t.Run("prefers authoritative codepoint on Dvorak/Colemak remapped layouts", func(t *testing.T) {
		defer tui.SetKittyProtocolActive(false)
		tui.SetKittyProtocolActive(true)

		// Dvorak Ctrl+K (codepoint 107) on QWERTY physical 'v' position (base 118)
		dvorakCtrlK := "\x1b[107::118;5u"
		if !tui.MatchesKey(dvorakCtrlK, "ctrl+k") {
			t.Fatalf("expected Dvorak Ctrl+K to match ctrl+k")
		}
		if tui.MatchesKey(dvorakCtrlK, "ctrl+v") {
			t.Fatalf("did not expect Dvorak Ctrl+K to match ctrl+v")
		}
		keysTestAssertStrPtr(t, tui.ParseKey(dvorakCtrlK), "ctrl+k")

		// Dvorak plain 'k' with base layout key 'v'
		dvorakPlainK := "\x1b[107::118u"
		if !tui.MatchesKey(dvorakPlainK, "k") || tui.MatchesKey(dvorakPlainK, "v") {
			t.Fatalf("plain k should match k and not v")
		}
		keysTestAssertStrPtr(t, tui.ParseKey(dvorakPlainK), "k")

		// Dvorak Shift+K: \x1b[107:75:118;2u
		dvorakShiftK := "\x1b[107:75:118;2u"
		if !tui.MatchesKey(dvorakShiftK, "shift+k") || tui.MatchesKey(dvorakShiftK, "shift+v") {
			t.Fatalf("Shift+K should match shift+k and not shift+v")
		}
		keysTestAssertStrPtr(t, tui.ParseKey(dvorakShiftK), "shift+k")

		// Dvorak Ctrl+/ (codepoint 47) on QWERTY '[' position (base 91)
		dvorakCtrlSlash := "\x1b[47::91;5u"
		if !tui.MatchesKey(dvorakCtrlSlash, "ctrl+/") || tui.MatchesKey(dvorakCtrlSlash, "ctrl+[") {
			t.Fatalf("Dvorak Ctrl+/ should match ctrl+/ and not ctrl+[")
		}
		keysTestAssertStrPtr(t, tui.ParseKey(dvorakCtrlSlash), "ctrl+/")
	})

	t.Run("does not allow base layout key fallback with wrong modifiers", func(t *testing.T) {
		defer tui.SetKittyProtocolActive(false)
		tui.SetKittyProtocolActive(true)

		cyrillicCtrlC := "\x1b[1089::99;5u"
		if tui.MatchesKey(cyrillicCtrlC, "c") {
			t.Fatalf("ctrl+c must not match plain c")
		}
		if tui.MatchesKey(cyrillicCtrlC, "alt+c") {
			t.Fatalf("ctrl+c must not match alt+c")
		}
		if tui.MatchesKey(cyrillicCtrlC, "ctrl+d") {
			t.Fatalf("ctrl+c must not match ctrl+d")
		}
	})

	t.Run("matches keys when lock modifiers are active", func(t *testing.T) {
		defer tui.SetKittyProtocolActive(false)
		tui.SetKittyProtocolActive(true)

		// Caps Lock (64) + 1 = 65 -> mod=64
		if !tui.MatchesKey("\x1b[97;65u", "a") {
			t.Fatalf("Caps Lock 'a' should match 'a'")
		}
		keysTestAssertStrPtr(t, tui.ParseKey("\x1b[97;65u"), "a")

		// Num Lock (128) + Ctrl (4) + 1 = 133 -> mod=132
		if !tui.MatchesKey("\x1b[99;133u", "ctrl+c") {
			t.Fatalf("Num Lock + Ctrl+C should match ctrl+c")
		}
		keysTestAssertStrPtr(t, tui.ParseKey("\x1b[99;133u"), "ctrl+c")

		// Caps Lock + Num Lock + Ctrl + Shift + 1 = 197 -> uppercase Z (90)
		if !tui.MatchesKey("\x1b[90;198u", "ctrl+shift+z") {
			t.Fatalf("Caps Lock + Num Lock + Ctrl+Shift+Z should match ctrl+shift+z")
		}
		keysTestAssertStrPtr(t, tui.ParseKey("\x1b[90;198u"), "shift+ctrl+z")
	})

	t.Run("treats shifted uppercase CSI-u letters as uppercase identity", func(t *testing.T) {
		defer tui.SetKittyProtocolActive(false)
		tui.SetKittyProtocolActive(true)

		if !tui.MatchesKey("\x1b[71;6u", "ctrl+shift+g") {
			t.Fatalf("shifted G in CSI-u should match ctrl+shift+g")
		}
		keysTestAssertStrPtr(t, tui.ParseKey("\x1b[71;6u"), "shift+ctrl+g")

		if !tui.MatchesKey("\x1b[71;2u", "shift+g") {
			t.Fatalf("shifted G without ctrl should match shift+g")
		}
		keysTestAssertStrPtr(t, tui.ParseKey("\x1b[71;2u"), "shift+g")
	})

	t.Run("matches logical shifted symbols in Kitty and modifyOtherKeys", func(t *testing.T) {
		defer tui.SetKittyProtocolActive(false)
		tui.SetKittyProtocolActive(true)

		if !tui.MatchesKey("\x1b[61:43;2u", "+") || !tui.MatchesKey("\x1b[61:43;2u", "shift+=") {
			t.Fatalf("Kitty Shift+= producing + should match + and shift+=")
		}
		keysTestAssertStrPtr(t, tui.ParseKey("\x1b[61:43;2u"), "+")

		if !tui.MatchesKey("\x1b[61:43;6u", "ctrl++") || !tui.MatchesKey("\x1b[61:43;6u", "ctrl+shift+=") {
			t.Fatalf("Kitty Ctrl+Shift+= producing ctrl++ should match both")
		}
		keysTestAssertStrPtr(t, tui.ParseKey("\x1b[61:43;6u"), "ctrl++")

		if !tui.MatchesKey("\x1b[61:43;10u", "super++") {
			t.Fatalf("Kitty Super+Shift+= producing super++ should match super++")
		}
		keysTestAssertStrPtr(t, tui.ParseKey("\x1b[61:43;10u"), "super++")

		if !tui.MatchesKey("\x1b[47:63;6u", "ctrl+?") || !tui.MatchesKey("\x1b[47:63;6u", "ctrl+shift+/") {
			t.Fatalf("Kitty Ctrl+Shift+/ producing ctrl+? should match both")
		}
		keysTestAssertStrPtr(t, tui.ParseKey("\x1b[47:63;6u"), "ctrl+?")

		if !tui.MatchesKey("\x1b[27;6;43~", "ctrl++") {
			t.Fatalf("modifyOtherKeys Ctrl+Shift++ should match ctrl++")
		}
		keysTestAssertStrPtr(t, tui.ParseKey("\x1b[27;6;43~"), "ctrl++")

		if !tui.MatchesKey("\x1b[27;6;63~", "ctrl+?") {
			t.Fatalf("modifyOtherKeys Ctrl+Shift+? should match ctrl+?")
		}
		keysTestAssertStrPtr(t, tui.ParseKey("\x1b[27;6;63~"), "ctrl+?")

		if !tui.MatchesKey("\x1b[27;6;71~", "ctrl+shift+g") {
			t.Fatalf("modifyOtherKeys Ctrl+Shift+G should match ctrl+shift+g")
		}
		keysTestAssertStrPtr(t, tui.ParseKey("\x1b[27;6;71~"), "shift+ctrl+g")

		if !tui.MatchesKey("+", "+") || !tui.MatchesKey("+", tui.KeyPlus) {
			t.Fatalf("plain '+' should match '+'")
		}
	})

	t.Run("matches Kitty keypad enter and functional equivalents", func(t *testing.T) {
		defer tui.SetKittyProtocolActive(false)
		tui.SetKittyProtocolActive(true)

		if !tui.MatchesKey("\x1b[57414u", "enter") || !tui.MatchesKey("\x1b[57414u", "return") {
			t.Fatalf("kpEnter should match enter and return")
		}
		if !tui.MatchesKey("\x1b[57414;2u", "shift+enter") || !tui.MatchesKey("\x1b[57414;5u", "ctrl+enter") {
			t.Fatalf("modified kpEnter should match shift/ctrl+enter")
		}
		if !tui.MatchesKey("\x1b[57417u", "left") || !tui.MatchesKey("\x1b[57419;3u", "alt+up") {
			t.Fatalf("KP arrows should match arrows")
		}
		if !tui.MatchesKey("\x1b[57423;5u", "ctrl+home") || !tui.MatchesKey("\x1b[57426u", "delete") {
			t.Fatalf("KP home/delete should match")
		}
		keysTestAssertStrPtr(t, tui.ParseKey("\x1b[57414u"), "enter")
		keysTestAssertStrPtr(t, tui.ParseKey("\x1b[57421;5u"), "ctrl+pageUp")
	})

	t.Run("handles event types and release/repeat helpers", func(t *testing.T) {
		defer tui.SetKittyProtocolActive(false)
		tui.SetKittyProtocolActive(true)

		if !tui.MatchesKey("\x1b[99;5:1u", "ctrl+c") || tui.IsKeyRelease("\x1b[99;5:1u") || tui.IsKeyRepeat("\x1b[99;5:1u") {
			t.Fatalf("press event mismatch")
		}
		if !tui.MatchesKey("\x1b[99;5:2u", "ctrl+c") || !tui.IsKeyRepeat("\x1b[99;5:2u") || tui.IsKeyRelease("\x1b[99;5:2u") {
			t.Fatalf("repeat event mismatch")
		}
		if !tui.MatchesKey("\x1b[99;5:3u", "ctrl+c") || !tui.IsKeyRelease("\x1b[99;5:3u") || tui.IsKeyRepeat("\x1b[99;5:3u") {
			t.Fatalf("release event mismatch")
		}
		if tui.IsKeyRelease("\x1b[200~90:62:3F:A5\x1b[201~") {
			t.Fatalf("bracketed paste MAC address must not be flagged as release")
		}
		if tui.IsKeyRepeat("\x1b[200~90:62:2F:A5\x1b[201~") {
			t.Fatalf("bracketed paste MAC address must not be flagged as repeat")
		}
	})

	t.Run("handles legacy sequences and terminal heuristics", func(t *testing.T) {
		tui.SetKittyProtocolActive(false)

		if !tui.MatchesKey("\x1bB", "alt+left") || !tui.MatchesKey("\x1bF", "alt+right") {
			t.Fatalf("macOS Terminal Option+Left/Right should match in legacy mode")
		}
		keysTestAssertStrPtr(t, tui.ParseKey("\x1bB"), "alt+left")
		keysTestAssertStrPtr(t, tui.ParseKey("\x1bF"), "alt+right")
		keysTestAssertStrPtr(t, tui.ParseKey("\x1bp"), "alt+up")
		keysTestAssertStrPtr(t, tui.ParseKey("\x1bn"), "alt+down")

		// Legacy modifier arrow/functional sequences
		if !tui.MatchesKey("\x1b[a", "shift+up") || !tui.MatchesKey("\x1bOa", "ctrl+up") {
			t.Fatalf("urxvt/rxvt shift/ctrl arrow mismatch")
		}
		if !tui.MatchesKey("\x1b[[5~", "pageUp") || !tui.MatchesKey("\x1b[5^", "ctrl+pageUp") {
			t.Fatalf("legacy pageUp/ctrl+pageUp mismatch")
		}

		// Legacy ctrl+alt+letter
		if !tui.MatchesKey("\x1b\x01", "ctrl+alt+a") {
			t.Fatalf("legacy ctrl+alt+a mismatch")
		}
		keysTestAssertStrPtr(t, tui.ParseKey("\x1b\x01"), "ctrl+alt+a")

		// ctrl+space
		if !tui.MatchesKey("\x00", "ctrl+space") {
			t.Fatalf("ctrl+space mismatch")
		}
		keysTestAssertStrPtr(t, tui.ParseKey("\x00"), "ctrl+space")

		// Ambiguous Enter / Alt+Enter across Kitty mode toggle
		if tui.MatchesKey("\n", "shift+enter") || !tui.MatchesKey("\n", "enter") {
			t.Fatalf("in legacy mode LF is enter")
		}
		if !tui.MatchesKey("\x1b\r", "alt+enter") {
			t.Fatalf("in legacy mode ESC+CR is alt+enter")
		}

		tui.SetKittyProtocolActive(true)
		if tui.MatchesKey("\x1bB", "alt+left") || tui.MatchesKey("\x1bF", "alt+right") {
			t.Fatalf("in Kitty mode ESC+B/F must not match alt+left/right")
		}
		if !tui.MatchesKey("\n", "shift+enter") || !tui.MatchesKey("\x1b\r", "shift+enter") {
			t.Fatalf("in Kitty mode LF and ESC+CR match shift+enter")
		}
		tui.SetKittyProtocolActive(false)
	})

	t.Run("matches and parses Windows Terminal raw backspace heuristic", func(t *testing.T) {
		t.Setenv("WT_SESSION", "")
		t.Setenv("SSH_CONNECTION", "")
		t.Setenv("SSH_CLIENT", "")
		t.Setenv("SSH_TTY", "")

		if !tui.MatchesKey("\x08", "backspace") || tui.MatchesKey("\x08", "ctrl+backspace") {
			t.Fatalf("outside WT, raw 0x08 should be backspace")
		}
		keysTestAssertStrPtr(t, tui.ParseKey("\x08"), "backspace")

		t.Setenv("WT_SESSION", "wt-guid")
		if tui.MatchesKey("\x08", "backspace") || !tui.MatchesKey("\x08", "ctrl+backspace") {
			t.Fatalf("inside local WT, raw 0x08 should be ctrl+backspace")
		}
		keysTestAssertStrPtr(t, tui.ParseKey("\x08"), "ctrl+backspace")

		t.Setenv("SSH_TTY", "/dev/pts/1")
		if !tui.MatchesKey("\x08", "backspace") || tui.MatchesKey("\x08", "ctrl+backspace") {
			t.Fatalf("inside SSH over WT, raw 0x08 falls back to backspace")
		}
	})

	t.Run("rejects malformed or unknown inputs and validates ParseKeyId", func(t *testing.T) {
		if tui.MatchesKey("a", "invalid++") {
			t.Fatalf("invalid keyId should not match")
		}
		if tui.MatchesKey("a", "ctrl+ctrl+a") {
			t.Fatalf("duplicate modifier keyId should not match")
		}
		if got := tui.ParseKey("\x1b[999;5u"); got != nil {
			// Codepoint 999 is non-ASCII/unmapped printable letter/symbol so ParseKey returns nil
			t.Fatalf("expected nil for codepoint 999, got %q", *got)
		}
		if got := tui.ParseKey("abc"); got != nil {
			t.Fatalf("expected nil for multi-char plain string, got %q", *got)
		}
	})
}

func TestDecodePrintableKeys(t *testing.T) {
	keysTestAssertStrPtr(t, tui.DecodeKittyPrintable("\x1b[97u"), "a")
	keysTestAssertStrPtr(t, tui.DecodeKittyPrintable("\x1b[97:65;2u"), "A")
	keysTestAssertStrPtr(t, tui.DecodeKittyPrintable("\x1b[97:65;66u"), "A") // Shift + Caps Lock
	keysTestAssertStrPtr(t, tui.DecodeKittyPrintable("\x1b[57400u"), "1")    // KP_1
	keysTestAssertStrPtr(t, tui.DecodeKittyPrintable("\x1b[128512u"), "😀")
	if got := tui.DecodeKittyPrintable("\x1b[97;5u"); got != nil {
		t.Fatalf("ctrl+a must not decode as printable, got %q", *got)
	}
	if got := tui.DecodeKittyPrintable("\x1b[57376u"); got != nil {
		t.Fatalf("PUA functional key must return nil, got %q", *got)
	}
	if got := tui.DecodeKittyPrintable("\x1b[27u"); got != nil {
		t.Fatalf("escape must return nil, got %q", *got)
	}

	keysTestAssertStrPtr(t, tui.DecodePrintableKey("\x1b[97:65;2u"), "A")
	keysTestAssertStrPtr(t, tui.DecodePrintableKey("\x1b[27;2;65~"), "A")
	keysTestAssertStrPtr(t, tui.DecodePrintableKey("\x1b[27;1;32~"), " ")
	if got := tui.DecodePrintableKey("\x1b[27;5;97~"); got != nil {
		t.Fatalf("modifyOtherKeys ctrl+a must return nil, got %q", *got)
	}

	// Ensure KeyId combiners work properly
	if id := tui.KeyCtrlShift(tui.KeyId(strings.ToLower("P"))); id != "ctrl+shift+p" {
		t.Fatalf("unexpected combiner result: %q", id)
	}
}

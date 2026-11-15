package theme

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/tui"
)

// khPlatform is overridden in tests to verify platform-specific key formatting.
var khPlatform = js.Platform

// KeyTextFormatOptions controls formatting of human-readable keybinding hints.
type KeyTextFormatOptions struct {
	Capitalize bool
}

// FormatKeyText formats a keybinding string (for example "alt+enter" or "escape/ctrl+c")
// for display, translating "alt" to "option" on macOS and optionally capitalizing each key part.
func FormatKeyText(key string, options ...KeyTextFormatOptions) string {
	capitalize := len(options) > 0 && options[0].Capitalize
	isMac := khPlatform() == "darwin"

	combos := strings.Split(key, "/")
	for i, combo := range combos {
		parts := strings.Split(combo, "+")
		for j, part := range parts {
			formatted := part
			if isMac && strings.ToLower(part) == "alt" {
				formatted = "option"
			}
			if capitalize && formatted != "" {
				r, size := utf8.DecodeRuneInString(formatted)
				formatted = string(unicode.ToUpper(r)) + formatted[size:]
			}
			parts[j] = formatted
		}
		combos[i] = strings.Join(parts, "+")
	}
	return strings.Join(combos, "/")
}

// FormatKeys formats a slice of key IDs joined by "/".
func FormatKeys(keys []tui.KeyId, options ...KeyTextFormatOptions) string {
	if len(keys) == 0 {
		return ""
	}
	raw := make([]string, len(keys))
	for i, k := range keys {
		raw[i] = string(k)
	}
	return FormatKeyText(strings.Join(raw, "/"), options...)
}

// KeyText returns lowercase formatted keybinding text for an app keybinding action.
func KeyText(keybinding tui.Keybinding) string {
	return FormatKeys(tui.GetKeybindings().GetKeys(keybinding))
}

// KeyDisplayText returns capitalized formatted keybinding text for an app keybinding action.
func KeyDisplayText(keybinding tui.Keybinding) string {
	return FormatKeys(tui.GetKeybindings().GetKeys(keybinding), KeyTextFormatOptions{Capitalize: true})
}

// AppKey formats the primary app keybinding action in the theme's dim foreground color.
func AppKey(t *Theme, action tui.Keybinding) string {
	keys := tui.GetKeybindings().GetKeys(action)
	if len(keys) == 0 {
		return ""
	}
	return t.Fg(FgDim, FormatKeys(keys))
}

// RawKeyHint renders a key hint with dim key text followed by muted description text.
func RawKeyHint(t *Theme, key, description string) string {
	return t.Fg(FgDim, FormatKeyText(key)) + t.Fg(FgMuted, " "+description)
}

// AppKeyHint renders a key hint for an app keybinding action using the provided theme.
func AppKeyHint(t *Theme, action tui.Keybinding, description string) string {
	return RawKeyHint(t, FormatKeys(tui.GetKeybindings().GetKeys(action)), description)
}

// KeyHint renders a key hint using the currently active theme.
func KeyHint(key, description string) string {
	return RawKeyHint(CurrentTheme(), key, description)
}

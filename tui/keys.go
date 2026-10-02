// Ported from packages/tui/src/keys.ts (pi v1.0.0).

package tui

// keysKittyProtocolActive is the global Kitty keyboard protocol flag.
// ProcessTerminal sets it after detecting protocol support.
var keysKittyProtocolActive bool

// SetKittyProtocolActive sets the global Kitty keyboard protocol flag.
func SetKittyProtocolActive(active bool) {
	keysKittyProtocolActive = active
}

// IsKittyProtocolActive reports whether the Kitty keyboard protocol is active.
func IsKittyProtocolActive() bool {
	return keysKittyProtocolActive
}

// KeyId is a key identifier: a base key, or base key with ctrl/shift/alt/super
// prefixes in any order ("ctrl+c", "shift+ctrl+p", "super+k").
// The TS union is generated; any of those strings is a KeyId.
type KeyId string

// Key names from the TS Key helper. Symbol properties keep the helper's name
// (Key.backtick is "`"). Modifier combiners are KeyCtrl, KeyCtrlShift, and so on.
const (
	KeyEscape    KeyId = "escape"
	KeyEsc       KeyId = "esc"
	KeyEnter     KeyId = "enter"
	KeyReturn    KeyId = "return"
	KeyTab       KeyId = "tab"
	KeySpace     KeyId = "space"
	KeyBackspace KeyId = "backspace"
	KeyDelete    KeyId = "delete"
	KeyInsert    KeyId = "insert"
	KeyClear     KeyId = "clear"
	KeyHome      KeyId = "home"
	KeyEnd       KeyId = "end"
	KeyPageUp    KeyId = "pageUp"
	KeyPageDown  KeyId = "pageDown"
	KeyUp        KeyId = "up"
	KeyDown      KeyId = "down"
	KeyLeft      KeyId = "left"
	KeyRight     KeyId = "right"
	KeyF1        KeyId = "f1"
	KeyF2        KeyId = "f2"
	KeyF3        KeyId = "f3"
	KeyF4        KeyId = "f4"
	KeyF5        KeyId = "f5"
	KeyF6        KeyId = "f6"
	KeyF7        KeyId = "f7"
	KeyF8        KeyId = "f8"
	KeyF9        KeyId = "f9"
	KeyF10       KeyId = "f10"
	KeyF11       KeyId = "f11"
	KeyF12       KeyId = "f12"

	KeyBacktick     KeyId = "`"
	KeyHyphen       KeyId = "-"
	KeyEquals       KeyId = "="
	KeyLeftbracket  KeyId = "["
	KeyRightbracket KeyId = "]"
	KeyBackslash    KeyId = "\\"
	KeySemicolon    KeyId = ";"
	KeyQuote        KeyId = "'"
	KeyComma        KeyId = ","
	KeyPeriod       KeyId = "."
	KeySlash        KeyId = "/"
	KeyExclamation  KeyId = "!"
	KeyAt           KeyId = "@"
	KeyHash         KeyId = "#"
	KeyDollar       KeyId = "$"
	KeyPercent      KeyId = "%"
	KeyCaret        KeyId = "^"
	KeyAmpersand    KeyId = "&"
	KeyAsterisk     KeyId = "*"
	KeyLeftparen    KeyId = "("
	KeyRightparen   KeyId = ")"
	KeyUnderscore   KeyId = "_"
	KeyPlus         KeyId = "+"
	KeyPipe         KeyId = "|"
	KeyTilde        KeyId = "~"
	KeyLeftbrace    KeyId = "{"
	KeyRightbrace   KeyId = "}"
	KeyColon        KeyId = ":"
	KeyLessthan     KeyId = "<"
	KeyGreaterthan  KeyId = ">"
	KeyQuestion     KeyId = "?"
)

// KeyCtrl is Key.ctrl: "ctrl+" plus key.
func KeyCtrl(key KeyId) KeyId { return "ctrl+" + key }

// KeyShift is Key.shift: "shift+" plus key.
func KeyShift(key KeyId) KeyId { return "shift+" + key }

// KeyAlt is Key.alt: "alt+" plus key.
func KeyAlt(key KeyId) KeyId { return "alt+" + key }

// KeySuper is Key.super: "super+" plus key.
func KeySuper(key KeyId) KeyId { return "super+" + key }

// KeyCtrlShift is Key.ctrlShift: "ctrl+shift+" plus key.
func KeyCtrlShift(key KeyId) KeyId { return "ctrl+shift+" + key }

// KeyShiftCtrl is Key.shiftCtrl: "shift+ctrl+" plus key.
func KeyShiftCtrl(key KeyId) KeyId { return "shift+ctrl+" + key }

// KeyCtrlAlt is Key.ctrlAlt: "ctrl+alt+" plus key.
func KeyCtrlAlt(key KeyId) KeyId { return "ctrl+alt+" + key }

// KeyAltCtrl is Key.altCtrl: "alt+ctrl+" plus key.
func KeyAltCtrl(key KeyId) KeyId { return "alt+ctrl+" + key }

// KeyShiftAlt is Key.shiftAlt: "shift+alt+" plus key.
func KeyShiftAlt(key KeyId) KeyId { return "shift+alt+" + key }

// KeyAltShift is Key.altShift: "alt+shift+" plus key.
func KeyAltShift(key KeyId) KeyId { return "alt+shift+" + key }

// KeyCtrlSuper is Key.ctrlSuper: "ctrl+super+" plus key.
func KeyCtrlSuper(key KeyId) KeyId { return "ctrl+super+" + key }

// KeySuperCtrl is Key.superCtrl: "super+ctrl+" plus key.
func KeySuperCtrl(key KeyId) KeyId { return "super+ctrl+" + key }

// KeyShiftSuper is Key.shiftSuper: "shift+super+" plus key.
func KeyShiftSuper(key KeyId) KeyId { return "shift+super+" + key }

// KeySuperShift is Key.superShift: "super+shift+" plus key.
func KeySuperShift(key KeyId) KeyId { return "super+shift+" + key }

// KeyAltSuper is Key.altSuper: "alt+super+" plus key.
func KeyAltSuper(key KeyId) KeyId { return "alt+super+" + key }

// KeySuperAlt is Key.superAlt: "super+alt+" plus key.
func KeySuperAlt(key KeyId) KeyId { return "super+alt+" + key }

// KeyCtrlShiftAlt is Key.ctrlShiftAlt: "ctrl+shift+alt+" plus key.
func KeyCtrlShiftAlt(key KeyId) KeyId { return "ctrl+shift+alt+" + key }

// KeyCtrlShiftSuper is Key.ctrlShiftSuper: "ctrl+shift+super+" plus key.
func KeyCtrlShiftSuper(key KeyId) KeyId { return "ctrl+shift+super+" + key }

// KeyEventType is a Kitty keyboard event (protocol flag 2): press, repeat, or release.
type KeyEventType string

const (
	KeyEventTypePress   KeyEventType = "press"
	KeyEventTypeRepeat  KeyEventType = "repeat"
	KeyEventTypeRelease KeyEventType = "release"
)

// IsKeyRelease reports whether data is a Kitty key-release sequence.
// Bracketed paste is never a release. Meaningful when protocol flag 2 is active.
func IsKeyRelease(data string) bool {
	panic("unported: IsKeyRelease")
}

// IsKeyRepeat reports whether data is a Kitty key-repeat sequence.
// Bracketed paste is never a repeat. Meaningful when protocol flag 2 is active.
func IsKeyRepeat(data string) bool {
	panic("unported: IsKeyRepeat")
}

// MatchesKey reports whether raw terminal input data matches keyId.
func MatchesKey(data string, keyId KeyId) bool {
	panic("unported: MatchesKey")
}

// ParseKey parses raw terminal input into a key identifier.
// Nil means the input was not recognized.
func ParseKey(data string) *string {
	panic("unported: ParseKey")
}

// DecodeKittyPrintable decodes a Kitty CSI-u sequence into a printable character.
// Nil means data is not a plain or Shift-modified printable CSI-u sequence.
func DecodeKittyPrintable(data string) *string {
	panic("unported: DecodeKittyPrintable")
}

// DecodePrintableKey decodes a Kitty CSI-u or xterm modifyOtherKeys sequence
// into a printable character. Nil means data is not one of those sequences.
func DecodePrintableKey(data string) *string {
	panic("unported: DecodePrintableKey")
}

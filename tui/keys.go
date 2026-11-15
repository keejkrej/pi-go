// Ported from packages/tui/src/keys.ts (pi v1.5.2).

package tui

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

var keysMu sync.Mutex

// keysKittyProtocolActive is the global Kitty keyboard protocol flag.
// ProcessTerminal sets it after detecting protocol support.
var keysKittyProtocolActive bool

// SetKittyProtocolActive sets the global Kitty keyboard protocol flag.
func SetKittyProtocolActive(active bool) {
	keysMu.Lock()
	keysKittyProtocolActive = active
	keysMu.Unlock()
}

// IsKittyProtocolActive reports whether the Kitty keyboard protocol is active.
func IsKittyProtocolActive() bool {
	keysMu.Lock()
	active := keysKittyProtocolActive
	keysMu.Unlock()
	return active
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

const (
	keysModShift = 1
	keysModAlt   = 2
	keysModCtrl  = 4
	keysModSuper = 8
	keysLockMask = 64 + 128

	keysCpEscape    = 27
	keysCpTab       = 9
	keysCpEnter     = 13
	keysCpSpace     = 32
	keysCpBackspace = 127
	keysCpKpEnter   = 57414

	keysCpUp    = -1
	keysCpDown  = -2
	keysCpRight = -3
	keysCpLeft  = -4

	keysCpDelete   = -10
	keysCpInsert   = -11
	keysCpPageUp   = -12
	keysCpPageDown = -13
	keysCpHome     = -14
	keysCpEnd      = -15
	keysCpF1       = -21
	keysCpF2       = -22
	keysCpF3       = -23
	keysCpF4       = -24
	keysCpF5       = -25
	keysCpF6       = -26
	keysCpF7       = -27
	keysCpF8       = -28
	keysCpF9       = -29
	keysCpF10      = -30
	keysCpF11      = -31
	keysCpF12      = -32

	keysKittyPuaStart = 57344
	keysKittyPuaEnd   = 63743
)

var (
	keysSymbolKeys = map[string]struct{}{
		"`": {}, "-": {}, "=": {}, "[": {}, "]": {}, "\\": {}, ";": {}, "'": {},
		",": {}, ".": {}, "/": {}, "!": {}, "@": {}, "#": {}, "$": {}, "%": {},
		"^": {}, "&": {}, "*": {}, "(": {}, ")": {}, "_": {}, "+": {}, "|": {},
		"~": {}, "{": {}, "}": {}, ":": {}, "<": {}, ">": {}, "?": {},
	}

	keysShiftedSymbolKeys = map[string]struct{}{
		"~": {}, "!": {}, "@": {}, "#": {}, "$": {}, "%": {}, "^": {}, "&": {},
		"*": {}, "(": {}, ")": {}, "_": {}, "+": {}, "|": {}, "{": {}, "}": {},
		":": {}, "<": {}, ">": {}, "?": {},
	}

	keysModifierNames = [4]string{"shift", "alt", "ctrl", "super"}

	keysKittyFunctionalEquivalents = map[int]int{
		57399: 48,
		57400: 49,
		57401: 50,
		57402: 51,
		57403: 52,
		57404: 53,
		57405: 54,
		57406: 55,
		57407: 56,
		57408: 57,
		57409: 46,
		57410: 47,
		57411: 42,
		57412: 45,
		57413: 43,
		57415: 61,
		57416: 44,
		57417: keysCpLeft,
		57418: keysCpRight,
		57419: keysCpUp,
		57420: keysCpDown,
		57421: keysCpPageUp,
		57422: keysCpPageDown,
		57423: keysCpHome,
		57424: keysCpEnd,
		57425: keysCpInsert,
		57426: keysCpDelete,
	}

	keysLegacyKeySequences = map[string][]string{
		"up":       {"\x1b[A", "\x1bOA"},
		"down":     {"\x1b[B", "\x1bOB"},
		"right":    {"\x1b[C", "\x1bOC"},
		"left":     {"\x1b[D", "\x1bOD"},
		"home":     {"\x1b[H", "\x1bOH", "\x1b[1~", "\x1b[7~"},
		"end":      {"\x1b[F", "\x1bOF", "\x1b[4~", "\x1b[8~"},
		"insert":   {"\x1b[2~"},
		"delete":   {"\x1b[3~"},
		"pageUp":   {"\x1b[5~", "\x1b[[5~"},
		"pageDown": {"\x1b[6~", "\x1b[[6~"},
		"clear":    {"\x1b[E", "\x1bOE"},
		"f1":       {"\x1bOP", "\x1b[11~", "\x1b[[A"},
		"f2":       {"\x1bOQ", "\x1b[12~", "\x1b[[B"},
		"f3":       {"\x1bOR", "\x1b[13~", "\x1b[[C"},
		"f4":       {"\x1bOS", "\x1b[14~", "\x1b[[D"},
		"f5":       {"\x1b[15~", "\x1b[[E"},
		"f6":       {"\x1b[17~"},
		"f7":       {"\x1b[18~"},
		"f8":       {"\x1b[19~"},
		"f9":       {"\x1b[20~"},
		"f10":      {"\x1b[21~"},
		"f11":      {"\x1b[23~"},
		"f12":      {"\x1b[24~"},
	}

	keysLegacyShiftSequences = map[string][]string{
		"up":       {"\x1b[a"},
		"down":     {"\x1b[b"},
		"right":    {"\x1b[c"},
		"left":     {"\x1b[d"},
		"clear":    {"\x1b[e"},
		"insert":   {"\x1b[2$"},
		"delete":   {"\x1b[3$"},
		"pageUp":   {"\x1b[5$"},
		"pageDown": {"\x1b[6$"},
		"home":     {"\x1b[7$"},
		"end":      {"\x1b[8$"},
	}

	keysLegacyCtrlSequences = map[string][]string{
		"up":       {"\x1bOa"},
		"down":     {"\x1bOb"},
		"right":    {"\x1bOc"},
		"left":     {"\x1bOd"},
		"clear":    {"\x1bOe"},
		"insert":   {"\x1b[2^"},
		"delete":   {"\x1b[3^"},
		"pageUp":   {"\x1b[5^"},
		"pageDown": {"\x1b[6^"},
		"home":     {"\x1b[7^"},
		"end":      {"\x1b[8^"},
	}

	keysLegacySequenceKeyIds = map[string]KeyId{
		"\x1bOA":   "up",
		"\x1bOB":   "down",
		"\x1bOC":   "right",
		"\x1bOD":   "left",
		"\x1bOH":   "home",
		"\x1bOF":   "end",
		"\x1b[E":   "clear",
		"\x1bOE":   "clear",
		"\x1bOe":   "ctrl+clear",
		"\x1b[e":   "shift+clear",
		"\x1b[2~":  "insert",
		"\x1b[2$":  "shift+insert",
		"\x1b[2^":  "ctrl+insert",
		"\x1b[3$":  "shift+delete",
		"\x1b[3^":  "ctrl+delete",
		"\x1b[[5~": "pageUp",
		"\x1b[[6~": "pageDown",
		"\x1b[a":   "shift+up",
		"\x1b[b":   "shift+down",
		"\x1b[c":   "shift+right",
		"\x1b[d":   "shift+left",
		"\x1bOa":   "ctrl+up",
		"\x1bOb":   "ctrl+down",
		"\x1bOc":   "ctrl+right",
		"\x1bOd":   "ctrl+left",
		"\x1b[5$":  "shift+pageUp",
		"\x1b[6$":  "shift+pageDown",
		"\x1b[7$":  "shift+home",
		"\x1b[8$":  "shift+end",
		"\x1b[5^":  "ctrl+pageUp",
		"\x1b[6^":  "ctrl+pageDown",
		"\x1b[7^":  "ctrl+home",
		"\x1b[8^":  "ctrl+end",
		"\x1bOP":   "f1",
		"\x1bOQ":   "f2",
		"\x1bOR":   "f3",
		"\x1bOS":   "f4",
		"\x1b[11~": "f1",
		"\x1b[12~": "f2",
		"\x1b[13~": "f3",
		"\x1b[14~": "f4",
		"\x1b[[A":  "f1",
		"\x1b[[B":  "f2",
		"\x1b[[C":  "f3",
		"\x1b[[D":  "f4",
		"\x1b[[E":  "f5",
		"\x1b[15~": "f5",
		"\x1b[17~": "f6",
		"\x1b[18~": "f7",
		"\x1b[19~": "f8",
		"\x1b[20~": "f9",
		"\x1b[21~": "f10",
		"\x1b[23~": "f11",
		"\x1b[24~": "f12",
		"\x1bb":    "alt+left",
		"\x1bf":    "alt+right",
		"\x1bp":    "alt+up",
		"\x1bn":    "alt+down",
	}

	keysFunctionalCodepoints = map[string]int{
		"delete":   keysCpDelete,
		"insert":   keysCpInsert,
		"pageup":   keysCpPageUp,
		"pagedown": keysCpPageDown,
		"home":     keysCpHome,
		"end":      keysCpEnd,
		"f1":       keysCpF1,
		"f2":       keysCpF2,
		"f3":       keysCpF3,
		"f4":       keysCpF4,
		"f5":       keysCpF5,
		"f6":       keysCpF6,
		"f7":       keysCpF7,
		"f8":       keysCpF8,
		"f9":       keysCpF9,
		"f10":      keysCpF10,
		"f11":      keysCpF11,
		"f12":      keysCpF12,
	}

	keysCsiURegex            = regexp.MustCompile("^\x1b\\[(\\d+)(?::(\\d*))?(?::(\\d+))?(?:;(\\d+))?(?::(\\d+))?u$")
	keysArrowRegex           = regexp.MustCompile("^\x1b\\[1;(\\d+)(?::(\\d+))?([ABCD])$")
	keysFuncRegex            = regexp.MustCompile("^\x1b\\[(\\d+)(?:;(\\d+))?(?::(\\d+))?~$")
	keysHomeEndRegex         = regexp.MustCompile("^\x1b\\[1;(\\d+)(?::(\\d+))?([HF])$")
	keysFKeyRegex            = regexp.MustCompile("^\x1b\\[(?:1;(\\d+)(?::(\\d+))?)?([PQS])$")
	keysModifyOtherKeysRegex = regexp.MustCompile("^\x1b\\[27;(\\d+);(\\d+)~$")
)

type keysParsedKitty struct {
	codepoint     int
	shiftedKey    *int
	baseLayoutKey *int
	modifier      int
	eventType     KeyEventType
}

type keysParsedModifyOtherKeys struct {
	codepoint int
	modifier  int
}

func keysNormalizeKittyFunctionalCodepoint(codepoint int) int {
	if eq, ok := keysKittyFunctionalEquivalents[codepoint]; ok {
		return eq
	}
	return codepoint
}

func keysNormalizeShiftedLetterIdentityCodepoint(codepoint, modifier int) int {
	effectiveMod := modifier &^ keysLockMask
	if (effectiveMod&keysModShift) != 0 && codepoint >= 'A' && codepoint <= 'Z' {
		return codepoint + 32
	}
	return codepoint
}

func keysGetLogicalShiftedSymbolCandidate(codepoint *int, modifier int, allowedSymbols map[string]struct{}) (cp int, mod int, ok bool) {
	if codepoint == nil || (modifier&keysModShift) == 0 {
		return 0, 0, false
	}
	normalized := keysNormalizeKittyFunctionalCodepoint(*codepoint)
	if normalized < 0 || normalized > 0x10ffff {
		return 0, 0, false
	}
	if _, allowed := allowedSymbols[string(rune(normalized))]; !allowed {
		return 0, 0, false
	}
	return normalized, modifier &^ keysModShift, true
}

func keysMatchesLegacySequence(data string, seqs []string) bool {
	for _, s := range seqs {
		if data == s {
			return true
		}
	}
	return false
}

func keysMatchesLegacyModifierSequence(data, key string, modifier int) bool {
	if modifier == keysModShift {
		return keysMatchesLegacySequence(data, keysLegacyShiftSequences[key])
	}
	if modifier == keysModCtrl {
		return keysMatchesLegacySequence(data, keysLegacyCtrlSequences[key])
	}
	return false
}

// IsKeyRelease reports whether data is a Kitty key-release sequence.
// Bracketed paste is never a release. Meaningful when protocol flag 2 is active.
func IsKeyRelease(data string) bool {
	if strings.Contains(data, "\x1b[200~") {
		return false
	}
	for _, suffix := range []string{":3u", ":3~", ":3A", ":3B", ":3C", ":3D", ":3H", ":3F", ":3P", ":3Q", ":3S"} {
		if strings.Contains(data, suffix) {
			return true
		}
	}
	return false
}

// IsKeyRepeat reports whether data is a Kitty key-repeat sequence.
// Bracketed paste is never a repeat. Meaningful when protocol flag 2 is active.
func IsKeyRepeat(data string) bool {
	if strings.Contains(data, "\x1b[200~") {
		return false
	}
	for _, suffix := range []string{":2u", ":2~", ":2A", ":2B", ":2C", ":2D", ":2H", ":2F", ":2P", ":2Q", ":2S"} {
		if strings.Contains(data, suffix) {
			return true
		}
	}
	return false
}

func keysParseEventType(s string) KeyEventType {
	if s == "" {
		return KeyEventTypePress
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return KeyEventTypePress
	}
	switch n {
	case 2:
		return KeyEventTypeRepeat
	case 3:
		return KeyEventTypeRelease
	default:
		return KeyEventTypePress
	}
}

func keysParseKittySequence(data string) *keysParsedKitty {
	if m := keysCsiURegex.FindStringSubmatch(data); m != nil {
		cp, err := strconv.Atoi(m[1])
		if err != nil {
			return nil
		}
		var shifted *int
		if m[2] != "" {
			if v, err := strconv.Atoi(m[2]); err == nil {
				shifted = &v
			}
		}
		var base *int
		if m[3] != "" {
			if v, err := strconv.Atoi(m[3]); err == nil {
				base = &v
			}
		}
		modValue := 1
		if m[4] != "" {
			if v, err := strconv.Atoi(m[4]); err == nil {
				modValue = v
			}
		}
		return &keysParsedKitty{
			codepoint:     cp,
			shiftedKey:    shifted,
			baseLayoutKey: base,
			modifier:      modValue - 1,
			eventType:     keysParseEventType(m[5]),
		}
	}

	if m := keysArrowRegex.FindStringSubmatch(data); m != nil {
		modValue, err := strconv.Atoi(m[1])
		if err != nil {
			return nil
		}
		var cp int
		switch m[3] {
		case "A":
			cp = keysCpUp
		case "B":
			cp = keysCpDown
		case "C":
			cp = keysCpRight
		case "D":
			cp = keysCpLeft
		}
		return &keysParsedKitty{
			codepoint: cp,
			modifier:  modValue - 1,
			eventType: keysParseEventType(m[2]),
		}
	}

	if m := keysFuncRegex.FindStringSubmatch(data); m != nil {
		keyNum, err := strconv.Atoi(m[1])
		if err != nil {
			return nil
		}
		modValue := 1
		if m[2] != "" {
			if v, err := strconv.Atoi(m[2]); err == nil {
				modValue = v
			}
		}
		var cp int
		var ok bool
		switch keyNum {
		case 2:
			cp, ok = keysCpInsert, true
		case 3:
			cp, ok = keysCpDelete, true
		case 5:
			cp, ok = keysCpPageUp, true
		case 6:
			cp, ok = keysCpPageDown, true
		case 7:
			cp, ok = keysCpHome, true
		case 8:
			cp, ok = keysCpEnd, true
		case 11:
			cp, ok = keysCpF1, true
		case 12:
			cp, ok = keysCpF2, true
		case 13:
			cp, ok = keysCpF3, true
		case 14:
			cp, ok = keysCpF4, true
		case 15:
			cp, ok = keysCpF5, true
		case 17:
			cp, ok = keysCpF6, true
		case 18:
			cp, ok = keysCpF7, true
		case 19:
			cp, ok = keysCpF8, true
		case 20:
			cp, ok = keysCpF9, true
		case 21:
			cp, ok = keysCpF10, true
		case 23:
			cp, ok = keysCpF11, true
		case 24:
			cp, ok = keysCpF12, true
		}
		if ok {
			return &keysParsedKitty{
				codepoint: cp,
				modifier:  modValue - 1,
				eventType: keysParseEventType(m[3]),
			}
		}
	}

	if m := keysHomeEndRegex.FindStringSubmatch(data); m != nil {
		modValue, err := strconv.Atoi(m[1])
		if err != nil {
			return nil
		}
		cp := keysCpEnd
		if m[3] == "H" {
			cp = keysCpHome
		}
		return &keysParsedKitty{
			codepoint: cp,
			modifier:  modValue - 1,
			eventType: keysParseEventType(m[2]),
		}
	}

	if m := keysFKeyRegex.FindStringSubmatch(data); m != nil {
		modValue := 1
		if m[1] != "" {
			if v, err := strconv.Atoi(m[1]); err == nil {
				modValue = v
			}
		}
		var cp int
		switch m[3] {
		case "P":
			cp = keysCpF1
		case "Q":
			cp = keysCpF2
		case "S":
			cp = keysCpF4
		}
		return &keysParsedKitty{
			codepoint: cp,
			modifier:  modValue - 1,
			eventType: keysParseEventType(m[2]),
		}
	}

	return nil
}

func keysMatchesKittySequence(data string, expectedCodepoint, expectedModifier int) bool {
	parsed := keysParseKittySequence(data)
	if parsed == nil {
		return false
	}
	actualMod := parsed.modifier &^ keysLockMask
	expectedMod := expectedModifier &^ keysLockMask

	normalizedCodepoint := keysNormalizeShiftedLetterIdentityCodepoint(
		keysNormalizeKittyFunctionalCodepoint(parsed.codepoint),
		parsed.modifier,
	)
	normalizedExpectedCodepoint := keysNormalizeShiftedLetterIdentityCodepoint(
		keysNormalizeKittyFunctionalCodepoint(expectedCodepoint),
		expectedModifier,
	)

	if actualMod == expectedMod && normalizedCodepoint == normalizedExpectedCodepoint {
		return true
	}

	if cp, mod, ok := keysGetLogicalShiftedSymbolCandidate(parsed.shiftedKey, actualMod, keysSymbolKeys); ok {
		if (mod&^keysLockMask) == expectedMod && cp == keysNormalizeKittyFunctionalCodepoint(expectedCodepoint) {
			return true
		}
	}

	if actualMod != expectedMod {
		return false
	}

	if parsed.baseLayoutKey != nil && *parsed.baseLayoutKey == expectedCodepoint {
		cp := normalizedCodepoint
		isLatinLetter := cp >= 'a' && cp <= 'z'
		isKnownSymbol := false
		if cp >= 0 && cp <= 0x10ffff {
			_, isKnownSymbol = keysSymbolKeys[string(rune(cp))]
		}
		if !isLatinLetter && !isKnownSymbol {
			return true
		}
	}

	return false
}

func keysParseModifyOtherKeysSequence(data string) *keysParsedModifyOtherKeys {
	m := keysModifyOtherKeysRegex.FindStringSubmatch(data)
	if m == nil {
		return nil
	}
	modValue, err := strconv.Atoi(m[1])
	if err != nil {
		return nil
	}
	codepoint, err := strconv.Atoi(m[2])
	if err != nil {
		return nil
	}
	return &keysParsedModifyOtherKeys{codepoint: codepoint, modifier: modValue - 1}
}

func keysMatchesModifyOtherKeys(data string, expectedKeycode, expectedModifier int) bool {
	parsed := keysParseModifyOtherKeysSequence(data)
	if parsed == nil {
		return false
	}
	return parsed.codepoint == expectedKeycode && parsed.modifier == expectedModifier
}

func keysIsWindowsTerminalSession() bool {
	return os.Getenv("WT_SESSION") != "" &&
		os.Getenv("SSH_CONNECTION") == "" &&
		os.Getenv("SSH_CLIENT") == "" &&
		os.Getenv("SSH_TTY") == ""
}

func keysMatchesRawBackspace(data string, expectedModifier int) bool {
	if data == "\x7f" {
		return expectedModifier == 0
	}
	if data != "\x08" {
		return false
	}
	if keysIsWindowsTerminalSession() {
		return expectedModifier == keysModCtrl
	}
	return expectedModifier == 0
}

func keysRawCtrlChar(key string) (string, bool) {
	if len(key) == 0 {
		return "", false
	}
	ch := strings.ToLower(key[:1])
	code := ch[0]
	if (code >= 'a' && code <= 'z') || ch == "[" || ch == "\\" || ch == "]" || ch == "_" {
		return string(rune(code & 0x1f)), true
	}
	if ch == "-" {
		return string(rune(31)), true
	}
	return "", false
}

func keysIsDigitKey(key string) bool {
	return len(key) == 1 && key[0] >= '0' && key[0] <= '9'
}

func keysMatchesPrintableModifyOtherKeys(data string, expectedKeycode, expectedModifier int) bool {
	parsed := keysParseModifyOtherKeysSequence(data)
	if parsed == nil {
		return false
	}
	physicalMatch := parsed.modifier == expectedModifier &&
		keysNormalizeShiftedLetterIdentityCodepoint(parsed.codepoint, parsed.modifier) ==
			keysNormalizeShiftedLetterIdentityCodepoint(expectedKeycode, expectedModifier)
	if physicalMatch {
		return true
	}
	cp := parsed.codepoint
	if symCp, symMod, ok := keysGetLogicalShiftedSymbolCandidate(&cp, parsed.modifier, keysShiftedSymbolKeys); ok {
		return symMod == expectedModifier && symCp == expectedKeycode
	}
	return false
}

func keysFormatKeyNameWithModifiers(keyName string, modifier int) *string {
	effectiveMod := modifier &^ keysLockMask
	supportedMask := keysModShift | keysModCtrl | keysModAlt | keysModSuper
	if (effectiveMod &^ supportedMask) != 0 {
		return nil
	}
	var mods []string
	if (effectiveMod & keysModShift) != 0 {
		mods = append(mods, "shift")
	}
	if (effectiveMod & keysModCtrl) != 0 {
		mods = append(mods, "ctrl")
	}
	if (effectiveMod & keysModAlt) != 0 {
		mods = append(mods, "alt")
	}
	if (effectiveMod & keysModSuper) != 0 {
		mods = append(mods, "super")
	}
	var res string
	if len(mods) > 0 {
		res = strings.Join(mods, "+") + "+" + keyName
	} else {
		res = keyName
	}
	return &res
}

type keysParsedKeyId struct {
	key      string
	ctrl     bool
	shift    bool
	alt      bool
	superMod bool
}

func keysParseKeyId(keyId string) *keysParsedKeyId {
	key := strings.ToLower(keyId)
	seen := make(map[string]bool, 4)
	for {
		matched := ""
		for _, name := range keysModifierNames {
			if strings.HasPrefix(key, name+"+") {
				matched = name
				break
			}
		}
		if matched == "" {
			break
		}
		if seen[matched] {
			return nil
		}
		seen[matched] = true
		key = key[len(matched)+1:]
	}
	if key == "" || (strings.Contains(key, "+") && key != "+") {
		return nil
	}
	return &keysParsedKeyId{
		key:      key,
		ctrl:     seen["ctrl"],
		shift:    seen["shift"],
		alt:      seen["alt"],
		superMod: seen["super"],
	}
}

// MatchesKey reports whether raw terminal input data matches keyId.
func MatchesKey(data string, keyId KeyId) bool {
	parsed := keysParseKeyId(string(keyId))
	if parsed == nil {
		return false
	}

	modifier := 0
	if parsed.shift {
		modifier |= keysModShift
	}
	if parsed.alt {
		modifier |= keysModAlt
	}
	if parsed.ctrl {
		modifier |= keysModCtrl
	}
	if parsed.superMod {
		modifier |= keysModSuper
	}

	kittyActive := IsKittyProtocolActive()

	switch parsed.key {
	case "escape", "esc":
		if modifier != 0 {
			return false
		}
		return data == "\x1b" ||
			keysMatchesKittySequence(data, keysCpEscape, 0) ||
			keysMatchesModifyOtherKeys(data, keysCpEscape, 0)

	case "space":
		if !kittyActive {
			if modifier == keysModCtrl && data == "\x00" {
				return true
			}
			if modifier == keysModAlt && data == "\x1b " {
				return true
			}
		}
		if modifier == 0 {
			return data == " " ||
				keysMatchesKittySequence(data, keysCpSpace, 0) ||
				keysMatchesModifyOtherKeys(data, keysCpSpace, 0)
		}
		return keysMatchesKittySequence(data, keysCpSpace, modifier) ||
			keysMatchesModifyOtherKeys(data, keysCpSpace, modifier)

	case "tab":
		if modifier == keysModShift {
			return data == "\x1b[Z" ||
				keysMatchesKittySequence(data, keysCpTab, keysModShift) ||
				keysMatchesModifyOtherKeys(data, keysCpTab, keysModShift)
		}
		if modifier == 0 {
			return data == "\t" || keysMatchesKittySequence(data, keysCpTab, 0)
		}
		return keysMatchesKittySequence(data, keysCpTab, modifier) ||
			keysMatchesModifyOtherKeys(data, keysCpTab, modifier)

	case "enter", "return":
		if modifier == keysModShift {
			if keysMatchesKittySequence(data, keysCpEnter, keysModShift) ||
				keysMatchesKittySequence(data, keysCpKpEnter, keysModShift) {
				return true
			}
			if keysMatchesModifyOtherKeys(data, keysCpEnter, keysModShift) {
				return true
			}
			if kittyActive {
				return data == "\x1b\r" || data == "\n"
			}
			return false
		}
		if modifier == keysModAlt {
			if keysMatchesKittySequence(data, keysCpEnter, keysModAlt) ||
				keysMatchesKittySequence(data, keysCpKpEnter, keysModAlt) {
				return true
			}
			if keysMatchesModifyOtherKeys(data, keysCpEnter, keysModAlt) {
				return true
			}
			if !kittyActive {
				return data == "\x1b\r"
			}
			return false
		}
		if modifier == 0 {
			return data == "\r" ||
				(!kittyActive && data == "\n") ||
				data == "\x1bOM" ||
				keysMatchesKittySequence(data, keysCpEnter, 0) ||
				keysMatchesKittySequence(data, keysCpKpEnter, 0)
		}
		return keysMatchesKittySequence(data, keysCpEnter, modifier) ||
			keysMatchesKittySequence(data, keysCpKpEnter, modifier) ||
			keysMatchesModifyOtherKeys(data, keysCpEnter, modifier)

	case "backspace":
		if modifier == keysModAlt {
			if data == "\x1b\x7f" || data == "\x1b\b" {
				return true
			}
			return keysMatchesKittySequence(data, keysCpBackspace, keysModAlt) ||
				keysMatchesModifyOtherKeys(data, keysCpBackspace, keysModAlt)
		}
		if modifier == keysModCtrl {
			if keysMatchesRawBackspace(data, keysModCtrl) {
				return true
			}
			return keysMatchesKittySequence(data, keysCpBackspace, keysModCtrl) ||
				keysMatchesModifyOtherKeys(data, keysCpBackspace, keysModCtrl)
		}
		if modifier == 0 {
			return keysMatchesRawBackspace(data, 0) ||
				keysMatchesKittySequence(data, keysCpBackspace, 0) ||
				keysMatchesModifyOtherKeys(data, keysCpBackspace, 0)
		}
		return keysMatchesKittySequence(data, keysCpBackspace, modifier) ||
			keysMatchesModifyOtherKeys(data, keysCpBackspace, modifier)

	case "insert":
		if modifier == 0 {
			return keysMatchesLegacySequence(data, keysLegacyKeySequences["insert"]) ||
				keysMatchesKittySequence(data, keysCpInsert, 0)
		}
		if keysMatchesLegacyModifierSequence(data, "insert", modifier) {
			return true
		}
		return keysMatchesKittySequence(data, keysCpInsert, modifier)

	case "delete":
		if modifier == 0 {
			return keysMatchesLegacySequence(data, keysLegacyKeySequences["delete"]) ||
				keysMatchesKittySequence(data, keysCpDelete, 0)
		}
		if keysMatchesLegacyModifierSequence(data, "delete", modifier) {
			return true
		}
		return keysMatchesKittySequence(data, keysCpDelete, modifier)

	case "clear":
		if modifier == 0 {
			return keysMatchesLegacySequence(data, keysLegacyKeySequences["clear"])
		}
		return keysMatchesLegacyModifierSequence(data, "clear", modifier)

	case "home":
		if modifier == 0 {
			return keysMatchesLegacySequence(data, keysLegacyKeySequences["home"]) ||
				keysMatchesKittySequence(data, keysCpHome, 0)
		}
		if keysMatchesLegacyModifierSequence(data, "home", modifier) {
			return true
		}
		return keysMatchesKittySequence(data, keysCpHome, modifier)

	case "end":
		if modifier == 0 {
			return keysMatchesLegacySequence(data, keysLegacyKeySequences["end"]) ||
				keysMatchesKittySequence(data, keysCpEnd, 0)
		}
		if keysMatchesLegacyModifierSequence(data, "end", modifier) {
			return true
		}
		return keysMatchesKittySequence(data, keysCpEnd, modifier)

	case "pageup":
		if modifier == 0 {
			return keysMatchesLegacySequence(data, keysLegacyKeySequences["pageUp"]) ||
				keysMatchesKittySequence(data, keysCpPageUp, 0)
		}
		if keysMatchesLegacyModifierSequence(data, "pageUp", modifier) {
			return true
		}
		return keysMatchesKittySequence(data, keysCpPageUp, modifier)

	case "pagedown":
		if modifier == 0 {
			return keysMatchesLegacySequence(data, keysLegacyKeySequences["pageDown"]) ||
				keysMatchesKittySequence(data, keysCpPageDown, 0)
		}
		if keysMatchesLegacyModifierSequence(data, "pageDown", modifier) {
			return true
		}
		return keysMatchesKittySequence(data, keysCpPageDown, modifier)

	case "up":
		if modifier == keysModAlt {
			return data == "\x1bp" || keysMatchesKittySequence(data, keysCpUp, keysModAlt)
		}
		if modifier == 0 {
			return keysMatchesLegacySequence(data, keysLegacyKeySequences["up"]) ||
				keysMatchesKittySequence(data, keysCpUp, 0)
		}
		if keysMatchesLegacyModifierSequence(data, "up", modifier) {
			return true
		}
		return keysMatchesKittySequence(data, keysCpUp, modifier)

	case "down":
		if modifier == keysModAlt {
			return data == "\x1bn" || keysMatchesKittySequence(data, keysCpDown, keysModAlt)
		}
		if modifier == 0 {
			return keysMatchesLegacySequence(data, keysLegacyKeySequences["down"]) ||
				keysMatchesKittySequence(data, keysCpDown, 0)
		}
		if keysMatchesLegacyModifierSequence(data, "down", modifier) {
			return true
		}
		return keysMatchesKittySequence(data, keysCpDown, modifier)

	case "left":
		if modifier == keysModAlt {
			return data == "\x1b[1;3D" ||
				(!kittyActive && data == "\x1bB") ||
				data == "\x1bb" ||
				keysMatchesKittySequence(data, keysCpLeft, keysModAlt)
		}
		if modifier == keysModCtrl {
			return data == "\x1b[1;5D" ||
				keysMatchesLegacyModifierSequence(data, "left", keysModCtrl) ||
				keysMatchesKittySequence(data, keysCpLeft, keysModCtrl)
		}
		if modifier == 0 {
			return keysMatchesLegacySequence(data, keysLegacyKeySequences["left"]) ||
				keysMatchesKittySequence(data, keysCpLeft, 0)
		}
		if keysMatchesLegacyModifierSequence(data, "left", modifier) {
			return true
		}
		return keysMatchesKittySequence(data, keysCpLeft, modifier)

	case "right":
		if modifier == keysModAlt {
			return data == "\x1b[1;3C" ||
				(!kittyActive && data == "\x1bF") ||
				data == "\x1bf" ||
				keysMatchesKittySequence(data, keysCpRight, keysModAlt)
		}
		if modifier == keysModCtrl {
			return data == "\x1b[1;5C" ||
				keysMatchesLegacyModifierSequence(data, "right", keysModCtrl) ||
				keysMatchesKittySequence(data, keysCpRight, keysModCtrl)
		}
		if modifier == 0 {
			return keysMatchesLegacySequence(data, keysLegacyKeySequences["right"]) ||
				keysMatchesKittySequence(data, keysCpRight, 0)
		}
		if keysMatchesLegacyModifierSequence(data, "right", modifier) {
			return true
		}
		return keysMatchesKittySequence(data, keysCpRight, modifier)

	case "f1", "f2", "f3", "f4", "f5", "f6", "f7", "f8", "f9", "f10", "f11", "f12":
		if modifier == 0 && keysMatchesLegacySequence(data, keysLegacyKeySequences[parsed.key]) {
			return true
		}
		return keysMatchesKittySequence(data, keysFunctionalCodepoints[parsed.key], modifier)
	}

	if len(parsed.key) == 1 {
		b := parsed.key[0]
		isLetter := b >= 'a' && b <= 'z'
		isDigit := keysIsDigitKey(parsed.key)
		_, isSymbol := keysSymbolKeys[parsed.key]
		if isLetter || isDigit || isSymbol {
			codepoint := int(b)
			rawCtrl, hasRawCtrl := keysRawCtrlChar(parsed.key)

			if modifier == keysModCtrl+keysModAlt && !kittyActive && hasRawCtrl {
				if data == "\x1b"+rawCtrl {
					return true
				}
			}

			if modifier == keysModAlt && !kittyActive && (isLetter || isDigit || isSymbol) {
				if data == "\x1b"+parsed.key {
					return true
				}
			}

			if modifier == keysModCtrl {
				if hasRawCtrl && data == rawCtrl {
					return true
				}
				return keysMatchesKittySequence(data, codepoint, keysModCtrl) ||
					keysMatchesPrintableModifyOtherKeys(data, codepoint, keysModCtrl)
			}

			if modifier == keysModShift+keysModCtrl {
				return keysMatchesKittySequence(data, codepoint, keysModShift+keysModCtrl) ||
					keysMatchesPrintableModifyOtherKeys(data, codepoint, keysModShift+keysModCtrl)
			}

			if modifier == keysModShift {
				if isLetter && data == strings.ToUpper(parsed.key) {
					return true
				}
				return keysMatchesKittySequence(data, codepoint, keysModShift) ||
					keysMatchesPrintableModifyOtherKeys(data, codepoint, keysModShift)
			}

			if modifier != 0 {
				return keysMatchesKittySequence(data, codepoint, modifier) ||
					keysMatchesPrintableModifyOtherKeys(data, codepoint, modifier)
			}

			return data == parsed.key ||
				keysMatchesKittySequence(data, codepoint, 0) ||
				keysMatchesPrintableModifyOtherKeys(data, codepoint, 0)
		}
	}

	return false
}

func keysFormatParsedKey(codepoint, modifier int, baseLayoutKey *int) *string {
	normalizedCodepoint := keysNormalizeKittyFunctionalCodepoint(codepoint)
	identityCodepoint := keysNormalizeShiftedLetterIdentityCodepoint(normalizedCodepoint, modifier)

	isLatinLetter := identityCodepoint >= 'a' && identityCodepoint <= 'z'
	isDigit := identityCodepoint >= '0' && identityCodepoint <= '9'
	isKnownSymbol := false
	if identityCodepoint >= 0 && identityCodepoint <= 0x10ffff {
		_, isKnownSymbol = keysSymbolKeys[string(rune(identityCodepoint))]
	}
	effectiveCodepoint := identityCodepoint
	if !isLatinLetter && !isDigit && !isKnownSymbol && baseLayoutKey != nil {
		effectiveCodepoint = *baseLayoutKey
	}

	var keyName string
	switch {
	case effectiveCodepoint == keysCpEscape:
		keyName = "escape"
	case effectiveCodepoint == keysCpTab:
		keyName = "tab"
	case effectiveCodepoint == keysCpEnter || effectiveCodepoint == keysCpKpEnter:
		keyName = "enter"
	case effectiveCodepoint == keysCpSpace:
		keyName = "space"
	case effectiveCodepoint == keysCpBackspace:
		keyName = "backspace"
	case effectiveCodepoint == keysCpDelete:
		keyName = "delete"
	case effectiveCodepoint == keysCpInsert:
		keyName = "insert"
	case effectiveCodepoint == keysCpHome:
		keyName = "home"
	case effectiveCodepoint == keysCpEnd:
		keyName = "end"
	case effectiveCodepoint == keysCpPageUp:
		keyName = "pageUp"
	case effectiveCodepoint == keysCpPageDown:
		keyName = "pageDown"
	case effectiveCodepoint <= keysCpF1 && effectiveCodepoint >= keysCpF12:
		keyName = fmt.Sprintf("f%d", keysCpF1-effectiveCodepoint+1)
	case effectiveCodepoint == keysCpUp:
		keyName = "up"
	case effectiveCodepoint == keysCpDown:
		keyName = "down"
	case effectiveCodepoint == keysCpLeft:
		keyName = "left"
	case effectiveCodepoint == keysCpRight:
		keyName = "right"
	case effectiveCodepoint >= '0' && effectiveCodepoint <= '9':
		keyName = string(rune(effectiveCodepoint))
	case effectiveCodepoint >= 'a' && effectiveCodepoint <= 'z':
		keyName = string(rune(effectiveCodepoint))
	case effectiveCodepoint >= 0 && effectiveCodepoint <= 0x10ffff:
		ch := string(rune(effectiveCodepoint))
		if _, ok := keysSymbolKeys[ch]; ok {
			keyName = ch
		}
	}

	if keyName == "" {
		return nil
	}
	return keysFormatKeyNameWithModifiers(keyName, modifier)
}

// ParseKey parses raw terminal input into a key identifier.
// Nil means the input was not recognized.
func ParseKey(data string) *string {
	if kitty := keysParseKittySequence(data); kitty != nil {
		cp, mod, ok := keysGetLogicalShiftedSymbolCandidate(kitty.shiftedKey, kitty.modifier, keysSymbolKeys)
		if !ok {
			cp = kitty.codepoint
			mod = kitty.modifier
		}
		return keysFormatParsedKey(cp, mod, kitty.baseLayoutKey)
	}

	if mok := keysParseModifyOtherKeysSequence(data); mok != nil {
		codepoint := mok.codepoint
		cp, mod, ok := keysGetLogicalShiftedSymbolCandidate(&codepoint, mok.modifier, keysShiftedSymbolKeys)
		if !ok {
			cp = mok.codepoint
			mod = mok.modifier
		}
		return keysFormatParsedKey(cp, mod, nil)
	}

	kittyActive := IsKittyProtocolActive()

	if kittyActive && (data == "\x1b\r" || data == "\n") {
		s := "shift+enter"
		return &s
	}

	if id, ok := keysLegacySequenceKeyIds[data]; ok {
		s := string(id)
		return &s
	}

	switch data {
	case "\x1b":
		s := "escape"
		return &s
	case "\x1c":
		s := "ctrl+\\"
		return &s
	case "\x1d":
		s := "ctrl+]"
		return &s
	case "\x1f":
		s := "ctrl+-"
		return &s
	case "\x1b\x1b":
		s := "ctrl+alt+["
		return &s
	case "\x1b\x1c":
		s := "ctrl+alt+\\"
		return &s
	case "\x1b\x1d":
		s := "ctrl+alt+]"
		return &s
	case "\x1b\x1f":
		s := "ctrl+alt+-"
		return &s
	case "\t":
		s := "tab"
		return &s
	case "\r", "\x1bOM":
		s := "enter"
		return &s
	case "\n":
		if !kittyActive {
			s := "enter"
			return &s
		}
	case "\x00":
		s := "ctrl+space"
		return &s
	case " ":
		s := "space"
		return &s
	case "\x7f":
		s := "backspace"
		return &s
	case "\x08":
		if keysIsWindowsTerminalSession() {
			s := "ctrl+backspace"
			return &s
		}
		s := "backspace"
		return &s
	case "\x1b[Z":
		s := "shift+tab"
		return &s
	case "\x1b\x7f", "\x1b\b":
		s := "alt+backspace"
		return &s
	}

	if !kittyActive {
		switch data {
		case "\x1b\r":
			s := "alt+enter"
			return &s
		case "\x1b ":
			s := "alt+space"
			return &s
		case "\x1bB":
			s := "alt+left"
			return &s
		case "\x1bF":
			s := "alt+right"
			return &s
		}
		if len(data) == 2 && data[0] == '\x1b' {
			code := data[1]
			if code >= 1 && code <= 26 {
				s := "ctrl+alt+" + string(rune(code+96))
				return &s
			}
			key := string(code)
			_, isSym := keysSymbolKeys[key]
			if (code >= 'a' && code <= 'z') || (code >= '0' && code <= '9') || isSym {
				s := "alt+" + key
				return &s
			}
		}
	}

	switch data {
	case "\x1b[A":
		s := "up"
		return &s
	case "\x1b[B":
		s := "down"
		return &s
	case "\x1b[C":
		s := "right"
		return &s
	case "\x1b[D":
		s := "left"
		return &s
	case "\x1b[H", "\x1bOH":
		s := "home"
		return &s
	case "\x1b[F", "\x1bOF":
		s := "end"
		return &s
	case "\x1b[3~":
		s := "delete"
		return &s
	case "\x1b[5~":
		s := "pageUp"
		return &s
	case "\x1b[6~":
		s := "pageDown"
		return &s
	}

	if len(data) == 1 {
		code := data[0]
		if code >= 1 && code <= 26 {
			s := "ctrl+" + string(rune(code+96))
			return &s
		}
		if code >= 32 && code <= 126 {
			s := data
			return &s
		}
	}

	return nil
}

// DecodeKittyPrintable decodes a Kitty CSI-u sequence into a printable character.
// Nil means data is not a plain or Shift-modified printable CSI-u sequence.
func DecodeKittyPrintable(data string) *string {
	m := keysCsiURegex.FindStringSubmatch(data)
	if m == nil {
		return nil
	}
	codepoint, err := strconv.Atoi(m[1])
	if err != nil {
		return nil
	}

	var shiftedKey *int
	if m[2] != "" {
		if v, err := strconv.Atoi(m[2]); err == nil {
			shiftedKey = &v
		}
	}

	modValue := 1
	if m[4] != "" {
		if v, err := strconv.Atoi(m[4]); err == nil {
			modValue = v
		}
	}
	modifier := modValue - 1

	const allowedModifiers = keysModShift | keysLockMask
	if (modifier &^ allowedModifiers) != 0 {
		return nil
	}
	if (modifier & (keysModAlt | keysModCtrl)) != 0 {
		return nil
	}

	effectiveCodepoint := codepoint
	if (modifier&keysModShift) != 0 && shiftedKey != nil {
		effectiveCodepoint = *shiftedKey
	}
	effectiveCodepoint = keysNormalizeKittyFunctionalCodepoint(effectiveCodepoint)
	if effectiveCodepoint < 32 || effectiveCodepoint > 0x10ffff {
		return nil
	}
	if effectiveCodepoint >= keysKittyPuaStart && effectiveCodepoint <= keysKittyPuaEnd {
		return nil
	}

	s := string(rune(effectiveCodepoint))
	return &s
}

func keysDecodeModifyOtherKeysPrintable(data string) *string {
	parsed := keysParseModifyOtherKeysSequence(data)
	if parsed == nil {
		return nil
	}
	modifier := parsed.modifier &^ keysLockMask
	if (modifier &^ keysModShift) != 0 {
		return nil
	}
	if parsed.codepoint < 32 || parsed.codepoint > 0x10ffff {
		return nil
	}
	s := string(rune(parsed.codepoint))
	return &s
}

// DecodePrintableKey decodes a Kitty CSI-u or xterm modifyOtherKeys sequence
// into a printable character. Nil means data is not one of those sequences.
func DecodePrintableKey(data string) *string {
	if s := DecodeKittyPrintable(data); s != nil {
		return s
	}
	return keysDecodeModifyOtherKeysPrintable(data)
}

// Ported from packages/coding-agent/src/core/key-id-schema.ts (pi v1.5.2).

package core

import (
	"regexp"
	"strings"

	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/typebox"
	"github.com/keejkrej/pi-go/tui"
)

var (
	kisModifiers = []string{"ctrl", "shift", "alt", "super"}

	kisKeyValues = []tui.KeyId{
		tui.KeyEscape, tui.KeyEsc, tui.KeyEnter, tui.KeyReturn, tui.KeyTab, tui.KeySpace,
		tui.KeyBackspace, tui.KeyDelete, tui.KeyInsert, tui.KeyClear, tui.KeyHome, tui.KeyEnd,
		tui.KeyPageUp, tui.KeyPageDown, tui.KeyUp, tui.KeyDown, tui.KeyLeft, tui.KeyRight,
		tui.KeyF1, tui.KeyF2, tui.KeyF3, tui.KeyF4, tui.KeyF5, tui.KeyF6,
		tui.KeyF7, tui.KeyF8, tui.KeyF9, tui.KeyF10, tui.KeyF11, tui.KeyF12,
		tui.KeyBacktick, tui.KeyHyphen, tui.KeyEquals, tui.KeyLeftbracket, tui.KeyRightbracket,
		tui.KeyBackslash, tui.KeySemicolon, tui.KeyQuote, tui.KeyComma, tui.KeyPeriod,
		tui.KeySlash, tui.KeyExclamation, tui.KeyAt, tui.KeyHash, tui.KeyDollar,
		tui.KeyPercent, tui.KeyCaret, tui.KeyAmpersand, tui.KeyAsterisk, tui.KeyLeftparen,
		tui.KeyRightparen, tui.KeyUnderscore, tui.KeyPlus, tui.KeyPipe, tui.KeyTilde,
		tui.KeyLeftbrace, tui.KeyRightbrace, tui.KeyColon, tui.KeyLessthan, tui.KeyGreaterthan,
		tui.KeyQuestion,
	}

	kisEscapePatternRE = regexp.MustCompile(`[\\^$.*+?()[\]{}|]`)

	kisKeyIdPattern = func() string {
		escaped := make([]string, len(kisKeyValues))
		for i, k := range kisKeyValues {
			escaped[i] = kisEscapePatternRE.ReplaceAllString(string(k), `\$0`)
		}
		baseKeyPattern := `(?:[a-z0-9]|` + strings.Join(escaped, "|") + `)`
		dupParts := make([]string, len(kisModifiers))
		for i, mod := range kisModifiers {
			dupParts[i] = mod + `\+.*` + mod + `\+`
		}
		dupPattern := strings.Join(dupParts, "|")
		return `^(?!.*(?:` + dupPattern + `))(?:(?:` + strings.Join(kisModifiers, "|") + `)\+){0,4}` + baseKeyPattern + `$`
	}()

	// KeyIdSchema validates KeyId strings with optional non-duplicate ctrl/shift/alt/super modifiers.
	KeyIdSchema = typebox.Unsafe(
		typebox.String(jsonx.ObjectOf(
			"pattern", kisKeyIdPattern,
			"description", "Key identifier with optional ctrl, shift, alt, or super modifiers.",
		)).Object,
	)

	// KeybindingValueSchema validates a single KeyId or array of KeyId values.
	KeybindingValueSchema = NewKeybindingValueSchema()
)

// NewKeybindingValueSchema builds a TypeBox union schema for KeyId | KeyId[] with optional annotations.
func NewKeybindingValueSchema(opts ...*jsonx.Object) *typebox.Schema {
	return typebox.Union([]*typebox.Schema{
		KeyIdSchema,
		typebox.Array(KeyIdSchema),
	}, opts...)
}

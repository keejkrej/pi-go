// Ported from packages/ai/src/utils/json-parse.ts (pi v1.5.2).

package ai

import (
	"fmt"
	"strings"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/partialjson"
)

func ujpIsValidJsonEscape(b byte) bool {
	switch b {
	case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
		return true
	default:
		return false
	}
}

func ujpIsHexDigit(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}

func ujpIsControlChar(b byte) bool {
	return b <= 0x1f
}

func ujpEscapeControlChar(b byte) string {
	switch b {
	case '\b':
		return `\b`
	case '\f':
		return `\f`
	case '\n':
		return `\n`
	case '\r':
		return `\r`
	case '\t':
		return `\t`
	default:
		return fmt.Sprintf(`\u%04x`, b)
	}
}

// RepairJson repairs malformed JSON string literals by escaping raw control
// characters inside strings and doubling backslashes before invalid escapes.
func RepairJson(jsonStr string) string {
	var b strings.Builder
	b.Grow(len(jsonStr))
	inString := false

	for i := 0; i < len(jsonStr); i++ {
		ch := jsonStr[i]

		if !inString {
			b.WriteByte(ch)
			if ch == '"' {
				inString = true
			}
			continue
		}

		if ch == '"' {
			b.WriteByte(ch)
			inString = false
			continue
		}

		if ch == '\\' {
			if i+1 >= len(jsonStr) {
				b.WriteString(`\\`)
				continue
			}
			next := jsonStr[i+1]
			if next == 'u' && i+5 < len(jsonStr) &&
				ujpIsHexDigit(jsonStr[i+2]) && ujpIsHexDigit(jsonStr[i+3]) &&
				ujpIsHexDigit(jsonStr[i+4]) && ujpIsHexDigit(jsonStr[i+5]) {
				b.WriteString(jsonStr[i : i+6])
				i += 5
				continue
			}
			if ujpIsValidJsonEscape(next) {
				b.WriteByte('\\')
				b.WriteByte(next)
				i++
				continue
			}
			b.WriteString(`\\`)
			continue
		}

		if ujpIsControlChar(ch) {
			b.WriteString(ujpEscapeControlChar(ch))
		} else {
			b.WriteByte(ch)
		}
	}

	return b.String()
}

// ParseJsonWithRepair parses JSON, falling back once to RepairJson on syntax failure.
func ParseJsonWithRepair(jsonStr string) (any, error) {
	val, err := jsonx.ParseString(jsonStr)
	if err == nil {
		return val, nil
	}
	repaired := RepairJson(jsonStr)
	if repaired != jsonStr {
		return jsonx.ParseString(repaired)
	}
	return nil, err
}

// ParseToolCallArguments parses the complete arguments text of a finished tool call.
// Empty/whitespace-only text returns empty arguments with nil argumentsError.
// Invalid JSON or non-object JSON values return empty arguments and a descriptive argumentsError.
func ParseToolCallArguments(jsonStr string) (*jsonx.Object, *string) {
	if js.Trim(jsonStr) == "" {
		return jsonx.NewObject(), nil
	}
	parsed, err := ParseJsonWithRepair(jsonStr)
	if err != nil {
		msg := fmt.Sprintf("%s\n\nReceived arguments:\n%s", jsonx.ParseErrorMessage(err, jsonStr), jsonStr)
		return jsonx.NewObject(), &msg
	}
	obj, ok := parsed.(*jsonx.Object)
	if !ok || obj == nil {
		msg := fmt.Sprintf("Tool arguments must be a JSON object\n\nReceived arguments:\n%s", jsonStr)
		return jsonx.NewObject(), &msg
	}
	return obj, nil
}

// ParseStreamingJson attempts to parse potentially incomplete JSON during streaming.
// Always returns a non-nil *jsonx.Object, falling back to an empty object on failure.
func ParseStreamingJson(partialJson string) *jsonx.Object {
	if js.Trim(partialJson) == "" {
		return jsonx.NewObject()
	}
	if parsed, err := ParseJsonWithRepair(partialJson); err == nil {
		if obj, ok := parsed.(*jsonx.Object); ok && obj != nil {
			return obj
		}
		return jsonx.NewObject()
	}
	if parsed, err := partialjson.Parse(partialJson, partialjson.ALL); err == nil {
		if obj, ok := parsed.(*jsonx.Object); ok && obj != nil {
			return obj
		}
		return jsonx.NewObject()
	}
	if parsed, err := partialjson.Parse(RepairJson(partialJson), partialjson.ALL); err == nil {
		if obj, ok := parsed.(*jsonx.Object); ok && obj != nil {
			return obj
		}
		return jsonx.NewObject()
	}
	return jsonx.NewObject()
}

// Ported from packages/coding-agent/src/utils/html.ts (pi v1.5.2).

package utils

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// DecodedHtmlEntity holds a decoded character sequence and the number of input bytes consumed.
type DecodedHtmlEntity struct {
	Char   string
	Length int
}

var htmlNamedEntities = map[string]string{
	"amp":  "&",
	"lt":   "<",
	"gt":   ">",
	"quot": `"`,
	"apos": "'",
	"#39":  "'",
	"nbsp": "\u00a0",
}

// DecodeHtmlEntityAt attempts to decode a single HTML entity starting at byte index in input.
// Returns nil if input[index] is not '&' or does not form a recognized HTML entity.
func DecodeHtmlEntityAt(input string, index int) *DecodedHtmlEntity {
	if index < 0 || index >= len(input) || input[index] != '&' {
		return nil
	}
	rest := input[index+1:]
	semi := strings.IndexByte(rest, ';')
	if semi <= 0 || semi > 16 {
		return nil
	}
	entity := rest[:semi]
	totalLen := semi + 2

	if ch, ok := htmlNamedEntities[entity]; ok {
		return &DecodedHtmlEntity{Char: ch, Length: totalLen}
	}

	if entity[0] == '#' {
		numStr := entity[1:]
		base := 10
		if len(numStr) > 1 && (numStr[0] == 'x' || numStr[0] == 'X') {
			base = 16
			numStr = numStr[1:]
		}
		if numStr == "" {
			return nil
		}
		val, err := strconv.ParseInt(numStr, base, 64)
		if err != nil {
			return nil
		}
		r := rune(val)
		if val <= 0 || val > 0x10ffff || (val >= 0xd800 && val <= 0xdfff) {
			r = utf8.RuneError
		}
		return &DecodedHtmlEntity{Char: string(r), Length: totalLen}
	}

	return nil
}

// DecodeHtmlEntities decodes HTML entities in a single pass without double-unescaping.
func DecodeHtmlEntities(input string) string {
	if !strings.ContainsRune(input, '&') {
		return input
	}
	var b strings.Builder
	b.Grow(len(input))
	for i := 0; i < len(input); {
		if input[i] == '&' {
			if decoded := DecodeHtmlEntityAt(input, i); decoded != nil {
				b.WriteString(decoded.Char)
				i += decoded.Length
				continue
			}
		}
		b.WriteByte(input[i])
		i++
	}
	return b.String()
}

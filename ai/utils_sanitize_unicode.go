// Ported from packages/ai/src/utils/sanitize-unicode.ts (pi v1.5.2).

package ai

import "unicode/utf8"

// SanitizeSurrogates removes unpaired Unicode surrogate characters from text.
//
// Properly paired characters outside the Basic Multilingual Plane remain valid UTF-8,
// and adjacent WTF-8 high+low surrogate pairs are combined to valid 4-byte UTF-8.
// Unpaired WTF-8 surrogates (0xED 0xA0..0xBF 0x80..0xBF) are removed.
func SanitizeSurrogates(text string) string {
	// Fast path: 0xED is the leading byte of all WTF-8 surrogates (U+D800..U+DFFF).
	hasED := false
	for i := 0; i < len(text); i++ {
		if text[i] == 0xED {
			hasED = true
			break
		}
	}
	if !hasED {
		return text
	}

	out := make([]byte, 0, len(text))
	for i := 0; i < len(text); {
		if code, ok := usuDecodeWTF8Surrogate(text, i); ok {
			if code >= 0xD800 && code <= 0xDBFF {
				if low, ok2 := usuDecodeWTF8Surrogate(text, i+3); ok2 && low >= 0xDC00 && low <= 0xDFFF {
					r := 0x10000 + ((code - 0xD800) << 10) + (low - 0xDC00)
					out = utf8.AppendRune(out, r)
					i += 6
					continue
				}
			}
			// Unpaired high or low surrogate: strip it.
			i += 3
			continue
		}
		out = append(out, text[i])
		i++
	}
	return string(out)
}

func usuDecodeWTF8Surrogate(s string, i int) (rune, bool) {
	if i+3 > len(s) {
		return 0, false
	}
	b0, b1, b2 := s[i], s[i+1], s[i+2]
	if b0 != 0xED || b1 < 0xA0 || b1 > 0xBF || b2 < 0x80 || b2 > 0xBF {
		return 0, false
	}
	code := (rune(b0&0x0F) << 12) | (rune(b1&0x3F) << 6) | rune(b2&0x3F)
	return code, true
}

package jsdiff

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// decodeChar decodes one JavaScript code point starting at s[i] and returns it with its
// byte length. A WTF-8 encoded surrogate (ED A0..BF 80..BF) is returned as the surrogate
// code point (a lone surrogate in the JS string). Invalid UTF-8 yields utf8.RuneError
// with the length of the maximal invalid subsequence, matching how jsonx reads strings.
func decodeChar(s string, i int) (rune, int) {
	c0 := s[i]
	if c0 < utf8.RuneSelf {
		return rune(c0), 1
	}
	var need int
	lo, hi := byte(0x80), byte(0xBF)
	var r rune
	switch {
	case c0 >= 0xC2 && c0 <= 0xDF:
		need = 1
		r = rune(c0 & 0x1F)
	case c0 == 0xE0:
		need = 2
		lo = 0xA0
		r = rune(c0 & 0x0F)
	case c0 >= 0xE1 && c0 <= 0xEF:
		need = 2
		r = rune(c0 & 0x0F)
	case c0 == 0xF0:
		need = 3
		lo = 0x90
		r = rune(c0 & 0x07)
	case c0 >= 0xF1 && c0 <= 0xF3:
		need = 3
		r = rune(c0 & 0x07)
	case c0 == 0xF4:
		need = 3
		hi = 0x8F
		r = rune(c0 & 0x07)
	default:
		return utf8.RuneError, 1
	}
	for k := 1; k <= need; k++ {
		if i+k >= len(s) {
			return utf8.RuneError, k
		}
		c := s[i+k]
		if k == 1 {
			if c < lo || c > hi {
				return utf8.RuneError, 1
			}
		} else if c < 0x80 || c > 0xBF {
			return utf8.RuneError, k
		}
		r = r<<6 | rune(c&0x3F)
	}
	return r, need + 1
}

// lastChar decodes the code point that ends at s[end] (exclusive) and returns it with its
// byte length.
func lastChar(s string, end int) (rune, int) {
	start := max(end-4, 0)
	for i := end - 1; i >= start; i-- {
		if s[i] < 0x80 || s[i] >= 0xC0 {
			r, size := decodeChar(s, i)
			if i+size == end {
				return r, size
			}
			break
		}
	}
	return utf8.RuneError, 1
}

func isSurrogate(r rune) bool { return r >= 0xD800 && r <= 0xDFFF }

// appendChar appends r in UTF-8, using WTF-8 for surrogates.
func appendChar(dst []byte, r rune) []byte {
	if isSurrogate(r) {
		return append(dst, 0xED, byte(0x80|(r>>6)&0x3F), byte(0x80|r&0x3F))
	}
	return utf8.AppendRune(dst, r)
}

// utf16Len returns the JavaScript length (UTF-16 code units) of s.
func utf16Len(s string) int {
	n := 0
	for i := 0; i < len(s); {
		if s[i] < utf8.RuneSelf {
			n++
			i++
			continue
		}
		r, size := decodeChar(s, i)
		i += size
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// toUTF16 returns the UTF-16 code units of the JavaScript string s stands for.
func toUTF16(s string) []uint16 {
	units := make([]uint16, 0, len(s))
	for i := 0; i < len(s); {
		r, size := decodeChar(s, i)
		i += size
		if r >= 0x10000 {
			r -= 0x10000
			units = append(units, uint16(0xD800+(r>>10)), uint16(0xDC00+(r&0x3FF)))
		} else {
			units = append(units, uint16(r))
		}
	}
	return units
}

// fromUTF16 converts code units back to a Go string (WTF-8 for unpaired surrogates).
func fromUTF16(units []uint16) string {
	b := make([]byte, 0, len(units))
	for i := 0; i < len(units); i++ {
		u := rune(units[i])
		if u >= 0xD800 && u <= 0xDBFF && i+1 < len(units) && units[i+1] >= 0xDC00 && units[i+1] <= 0xDFFF {
			b = utf8.AppendRune(b, 0x10000+(u-0xD800)<<10+(rune(units[i+1])-0xDC00))
			i++
			continue
		}
		b = appendChar(b, u)
	}
	return string(b)
}

// isJSSpace reports whether r matches the JavaScript regex class \s (ECMAScript
// WhiteSpace and LineTerminator), which is also the set String.prototype.trim removes.
func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0x00A0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}

// containsJSSpace is /\s/.test(s).
func containsJSSpace(s string) bool {
	for i := 0; i < len(s); {
		r, size := decodeChar(s, i)
		if isJSSpace(r) {
			return true
		}
		i += size
	}
	return false
}

// leadingSpaceLen returns the byte length of the run of \s characters at the start of s.
func leadingSpaceLen(s string) int {
	i := 0
	for i < len(s) {
		r, size := decodeChar(s, i)
		if !isJSSpace(r) {
			break
		}
		i += size
	}
	return i
}

// trailingSpaceStart returns the byte offset where the trailing run of \s characters of
// s starts.
func trailingSpaceStart(s string) int {
	end := len(s)
	for end > 0 {
		r, size := lastChar(s, end)
		if !isJSSpace(r) {
			break
		}
		end -= size
	}
	return end
}

// jsTrim is String.prototype.trim.
func jsTrim(s string) string {
	start := leadingSpaceLen(s)
	if start == len(s) {
		return ""
	}
	return s[start:trailingSpaceStart(s)]
}

// isCased reports whether r is a cased letter (Unicode property Cased).
func isCased(r rune) bool {
	return unicode.IsUpper(r) || unicode.IsLower(r) || unicode.IsTitle(r) ||
		unicode.Is(unicode.Other_Lowercase, r) || unicode.Is(unicode.Other_Uppercase, r)
}

// isCaseIgnorable reports whether r has the Unicode property Case_Ignorable.
func isCaseIgnorable(r rune) bool {
	switch r {
	case '\'', '.', ':', '^', '`', 0x00A8, 0x00AD, 0x00AF, 0x00B4, 0x00B7, 0x00B8, 0x0387, 0x055F, 0x05F4,
		0x2018, 0x2019, 0x2024, 0x2027, 0xFE13, 0xFE52, 0xFE55, 0xFF07, 0xFF0E, 0xFF1A:
		return true
	}
	return unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf, unicode.Lm, unicode.Sk)
}

// jsToLower is String.prototype.toLowerCase: full Unicode lowercase mapping, including
// the unconditional SpecialCasing expansion of U+0130 and the Final_Sigma rule.
func jsToLower(s string) string {
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			ascii = false
			break
		}
	}
	if ascii {
		return strings.ToLower(s)
	}
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); {
		r, size := decodeChar(s, i)
		switch {
		case r == utf8.RuneError && size >= 1 && !(size == 3 && s[i] == 0xEF):
			// Invalid UTF-8 is kept byte for byte.
			b = append(b, s[i:i+size]...)
		case r == 0x0130:
			b = append(b, "i̇"...)
		case r == 0x03A3:
			if finalSigma(s, i, i+size) {
				b = utf8.AppendRune(b, 0x03C2)
			} else {
				b = utf8.AppendRune(b, 0x03C3)
			}
		case isSurrogate(r):
			b = append(b, s[i:i+size]...)
		default:
			b = appendChar(b, unicode.ToLower(r))
		}
		i += size
	}
	return string(b)
}

// finalSigma evaluates the Final_Sigma casing context for the sigma at s[start:end].
func finalSigma(s string, start, end int) bool {
	before := false
	for i := start; i > 0; {
		r, size := lastChar(s, i)
		i -= size
		if isCaseIgnorable(r) {
			continue
		}
		before = isCased(r)
		break
	}
	if !before {
		return false
	}
	for i := end; i < len(s); {
		r, size := decodeChar(s, i)
		i += size
		if isCaseIgnorable(r) {
			continue
		}
		return !isCased(r)
	}
	return true
}

package partialjson

import "unicode/utf8"

// decodeChar decodes one JavaScript character starting at s[i] and returns the code point
// and its byte length. A WTF-8 encoded surrogate (ED A0..BF 80..BF) is returned as the
// surrogate code point. Invalid UTF-8 yields utf8.RuneError with the length of the
// maximal invalid subsequence, the same reading jsonx applies to JS strings.
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

// fromUTF16 converts code units back to a Go string. Unpaired surrogates use their WTF-8
// form, which jsonx reads back as the same lone surrogate.
func fromUTF16(units []uint16) string {
	b := make([]byte, 0, len(units))
	for i := 0; i < len(units); i++ {
		u := rune(units[i])
		if u >= 0xD800 && u <= 0xDBFF && i+1 < len(units) && units[i+1] >= 0xDC00 && units[i+1] <= 0xDFFF {
			b = utf8.AppendRune(b, 0x10000+(u-0xD800)<<10+(rune(units[i+1])-0xDC00))
			i++
			continue
		}
		if u >= 0xD800 && u <= 0xDFFF {
			b = append(b, 0xED, byte(0x80|(u>>6)&0x3F), byte(0x80|u&0x3F))
			continue
		}
		b = utf8.AppendRune(b, u)
	}
	return string(b)
}

// isJSWhitespace reports whether the code unit is in the set String.prototype.trim removes
// (ECMAScript WhiteSpace and LineTerminator).
func isJSWhitespace(u uint16) bool {
	switch u {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0x00A0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return u >= 0x2000 && u <= 0x200A
}

// trimUnits is String.prototype.trim on code units.
func trimUnits(u []uint16) []uint16 {
	start, end := 0, len(u)
	for start < end && isJSWhitespace(u[start]) {
		start++
	}
	for end > start && isJSWhitespace(u[end-1]) {
		end--
	}
	return u[start:end]
}

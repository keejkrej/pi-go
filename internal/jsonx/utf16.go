package jsonx

import "unicode/utf8"

// byteString is the set of types the UTF helpers accept.
type byteString interface{ ~string | ~[]byte }

// decodeCharAt decodes one JavaScript character starting at s[i].
//
// It returns the code point and its byte length. A WTF-8 encoded surrogate
// (ED A0..BF 80..BF) is returned as the surrogate code point 0xD800-0xDFFF.
// Invalid UTF-8 yields utf8.RuneError with the length of the maximal invalid
// subsequence (WHATWG / Unicode "maximal subpart" replacement).
func decodeCharAt[S byteString](s S, i int) (rune, int) {
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
	case c0 == 0xED && i+1 < len(s) && s[i+1] >= 0xA0 && s[i+1] <= 0xBF:
		// ED A0..BF 80..BF is a WTF-8 surrogate, kept as a lone surrogate. An
		// incomplete one is invalid UTF-8; WHATWG (and so Node) reject it at the second
		// byte, which then counts as its own invalid sequence.
		if i+2 >= len(s) || s[i+2] < 0x80 || s[i+2] > 0xBF {
			return utf8.RuneError, 1
		}
		return 0xD000 | rune(s[i+1]&0x3F)<<6 | rune(s[i+2]&0x3F), 3
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

// isSurrogate reports whether r is a UTF-16 surrogate code point.
func isSurrogate(r rune) bool { return r >= 0xD800 && r <= 0xDFFF }

// appendWTF8 appends r in (generalized) UTF-8. Surrogates get the 3-byte WTF-8 form.
func appendWTF8(dst []byte, r rune) []byte {
	if isSurrogate(r) {
		return append(dst, 0xED, byte(0x80|(r>>6)&0x3F), byte(0x80|r&0x3F))
	}
	return utf8.AppendRune(dst, r)
}

// joinSurrogatePairs replaces every WTF-8 high surrogate that is directly followed by a
// WTF-8 low surrogate with the 4-byte UTF-8 encoding of the pair (JS strings are UTF-16,
// so two adjacent halves form one character).
func joinSurrogatePairs(b []byte) []byte {
	out := b[:0]
	for i := 0; i < len(b); {
		if i+5 < len(b) && b[i] == 0xED && b[i+1] >= 0xA0 && b[i+1] <= 0xAF && b[i+3] == 0xED && b[i+4] >= 0xB0 && b[i+4] <= 0xBF {
			hiR, _ := decodeCharAt(b, i)
			loR, _ := decodeCharAt(b, i+3)
			if isSurrogate(hiR) && isSurrogate(loR) {
				out = utf8.AppendRune(out, 0x10000+(hiR-0xD800)<<10+(loR-0xDC00))
				i += 6
				continue
			}
		}
		out = append(out, b[i])
		i++
	}
	return out
}

// toUTF16 converts s to the UTF-16 code units of the JavaScript string it stands for.
func toUTF16[S byteString](s S) []uint16 {
	units := make([]uint16, 0, len(s))
	for i := 0; i < len(s); {
		r, size := decodeCharAt(s, i)
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

// utf16Len returns the number of UTF-16 code units in s.
func utf16Len[S byteString](s S) int {
	n := 0
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			n++
			i++
			continue
		}
		r, size := decodeCharAt(s, i)
		i += size
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// fromUTF16 converts UTF-16 code units to a Go string; unpaired surrogates use WTF-8.
func fromUTF16(units []uint16) string {
	b := make([]byte, 0, len(units))
	for i := 0; i < len(units); i++ {
		u := rune(units[i])
		if u >= 0xD800 && u <= 0xDBFF && i+1 < len(units) && units[i+1] >= 0xDC00 && units[i+1] <= 0xDFFF {
			b = utf8.AppendRune(b, 0x10000+(u-0xD800)<<10+(rune(units[i+1])-0xDC00))
			i++
			continue
		}
		b = appendWTF8(b, u)
	}
	return string(b)
}

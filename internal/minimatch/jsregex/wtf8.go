package jsregex

import (
	"unicode/utf8"
)

// ToUTF16 decodes a WTF-8 string into UTF-16 code units. A 3-byte encoding of
// a surrogate (ED A0..BF 80..BF) becomes that lone code unit; every other
// invalid byte becomes U+FFFD.
func ToUTF16(s string) []uint16 {
	out := make([]uint16, 0, len(s))
	for i := 0; i < len(s); {
		b := s[i]
		if b < utf8.RuneSelf {
			out = append(out, uint16(b))
			i++
			continue
		}
		if b == 0xED && i+2 < len(s) && s[i+1] >= 0xA0 && s[i+1] <= 0xBF && s[i+2] >= 0x80 && s[i+2] <= 0xBF {
			out = append(out, 0xD000|uint16(s[i+1]&0x3F)<<6|uint16(s[i+2]&0x3F))
			i += 3
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		if r >= 0x10000 {
			r -= 0x10000
			out = append(out, uint16(0xD800+(r>>10)), uint16(0xDC00+(r&0x3FF)))
			continue
		}
		out = append(out, uint16(r))
	}
	return out
}

// FromUTF16 encodes UTF-16 code units as WTF-8: surrogate pairs become the
// 4-byte UTF-8 encoding of their code point and lone surrogates the 3-byte
// WTF-8 sequence, so ToUTF16(FromUTF16(u)) == u.
func FromUTF16(u []uint16) string {
	buf := make([]byte, 0, len(u))
	for i := 0; i < len(u); i++ {
		c := rune(u[i])
		if c >= 0xD800 && c <= 0xDBFF && i+1 < len(u) && u[i+1] >= 0xDC00 && u[i+1] <= 0xDFFF {
			r := 0x10000 + (c-0xD800)<<10 + (rune(u[i+1]) - 0xDC00)
			buf = utf8.AppendRune(buf, r)
			i++
			continue
		}
		if c >= 0xD800 && c <= 0xDFFF {
			buf = append(buf, 0xED, byte(0x80|(c>>6)&0x3F), byte(0x80|c&0x3F))
			continue
		}
		buf = utf8.AppendRune(buf, c)
	}
	return string(buf)
}

// Normalize re-encodes s so that surrogate halves that were concatenated
// into a pair become the UTF-8 encoding of the pair, which makes Go string
// equality agree with JS string equality.
func Normalize(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == 0xED && i+1 < len(s) && s[i+1] >= 0xA0 {
			return FromUTF16(ToUTF16(s))
		}
	}
	return s
}

// UnitAt returns the code unit at index i of u as a one-unit WTF-8 string, or
// "" when i is out of range (JS str[i] / charAt).
func UnitAt(u []uint16, i int) string {
	if i < 0 || i >= len(u) {
		return ""
	}
	return FromUTF16(u[i : i+1])
}

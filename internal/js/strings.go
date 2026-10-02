package js

import (
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

const replacementChar = "\uFFFD"

// decodeAt decodes the code point at byte offset i. It returns the rune (U+FFFD
// for an invalid byte), its byte size, and its UTF-16 length (1 or 2).
func decodeAt(s string, i int) (r rune, size int, units int) {
	c := s[i]
	if c < utf8.RuneSelf {
		return rune(c), 1, 1
	}
	r, size = utf8.DecodeRuneInString(s[i:])
	if r >= 0x10000 {
		return r, size, 2
	}
	return r, size, 1
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// Len returns s.length: the number of UTF-16 code units in s.
func Len(s string) int {
	n := 0
	for i := 0; i < len(s); {
		_, size, units := decodeAt(s, i)
		i += size
		n += units
	}
	return n
}

// locate returns the byte offset of UTF-16 index u (0 <= u <= Len(s)). When u
// points at the second half of a surrogate pair, mid is true and the offset is
// the start of that code point.
func locate(s string, u int) (b int, mid bool) {
	n := 0
	for i := 0; i < len(s); {
		if n == u {
			return i, false
		}
		_, size, units := decodeAt(s, i)
		if units == 2 && n+1 == u {
			return i, true
		}
		n += units
		i += size
	}
	return len(s), false
}

// sliceUnits returns the UTF-16 range [start, end) of s; 0 <= start < end <= Len(s).
func sliceUnits(s string, start, end int) string {
	if isASCII(s) {
		return s[start:end]
	}
	bs, midStart := locate(s, start)
	be, midEnd := locate(s, end)
	var prefix, suffix string
	if midStart {
		_, size, _ := decodeAt(s, bs)
		bs += size
		prefix = replacementChar
	}
	if midEnd {
		suffix = replacementChar
	}
	if prefix == "" && suffix == "" {
		return s[bs:be]
	}
	if bs > be {
		bs = be
	}
	return prefix + s[bs:be] + suffix
}

func relativeIndex(i, n int) int {
	if i < 0 {
		i += n
		if i < 0 {
			return 0
		}
		return i
	}
	if i > n {
		return n
	}
	return i
}

func clampIndex(i, n int) int {
	if i < 0 {
		return 0
	}
	if i > n {
		return n
	}
	return i
}

// Slice returns s.slice(start, end) with UTF-16 indices. Negative indices
// count from the end.
func Slice(s string, start, end int) string {
	n := Len(s)
	start = relativeIndex(start, n)
	end = relativeIndex(end, n)
	if start >= end {
		return ""
	}
	return sliceUnits(s, start, end)
}

// SliceFrom returns s.slice(start).
func SliceFrom(s string, start int) string {
	n := Len(s)
	start = relativeIndex(start, n)
	if start >= n {
		return ""
	}
	return sliceUnits(s, start, n)
}

// Substring returns s.substring(start, end): negative indices clamp to 0 and
// the bounds are swapped when start > end.
func Substring(s string, start, end int) string {
	n := Len(s)
	start = clampIndex(start, n)
	end = clampIndex(end, n)
	if start > end {
		start, end = end, start
	}
	if start == end {
		return ""
	}
	return sliceUnits(s, start, end)
}

// SubstringFrom returns s.substring(start).
func SubstringFrom(s string, start int) string {
	n := Len(s)
	start = clampIndex(start, n)
	if start == n {
		return ""
	}
	return sliceUnits(s, start, n)
}

// unitAt returns the UTF-16 code unit at index i, the byte range of the code
// point containing it, and whether i is in range.
func unitAt(s string, i int) (unit int, b, size int, ok bool) {
	if i < 0 {
		return 0, 0, 0, false
	}
	n := 0
	for j := 0; j < len(s); {
		r, sz, units := decodeAt(s, j)
		if units == 1 {
			if n == i {
				return int(r), j, sz, true
			}
		} else if n == i || n+1 == i {
			hi, lo := utf16.EncodeRune(r)
			if n == i {
				return int(hi), j, sz, true
			}
			return int(lo), j, sz, true
		}
		n += units
		j += sz
	}
	return 0, 0, 0, false
}

// CharCodeAt returns s.charCodeAt(i), or -1 where JS returns NaN (i out of
// range). Invalid UTF-8 bytes read as 0xFFFD.
func CharCodeAt(s string, i int) int {
	if i >= 0 && i < len(s) && isASCIIPrefix(s, i) {
		return int(s[i])
	}
	unit, _, _, ok := unitAt(s, i)
	if !ok {
		return -1
	}
	return unit
}

// isASCIIPrefix reports whether s[:i+1] is ASCII, so byte i is unit i.
func isASCIIPrefix(s string, i int) bool {
	for j := 0; j <= i; j++ {
		if s[j] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// CodePointAt returns s.codePointAt(i), or -1 where JS returns undefined.
// At the second half of a surrogate pair it returns that half, as JS does.
func CodePointAt(s string, i int) int {
	unit, b, size, ok := unitAt(s, i)
	if !ok {
		return -1
	}
	if unit >= 0xD800 && unit <= 0xDBFF {
		r, _ := utf8.DecodeRuneInString(s[b : b+size])
		return int(r)
	}
	return unit
}

// CharAt returns s.charAt(i) (also s[i]): the UTF-16 unit at i as a string,
// "" when out of range. A lone surrogate half reads as U+FFFD; an invalid
// UTF-8 byte is returned as is.
func CharAt(s string, i int) string {
	unit, b, size, ok := unitAt(s, i)
	if !ok {
		return ""
	}
	if unit >= 0xD800 && unit <= 0xDFFF {
		return replacementChar
	}
	return s[b : b+size]
}

// FromCharCode returns String.fromCharCode(...units). Each unit is reduced
// modulo 2^16; surrogate pairs combine and lone surrogates become U+FFFD.
func FromCharCode(units ...int) string {
	u16 := make([]uint16, len(units))
	for i, u := range units {
		u16[i] = uint16(u)
	}
	return FromUTF16(u16)
}

// FromCodePoint returns String.fromCodePoint(...codePoints). A code point
// outside 0..0x10FFFF returns the RangeError "Invalid code point <cp>"; a
// surrogate code point yields U+FFFD.
func FromCodePoint(codePoints ...int) (string, error) {
	var b strings.Builder
	for _, cp := range codePoints {
		if cp < 0 || cp > 0x10FFFF {
			return "", NewRangeError("Invalid code point " + strconv.Itoa(cp))
		}
		b.WriteRune(rune(cp))
	}
	return b.String(), nil
}

// ToUTF16 returns the UTF-16 code units of s. Invalid UTF-8 bytes become
// 0xFFFD.
func ToUTF16(s string) []uint16 {
	out := make([]uint16, 0, len(s))
	for i := 0; i < len(s); {
		r, size, _ := decodeAt(s, i)
		out = utf16.AppendRune(out, r)
		i += size
	}
	return out
}

// FromUTF16 converts UTF-16 code units to a string. Lone surrogates become
// U+FFFD.
func FromUTF16(units []uint16) string {
	var b strings.Builder
	b.Grow(len(units))
	for i := 0; i < len(units); i++ {
		u := units[i]
		switch {
		case u < 0x80:
			b.WriteByte(byte(u))
		case u >= 0xD800 && u <= 0xDBFF && i+1 < len(units) && units[i+1] >= 0xDC00 && units[i+1] <= 0xDFFF:
			b.WriteRune(utf16.DecodeRune(rune(u), rune(units[i+1])))
			i++
		case u >= 0xD800 && u <= 0xDFFF:
			b.WriteString(replacementChar)
		default:
			b.WriteRune(rune(u))
		}
	}
	return b.String()
}

// wellFormed replaces every invalid UTF-8 byte with U+FFFD.
func wellFormed(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	return string([]rune(s))
}

func indexUnits(hay, needle []uint16, from int) int {
	for i := from; i+len(needle) <= len(hay); i++ {
		match := true
		for j := range needle {
			if hay[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

func lastIndexUnits(hay, needle []uint16, from int) int {
	start := len(hay) - len(needle)
	if from < start {
		start = from
	}
	for i := start; i >= 0; i-- {
		match := true
		for j := range needle {
			if hay[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// IndexOf returns s.indexOf(sub, from) as a UTF-16 index, or -1.
func IndexOf(s, sub string, from int) int {
	n := Len(s)
	pos := clampIndex(from, n)
	if sub == "" {
		return pos
	}
	if !utf8.ValidString(s) || !utf8.ValidString(sub) {
		return indexUnits(ToUTF16(s), ToUTF16(sub), pos)
	}
	b, mid := locate(s, pos)
	if mid {
		_, size, _ := decodeAt(s, b)
		b += size
	}
	idx := strings.Index(s[b:], sub)
	if idx < 0 {
		return -1
	}
	return ByteToU16(s, b+idx)
}

// LastIndexOf returns s.lastIndexOf(sub) as a UTF-16 index, or -1.
func LastIndexOf(s, sub string) int {
	if !utf8.ValidString(s) || !utf8.ValidString(sub) {
		hay := ToUTF16(s)
		return lastIndexUnits(hay, ToUTF16(sub), len(hay))
	}
	idx := strings.LastIndex(s, sub)
	if idx < 0 {
		return -1
	}
	return ByteToU16(s, idx)
}

// LastIndexOfFrom returns s.lastIndexOf(sub, from) as a UTF-16 index, or -1.
func LastIndexOfFrom(s, sub string, from int) int {
	n := Len(s)
	pos := clampIndex(from, n)
	if sub == "" {
		return pos
	}
	if !utf8.ValidString(s) || !utf8.ValidString(sub) {
		return lastIndexUnits(ToUTF16(s), ToUTF16(sub), pos)
	}
	b, _ := locate(s, pos)
	limit := b + len(sub)
	if limit > len(s) {
		limit = len(s)
	}
	idx := strings.LastIndex(s[:limit], sub)
	if idx < 0 {
		return -1
	}
	return ByteToU16(s, idx)
}

// ByteToU16 converts byte offset b of s to a UTF-16 index. b is clamped to
// [0, len(s)]; an offset inside a multi-byte code point maps to the index of
// that code point.
func ByteToU16(s string, b int) int {
	if b <= 0 {
		return 0
	}
	if b > len(s) {
		b = len(s)
	}
	n := 0
	for i := 0; i < b; {
		_, size, units := decodeAt(s, i)
		if i+size > b {
			break
		}
		i += size
		n += units
	}
	return n
}

// U16ToByte converts UTF-16 index u of s to a byte offset. u is clamped to
// [0, Len(s)]; an index between the halves of a surrogate pair maps to the
// start of that code point.
func U16ToByte(s string, u int) int {
	if u <= 0 {
		return 0
	}
	b, _ := locate(s, u)
	return b
}

// firstUnits returns the UTF-16 units of r.
func firstUnits(r rune) (uint16, uint16) {
	if r >= 0x10000 {
		hi, lo := utf16.EncodeRune(r)
		return uint16(hi), uint16(lo)
	}
	return uint16(r), 0
}

// CompareUTF16 compares a and b by UTF-16 code units, the order of the default
// Array.prototype.sort comparator and of the < operator on strings. It returns
// -1, 0, or 1.
func CompareUTF16(a, b string) int {
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		ca, cb := a[i], b[j]
		if ca < utf8.RuneSelf && cb < utf8.RuneSelf {
			if ca != cb {
				if ca < cb {
					return -1
				}
				return 1
			}
			i++
			j++
			continue
		}
		ra, sa, _ := decodeAt(a, i)
		rb, sb, _ := decodeAt(b, j)
		if ra != rb {
			ha, la := firstUnits(ra)
			hb, lb := firstUnits(rb)
			switch {
			case ha < hb:
				return -1
			case ha > hb:
				return 1
			case la < lb:
				return -1
			case la > lb:
				return 1
			}
		}
		i += sa
		j += sb
	}
	switch {
	case i < len(a):
		return 1
	case j < len(b):
		return -1
	}
	return 0
}

// IsJSWhitespace reports whether r is JS WhiteSpace or LineTerminator: the set
// matched by \s and removed by trim().
func IsJSWhitespace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0x00A0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}

// Trim returns s.trim().
func Trim(s string) string {
	return strings.TrimFunc(s, IsJSWhitespace)
}

// TrimStart returns s.trimStart().
func TrimStart(s string) string {
	return strings.TrimLeftFunc(s, IsJSWhitespace)
}

// TrimEnd returns s.trimEnd().
func TrimEnd(s string) string {
	return strings.TrimRightFunc(s, IsJSWhitespace)
}

// Split returns s.split(sep). An empty sep splits s into UTF-16 code units
// (each half of a surrogate pair becomes U+FFFD; an invalid UTF-8 byte stays
// as is). Split("", "") is empty and Split("", sep) is [""], as in JS.
func Split(s, sep string) []string {
	if sep == "" {
		out := make([]string, 0, len(s))
		for i := 0; i < len(s); {
			_, size, units := decodeAt(s, i)
			if units == 2 {
				out = append(out, replacementChar, replacementChar)
			} else {
				out = append(out, s[i:i+size])
			}
			i += size
		}
		return out
	}
	if !utf8.ValidString(sep) {
		return strings.Split(wellFormed(s), wellFormed(sep))
	}
	return strings.Split(s, sep)
}

// SplitLimit returns s.split(sep, limit) for limit >= 0.
func SplitLimit(s, sep string, limit int) []string {
	if limit <= 0 {
		return []string{}
	}
	parts := Split(s, sep)
	if len(parts) > limit {
		parts = parts[:limit]
	}
	return parts
}

func pad(s string, n int, fill string) (string, bool) {
	l := Len(s)
	if n <= l || fill == "" {
		return "", false
	}
	want := n - l
	fl := Len(fill)
	var b strings.Builder
	for got := 0; got < want; got += fl {
		b.WriteString(fill)
	}
	return Slice(b.String(), 0, want), true
}

// PadStart returns s.padStart(n, fill) with UTF-16 lengths. Pass " " for the JS
// default fill.
func PadStart(s string, n int, fill string) string {
	p, ok := pad(s, n, fill)
	if !ok {
		return s
	}
	return p + s
}

// PadEnd returns s.padEnd(n, fill) with UTF-16 lengths. Pass " " for the JS
// default fill.
func PadEnd(s string, n int, fill string) string {
	p, ok := pad(s, n, fill)
	if !ok {
		return s
	}
	return s + p
}

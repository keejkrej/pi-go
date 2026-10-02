package jsre

import (
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"
)

// subject is a Go string prepared for matching. JS strings are sequences of
// UTF-16 code units; a Go string is converted as if it had been decoded with
// []rune (each invalid UTF-8 byte is one U+FFFD).
//
// Without /u the matching domain is UTF-16 code units, so a surrogate pair is
// two runes (each shifted by surrogateShift); with /u it is code points. All
// indices exposed by the package are UTF-16 indices.
type subject struct {
	s     string
	runes []rune
	// boff[i] is the byte offset in s of rune i (len(runes)+1 entries), or
	// -1 when rune i is the low half of a surrogate pair. nil when every rune
	// is one byte.
	boff []int32
	// u16[i] is the UTF-16 index of rune i (len(runes)+1 entries). nil when
	// rune indices are UTF-16 indices.
	u16 []int32
	n16 int
}

func newSubject(s string, unicodeMode bool) *subject {
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			ascii = false
			break
		}
	}
	if ascii {
		runes := make([]rune, len(s))
		for i := 0; i < len(s); i++ {
			runes[i] = rune(s[i])
		}
		return &subject{s: s, runes: runes, n16: len(s)}
	}
	sub := &subject{s: s}
	n16 := 0
	if unicodeMode {
		runes := make([]rune, 0, len(s))
		boff := make([]int32, 0, len(s)+1)
		u16 := make([]int32, 0, len(s)+1)
		for i := 0; i < len(s); {
			r, size := utf8.DecodeRuneInString(s[i:])
			runes = append(runes, r)
			boff = append(boff, int32(i))
			u16 = append(u16, int32(n16))
			if r > 0xFFFF {
				n16 += 2
			} else {
				n16++
			}
			i += size
		}
		boff = append(boff, int32(len(s)))
		u16 = append(u16, int32(n16))
		sub.runes, sub.boff, sub.u16 = runes, boff, u16
	} else {
		units := make([]rune, 0, len(s))
		boff := make([]int32, 0, len(s)+1)
		for i := 0; i < len(s); {
			r, size := utf8.DecodeRuneInString(s[i:])
			if r > 0xFFFF {
				hi, lo := utf16.EncodeRune(r)
				units = append(units, hi+surrogateShift, lo+surrogateShift)
				boff = append(boff, int32(i), -1)
			} else {
				units = append(units, r)
				boff = append(boff, int32(i))
			}
			i += size
		}
		boff = append(boff, int32(len(s)))
		n16 = len(units)
		sub.runes, sub.boff = units, boff
	}
	sub.n16 = n16
	return sub
}

// sameString reports whether a and b share their backing bytes, so a
// prepared subject can be reused without comparing contents.
func sameString(a, b string) bool {
	return len(a) == len(b) && unsafe.StringData(a) == unsafe.StringData(b)
}

// index16 converts a rune index to a UTF-16 index.
func (t *subject) index16(i int) int {
	if t.u16 == nil {
		return i
	}
	return int(t.u16[i])
}

// runeIndex converts a UTF-16 index (0 <= i <= n16) to a rune index. In
// /u mode an index inside a surrogate pair moves back to the pair start,
// as V8 does for lastIndex.
func (t *subject) runeIndex(i int) int {
	if t.u16 == nil {
		return i
	}
	lo, hi := 0, len(t.u16)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if int(t.u16[mid]) <= i {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo
}

// slice returns the substring between two rune indices. A boundary inside a
// surrogate pair yields U+FFFD for the lone half.
func (t *subject) slice(a, b int) string {
	if a >= b {
		return ""
	}
	if t.boff == nil {
		return t.s[a:b]
	}
	ba, bb := t.boff[a], t.boff[b]
	if ba >= 0 && bb >= 0 {
		return t.s[ba:bb]
	}
	units := make([]uint16, b-a)
	for i, r := range t.runes[a:b] {
		units[i] = uint16(r) // also undoes surrogateShift
	}
	return string(utf16.Decode(units))
}

// advance implements AdvanceStringIndex on rune indices: one rune in both
// modes (a code point with /u, a code unit without).
func (t *subject) advance(i int) int { return i + 1 }

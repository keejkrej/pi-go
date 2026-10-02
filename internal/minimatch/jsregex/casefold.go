package jsregex

import (
	"sync"
	"unicode"

	"github.com/keejkrej/pi-go/internal/js"
)

// Case-insensitive matching follows ECMA-262 Canonicalize:
//   - without u: ch maps to toUpperCase(ch) when that is a single code unit,
//     except that a non-ASCII unit never maps into ASCII;
//   - with u: ch maps to its simple case folding. Two code points are
//     equivalent exactly when they lie in the same unicode.SimpleFold orbit.

var (
	cfOnce    sync.Once
	cfCanon   []uint16            // non-u canonical unit per code unit
	cfClasses map[uint16][]uint16 // canonical unit -> all units mapping to it (only classes of size > 1)
)

func cfInit() {
	cfCanon = make([]uint16, 0x10000)
	cfClasses = map[uint16][]uint16{}
	for c := 0; c < 0x10000; c++ {
		cfCanon[c] = uint16(c)
		if c >= 0xD800 && c <= 0xDFFF {
			continue
		}
		up := js.ToUTF16(js.ToUpper(string(rune(c))))
		if len(up) != 1 {
			continue
		}
		cu := up[0]
		if c >= 128 && cu < 128 {
			continue
		}
		cfCanon[c] = cu
	}
	groups := map[uint16][]uint16{}
	for c := 0; c < 0x10000; c++ {
		k := cfCanon[c]
		groups[k] = append(groups[k], uint16(c))
	}
	for k, g := range groups {
		if len(g) > 1 {
			cfClasses[k] = g
		}
	}
}

// canonUnit returns Canonicalize(c) for the non-unicode mode.
func canonUnit(c rune) rune {
	if c < 0 || c > 0xFFFF {
		return c
	}
	cfOnce.Do(cfInit)
	return rune(cfCanon[c])
}

// canonFold returns a representative of the simple case folding class of r
// (the smallest code point of its SimpleFold orbit).
func canonFold(r rune) rune {
	m := r
	for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
		if f < m {
			m = f
		}
	}
	return m
}

// equivalents calls f for every character that matches c case-insensitively
// (c itself included) until f returns true.
func equivalents(c rune, unicodeMode bool, f func(rune) bool) bool {
	if f(c) {
		return true
	}
	if unicodeMode {
		for e := unicode.SimpleFold(c); e != c; e = unicode.SimpleFold(e) {
			if f(e) {
				return true
			}
		}
		return false
	}
	if c > 0xFFFF {
		return false
	}
	cfOnce.Do(cfInit)
	for _, e := range cfClasses[cfCanon[c]] {
		if rune(e) != c && f(rune(e)) {
			return true
		}
	}
	return false
}

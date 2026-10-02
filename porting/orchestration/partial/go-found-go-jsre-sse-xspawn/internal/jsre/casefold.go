package jsre

import (
	"slices"
	"sync"
	"unicode"
)

// caseTable maps every character that has case-insensitive equivalents to
// its equivalence class. keys is sorted for the closure scan.
type caseTable struct {
	classOf map[rune][]rune
	keys    []rune
}

var nonUnicodeCases = sync.OnceValue(func() *caseTable {
	t := &caseTable{classOf: make(map[rune][]rune)}
	start := 0
	for i, c := range nonUnicodeCaseClasses {
		if c != 0 {
			continue
		}
		cls := make([]rune, 0, i-start)
		for _, m := range nonUnicodeCaseClasses[start:i] {
			cls = append(cls, rune(m))
		}
		for _, m := range cls {
			t.classOf[m] = cls
			t.keys = append(t.keys, m)
		}
		start = i + 1
	}
	slices.Sort(t.keys)
	return t
})

// unicodeCases holds the simple case folding orbits used by /iu (V8 uses
// ICU closeOver(USET_SIMPLE_CASE_INSENSITIVE); Go's SimpleFold orbits are
// the same relation, which TestCaseClassesGolden verifies).
var unicodeCases = sync.OnceValue(func() *caseTable {
	t := &caseTable{classOf: make(map[rune][]rune)}
	add := func(c rune) {
		if _, done := t.classOf[c]; done || unicode.SimpleFold(c) == c {
			return
		}
		orbit := []rune{c}
		for f := unicode.SimpleFold(c); f != c; f = unicode.SimpleFold(f) {
			orbit = append(orbit, f)
		}
		slices.Sort(orbit)
		for _, m := range orbit {
			t.classOf[m] = orbit
			t.keys = append(t.keys, m)
		}
	}
	// Some orbits (U+0390 and U+1FD3, for example) have no upper/lower case
	// mapping and so are not in unicode.CaseRanges: scan the first two
	// planes, where all case pairs are, and CaseRanges for the rest.
	for c := rune(0); c <= 0x1FFFF; c++ {
		add(c)
	}
	for _, cr := range unicode.CaseRanges {
		for c := max(rune(cr.Lo), 0x20000); c <= rune(cr.Hi); c++ {
			add(c)
		}
	}
	slices.Sort(t.keys)
	return t
})

func caseTableFor(unicodeMode bool) *caseTable {
	if unicodeMode {
		return unicodeCases()
	}
	return nonUnicodeCases()
}

// caseClosure adds every case-insensitive equivalent of the members of s.
func caseClosure(s runeSet, unicodeMode bool) runeSet {
	if len(s) == 0 || (len(s) == 1 && s[0].lo == 0 && s[0].hi == maxCodePoint) {
		return s
	}
	t := caseTableFor(unicodeMode)
	var extra []runeRange
	for _, r := range s {
		lo, _ := slices.BinarySearch(t.keys, r.lo)
		for i := lo; i < len(t.keys) && t.keys[i] <= r.hi; i++ {
			for _, m := range t.classOf[t.keys[i]] {
				if !s.contains(m) {
					extra = append(extra, runeRange{m, m})
				}
			}
		}
	}
	if len(extra) == 0 {
		return s
	}
	return unionSets(s, extra)
}

// charClosure returns the case-insensitive equivalents of c, including c.
func charClosure(c rune, unicodeMode bool) []rune {
	if cls := caseTableFor(unicodeMode).classOf[c]; cls != nil {
		return cls
	}
	return []rune{c}
}

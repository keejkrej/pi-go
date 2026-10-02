package jsre

import (
	"slices"
	"sync"
)

// caseTable maps every character that has case-insensitive equivalents to
// its equivalence class. keys is sorted for the closure scan.
type caseTable struct {
	classOf map[rune][]rune
	keys    []rune
}

func loadCaseTable(classes []rune) *caseTable {
	t := &caseTable{classOf: make(map[rune][]rune)}
	start := 0
	for i, c := range classes {
		if c != 0 {
			continue
		}
		cls := append([]rune(nil), classes[start:i]...)
		for _, m := range cls {
			t.classOf[m] = cls
			t.keys = append(t.keys, m)
		}
		start = i + 1
	}
	slices.Sort(t.keys)
	return t
}

var nonUnicodeCases = sync.OnceValue(func() *caseTable {
	cls := make([]rune, len(nonUnicodeCaseClasses))
	for i, c := range nonUnicodeCaseClasses {
		cls[i] = rune(c)
	}
	return loadCaseTable(cls)
})

// unicodeCases is V8's /iu simple case-fold closure (ICU
// closeOver(USET_SIMPLE_CASE_INSENSITIVE), Unicode 17). Go's
// unicode.SimpleFold table is an older Unicode and misses pairs such as
// U+019B/U+A7DC, so the classes come from Node instead.
var unicodeCases = sync.OnceValue(func() *caseTable {
	return loadCaseTable(unicodeCaseClasses)
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

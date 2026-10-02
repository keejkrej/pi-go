package jsre

import (
	"slices"
	"unicode"
)

const maxCodePoint = 0x10FFFF

type runeRange struct{ lo, hi rune }

// runeSet is a sorted list of disjoint, non-adjacent ranges.
type runeSet []runeRange

func normalizeSet(rs []runeRange) runeSet {
	if len(rs) == 0 {
		return nil
	}
	slices.SortFunc(rs, func(a, b runeRange) int {
		if a.lo != b.lo {
			return int(a.lo - b.lo)
		}
		return int(a.hi - b.hi)
	})
	out := rs[:1]
	for _, r := range rs[1:] {
		last := &out[len(out)-1]
		if r.lo <= last.hi+1 {
			last.hi = max(last.hi, r.hi)
			continue
		}
		out = append(out, r)
	}
	return runeSet(out)
}

func unionSets(sets ...runeSet) runeSet {
	var all []runeRange
	for _, s := range sets {
		all = append(all, s...)
	}
	return normalizeSet(all)
}

func complementSet(s runeSet) runeSet {
	var out runeSet
	next := rune(0)
	for _, r := range s {
		if next < r.lo {
			out = append(out, runeRange{next, r.lo - 1})
		}
		next = r.hi + 1
	}
	if next <= maxCodePoint {
		out = append(out, runeRange{next, maxCodePoint})
	}
	return out
}

func (s runeSet) contains(c rune) bool {
	i, found := slices.BinarySearchFunc(s, c, func(r runeRange, c rune) int {
		switch {
		case r.hi < c:
			return -1
		case r.lo > c:
			return 1
		}
		return 0
	})
	_ = i
	return found
}

func setFromTable(t *unicode.RangeTable) runeSet {
	var out []runeRange
	add := func(lo, hi, stride rune) {
		if stride == 1 {
			out = append(out, runeRange{lo, hi})
			return
		}
		for c := lo; c <= hi; c += stride {
			out = append(out, runeRange{c, c})
		}
	}
	for _, r := range t.R16 {
		add(rune(r.Lo), rune(r.Hi), rune(r.Stride))
	}
	for _, r := range t.R32 {
		add(rune(r.Lo), rune(r.Hi), rune(r.Stride))
	}
	return normalizeSet(out)
}

// Standard character classes (CharacterRange::AddClassEscape).
var (
	digitSet = runeSet{{'0', '9'}}
	spaceSet = normalizeSet([]runeRange{
		{'\t', '\r'}, {' ', ' '}, {0xA0, 0xA0}, {0x1680, 0x1680}, {0x2000, 0x200A},
		{0x2028, 0x2029}, {0x202F, 0x202F}, {0x205F, 0x205F}, {0x3000, 0x3000}, {0xFEFF, 0xFEFF},
	})
	wordSet = runeSet{{'0', '9'}, {'A', 'Z'}, {'_', '_'}, {'a', 'z'}}
	// \w under /iu also contains U+017F (long s) and U+212A (Kelvin sign).
	wordSetFolded     = unionSets(wordSet, runeSet{{0x017F, 0x017F}, {0x212A, 0x212A}})
	lineTerminators   = runeSet{{'\n', '\n'}, {'\r', '\r'}, {0x2028, 0x2029}}
	notLineTerminator = complementSet(lineTerminators)
	everything        = runeSet{{0, maxCodePoint}}
)

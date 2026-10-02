package js

import (
	"strings"
	"sync"

	"golang.org/x/text/cases"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
	"golang.org/x/text/unicode/norm"
)

// ToUpper returns s.toUpperCase(): locale-independent full Unicode case
// mapping, including SpecialCasing expansions such as "ß" -> "SS".
func ToUpper(s string) string {
	if isASCII(s) {
		b := []byte(s)
		changed := false
		for i, c := range b {
			if c >= 'a' && c <= 'z' {
				b[i] = c - ('a' - 'A')
				changed = true
			}
		}
		if !changed {
			return s
		}
		return string(b)
	}
	return applyCase(cases.Upper(language.Und).String(s), unicode17Upper)
}

// ToLower returns s.toLowerCase(): locale-independent full Unicode case
// mapping, including the final-sigma rule and "İ" -> "i̇".
func ToLower(s string) string {
	if isASCII(s) {
		b := []byte(s)
		changed := false
		for i, c := range b {
			if c >= 'A' && c <= 'Z' {
				b[i] = c + ('a' - 'A')
				changed = true
			}
		}
		if !changed {
			return s
		}
		return string(b)
	}
	return applyCase(cases.Lower(language.Und).String(s), unicode17Lower)
}

// Normalize returns s.normalize(form). form is "NFC", "NFD", "NFKC", or
// "NFKD"; "" means the JS default, NFC. Any other form is a programmer error
// (JS throws a RangeError) and panics with that RangeError.
func Normalize(s, form string) string {
	switch form {
	case "", "NFC":
		return norm.NFC.String(s)
	case "NFD":
		return normWithExtra(norm.NFD, s, unicode17NFD)
	case "NFKC":
		return normWithExtra(norm.NFKC, s, unicode17NFKC)
	case "NFKD":
		return normWithExtra(norm.NFKD, s, unicode17NFKD)
	}
	panic(NewRangeError("The normalization form should be one of NFC, NFD, NFKC, NFKD."))
}

// applyCase overlays 1:1 mappings that the linked Unicode tables do not have.
// extra's keys are source characters; a hit means this Go version left them unchanged.
func applyCase(s string, extra map[rune]rune) string {
	for i, r := range s {
		if _, ok := extra[r]; !ok {
			continue
		}
		var b strings.Builder
		b.Grow(len(s))
		b.WriteString(s[:i])
		for _, r := range s[i:] {
			if n, ok := extra[r]; ok {
				b.WriteRune(n)
			} else {
				b.WriteRune(r)
			}
		}
		return b.String()
	}
	return s
}

// normWithExtra applies decompositions missing from the linked Unicode tables,
// then normalizes again so new combining marks join the canonical order.
// NFC is not given a table: those characters are already their composed form.
func normWithExtra(f norm.Form, s string, extra map[rune]string) string {
	s = f.String(s)
	for range 4 {
		next := applyDecomp(s, extra)
		if next == s {
			return s
		}
		s = f.String(next)
	}
	return s
}

func applyDecomp(s string, extra map[rune]string) string {
	for i, r := range s {
		if _, ok := extra[r]; !ok {
			continue
		}
		var b strings.Builder
		b.Grow(len(s) + 8)
		b.WriteString(s[:i])
		for _, r := range s[i:] {
			if sub, ok := extra[r]; ok {
				b.WriteString(sub)
			} else {
				b.WriteRune(r)
			}
		}
		return b.String()
	}
	return s
}

var jsCollators = sync.Pool{New: func() any { return collate.New(language.Und) }}

// LocaleCompare returns a.localeCompare(b) (-1, 0, or 1) using the root
// collation, which is what Node's default en-US locale uses.
func LocaleCompare(a, b string) int {
	c := jsCollators.Get().(*collate.Collator)
	defer jsCollators.Put(c)
	return c.CompareString(a, b)
}

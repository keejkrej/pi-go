package js

import (
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
	return cases.Upper(language.Und).String(s)
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
	return cases.Lower(language.Und).String(s)
}

// Normalize returns s.normalize(form). form is "NFC", "NFD", "NFKC", or
// "NFKD"; "" means the JS default, NFC. Any other form is a programmer error
// (JS throws a RangeError) and panics with that RangeError.
func Normalize(s, form string) string {
	switch form {
	case "", "NFC":
		return norm.NFC.String(s)
	case "NFD":
		return norm.NFD.String(s)
	case "NFKC":
		return norm.NFKC.String(s)
	case "NFKD":
		return norm.NFKD.String(s)
	}
	panic(NewRangeError("The normalization form should be one of NFC, NFD, NFKC, NFKD."))
}

var jsCollators = sync.Pool{New: func() any { return collate.New(language.Und) }}

// LocaleCompare returns a.localeCompare(b) (-1, 0, or 1) using the root
// collation, which is what Node's default en-US locale uses.
func LocaleCompare(a, b string) int {
	c := jsCollators.Get().(*collate.Collator)
	defer jsCollators.Put(c)
	return c.CompareString(a, b)
}

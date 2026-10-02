package jsre

import (
	"strings"
	"sync"
	"unicode"

	"github.com/dlclark/regexp2/v2/syntax"
)

// Unicode property escapes (\p{...} in /u patterns) are resolved through
// regexp2's ECMAScript property support, which implements the ECMA-262
// property and value alias tables (exact names, no loose matching) for the
// Unicode version of the Go toolchain. V8 accepts exactly the same names,
// except for properties of strings, which are only valid with /v.

var propertyParseOptions = syntax.ParseOptions{RegexOptions: syntax.ECMAScript | syntax.Unicode}

func parsePropertyEscape(name string) *syntax.CharSet {
	tree, err := syntax.Parse(`\p{`+name+`}`, propertyParseOptions)
	if err != nil {
		return nil
	}
	return findCharSet(tree.Root)
}

func findCharSet(n *syntax.RegexNode) *syntax.CharSet {
	if n == nil {
		return nil
	}
	if n.Set != nil {
		return n.Set
	}
	for _, k := range n.Children {
		if s := findCharSet(k); s != nil {
			return s
		}
	}
	return nil
}

var validPropertyCache sync.Map // name -> bool

func validProperty(name string) bool {
	if v, ok := validPropertyCache.Load(name); ok {
		return v.(bool)
	}
	tree, err := syntax.Parse(`\p{`+name+`}`, propertyParseOptions)
	ok := err == nil && findCharSet(tree.Root) != nil
	validPropertyCache.Store(name, ok)
	return ok
}

var propertySetCache sync.Map // name -> runeSet

// propertySet returns the code points of a valid property name ("Letter",
// "Script=Greek", ...).
func propertySet(name string) runeSet {
	if v, ok := propertySetCache.Load(name); ok {
		return v.(runeSet)
	}
	set := computePropertySet(name)
	propertySetCache.Store(name, set)
	return set
}

func computePropertySet(name string) runeSet {
	cs := parsePropertyEscape(name)
	if cs == nil {
		return nil
	}
	desc := cs.String()
	// Large properties are kept by regexp2 as a named category such as
	// [\p{gc=L}], [\P{gc=Cn}], [\p{sc=Greek}] or [\p{scx=Han}]. Small ones
	// are inlined as explicit ranges.
	if key, negate, ok := categoryKey(desc); ok {
		if set, ok := setForKey(key); ok {
			if negate {
				return complementSet(set)
			}
			return set
		}
		return scanCharSet(cs)
	}
	if strings.Contains(desc, `\p{`) || strings.Contains(desc, `\P{`) {
		return scanCharSet(cs)
	}
	for n := 1; n <= 64; n++ {
		if ranges := cs.GetIfNRanges(n); ranges != nil {
			out := make([]runeRange, len(ranges))
			for i, r := range ranges {
				out[i] = runeRange{r.First, r.Last}
			}
			set := normalizeSet(out)
			if cs.IsNegated() {
				return complementSet(set)
			}
			return set
		}
	}
	return scanCharSet(cs)
}

func categoryKey(desc string) (key string, negate, ok bool) {
	inner, found := strings.CutPrefix(desc, "[")
	if !found {
		return "", false, false
	}
	inner, found = strings.CutSuffix(inner, "]")
	if !found {
		return "", false, false
	}
	switch {
	case strings.HasPrefix(inner, `\p{`):
	case strings.HasPrefix(inner, `\P{`):
		negate = true
	default:
		return "", false, false
	}
	rest := inner[3:]
	end := strings.IndexByte(rest, '}')
	if end != len(rest)-1 {
		return "", false, false
	}
	return rest[:end], negate, true
}

func setForKey(key string) (runeSet, bool) {
	if cat, ok := strings.CutPrefix(key, "gc="); ok {
		if t := unicode.Categories[cat]; t != nil {
			return setFromTable(t), true
		}
		return nil, false
	}
	if script, ok := strings.CutPrefix(key, "sc="); ok {
		if t := unicode.Scripts[script]; t != nil {
			return setFromTable(t), true
		}
		return nil, false
	}
	if strings.Contains(key, "=") {
		return nil, false
	}
	if t := unicode.Properties[key]; t != nil {
		return setFromTable(t), true
	}
	return nil, false
}

func scanCharSet(cs *syntax.CharSet) runeSet {
	var out runeSet
	inRange := false
	var start rune
	for c := rune(0); c <= maxCodePoint; c++ {
		in := cs.Contains(c)
		if in && !inRange {
			start = c
			inRange = true
		} else if !in && inRange {
			out = append(out, runeRange{start, c - 1})
			inRange = false
		}
	}
	if inRange {
		out = append(out, runeRange{start, maxCodePoint})
	}
	return out
}

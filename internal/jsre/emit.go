package jsre

import (
	"strconv"
	"strings"
)

// The emitter translates the parsed tree into a regexp2 pattern in .NET
// syntax (no regexp2 options), spelling out every JS semantic explicitly:
//
//   - Characters and classes become explicit code point sets. /i is applied
//     by adding case-insensitive equivalents (V8's canonicalization without
//     /u, simple case folding with /u).
//   - Capture groups are numbered explicitly, (?<N>...), so regexp2 group N
//     is JS group N. Helper groups get numbers after the last JS group.
//   - A back reference to group N is (?(N)\k<N>|): a group that has not
//     participated matches the empty string.
//   - Each iteration of a quantified atom first clears the captures inside
//     it: (?(N)(?<-N>)) pops the previous iteration's capture.
//   - An iteration beyond the minimum that matches the empty string fails
//     (ECMA-262 RepeatMatcher step 2.b). regexp2 instead accepts one empty
//     iteration and stops, so atoms that can match empty get a guard that
//     captures the rest of the input at the start of the iteration and fails
//     if the input position did not move.
//   - ^ $ \b \B are lookaround assertions over explicit sets; lookbehinds
//     use regexp2's right-to-left matching, as JS lookbehinds do.
type emitter struct {
	unicode  bool
	nextHelp int
	backward bool
}

func emitProgram(res parseResult, unicodeMode, sticky bool) string {
	e := &emitter{unicode: unicodeMode, nextHelp: res.captures + 1}
	body := e.str(res.root)
	if sticky {
		return `\G(?:` + body + `)`
	}
	return body
}

// surrogateShift moves UTF-16 surrogate code units out of D800-DFFF before
// they reach regexp2. Several regexp2 search optimizations store pattern
// characters in Go strings, where a surrogate becomes U+FFFD, so a set such
// as [\uD83D\uD83E] would never be found. Without /u the subject contains
// only code units (no rune above 0xFFFF), so surrogate unit u is matched as
// rune u+surrogateShift (plane 16), in the subject and in every emitted set.
// With /u a Go string never contains a surrogate code point, so sets drop
// D800-DFFF.
const surrogateShift = 0x100000

// clip returns the part of s inside [lo, hi].
func clip(s runeSet, lo, hi rune) runeSet {
	var out runeSet
	for _, r := range s {
		a, b := max(r.lo, lo), min(r.hi, hi)
		if a <= b {
			out = append(out, runeRange{a, b})
		}
	}
	return out
}

// domain maps a set of code points (/u) or code units to the runes regexp2
// sees (see surrogateShift).
func (e *emitter) domain(s runeSet) runeSet {
	if e.unicode {
		return append(clip(s, 0, 0xD7FF), clip(s, 0xE000, maxCodePoint)...)
	}
	out := clip(s, 0, 0xD7FF)
	for _, r := range clip(s, 0xD800, 0xDFFF) {
		out = append(out, runeRange{r.lo + surrogateShift, r.hi + surrogateShift})
	}
	return normalizeSet(append(out, clip(s, 0xE000, 0xFFFF)...))
}

// writeEscape writes c as a regexp2 escape, or raw for astral code points.
func writeEscape(sb *strings.Builder, c rune) {
	if c > 0xFFFF {
		sb.WriteRune(c)
		return
	}
	const hex = "0123456789ABCDEF"
	sb.WriteByte('\\')
	sb.WriteByte('u')
	sb.WriteByte(hex[c>>12&0xF])
	sb.WriteByte(hex[c>>8&0xF])
	sb.WriteByte(hex[c>>4&0xF])
	sb.WriteByte(hex[c&0xF])
}

// set renders a set of code points (/u) or code units as one regexp2 atom.
func (e *emitter) set(s runeSet) string {
	s = e.domain(s)
	if len(s) == 0 {
		return `(?!)`
	}
	var sb strings.Builder
	if len(s) == 1 && s[0].lo == s[0].hi {
		writeEscape(&sb, s[0].lo)
		return sb.String()
	}
	sb.WriteByte('[')
	for _, r := range s {
		writeEscape(&sb, r.lo)
		if r.hi != r.lo {
			sb.WriteByte('-')
			writeEscape(&sb, r.hi)
		}
	}
	sb.WriteByte(']')
	return sb.String()
}

func (e *emitter) charSet(c rune, fl modFlags) runeSet {
	if fl&flagIgnoreCase == 0 {
		return runeSet{{c, c}}
	}
	members := charClosure(c, e.unicode)
	rs := make([]runeRange, len(members))
	for i, m := range members {
		rs[i] = runeRange{m, m}
	}
	return normalizeSet(rs)
}

func (e *emitter) classSet(cls *classNode, fl modFlags) runeSet {
	var parts []runeRange
	add := func(s runeSet) { parts = append(parts, s...) }
	for _, it := range cls.items {
		switch it.esc {
		case 0:
			parts = append(parts, runeRange{it.lo, it.hi})
		case 'd':
			add(digitSet)
		case 'D':
			add(complementSet(digitSet))
		case 's':
			add(spaceSet)
		case 'S':
			add(complementSet(spaceSet))
		case 'w':
			add(e.word(it.foldW))
		case 'W':
			add(complementSet(e.word(it.foldW)))
		case 'p':
			ps := propertySet(it.prop)
			if it.neg {
				ps = complementSet(ps)
			}
			add(ps)
		}
	}
	s := normalizeSet(parts)
	if fl&flagIgnoreCase != 0 {
		s = caseClosure(s, e.unicode)
	}
	if cls.negated {
		s = complementSet(s)
	}
	return s
}

func (e *emitter) word(folded bool) runeSet {
	if folded {
		return wordSetFolded
	}
	return wordSet
}

// isAtom reports whether the emitted form of n is a single regexp2 atom that
// a quantifier can follow directly.
func isAtom(n *node) bool {
	switch n.kind {
	case nChar, nClass, nDot:
		return true
	}
	return false
}

func (e *emitter) str(n *node) string {
	switch n.kind {
	case nEmpty:
		return ""
	case nChar:
		return e.set(e.charSet(n.ch, n.fl))
	case nDot:
		if n.fl&flagDotAll != 0 {
			return e.set(everything)
		}
		return e.set(notLineTerminator)
	case nClass:
		return e.set(e.classSet(n.cls, n.fl))
	case nSeq:
		var sb strings.Builder
		for _, k := range n.kids {
			sb.WriteString(e.str(k))
		}
		return sb.String()
	case nAlt:
		var sb strings.Builder
		sb.WriteString("(?:")
		for i, k := range n.kids {
			if i > 0 {
				sb.WriteByte('|')
			}
			sb.WriteString(e.str(k))
		}
		sb.WriteByte(')')
		return sb.String()
	case nCapture:
		return "(?<" + strconv.Itoa(n.index) + ">" + e.str(n.kids[0]) + ")"
	case nGroup:
		return "(?:" + e.str(n.kids[0]) + ")"
	case nLook:
		saved := e.backward
		e.backward = n.behind
		body := e.str(n.kids[0])
		e.backward = saved
		switch {
		case n.behind && n.neg:
			return "(?<!" + body + ")"
		case n.behind:
			return "(?<=" + body + ")"
		case n.neg:
			return "(?!" + body + ")"
		}
		return "(?=" + body + ")"
	case nBackref:
		return e.backref(n)
	case nAssert:
		return e.assertion(n)
	case nQuant:
		return e.quantifier(n)
	}
	return ""
}

func (e *emitter) backref(n *node) string {
	// (?(a)\k<a>|(?(b)\k<b>|)) for references to several groups with the
	// same name; at most one of them can have participated.
	var sb strings.Builder
	for _, idx := range n.refs {
		num := strconv.Itoa(idx)
		sb.WriteString("(?(" + num + ")")
		if n.fl&flagIgnoreCase != 0 {
			sb.WriteString(`(?i:\k<` + num + `>)`)
		} else {
			sb.WriteString(`\k<` + num + `>`)
		}
		sb.WriteByte('|')
	}
	for range n.refs {
		sb.WriteByte(')')
	}
	return sb.String()
}

func (e *emitter) assertion(n *node) string {
	terminators := e.set(complementSet(lineTerminators))
	switch n.assert {
	case assertStartOfInput:
		return `\A`
	case assertEndOfInput:
		return `\z`
	case assertStartOfLine:
		return `(?<!` + terminators + `)`
	case assertEndOfLine:
		return `(?!` + terminators + `)`
	}
	w := e.set(e.word(e.unicode && n.fl&flagIgnoreCase != 0))
	if n.assert == assertBoundary {
		return `(?:(?<=` + w + `)(?!` + w + `)|(?<!` + w + `)(?=` + w + `))`
	}
	return `(?:(?<=` + w + `)(?=` + w + `)|(?<!` + w + `)(?!` + w + `))`
}

func quantSuffix(lo, hi int, greedy bool) string {
	var s string
	switch {
	case lo == 0 && hi == infinity:
		s = "*"
	case lo == 1 && hi == infinity:
		s = "+"
	case lo == 0 && hi == 1:
		s = "?"
	case hi == infinity:
		s = "{" + strconv.Itoa(lo) + ",}"
	case lo == hi:
		s = "{" + strconv.Itoa(lo) + "}"
	default:
		s = "{" + strconv.Itoa(lo) + "," + strconv.Itoa(hi) + "}"
	}
	if !greedy {
		s += "?"
	}
	return s
}

// seq joins parts in matching order: reversed inside a lookbehind, where
// regexp2 matches a concatenation from right to left.
func (e *emitter) seq(parts ...string) string {
	if e.backward {
		for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
			parts[i], parts[j] = parts[j], parts[i]
		}
	}
	return strings.Join(parts, "")
}

func (e *emitter) quantifier(n *node) string {
	body := n.kids[0]
	var reset string
	if n.max > 1 {
		var sb strings.Builder
		for _, idx := range capturesIn(body, nil) {
			num := strconv.Itoa(idx)
			sb.WriteString("(?(" + num + ")(?<-" + num + ">))")
		}
		reset = sb.String()
	}
	inner := e.str(body)
	guard := minMatch(body) == 0 && n.max > n.min
	if !guard {
		if reset == "" && isAtom(body) {
			return inner + quantSuffix(n.min, n.max, n.greedy)
		}
		return "(?:" + e.seq(reset, inner) + ")" + quantSuffix(n.min, n.max, n.greedy)
	}
	var out string
	if n.min > 0 {
		out = "(?:" + e.seq(reset, inner) + ")" + quantSuffix(n.min, n.min, true)
	}
	help := strconv.Itoa(e.nextHelp)
	e.nextHelp++
	any := e.set(everything)
	var guarded string
	if e.backward {
		// Matched right to left: the capture of the text before the start
		// position runs first, the check last.
		guarded = e.seq(reset, `(?<=(?<`+help+`>`+any+`*))`, inner, `(?<!\k<`+help+`>)`)
	} else {
		guarded = e.seq(reset, `(?=(?<`+help+`>`+any+`*))`, inner, `(?!\k<`+help+`>)`)
	}
	rest := infinity
	if n.max != infinity {
		rest = n.max - n.min
	}
	return out + "(?:" + guarded + ")" + quantSuffix(0, rest, n.greedy)
}

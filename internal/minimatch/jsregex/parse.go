package jsregex

import (
	"strconv"
	"unicode"
)

type node interface{}

type nChar struct {
	c     rune
	canon rune
}

type nClass struct {
	items []classItem
	neg   bool
}

type classItem struct {
	lo, hi rune
	pred   func(rune) bool
}

type nSeq struct{ items []node }

type nAlt struct{ alts []node }

type nGroup struct {
	idx int
	sub node
}

type nRepeat struct {
	sub          node
	min, max     int // max < 0: unbounded
	greedy       bool
	capLo, capHi int // capture indices [capLo, capHi) inside sub
}

type nLook struct {
	sub          node
	neg          bool
	capLo, capHi int
}

type nBackref struct{ idx int }

type nAssert struct{ kind byte } // '^', '$', 'b', 'B'

type parser struct {
	src     []uint16
	pos     int
	u       bool
	icase   bool
	ncap    int
	capIdx  int
	message string
}

type syntaxError struct{ msg string }

func (p *parser) fail(msg string) {
	panic(syntaxError{msg})
}

func (p *parser) eof() bool { return p.pos >= len(p.src) }

// peekAt returns the character at unit offset p.pos+off without u-mode pair
// combination (for syntax characters, which are all ASCII).
func (p *parser) peekAt(off int) rune {
	if p.pos+off >= len(p.src) {
		return -1
	}
	return rune(p.src[p.pos+off])
}

func (p *parser) peek() rune { return p.peekAt(0) }

// next consumes one pattern character: a code unit, or in u mode a code
// point (a surrogate pair is combined).
func (p *parser) next() rune {
	c := rune(p.src[p.pos])
	p.pos++
	if p.u && c >= 0xD800 && c <= 0xDBFF && p.pos < len(p.src) {
		d := rune(p.src[p.pos])
		if d >= 0xDC00 && d <= 0xDFFF {
			p.pos++
			return 0x10000 + (c-0xD800)<<10 + (d - 0xDC00)
		}
	}
	return c
}

func countCaptures(src []uint16) int {
	n := 0
	inClass := false
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch {
		case c == '\\':
			i++
		case inClass:
			if c == ']' {
				inClass = false
			}
		case c == '[':
			inClass = true
		case c == '(':
			if i+1 < len(src) && src[i+1] == '?' {
				continue
			}
			n++
		}
	}
	return n
}

func parse(src []uint16, u, icase bool) (root node, ncap int, err error) {
	p := &parser{src: src, u: u, icase: icase, ncap: countCaptures(src)}
	defer func() {
		if r := recover(); r != nil {
			se, ok := r.(syntaxError)
			if !ok {
				panic(r)
			}
			err = errorf(se.msg)
		}
	}()
	root = p.parseDisjunction()
	if !p.eof() {
		// Only an unmatched ')' stops the top-level disjunction.
		p.fail("Unmatched ')'")
	}
	return root, p.capIdx, nil
}

func (p *parser) parseDisjunction() node {
	alts := []node{p.parseAlternative()}
	for !p.eof() && p.peek() == '|' {
		p.pos++
		alts = append(alts, p.parseAlternative())
	}
	if len(alts) == 1 {
		return alts[0]
	}
	return &nAlt{alts: alts}
}

func (p *parser) parseAlternative() node {
	var items []node
	for !p.eof() {
		c := p.peek()
		if c == '|' || c == ')' {
			break
		}
		items = append(items, p.parseTerm())
	}
	if len(items) == 1 {
		return items[0]
	}
	return &nSeq{items: items}
}

func (p *parser) parseTerm() node {
	c := p.peek()
	capBefore := p.capIdx
	var atom node
	switch c {
	case '^':
		p.pos++
		return &nAssert{kind: '^'}
	case '$':
		p.pos++
		return &nAssert{kind: '$'}
	case '\\':
		if p.peekAt(1) == 'b' || p.peekAt(1) == 'B' {
			k := byte(p.peekAt(1))
			p.pos += 2
			return &nAssert{kind: k}
		}
		p.pos++
		atom = p.parseAtomEscape()
	case '(':
		p.pos++
		if p.peek() == '?' {
			switch p.peekAt(1) {
			case ':':
				p.pos += 2
				sub := p.parseDisjunction()
				p.expectClose()
				atom = &nSeq{items: []node{sub}}
			case '=', '!':
				neg := p.peekAt(1) == '!'
				p.pos += 2
				sub := p.parseDisjunction()
				p.expectClose()
				look := &nLook{sub: sub, neg: neg, capLo: capBefore, capHi: p.capIdx}
				if p.u {
					return look
				}
				// Annex B: a lookahead may be quantified without u.
				atom = look
			default:
				p.fail("Invalid group")
			}
		} else {
			p.capIdx++
			idx := p.capIdx
			sub := p.parseDisjunction()
			p.expectClose()
			atom = &nGroup{idx: idx, sub: sub}
		}
	case '.':
		p.pos++
		atom = &nClass{items: []classItem{{pred: isNotLineTerminator}}}
	case '[':
		p.pos++
		atom = p.parseClass()
	case '*', '+', '?':
		p.fail("Nothing to repeat")
	case '{':
		if p.u {
			if _, _, ok := p.tryBraceQuantifier(); ok {
				p.fail("Nothing to repeat")
			}
			p.fail("Lone quantifier brackets")
		}
		if _, _, ok := p.tryBraceQuantifier(); ok {
			p.fail("Nothing to repeat")
		}
		p.pos++
		atom = p.char('{')
	case '}', ']':
		if p.u {
			p.fail("Lone quantifier brackets")
		}
		p.pos++
		atom = p.char(c)
	default:
		atom = p.char(p.next())
	}
	return p.parseQuantifier(atom, capBefore)
}

func (p *parser) expectClose() {
	if p.eof() || p.peek() != ')' {
		p.fail("Unterminated group")
	}
	p.pos++
}

func (p *parser) char(c rune) node {
	n := &nChar{c: c, canon: c}
	if p.icase {
		if p.u {
			n.canon = canonFold(c)
		} else {
			n.canon = canonUnit(c)
		}
	}
	return n
}

// tryBraceQuantifier parses {n}, {n,} or {n,m} at p.pos without consuming.
func (p *parser) tryBraceQuantifier() (min, max int, ok bool) {
	i := p.pos
	if i >= len(p.src) || p.src[i] != '{' {
		return 0, 0, false
	}
	i++
	readInt := func() (int, bool) {
		start := i
		v := 0
		for i < len(p.src) && p.src[i] >= '0' && p.src[i] <= '9' {
			if v < 1<<30 {
				v = v*10 + int(p.src[i]-'0')
			}
			i++
		}
		return v, i > start
	}
	min, ok = readInt()
	if !ok {
		return 0, 0, false
	}
	max = min
	if i < len(p.src) && p.src[i] == ',' {
		i++
		if i < len(p.src) && p.src[i] == '}' {
			max = -1
		} else {
			var ok2 bool
			max, ok2 = readInt()
			if !ok2 {
				return 0, 0, false
			}
		}
	}
	if i >= len(p.src) || p.src[i] != '}' {
		return 0, 0, false
	}
	return min, max, true
}

func (p *parser) braceQuantifierEnd() int {
	i := p.pos
	for p.src[i] != '}' {
		i++
	}
	return i + 1
}

func (p *parser) parseQuantifier(atom node, capBefore int) node {
	if p.eof() {
		return atom
	}
	var min, max int
	switch p.peek() {
	case '*':
		min, max = 0, -1
		p.pos++
	case '+':
		min, max = 1, -1
		p.pos++
	case '?':
		min, max = 0, 1
		p.pos++
	case '{':
		mn, mx, ok := p.tryBraceQuantifier()
		if !ok {
			if p.u {
				p.fail("Incomplete quantifier")
			}
			return atom
		}
		if mx >= 0 && mx < mn {
			p.fail("numbers out of order in {} quantifier")
		}
		p.pos = p.braceQuantifierEnd()
		min, max = mn, mx
	default:
		return atom
	}
	greedy := true
	if !p.eof() && p.peek() == '?' {
		greedy = false
		p.pos++
	}
	return &nRepeat{sub: atom, min: min, max: max, greedy: greedy, capLo: capBefore, capHi: p.capIdx}
}

func isHex(c rune) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func hexVal(c rune) rune {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	default:
		return c - 'A' + 10
	}
}

// readHex reads exactly n hex digits at p.pos.
func (p *parser) readHex(n int) (rune, bool) {
	if p.pos+n > len(p.src) {
		return 0, false
	}
	var v rune
	for i := 0; i < n; i++ {
		c := rune(p.src[p.pos+i])
		if !isHex(c) {
			return 0, false
		}
		v = v*16 + hexVal(c)
	}
	p.pos += n
	return v, true
}

// parseUnicodeEscape parses what follows "\u". ok is false when the escape
// is not well formed (an identity escape of 'u' without the u flag).
func (p *parser) parseUnicodeEscape() (rune, bool) {
	if p.u && p.peek() == '{' {
		save := p.pos
		p.pos++
		var v rune
		digits := 0
		for !p.eof() && isHex(p.peek()) {
			v = v*16 + hexVal(p.peek())
			if v > 0x10FFFF {
				p.fail("Invalid Unicode escape")
			}
			p.pos++
			digits++
		}
		if digits == 0 || p.eof() || p.peek() != '}' {
			p.pos = save
			return 0, false
		}
		p.pos++
		return v, true
	}
	v, ok := p.readHex(4)
	if !ok {
		return 0, false
	}
	if p.u && v >= 0xD800 && v <= 0xDBFF && p.peek() == '\\' && p.peekAt(1) == 'u' {
		save := p.pos
		p.pos += 2
		if w, ok := p.readHex(4); ok && w >= 0xDC00 && w <= 0xDFFF {
			return 0x10000 + (v-0xD800)<<10 + (w - 0xDC00), true
		}
		p.pos = save
	}
	return v, true
}

func isSyntaxChar(c rune) bool {
	switch c {
	case '^', '$', '\\', '.', '*', '+', '?', '(', ')', '[', ']', '{', '}', '|', '/':
		return true
	}
	return false
}

// parseCharEscape parses the escape after '\' that denotes a single
// character (shared by atoms and classes). inClass enables the class-only
// forms. It returns ok=false when the escape is a character class escape
// (\d etc.), which the caller handles first.
func (p *parser) parseCharEscape(inClass bool) rune {
	if p.eof() {
		p.fail("\\ at end of pattern")
	}
	c := p.peek()
	switch c {
	case 'f':
		p.pos++
		return '\f'
	case 'n':
		p.pos++
		return '\n'
	case 'r':
		p.pos++
		return '\r'
	case 't':
		p.pos++
		return '\t'
	case 'v':
		p.pos++
		return '\v'
	case 'c':
		l := p.peekAt(1)
		if (l >= 'a' && l <= 'z') || (l >= 'A' && l <= 'Z') {
			p.pos += 2
			return l % 32
		}
		if !p.u && inClass && ((l >= '0' && l <= '9') || l == '_') {
			p.pos += 2
			return l % 32
		}
		if p.u {
			p.fail("Invalid unicode escape")
		}
		// Annex B: "\c" is a literal backslash; 'c' is parsed next.
		return '\\'
	case 'x':
		p.pos++
		if v, ok := p.readHex(2); ok {
			return v
		}
		if p.u {
			p.fail("Invalid escape")
		}
		return 'x'
	case 'u':
		p.pos++
		if v, ok := p.parseUnicodeEscape(); ok {
			return v
		}
		if p.u {
			p.fail("Invalid Unicode escape")
		}
		return 'u'
	case '0':
		if p.peekAt(1) < '0' || p.peekAt(1) > '9' {
			p.pos++
			return 0
		}
		if p.u {
			if inClass {
				p.fail("Invalid class escape")
			}
			p.fail("Invalid decimal escape")
		}
		return p.parseLegacyOctal()
	case '1', '2', '3', '4', '5', '6', '7':
		if p.u {
			if inClass {
				p.fail("Invalid class escape")
			}
			p.fail("Invalid escape")
		}
		return p.parseLegacyOctal()
	case '8', '9':
		if p.u {
			if inClass {
				p.fail("Invalid class escape")
			}
			p.fail("Invalid escape")
		}
		p.pos++
		return c
	case '-':
		if inClass {
			p.pos++
			return '-'
		}
	}
	if p.u {
		if isSyntaxChar(c) {
			p.pos++
			return c
		}
		if inClass {
			p.fail("Invalid class escape")
		}
		p.fail("Invalid escape")
	}
	if c == 'k' && !inClass {
		p.pos++
		return 'k'
	}
	return p.next()
}

func (p *parser) parseLegacyOctal() rune {
	v := rune(p.src[p.pos] - '0')
	p.pos++
	if !p.eof() && p.peek() >= '0' && p.peek() <= '7' {
		v = v*8 + p.peek() - '0'
		p.pos++
		if v < 32 && !p.eof() && p.peek() >= '0' && p.peek() <= '7' {
			v = v*8 + p.peek() - '0'
			p.pos++
		}
	}
	return v
}

// tryClassEscape handles \d \D \w \W \s \S \p{} \P{} at p.pos (after '\').
func (p *parser) tryClassEscape() ([]classItem, bool) {
	c := p.peek()
	switch c {
	case 'd':
		p.pos++
		return []classItem{{lo: '0', hi: '9'}}, true
	case 'D':
		p.pos++
		return []classItem{{pred: func(r rune) bool { return r < '0' || r > '9' }}}, true
	case 'w':
		p.pos++
		if p.u && p.icase {
			return []classItem{{pred: isWordUI}}, true
		}
		return []classItem{{pred: isWord}}, true
	case 'W':
		p.pos++
		if p.u && p.icase {
			return []classItem{{pred: func(r rune) bool { return !isWordUI(r) }}}, true
		}
		return []classItem{{pred: func(r rune) bool { return !isWord(r) }}}, true
	case 's':
		p.pos++
		return []classItem{{pred: isSpace}}, true
	case 'S':
		p.pos++
		return []classItem{{pred: func(r rune) bool { return !isSpace(r) }}}, true
	case 'p', 'P':
		if !p.u {
			return nil, false
		}
		p.pos++
		if p.peek() != '{' {
			p.fail("Invalid property name")
		}
		p.pos++
		start := p.pos
		for !p.eof() && p.peek() != '}' {
			p.pos++
		}
		if p.eof() {
			p.fail("Invalid property name")
		}
		name := string(utf16Runes(p.src[start:p.pos]))
		p.pos++
		pred := propertyPredicate(name)
		if pred == nil {
			p.fail("Invalid property name")
		}
		if c == 'P' {
			return []classItem{{pred: func(r rune) bool { return !pred(r) }}}, true
		}
		return []classItem{{pred: pred}}, true
	}
	return nil, false
}

func utf16Runes(u []uint16) []rune {
	out := make([]rune, 0, len(u))
	for _, c := range u {
		out = append(out, rune(c))
	}
	return out
}

func (p *parser) parseAtomEscape() node {
	if p.eof() {
		p.fail("\\ at end of pattern")
	}
	if items, ok := p.tryClassEscape(); ok {
		return &nClass{items: items}
	}
	c := p.peek()
	if c >= '1' && c <= '9' {
		save := p.pos
		v := 0
		for !p.eof() && p.peek() >= '0' && p.peek() <= '9' {
			if v < 1<<20 {
				v = v*10 + int(p.peek()-'0')
			}
			p.pos++
		}
		if v <= p.ncap {
			return &nBackref{idx: v}
		}
		if p.u {
			p.fail("Invalid escape")
		}
		p.pos = save
	}
	if c == 'k' && p.u {
		p.fail("Invalid named reference")
	}
	return p.char(p.parseCharEscape(false))
}

func (p *parser) parseClass() node {
	cls := &nClass{}
	if !p.eof() && p.peek() == '^' {
		cls.neg = true
		p.pos++
	}
	for {
		if p.eof() {
			p.fail("Unterminated character class")
		}
		if p.peek() == ']' {
			p.pos++
			break
		}
		loItems, lo, loIsChar := p.parseClassAtom()
		if !p.eof() && p.peek() == '-' && p.peekAt(1) != ']' && p.peekAt(1) != -1 {
			p.pos++
			hiItems, hi, hiIsChar := p.parseClassAtom()
			if !loIsChar || !hiIsChar {
				if p.u {
					p.fail("Invalid character class")
				}
				cls.items = append(cls.items, loItems...)
				cls.items = append(cls.items, classItem{lo: '-', hi: '-'})
				cls.items = append(cls.items, hiItems...)
				continue
			}
			if lo > hi {
				p.fail("Range out of order in character class")
			}
			cls.items = append(cls.items, classItem{lo: lo, hi: hi})
			continue
		}
		cls.items = append(cls.items, loItems...)
	}
	return cls
}

// parseClassAtom returns the items of one class atom and, when it is a single
// character, that character.
func (p *parser) parseClassAtom() ([]classItem, rune, bool) {
	if p.peek() == '\\' {
		p.pos++
		if p.eof() {
			p.fail("\\ at end of pattern")
		}
		if items, ok := p.tryClassEscape(); ok {
			return items, 0, false
		}
		var c rune
		if p.peek() == 'b' {
			p.pos++
			c = '\b'
		} else {
			c = p.parseCharEscape(true)
		}
		return []classItem{{lo: c, hi: c}}, c, true
	}
	c := p.next()
	return []classItem{{lo: c, hi: c}}, c, true
}

func isNotLineTerminator(r rune) bool {
	return r != '\n' && r != '\r' && r != 0x2028 && r != 0x2029
}

func isWord(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_'
}

// isWordUI is \w under the u and i flags, which also holds U+017F and U+212A
// (their simple case folding is in the basic word set).
func isWordUI(r rune) bool {
	return isWord(r) || r == 0x017F || r == 0x212A
}

func isSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}

var propCategoryAliases = map[string]string{
	"Letter": "L", "Cased_Letter": "LC", "Uppercase_Letter": "Lu", "Lowercase_Letter": "Ll",
	"Titlecase_Letter": "Lt", "Modifier_Letter": "Lm", "Other_Letter": "Lo", "Mark": "M",
	"Combining_Mark": "M", "Nonspacing_Mark": "Mn", "Spacing_Mark": "Mc", "Enclosing_Mark": "Me",
	"Number": "N", "Decimal_Number": "Nd", "digit": "Nd", "Letter_Number": "Nl", "Other_Number": "No",
	"Punctuation": "P", "punct": "P", "Connector_Punctuation": "Pc", "Dash_Punctuation": "Pd",
	"Open_Punctuation": "Ps", "Close_Punctuation": "Pe", "Initial_Punctuation": "Pi",
	"Final_Punctuation": "Pf", "Other_Punctuation": "Po", "Symbol": "S", "Math_Symbol": "Sm",
	"Currency_Symbol": "Sc", "Modifier_Symbol": "Sk", "Other_Symbol": "So", "Separator": "Z",
	"Space_Separator": "Zs", "Line_Separator": "Zl", "Paragraph_Separator": "Zp", "Other": "C",
	"Control": "Cc", "cntrl": "Cc", "Format": "Cf", "Surrogate": "Cs", "Private_Use": "Co",
	"Unassigned": "Cn",
}

func isAssigned(r rune) bool {
	return unicode.In(r, unicode.L, unicode.M, unicode.N, unicode.P, unicode.S, unicode.Z, unicode.C)
}

// propertyPredicate resolves a \p{...} body to a predicate (general
// categories only), or nil.
func propertyPredicate(name string) func(rune) bool {
	if v, ok := cutPrefix(name, "General_Category="); ok {
		name = v
	} else if v, ok := cutPrefix(name, "gc="); ok {
		name = v
	}
	if a, ok := propCategoryAliases[name]; ok {
		name = a
	}
	switch name {
	case "Any":
		return func(rune) bool { return true }
	case "ASCII":
		return func(r rune) bool { return r < 0x80 }
	case "Cn":
		return func(r rune) bool { return !isAssigned(r) }
	case "C":
		return func(r rune) bool { return unicode.Is(unicode.C, r) || !isAssigned(r) }
	case "LC":
		return func(r rune) bool { return unicode.In(r, unicode.Lu, unicode.Ll, unicode.Lt) }
	}
	if t, ok := unicode.Categories[name]; ok {
		return func(r rune) bool { return unicode.Is(t, r) }
	}
	return nil
}

func cutPrefix(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && s[:len(prefix)] == prefix {
		return s[len(prefix):], true
	}
	return s, false
}

var _ = strconv.Itoa

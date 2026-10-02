package jsre

import (
	"unicode"
	"unicode/utf16"
)

// The parser is a port of V8's RegExpParserImpl (src/regexp/regexp-parser.cc,
// V8 13.6 as shipped in Node 24) for the d g i m s u y flags. It reports the
// same first error, with the same message, for every invalid pattern, and
// builds a node tree with V8's builder rules (quantifiers on zero-width atoms,
// empty back references inside their own group, and so on).

const (
	endMarker   rune = 1 << 21 // kEndMarker
	maxCaptures      = 1 << 16 // RegExpMacroAssembler::kMaxCaptures
)

type subexpressionType uint8

const (
	stInitial subexpressionType = iota
	stCapture
	stGrouping
	stPositiveLookaround
	stNegativeLookaround
)

type inClassState bool

const (
	notInClass inClassState = false
	inClass    inClassState = true
)

// altStep identifies one alternative of one disjunction; a capture's path is
// the list of alternatives enclosing it. Two named groups with the same name
// are allowed only if their paths diverge (MightBothParticipate is false).
type altStep struct{ disj, alt int }

type parserState struct {
	prev         *parserState
	b            *builder
	typ          subexpressionType
	behind       bool // lookaround direction of this disjunction
	captureIndex int
	captureName  string
	hasName      bool
	disj         int
	alt          int
}

func (s *parserState) isInsideCaptureGroup(index int) bool {
	for st := s; st != nil; st = st.prev {
		if st.typ != stCapture {
			continue
		}
		if index == st.captureIndex {
			return true
		}
		if index > st.captureIndex {
			return false
		}
	}
	return false
}

func (s *parserState) isInsideCaptureGroupNamed(name string) bool {
	for st := s; st != nil; st = st.prev {
		if st.hasName && st.captureName == name {
			return true
		}
	}
	return false
}

func (s *parserState) path() []altStep {
	var out []altStep
	for st := s; st != nil; st = st.prev {
		out = append(out, altStep{st.disj, st.alt})
	}
	return out
}

type builder struct {
	flags        modFlags
	pendingEmpty bool
	terms        []*node
	alts         []*node
}

func (b *builder) addTerm(n *node) {
	b.pendingEmpty = false
	b.terms = append(b.terms, n)
}

func (b *builder) addAtom(n *node) {
	if n.kind == nEmpty {
		b.pendingEmpty = true
		return
	}
	b.addTerm(n)
}

func (b *builder) addEmpty() { b.pendingEmpty = true }

func (b *builder) addAssertion(kind assertKind) {
	b.pendingEmpty = false
	b.terms = append(b.terms, &node{kind: nAssert, assert: kind, fl: b.flags})
}

func (b *builder) addCharacter(c rune) {
	b.addTerm(&node{kind: nChar, ch: c, fl: b.flags})
}

func (b *builder) flushTerms() {
	var alt *node
	switch len(b.terms) {
	case 0:
		alt = &node{kind: nEmpty}
	case 1:
		alt = b.terms[0]
	default:
		alt = &node{kind: nSeq, kids: b.terms}
	}
	b.alts = append(b.alts, alt)
	b.terms = nil
}

func (b *builder) toRegExp() *node {
	b.flushTerms()
	if len(b.alts) == 1 {
		return b.alts[0]
	}
	return &node{kind: nAlt, kids: b.alts}
}

// addQuantifierToAtom mirrors RegExpBuilder::AddQuantifierToAtom. It returns
// false for quantifiers V8 rejects (lookbehinds, and lookarounds in /u).
func (b *builder) addQuantifierToAtom(minRep, maxRep int, greedy, unicodeMode bool) bool {
	if b.pendingEmpty {
		b.pendingEmpty = false
		return true
	}
	atom := b.terms[len(b.terms)-1]
	b.terms = b.terms[:len(b.terms)-1]
	if atom.kind == nLook {
		if unicodeMode || atom.behind {
			return false
		}
	}
	if maxMatch(atom) == 0 {
		// Guaranteed to only match an empty string.
		if minRep == 0 {
			return true
		}
		b.terms = append(b.terms, atom)
		return true
	}
	b.terms = append(b.terms, &node{kind: nQuant, min: minRep, max: maxRep, greedy: greedy, kids: []*node{atom}})
	return true
}

type namedCapture struct {
	name  string
	index int
	path  []altStep
}

type parser struct {
	in       []uint16
	cur      rune
	curStart int // code unit index where cur starts
	nextPos  int
	hasMore  bool

	unicode      bool // the u flag
	forceUnicode bool

	capturesStarted  int
	captureCount     int
	scanned          bool
	hasNamedCaptures bool

	captures  []*node // by index-1
	named     []namedCapture
	namedRefs []*node
	nextDisj  int
}

type parseResult struct {
	root     *node
	captures int
	names    []string // by capture index-1; "" for unnamed groups
	hasNames bool
}

func parsePattern(src []uint16, unicodeMode bool, flags modFlags) (res parseResult, err regexpError, failed bool) {
	p := &parser{in: src, unicode: unicodeMode, hasMore: true}
	defer func() {
		if r := recover(); r != nil {
			f, ok := r.(parseFailure)
			if !ok {
				panic(r)
			}
			err, failed = f.code, true
		}
	}()
	p.advance()
	root := p.parseDisjunction(flags)
	p.patchNamedBackReferences()
	res.root = root
	res.captures = p.capturesStarted
	res.names = make([]string, p.capturesStarted)
	for _, nc := range p.named {
		res.names[nc.index-1] = nc.name
		res.hasNames = true
	}
	return res, 0, false
}

func (p *parser) fail(code regexpError) {
	panic(parseFailure{code})
}

func (p *parser) isUnicodeMode() bool { return p.unicode || p.forceUnicode }

func (p *parser) hasNext() bool { return p.nextPos < len(p.in) }

func (p *parser) readNext(update bool) rune {
	pos := p.nextPos
	c0 := rune(p.in[pos])
	pos++
	if p.isUnicodeMode() && pos < len(p.in) && isLeadSurrogate(c0) {
		c1 := rune(p.in[pos])
		if isTrailSurrogate(c1) {
			c0 = utf16.DecodeRune(c0, c1)
			pos++
		}
	}
	if update {
		p.nextPos = pos
	}
	return c0
}

func (p *parser) next() rune {
	if p.hasNext() {
		return p.readNext(false)
	}
	return endMarker
}

func (p *parser) advance() {
	if p.hasNext() {
		p.curStart = p.nextPos
		p.cur = p.readNext(true)
	} else {
		p.curStart = len(p.in)
		p.cur = endMarker
		p.nextPos = len(p.in) + 1
		p.hasMore = false
	}
}

func (p *parser) advanceBy(dist int) {
	p.nextPos += dist - 1
	p.advance()
}

// position returns the start of the current character; resetTo(position())
// re-reads it.
func (p *parser) position() int { return p.curStart }

func (p *parser) resetTo(pos int) {
	p.nextPos = pos
	p.hasMore = pos < len(p.in)
	p.advance()
}

func isLeadSurrogate(c rune) bool  { return c >= 0xD800 && c <= 0xDBFF }
func isTrailSurrogate(c rune) bool { return c >= 0xDC00 && c <= 0xDFFF }

func isDecimalDigit(c rune) bool { return c >= '0' && c <= '9' }

func hexValue(c rune) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

func isSyntaxCharacterOrSlash(c rune) bool {
	switch c {
	case '^', '$', '\\', '.', '*', '+', '?', '(', ')', '[', ']', '{', '}', '|', '/':
		return true
	}
	return false
}

func (p *parser) newDisj() int {
	p.nextDisj++
	return p.nextDisj
}

func (p *parser) getCapture(index int) *node {
	for len(p.captures) < index {
		p.captures = append(p.captures, &node{kind: nCapture, index: len(p.captures) + 1})
	}
	return p.captures[index-1]
}

func (p *parser) parseDisjunction(flags modFlags) *node {
	state := &parserState{typ: stInitial, b: &builder{flags: flags}, disj: p.newDisj()}
	b := state.b
	for {
		switch p.cur {
		case endMarker:
			if state.typ != stInitial {
				p.fail(errUnterminatedGroup)
			}
			return b.toRegExp()
		case ')':
			if state.typ == stInitial {
				p.fail(errUnmatchedParen)
			}
			p.advance()
			body := b.toRegExp()
			switch state.typ {
			case stCapture:
				if state.hasName {
					p.createNamedCaptureAtIndex(state)
				}
				c := p.getCapture(state.captureIndex)
				c.kids = []*node{body}
				body = c
			case stGrouping:
				body = &node{kind: nGroup, kids: []*node{body}}
			default:
				body = &node{kind: nLook, neg: state.typ == stNegativeLookaround, behind: state.behind, kids: []*node{body}}
			}
			state = state.prev
			b = state.b
			b.addAtom(body)
			// For compatibility with JSC and ES3, quantifiers are allowed after
			// lookaheads.
		case '|':
			p.advance()
			state.alt++
			b.flushTerms()
			continue
		case '*', '+', '?':
			p.fail(errNothingToRepeat)
		case '^':
			p.advance()
			if b.flags&flagMultiline != 0 {
				b.addAssertion(assertStartOfLine)
			} else {
				b.addAssertion(assertStartOfInput)
			}
			continue
		case '$':
			p.advance()
			if b.flags&flagMultiline != 0 {
				b.addAssertion(assertEndOfLine)
			} else {
				b.addAssertion(assertEndOfInput)
			}
			continue
		case '.':
			p.advance()
			b.addTerm(&node{kind: nDot, fl: b.flags})
		case '(':
			state = p.parseOpenParenthesis(state)
			b = state.b
			continue
		case '[':
			cls := p.parseCharacterClass(b)
			b.addTerm(&node{kind: nClass, cls: cls, fl: b.flags})
		case '\\':
			switch p.next() {
			case endMarker:
				p.fail(errEscapeAtEndOfPattern)
			case '1', '2', '3', '4', '5', '6', '7', '8', '9':
				if index, ok := p.parseBackReferenceIndex(); ok {
					if state.isInsideCaptureGroup(index) {
						// Nothing can have been captured yet: V8 uses empty.
						b.addEmpty()
					} else {
						b.addAtom(&node{kind: nBackref, refs: []int{p.getCapture(index).index}, fl: b.flags})
					}
					break
				}
				// With /u, no identity escapes except for syntax characters
				// are allowed. Otherwise, all identity escapes are allowed.
				if p.isUnicodeMode() {
					p.fail(errInvalidEscape)
				}
				if first := p.next(); first == '8' || first == '9' {
					b.addCharacter(first)
					p.advanceBy(2)
					break
				}
				fallthrough
			case '0':
				p.advance()
				if p.isUnicodeMode() && isDecimalDigit(p.next()) {
					// With /u, decimal escapes with a leading 0 are not octal.
					p.fail(errInvalidDecimalEscape)
				}
				b.addCharacter(p.parseOctalLiteral())
			case 'b':
				p.advanceBy(2)
				b.addAssertion(assertBoundary)
				continue
			case 'B':
				p.advanceBy(2)
				b.addAssertion(assertNonBoundary)
				continue
			case 'd', 'D', 's', 'S', 'w', 'W', 'p', 'P':
				var items []classItem
				foldW := p.isUnicodeMode() && b.flags&flagIgnoreCase != 0
				if p.tryParseCharacterClassEscape(p.next(), notInClass, &items, foldW) {
					b.addTerm(&node{kind: nClass, cls: &classNode{items: items}, fl: b.flags})
				} else {
					p.advance() // Skip the backslash.
					b.addCharacter(p.cur)
					p.advance()
				}
			case 'k':
				// Either an identity escape or a named back reference: '\k' is an
				// identity escape for non-Unicode patterns without named capture
				// groups, and the start of a named back reference otherwise.
				if p.isUnicodeMode() || p.hasNamedCapturesScan(notInClass) {
					p.advanceBy(2)
					p.parseNamedBackReference(b, state)
					break
				}
				fallthrough
			default:
				c, escapedUnicode := p.parseCharacterEscape(notInClass)
				_ = escapedUnicode
				b.addCharacter(c)
			}
		case '{':
			if ok, _, _ := p.parseIntervalQuantifier(); ok {
				p.fail(errNothingToRepeat)
			}
			fallthrough
		case '}', ']':
			if p.isUnicodeMode() {
				p.fail(errLoneQuantifierBrackets)
			}
			fallthrough
		default:
			b.addCharacter(p.cur)
			p.advance()
		}

		var minRep, maxRep int
		switch p.cur {
		case '*':
			minRep, maxRep = 0, infinity
			p.advance()
		case '+':
			minRep, maxRep = 1, infinity
			p.advance()
		case '?':
			minRep, maxRep = 0, 1
			p.advance()
		case '{':
			ok, lo, hi := p.parseIntervalQuantifier()
			if ok {
				if hi < lo {
					p.fail(errRangeOutOfOrder)
				}
				minRep, maxRep = lo, hi
				break
			}
			if p.isUnicodeMode() {
				// Incomplete quantifiers are not allowed.
				p.fail(errIncompleteQuantifier)
			}
			continue
		default:
			continue
		}
		greedy := true
		if p.cur == '?' {
			greedy = false
			p.advance()
		}
		if !b.addQuantifierToAtom(minRep, maxRep, greedy, p.isUnicodeMode()) {
			p.fail(errInvalidQuantifier)
		}
	}
}

func flagFromChar(c rune) modFlags {
	switch c {
	case 'i':
		return flagIgnoreCase
	case 'm':
		return flagMultiline
	case 's':
		return flagDotAll
	}
	return 0
}

func (p *parser) parseOpenParenthesis(state *parserState) *parserState {
	behind := state.behind
	isNamedCapture := false
	var captureName string
	typ := stCapture
	flags := state.b.flags
	parsingModifiers := false
	modifiersPolarity := true
	var modifiers modFlags
	p.advance()
	if p.cur == '?' {
		for {
			switch p.next() {
			case '-':
				p.advance()
				parsingModifiers = true
				if !modifiersPolarity {
					p.fail(errMultipleFlagDashes)
				}
				modifiersPolarity = false
			case 'm', 'i', 's':
				p.advance()
				parsingModifiers = true
				flag := flagFromChar(p.cur)
				if modifiers&flag != 0 {
					p.fail(errRepeatedFlag)
				}
				modifiers |= flag
				if modifiersPolarity {
					flags |= flag
				} else {
					flags &^= flag
				}
			case ':':
				p.advanceBy(2)
				parsingModifiers = false
				typ = stGrouping
			case '=':
				if parsingModifiers {
					p.fail(errInvalidGroup)
				}
				p.advanceBy(2)
				behind = false
				typ = stPositiveLookaround
			case '!':
				if parsingModifiers {
					p.fail(errInvalidGroup)
				}
				p.advanceBy(2)
				behind = false
				typ = stNegativeLookaround
			case '<':
				p.advance()
				if parsingModifiers {
					p.fail(errInvalidGroup)
				}
				if p.next() == '=' {
					p.advanceBy(2)
					behind = true
					typ = stPositiveLookaround
					break
				} else if p.next() == '!' {
					p.advanceBy(2)
					behind = true
					typ = stNegativeLookaround
					break
				}
				isNamedCapture = true
				p.hasNamedCaptures = true
				p.advance()
			default:
				p.fail(errInvalidGroup)
			}
			if !parsingModifiers {
				break
			}
		}
	}
	if !modifiersPolarity && modifiers == 0 {
		// A dash without any flag.
		p.fail(errInvalidFlagGroup)
	}
	captureIndex := p.capturesStarted
	if typ == stCapture {
		if p.capturesStarted >= maxCaptures {
			p.fail(errTooManyCaptures)
		}
		p.capturesStarted++
		captureIndex = p.capturesStarted
		if isNamedCapture {
			captureName = p.parseCaptureGroupName()
		}
	}
	return &parserState{
		prev:         state,
		b:            &builder{flags: flags},
		typ:          typ,
		behind:       behind,
		captureIndex: captureIndex,
		captureName:  captureName,
		hasName:      isNamedCapture,
		disj:         p.newDisj(),
	}
}

func (p *parser) createNamedCaptureAtIndex(state *parserState) {
	path := state.prev.path()
	for _, other := range p.named {
		if other.name != state.captureName {
			continue
		}
		if mightBothParticipate(other.path, path) {
			p.fail(errDuplicateCaptureGroupName)
		}
	}
	c := p.getCapture(state.captureIndex)
	c.name = state.captureName
	p.named = append(p.named, namedCapture{name: state.captureName, index: state.captureIndex, path: path})
}

// mightBothParticipate is false when the two paths pass through different
// alternatives of the same disjunction.
func mightBothParticipate(a, b []altStep) bool {
	for _, x := range a {
		for _, y := range b {
			if x.disj == y.disj && x.alt != y.alt {
				return false
			}
		}
	}
	return true
}

func (p *parser) patchNamedBackReferences() {
	if len(p.namedRefs) == 0 {
		return
	}
	for _, ref := range p.namedRefs {
		for _, nc := range p.named {
			if nc.name == ref.name {
				ref.refs = append(ref.refs, nc.index)
			}
		}
		if len(ref.refs) == 0 {
			p.fail(errInvalidNamedCaptureReference)
		}
	}
}

// scanForCaptures counts all capture groups and notes named ones without
// otherwise validating the pattern.
func (p *parser) scanForCaptures(state inClassState) {
	saved := p.position()
	count := p.capturesStarted
	if state == inClass {
		for c := p.cur; c != endMarker; c = p.cur {
			p.advance()
			if c == '\\' {
				p.advance()
			} else if c == ']' {
				break
			}
		}
	}
	for n := p.cur; n != endMarker; n = p.cur {
		p.advance()
		switch n {
		case '\\':
			p.advance()
		case '[':
			for c := p.cur; c != endMarker; c = p.cur {
				p.advance()
				if c == '\\' {
					p.advance()
				} else if c == ']' {
					break
				}
			}
		case '(':
			if p.cur == '?' {
				// Only (?<name> is a capturing group among the (? forms.
				p.advance()
				if p.cur != '<' {
					continue
				}
				p.advance()
				if p.cur == '=' || p.cur == '!' {
					continue
				}
				p.hasNamedCaptures = true
			}
			count++
		}
	}
	p.captureCount = count
	p.scanned = true
	p.resetTo(saved)
}

func (p *parser) hasNamedCapturesScan(state inClassState) bool {
	if p.hasNamedCaptures || p.scanned {
		return p.hasNamedCaptures
	}
	p.scanForCaptures(state)
	return p.hasNamedCaptures
}

func (p *parser) parseBackReferenceIndex() (int, bool) {
	// Try to parse a decimal literal that is no greater than the total number
	// of left capturing parentheses in the input.
	start := p.position()
	value := int(p.next() - '0')
	p.advanceBy(2)
	for isDecimalDigit(p.cur) {
		value = 10*value + int(p.cur-'0')
		if value > maxCaptures {
			p.resetTo(start)
			return 0, false
		}
		p.advance()
	}
	if value > p.capturesStarted {
		if !p.scanned {
			p.scanForCaptures(notInClass)
		}
		if value > p.captureCount {
			p.resetTo(start)
			return 0, false
		}
	}
	return value, true
}

func (p *parser) parseNamedBackReference(b *builder, state *parserState) {
	// The parser is on the '<' in \k<name>.
	if p.cur != '<' {
		p.fail(errInvalidNamedReference)
	}
	p.advance()
	name := p.parseCaptureGroupName()
	if state.isInsideCaptureGroupNamed(name) {
		b.addEmpty()
		return
	}
	ref := &node{kind: nBackref, name: name, fl: b.flags}
	b.addAtom(ref)
	p.namedRefs = append(p.namedRefs, ref)
}

// parseCaptureGroupName parses a RegExpIdentifierName up to and including
// the closing '>'. The current character is the first character of the name.
// Names are always read in Unicode mode (surrogate pairs and \u{...}).
func (p *parser) parseCaptureGroupName() string {
	p.nextPos = p.curStart
	p.forceUnicode = true
	var name []rune
	atStart := true
	for {
		p.advance()
		c := p.cur
		if c == '\\' && p.next() == 'u' {
			p.advanceBy(2)
			v, ok := p.parseUnicodeEscape()
			if !ok {
				p.fail(errInvalidUnicodeEscape)
			}
			c = v
			// Re-read the character after the escape on the next iteration.
			p.nextPos = p.curStart
		}
		// The backslash is never part of an identifier.
		if c == '\\' {
			p.fail(errInvalidCaptureGroupName)
		}
		if atStart {
			if !isIdentifierStart(c) {
				p.fail(errInvalidCaptureGroupName)
			}
			name = append(name, c)
			atStart = false
			continue
		}
		if c == '>' {
			p.forceUnicode = false
			p.advance()
			break
		}
		if !isIdentifierPart(c) {
			p.fail(errInvalidCaptureGroupName)
		}
		name = append(name, c)
	}
	p.forceUnicode = false
	return string(name)
}

func isIdentifierStart(c rune) bool {
	if c == '$' || c == '_' {
		return true
	}
	if c > unicode.MaxRune {
		return false
	}
	return unicode.In(c, unicode.L, unicode.Nl, unicode.Other_ID_Start) &&
		!unicode.In(c, unicode.Pattern_Syntax, unicode.Pattern_White_Space)
}

func isIdentifierPart(c rune) bool {
	if c == '$' || c == '_' || c == 0x200C || c == 0x200D {
		return true
	}
	if c > unicode.MaxRune {
		return false
	}
	return unicode.In(c, unicode.L, unicode.Nl, unicode.Other_ID_Start, unicode.Mn, unicode.Mc,
		unicode.Nd, unicode.Pc, unicode.Other_ID_Continue) &&
		!unicode.In(c, unicode.Pattern_Syntax, unicode.Pattern_White_Space)
}

func (p *parser) parseIntervalQuantifier() (ok bool, minOut, maxOut int) {
	start := p.position()
	p.advance()
	if !isDecimalDigit(p.cur) {
		p.resetTo(start)
		return false, 0, 0
	}
	lo := 0
	for isDecimalDigit(p.cur) {
		next := int(p.cur - '0')
		if lo > (infinity-next)/10 {
			// Overflow: skip the remaining digits.
			for {
				p.advance()
				if !isDecimalDigit(p.cur) {
					break
				}
			}
			lo = infinity
			break
		}
		lo = 10*lo + next
		p.advance()
	}
	hi := 0
	switch p.cur {
	case '}':
		hi = lo
		p.advance()
	case ',':
		p.advance()
		if p.cur == '}' {
			hi = infinity
			p.advance()
		} else {
			for isDecimalDigit(p.cur) {
				next := int(p.cur - '0')
				if hi > (infinity-next)/10 {
					for {
						p.advance()
						if !isDecimalDigit(p.cur) {
							break
						}
					}
					hi = infinity
					break
				}
				hi = 10*hi + next
				p.advance()
			}
			if p.cur != '}' {
				p.resetTo(start)
				return false, 0, 0
			}
			p.advance()
		}
	default:
		p.resetTo(start)
		return false, 0, 0
	}
	return true, lo, hi
}

// parseOctalLiteral parses up to three octal digits with a value below 256
// (Annex B LegacyOctalEscapeSequence).
func (p *parser) parseOctalLiteral() rune {
	value := p.cur - '0'
	p.advance()
	if p.cur >= '0' && p.cur <= '7' {
		value = value*8 + p.cur - '0'
		p.advance()
		if value < 32 && p.cur >= '0' && p.cur <= '7' {
			value = value*8 + p.cur - '0'
			p.advance()
		}
	}
	return value
}

func (p *parser) parseHexEscape(length int) (rune, bool) {
	start := p.position()
	var val rune
	for range length {
		d := hexValue(p.cur)
		if d < 0 {
			p.resetTo(start)
			return 0, false
		}
		val = val*16 + rune(d)
		p.advance()
	}
	return val, true
}

func (p *parser) parseUnlimitedLengthHexNumber(maxValue rune) (rune, bool) {
	d := hexValue(p.cur)
	if d < 0 {
		return 0, false
	}
	var x rune
	for d >= 0 {
		x = x*16 + rune(d)
		if x > maxValue {
			return 0, false
		}
		p.advance()
		d = hexValue(p.cur)
	}
	return x, true
}

// parseUnicodeEscape parses the part after \u: XXXX, or {X...} in Unicode
// mode, combining an escaped surrogate pair in Unicode mode.
func (p *parser) parseUnicodeEscape() (rune, bool) {
	if p.cur == '{' && p.isUnicodeMode() {
		start := p.position()
		p.advance()
		if v, ok := p.parseUnlimitedLengthHexNumber(0x10FFFF); ok {
			if p.cur == '}' {
				p.advance()
				return v, true
			}
		}
		p.resetTo(start)
		return 0, false
	}
	value, ok := p.parseHexEscape(4)
	if ok && p.isUnicodeMode() && isLeadSurrogate(value) && p.cur == '\\' {
		start := p.position()
		if p.next() == 'u' {
			p.advanceBy(2)
			if trail, ok := p.parseHexEscape(4); ok && isTrailSurrogate(trail) {
				return utf16.DecodeRune(value, trail), true
			}
		}
		p.resetTo(start)
	}
	return value, ok
}

// parseCharacterEscape parses a CharacterEscape; the current character is
// the backslash. The bool result reports a \u escape.
func (p *parser) parseCharacterEscape(state inClassState) (rune, bool) {
	p.advance() // Past the backslash.
	c := p.cur
	switch c {
	case 'f':
		p.advance()
		return '\f', false
	case 'n':
		p.advance()
		return '\n', false
	case 'r':
		p.advance()
		return '\r', false
	case 't':
		p.advance()
		return '\t', false
	case 'v':
		p.advance()
		return '\v', false
	case 'c':
		controlLetter := p.next()
		letter := controlLetter &^ ('A' ^ 'a')
		if letter >= 'A' && letter <= 'Z' {
			p.advanceBy(2)
			return controlLetter & 0x1F, false
		}
		if p.isUnicodeMode() {
			p.fail(errInvalidUnicodeEscape)
		}
		if state == inClass {
			// Annex B ClassControlLetter: digits and underscore.
			if isDecimalDigit(controlLetter) || controlLetter == '_' {
				p.advanceBy(2)
				return controlLetter & 0x1F, false
			}
		}
		// Read the backslash as a literal character.
		return '\\', false
	case '0':
		// \0 is NUL if not followed by another digit.
		if !isDecimalDigit(p.next()) {
			p.advance()
			return 0, false
		}
		fallthrough
	case '1', '2', '3', '4', '5', '6', '7':
		// A decimal escape that is not a back reference is a legacy octal
		// escape outside Unicode mode.
		if p.isUnicodeMode() {
			p.fail(errInvalidDecimalEscape)
		}
		return p.parseOctalLiteral(), false
	case 'x':
		p.advance()
		if v, ok := p.parseHexEscape(2); ok {
			return v, false
		}
		if p.isUnicodeMode() {
			p.fail(errInvalidEscape)
		}
		return 'x', false
	case 'u':
		p.advance()
		if v, ok := p.parseUnicodeEscape(); ok {
			return v, true
		}
		if p.isUnicodeMode() {
			p.fail(errInvalidUnicodeEscape)
		}
		return 'u', false
	}
	if !p.isUnicodeMode() {
		if c != 'k' || !p.hasNamedCapturesScan(state) {
			p.advance()
			return c, false
		}
	} else if isSyntaxCharacterOrSlash(c) {
		p.advance()
		return c, false
	}
	p.fail(errInvalidEscape)
	return 0, false
}

func (p *parser) tryParseCharacterClassEscape(next rune, state inClassState, items *[]classItem, foldW bool) bool {
	switch next {
	case 'd', 'D', 's', 'S', 'w', 'W':
		*items = append(*items, classItem{esc: byte(next), foldW: foldW})
		p.advanceBy(2)
		return true
	case 'p', 'P':
		if !p.isUnicodeMode() {
			return false
		}
		negate := next == 'P'
		p.advanceBy(2)
		name, ok := p.parsePropertyClassName()
		if !ok || !validProperty(name) {
			if state == inClass {
				p.fail(errInvalidClassPropertyName)
			}
			p.fail(errInvalidPropertyName)
		}
		*items = append(*items, classItem{esc: 'p', neg: negate, prop: name})
		return true
	}
	return false
}

func isUnicodePropertyValueCharacter(c rune) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_'
}

func (p *parser) parsePropertyClassName() (string, bool) {
	if p.cur != '{' {
		return "", false
	}
	var name []byte
	for p.advance(); p.cur != '}' && p.cur != '='; p.advance() {
		if !isUnicodePropertyValueCharacter(p.cur) || !p.hasNext() {
			return "", false
		}
		name = append(name, byte(p.cur))
	}
	if p.cur == '=' {
		name = append(name, '=')
		for p.advance(); p.cur != '}'; p.advance() {
			if !isUnicodePropertyValueCharacter(p.cur) || !p.hasNext() {
				return "", false
			}
			name = append(name, byte(p.cur))
		}
	}
	p.advance()
	return string(name), true
}

func (p *parser) parseClassEscape(items *[]classItem, foldW bool) (rune, bool) {
	if p.cur != '\\' {
		c := p.cur
		p.advance()
		return c, false
	}
	next := p.next()
	switch next {
	case 'b':
		p.advanceBy(2)
		return '\b', false
	case '-':
		if p.isUnicodeMode() {
			p.advanceBy(2)
			return '-', false
		}
	case endMarker:
		p.fail(errEscapeAtEndOfPattern)
	}
	if p.tryParseCharacterClassEscape(next, inClass, items, foldW) {
		return 0, true
	}
	c, _ := p.parseCharacterEscape(inClass)
	return c, false
}

func (p *parser) parseCharacterClass(b *builder) *classNode {
	p.advance() // Past '['.
	cls := &classNode{}
	if p.cur == '^' {
		cls.negated = true
		p.advance()
	}
	foldW := p.isUnicodeMode() && b.flags&flagIgnoreCase != 0
	single := func(c rune) { cls.items = append(cls.items, classItem{lo: c, hi: c}) }
	for p.hasMore && p.cur != ']' {
		c1, isClass1 := p.parseClassEscape(&cls.items, foldW)
		if p.cur != '-' {
			if !isClass1 {
				single(c1)
			}
			continue
		}
		p.advance()
		if p.cur == endMarker {
			// Let the check below report the error.
			break
		}
		if p.cur == ']' {
			if !isClass1 {
				single(c1)
			}
			single('-')
			break
		}
		c2, isClass2 := p.parseClassEscape(&cls.items, foldW)
		if isClass1 || isClass2 {
			// Either end is an escaped character class: '-' is literal.
			if p.isUnicodeMode() {
				p.fail(errInvalidCharacterClass)
			}
			if !isClass1 {
				single(c1)
			}
			single('-')
			if !isClass2 {
				single(c2)
			}
			continue
		}
		if c1 > c2 {
			p.fail(errOutOfOrderCharacterClass)
		}
		cls.items = append(cls.items, classItem{lo: c1, hi: c2})
	}
	if !p.hasMore {
		p.fail(errUnterminatedCharacterClass)
	}
	p.advance()
	return cls
}

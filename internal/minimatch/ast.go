package minimatch

import (
	"strings"
	"unicode/utf8"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/minimatch/jsregex"
)

// Extglob AST follows minimatch's ast.js: adoption, usurp, fillNegs, and the
// empty-extglob quirks. Character indexing is by Go bytes. ASCII globs match
// npm; a non-BMP code point is one Go rune rather than a surrogate pair, and
// the in-class scan that skips '[' uses byte offsets.

const (
	maxExtDepth = 2

	startNoTraversal = `(?!(?:^|/)\.\.?(?:$|/))`
	startNoDot       = `(?!\.)`
	qmarkSrc         = `[^/]`
	starSrc          = qmarkSrc + `*?`
	starNoEmpty      = qmarkSrc + `+?`
	endNegSrc        = `(?:$|\/)`
)

type magicState int

const (
	magicUnset magicState = iota
	magicNo
	magicYes
)

type globOpts struct {
	dot, nocase, noext bool
}

type astPart struct {
	str string
	ast *ast
}

type ast struct {
	typ         string
	parent      *ast
	root        *ast
	parts       []astPart
	parentIndex int
	negs        []*ast
	filledNegs  bool
	emptyExt    bool
	magic       magicState
	uflag       bool
	toStr       string
	toStrSet    bool
	dot         bool
	nocase      bool
	noext       bool
}

func newAST(typ string, parent *ast) *ast {
	a := &ast{typ: typ, parent: parent}
	if parent == nil {
		a.root = a
	} else {
		a.root = parent.root
		a.parentIndex = len(parent.parts)
	}
	if typ != "" {
		a.magic = magicYes
	}
	if typ == "!" && a.root != nil && !a.root.filledNegs {
		a.root.negs = append(a.root.negs, a)
	}
	return a
}

func (a *ast) push(parts ...astPart) {
	for _, p := range parts {
		if p.ast == nil && p.str == "" {
			continue
		}
		a.parts = append(a.parts, p)
	}
}

func (a *ast) String() string {
	if a.toStrSet {
		return a.toStr
	}
	var b strings.Builder
	if a.typ == "" {
		for _, p := range a.parts {
			if p.ast != nil {
				b.WriteString(p.ast.String())
			} else {
				b.WriteString(p.str)
			}
		}
	} else {
		b.WriteString(a.typ)
		b.WriteByte('(')
		for i, p := range a.parts {
			if i > 0 {
				b.WriteByte('|')
			}
			if p.ast != nil {
				b.WriteString(p.ast.String())
			} else {
				b.WriteString(p.str)
			}
		}
		b.WriteByte(')')
	}
	a.toStr = b.String()
	a.toStrSet = true
	return a.toStr
}

func (a *ast) copyIn(part astPart) {
	if part.ast == nil {
		a.push(part)
		return
	}
	a.push(astPart{ast: part.ast.clone(a)})
}

func (a *ast) clone(parent *ast) *ast {
	c := newAST(a.typ, parent)
	for _, p := range a.parts {
		c.copyIn(p)
	}
	return c
}

func (a *ast) fillNegs() {
	if a != a.root || a.filledNegs {
		return
	}
	a.String()
	a.filledNegs = true
	for len(a.negs) > 0 {
		n := a.negs[len(a.negs)-1]
		a.negs = a.negs[:len(a.negs)-1]
		if n.typ != "!" {
			continue
		}
		p := n
		pp := p.parent
		for pp != nil {
			if pp.typ == "" {
				for i := p.parentIndex + 1; i < len(pp.parts); i++ {
					for _, part := range n.parts {
						if part.ast == nil {
							continue
						}
						part.ast.copyIn(pp.parts[i])
					}
				}
			}
			p = pp
			pp = p.parent
		}
	}
}

func (a *ast) isStart() bool {
	if a.root == a {
		return true
	}
	if a.parent == nil || !a.parent.isStart() {
		return false
	}
	if a.parentIndex == 0 {
		return true
	}
	for i := 0; i < a.parentIndex && i < len(a.parent.parts); i++ {
		pp := a.parent.parts[i]
		if pp.ast == nil || pp.ast.typ != "!" {
			return false
		}
	}
	return true
}

func (a *ast) isEnd() bool {
	if a.root == a {
		return true
	}
	if a.parent != nil && a.parent.typ == "!" {
		return true
	}
	if a.parent == nil || !a.parent.isEnd() {
		return false
	}
	if a.typ == "" {
		return true
	}
	return a.parentIndex == len(a.parent.parts)-1
}

func fromGlob(pattern string, opt globOpts) *ast {
	root := newAST("", nil)
	root.dot = opt.dot
	root.nocase = opt.nocase
	root.noext = opt.noext
	parseAST(pattern, root, 0, opt, 0)
	return root
}

func step(s string, i int) (string, int) {
	if i >= len(s) {
		return "", i
	}
	if s[i] < 0x80 {
		return s[i : i+1], i + 1
	}
	r, w := utf8.DecodeRuneInString(s[i:])
	if r == utf8.RuneError && w == 1 {
		return s[i : i+1], i + 1
	}
	return string(r), i + w
}

func isExtType(c string) bool {
	switch c {
	case "!", "?", "+", "*", "@":
		return true
	default:
		return false
	}
}

func parseAST(str string, a *ast, pos int, opt globOpts, extDepth int) int {
	escaping := false
	inBrace := false
	braceStart := -1
	braceNeg := false
	if a.typ == "" {
		i := pos
		var acc strings.Builder
		for i < len(str) {
			c, n := step(str, i)
			i = n
			if escaping || c == `\` {
				escaping = !escaping
				acc.WriteString(c)
				continue
			}
			if inBrace {
				if i == braceStart+1 {
					if c == "^" || c == "!" {
						braceNeg = true
					}
				} else if c == "]" && !(i == braceStart+2 && braceNeg) {
					inBrace = false
				}
				acc.WriteString(c)
				continue
			} else if c == "[" {
				inBrace = true
				braceStart = i
				braceNeg = false
				acc.WriteString(c)
				continue
			}
			if !opt.noext && isExtType(c) && i < len(str) && str[i] == '(' && extDepth <= maxExtDepth {
				a.push(astPart{str: acc.String()})
				acc.Reset()
				ext := newAST(c, a)
				i = parseAST(str, ext, i, opt, extDepth+1)
				a.push(astPart{ast: ext})
				continue
			}
			acc.WriteString(c)
		}
		a.push(astPart{str: acc.String()})
		return i
	}
	i := pos + 1
	part := newAST("", a)
	var parts []*ast
	var acc strings.Builder
	for i < len(str) {
		c, n := step(str, i)
		i = n
		if escaping || c == `\` {
			escaping = !escaping
			acc.WriteString(c)
			continue
		}
		if inBrace {
			if i == braceStart+1 {
				if c == "^" || c == "!" {
					braceNeg = true
				}
			} else if c == "]" && !(i == braceStart+2 && braceNeg) {
				inBrace = false
			}
			acc.WriteString(c)
			continue
		} else if c == "[" {
			inBrace = true
			braceStart = i
			braceNeg = false
			acc.WriteString(c)
			continue
		}
		adoptable := a.canAdoptType(c, adoptAny)
		if !opt.noext && isExtType(c) && i < len(str) && str[i] == '(' && (extDepth <= maxExtDepth || adoptable) {
			depthAdd := 1
			if adoptable {
				depthAdd = 0
			}
			part.push(astPart{str: acc.String()})
			acc.Reset()
			ext := newAST(c, part)
			part.push(astPart{ast: ext})
			i = parseAST(str, ext, i, opt, extDepth+depthAdd)
			continue
		}
		if c == "|" {
			part.push(astPart{str: acc.String()})
			acc.Reset()
			parts = append(parts, part)
			part = newAST("", a)
			continue
		}
		if c == ")" {
			if acc.Len() == 0 && len(a.parts) == 0 {
				a.emptyExt = true
			}
			part.push(astPart{str: acc.String()})
			acc.Reset()
			ps := make([]astPart, 0, len(parts)+1)
			for _, p := range parts {
				ps = append(ps, astPart{ast: p})
			}
			ps = append(ps, astPart{ast: part})
			a.push(ps...)
			return i
		}
		acc.WriteString(c)
	}
	a.typ = ""
	a.magic = magicUnset
	from := pos - 1
	if from < 0 {
		from = 0
	}
	a.parts = []astPart{{str: str[from:]}}
	return i
}

const (
	adoptNormal = iota
	adoptSpace
	adoptAny
)

func adoptList(parent string, kind int) []string {
	switch kind {
	case adoptSpace:
		switch parent {
		case "!":
			return []string{"?"}
		case "@":
			return []string{"?"}
		case "+":
			return []string{"?", "*"}
		}
	case adoptAny:
		switch parent {
		case "!":
			return []string{"?", "@"}
		case "?":
			return []string{"?", "@"}
		case "@":
			return []string{"?", "@"}
		case "*":
			return []string{"*", "+", "?", "@"}
		case "+":
			return []string{"+", "@", "?", "*"}
		}
	default:
		switch parent {
		case "!":
			return []string{"@"}
		case "?":
			return []string{"?", "@"}
		case "@":
			return []string{"@"}
		case "*":
			return []string{"*", "+", "?", "@"}
		case "+":
			return []string{"+", "@"}
		}
	}
	return nil
}

func listHas(list []string, c string) bool {
	for _, s := range list {
		if s == c {
			return true
		}
	}
	return false
}

func (a *ast) canAdoptType(c string, kind int) bool {
	return listHas(adoptList(a.typ, kind), c)
}

func (a *ast) canAdopt(child *ast, kind int) bool {
	if child == nil || child.typ != "" || len(child.parts) != 1 || a.typ == "" {
		return false
	}
	gc := child.parts[0]
	if gc.ast == nil || gc.ast.typ == "" {
		return false
	}
	return a.canAdoptType(gc.ast.typ, kind)
}

func (a *ast) adopt(child *ast, index int) {
	gc := child.parts[0].ast
	moved := append([]astPart(nil), gc.parts...)
	rest := append([]astPart(nil), a.parts[index+1:]...)
	a.parts = append(append(a.parts[:index], moved...), rest...)
	for _, p := range moved {
		if p.ast != nil {
			p.ast.parent = a
		}
	}
	a.toStrSet = false
}

func (a *ast) adoptWithSpace(child *ast, index int) {
	gc := child.parts[0].ast
	blank := newAST("", gc)
	blank.parts = append(blank.parts, astPart{str: ""})
	gc.push(astPart{ast: blank})
	a.adopt(child, index)
}

func usurpType(parent, child string) (string, bool) {
	switch parent {
	case "!":
		if child == "!" {
			return "@", true
		}
	case "?":
		if child == "*" || child == "+" {
			return "*", true
		}
	case "@":
		switch child {
		case "!", "?", "@", "*", "+":
			return child, true
		}
	case "+":
		if child == "?" || child == "*" {
			return "*", true
		}
	}
	return "", false
}

func (a *ast) canUsurp(child *ast) bool {
	if child == nil || child.typ != "" || len(child.parts) != 1 || a.typ == "" || len(a.parts) != 1 {
		return false
	}
	gc := child.parts[0]
	if gc.ast == nil || gc.ast.typ == "" {
		return false
	}
	_, ok := usurpType(a.typ, gc.ast.typ)
	return ok
}

func (a *ast) usurp(child *ast) {
	gc := child.parts[0].ast
	nt, ok := usurpType(a.typ, gc.typ)
	if !ok {
		return
	}
	a.parts = append([]astPart(nil), gc.parts...)
	for _, p := range a.parts {
		if p.ast != nil {
			p.ast.parent = a
		}
	}
	a.typ = nt
	a.toStrSet = false
	a.emptyExt = false
}

func (a *ast) flatten() {
	if a.typ == "" {
		for _, p := range a.parts {
			if p.ast != nil {
				p.ast.flatten()
			}
		}
	} else {
		iter := 0
		for {
			done := true
			for i := 0; i < len(a.parts); i++ {
				c := a.parts[i]
				if c.ast == nil {
					continue
				}
				c.ast.flatten()
				switch {
				case a.canAdopt(c.ast, adoptNormal):
					done = false
					a.adopt(c.ast, i)
				case a.canAdopt(c.ast, adoptSpace):
					done = false
					a.adoptWithSpace(c.ast, i)
				case a.canUsurp(c.ast):
					done = false
					a.usurp(c.ast)
				}
			}
			iter++
			if done || iter >= 10 {
				break
			}
		}
	}
	a.toStrSet = false
}

func (a *ast) toMMPattern() piece {
	glob := a.String()
	re, body, magic, uflag := a.toRegExpSource(nil)
	any := magic
	if a.root.nocase && js.ToUpper(glob) != js.ToLower(glob) {
		any = true
	}
	if !any {
		return piece{kind: kindLit, s: body}
	}
	flags := ""
	if a.root.nocase {
		flags = "i"
	}
	if uflag {
		flags += "u"
	}
	comp, err := jsregex.Compile("^"+re+"$", flags)
	if err != nil {
		return piece{bad: true}
	}
	return piece{kind: kindRE, src: re, flags: flags, re: comp}
}

func (a *ast) toRegExpSource(allowDot *bool) (re, body string, magic, uflag bool) {
	dot := a.root.dot
	if allowDot != nil {
		dot = *allowDot
	}
	if a.root == a {
		a.flatten()
		a.fillNegs()
	}
	if a.typ == "" {
		noEmpty := a.isStart() && a.isEnd()
		if noEmpty {
			for _, p := range a.parts {
				if p.ast != nil {
					noEmpty = false
					break
				}
			}
		}
		var src strings.Builder
		for _, p := range a.parts {
			var partRe string
			var partMagic, partU bool
			if p.ast == nil {
				partRe, partMagic, partU = parseGlob(p.str, noEmpty)
			} else {
				partRe, _, partMagic, partU = p.ast.toRegExpSource(allowDot)
			}
			if a.magic == magicYes || partMagic {
				a.magic = magicYes
			} else if a.magic == magicUnset {
				a.magic = magicNo
			}
			if partU {
				a.uflag = true
			}
			src.WriteString(partRe)
		}
		s := src.String()
		start := ""
		if a.isStart() && len(a.parts) > 0 && a.parts[0].ast == nil {
			only := ""
			if len(a.parts) == 1 {
				only = a.parts[0].str
			}
			dotTravAllowed := len(a.parts) == 1 && (only == "." || only == "..")
			if !dotTravAllowed {
				needNoTrav := (dot && srcStartsPattern(s, 0)) ||
					(strings.HasPrefix(s, `\.`) && srcStartsPattern(s, 2)) ||
					(strings.HasPrefix(s, `\.\.`) && srcStartsPattern(s, 4))
				allowDotFalse := allowDot == nil || !*allowDot
				needNoDot := !dot && allowDotFalse && srcStartsPattern(s, 0)
				switch {
				case needNoTrav:
					start = startNoTraversal
				case needNoDot:
					start = startNoDot
				}
			}
		}
		end := ""
		if a.isEnd() && a.root.filledNegs && a.parent != nil && a.parent.typ == "!" {
			end = endNegSrc
		}
		if a.magic != magicYes {
			a.magic = magicNo
		}
		return start + s + end, unescape(s), a.magic == magicYes, a.uflag
	}

	repeated := a.typ == "*" || a.typ == "+"
	start := "(?:"
	if a.typ == "!" {
		start = "(?:(?!(?:"
	}
	body = a.partsToRegExp(dot)
	if a.isStart() && a.isEnd() && body == "" && a.typ != "!" {
		s := a.String()
		a.parts = []astPart{{str: s}}
		a.typ = ""
		a.magic = magicUnset
		return s, unescape(a.String()), false, false
	}
	bodyDotAllowed := ""
	explicitAllow := allowDot != nil && *allowDot
	if repeated && !explicitAllow && !dot {
		bodyDotAllowed = a.partsToRegExp(true)
	}
	if bodyDotAllowed == body {
		bodyDotAllowed = ""
	}
	if bodyDotAllowed != "" {
		body = "(?:" + body + ")(?:" + bodyDotAllowed + ")*?"
	}
	var final string
	if a.typ == "!" && a.emptyExt {
		if a.isStart() && !dot {
			final = startNoDot + starNoEmpty
		} else {
			final = starNoEmpty
		}
	} else {
		allowDotFalse := allowDot == nil || !*allowDot
		var close string
		switch {
		case a.typ == "!":
			close = "))"
			if a.isStart() && !dot && allowDotFalse {
				close += startNoDot
			}
			close += starSrc + ")"
		case a.typ == "@":
			close = ")"
		case a.typ == "?":
			close = ")?"
		case a.typ == "+" && bodyDotAllowed != "":
			close = ")"
		case a.typ == "*" && bodyDotAllowed != "":
			close = ")?"
		default:
			close = ")" + a.typ
		}
		final = start + body + close
	}
	if a.magic != magicYes {
		a.magic = magicYes
	}
	return final, unescape(body), true, a.uflag
}

func (a *ast) partsToRegExp(dot bool) string {
	allow := dot
	se := a.isStart() && a.isEnd()
	bits := make([]string, 0, len(a.parts))
	for _, p := range a.parts {
		if p.ast == nil {
			continue
		}
		re, _, _, u := p.ast.toRegExpSource(&allow)
		if u {
			a.uflag = true
		}
		if se && re == "" {
			continue
		}
		bits = append(bits, re)
	}
	return strings.Join(bits, "|")
}

func srcStartsPattern(src string, i int) bool {
	if i < 0 || i >= len(src) {
		return false
	}
	return src[i] == '[' || src[i] == '.'
}

func parseGlob(glob string, noEmpty bool) (string, bool, bool) {
	onlyStars := glob != ""
	for i := 0; i < len(glob); i++ {
		if glob[i] != '*' {
			onlyStars = false
			break
		}
	}
	escaping := false
	inStar := false
	var b strings.Builder
	magic := false
	uflag := false
	for i := 0; i < len(glob); {
		c, n := step(glob, i)
		if escaping {
			escaping = false
			if len(c) == 1 && isRegSpecial(c[0]) {
				b.WriteByte('\\')
			}
			b.WriteString(c)
			i = n
			continue
		}
		if c == "*" {
			if !inStar {
				inStar = true
				if noEmpty && onlyStars {
					b.WriteString(starNoEmpty)
				} else {
					b.WriteString(starSrc)
				}
				magic = true
			}
			i = n
			continue
		}
		inStar = false
		if c == `\` {
			if n >= len(glob) {
				b.WriteString(`\\`)
			} else {
				escaping = true
			}
			i = n
			continue
		}
		if c == "[" {
			src, u, consumed, mg := parseClass(glob, i)
			if consumed > 0 {
				b.WriteString(src)
				uflag = uflag || u
				magic = magic || mg
				i += consumed
				continue
			}
		}
		if c == "?" {
			b.WriteString(qmarkSrc)
			magic = true
			i = n
			continue
		}
		b.WriteString(regExpEscape(c))
		i = n
	}
	return b.String(), magic, uflag
}

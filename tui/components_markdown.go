package tui

import (
	"runtime"
	"strconv"
	"strings"
	"unicode/utf8"
	"weak"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsre"
	"github.com/keejkrej/pi-go/internal/marked"
)

// Match GFM's strict double-tilde strikethrough without single-tilde false positives in chat text.
var cmStrictStrikethroughRegex = jsre.MustCompile(`^(~~)(?=[^\s~])((?:\\.|[^\\])*?(?:\\.|[^\s~\\]))\1(?=[^~]|$)`, "")

var (
	cmGfmUrlRegex              = jsre.MustCompile(`^((?:[hH][tT][tT][pP][sS]?|[fF][tT][pP]):\/\/|www\.)(?:[a-zA-Z0-9\-]+\.?)+[^\s<]*|^[A-Za-z0-9._+-]+(@)[a-zA-Z0-9-_]+(?:\.[a-zA-Z0-9-_]*[a-zA-Z0-9])+(?![-_])`, "")
	cmGfmBackpedalRegex        = jsre.MustCompile(`(?:[^?!.,:;*_'"~()&]+|\([^)]*\)|&(?![a-zA-Z0-9]+;$)|[?!.,:;*_'"~)]+(?!$))+`, "")
	cmUnicodeAlphaNumericRegex = jsre.MustCompile(`[\p{L}\p{N}]`, "u")
	cmMarkedPunctuationRegex   = jsre.MustCompile(`^((?![*_])[\s\p{P}\p{S}])`, "u")
	cmLDelimGfmRegex           = jsre.MustCompile(`^(?:\*+(?:((?!\*)(?!~)[\p{P}\p{S}])|([^\s*]))?)|^_+(?:((?!_)(?!~)[\p{P}\p{S}])|([^\s_]))?`, "u")

	cmCjkRanges = `\u1100-\u11ff\u20a9\u2329-\u232a\u2630-\u2637\u268a-\u268f\u2e80-\u2e99\u2e9b-\u2ef3\u2f00-\u2fd5\u2ff0-\u303e\u3041-\u3096\u3099-\u30ff\u3105-\u312f\u3131-\u318e\u3190-\u31e5\u31ef-\u321e\u3220-\u3247\u3250-\ua48c\ua490-\ua4c6\ua960-\ua97c\uac00-\ud7a3\ud7b0-\ud7c6\ud7cb-\ud7fb\uf900-\ufaff\ufe10-\ufe19\ufe30-\ufe52\ufe54-\ufe66\ufe68-\ufe6b\uff01-\uffbe\uffc2-\uffc7\uffca-\uffcf\uffd2-\uffd7\uffda-\uffdc\uffe0-\uffe6\uffe8-\uffee\u{16fe0}-\u{16fe4}\u{16ff0}-\u{16ff6}\u{17000}-\u{18cda}\u{18cff}-\u{18d20}\u{18d80}-\u{18df2}\u{18e00}-\u{19191}\u{191a0}-\u{191d2}\u{1aff0}-\u{1aff3}\u{1aff5}-\u{1affb}\u{1affd}-\u{1affe}\u{1b000}-\u{1b128}\u{1b132}\u{1b150}-\u{1b152}\u{1b155}\u{1b164}-\u{1b168}\u{1b170}-\u{1b2fb}\u{1d300}-\u{1d356}\u{1d360}-\u{1d376}\u{1f1ae}\u{1f200}\u{1f202}\u{1f210}-\u{1f219}\u{1f21b}-\u{1f22e}\u{1f230}-\u{1f231}\u{1f237}\u{1f23b}\u{1f240}-\u{1f248}\u{1f260}-\u{1f265}\u{1f7da}\u{20000}-\u{3fffd}`
	cmCjkRegexp = jsre.MustCompile(`[`+cmCjkRanges+`]`, "u")

	cmCjkPunctGfm         = `(?:(?!~)[\p{P}\p{S}])`
	cmCjkPunctSpaceGfm    = `(?:(?!~)[\s\p{P}\p{S}])`
	cmCjkNotPunctSpaceGfm = `(?:[^\s\p{P}\p{S}]|~)`
	cmCjkPunct            = `[\p{P}\p{S}]`
	cmCjkPunctSpace       = `[\s\p{P}\p{S}]`
	cmCjkNotPunctSpace    = `[^\s\p{P}\p{S}]`

	cmRDelimAstGfm = cmBuildRDelimAst(cmCjkPunctGfm, cmCjkPunctSpaceGfm, cmCjkNotPunctSpaceGfm)
	cmRDelimUnd    = cmBuildRDelimUnd(cmCjkPunct, cmCjkPunctSpace, cmCjkNotPunctSpace)

	cmLatexBlockOpenRegex  = jsre.MustCompile(`^ {0,3}(\$\$|\\\[)`, "")
	cmLatexBlockStartRegex = jsre.MustCompile(`(?:^|\n) {0,3}(\$\$|\\\[)`, "")
	cmTableDelimiterRegex  = jsre.MustCompile(`^ {0,3}(?:\| *)?(?::?-+:?)(?: *\| *:?-+:?)* *\|? *$`, "")
	cmFenceOpenRegex       = jsre.MustCompile(`^ {0,3}(`+"`"+`{3,}|~{3,})`, "")
	cmHtmlCommentRegex     = jsre.MustCompile(`<!--[\s\S]*?(?:-->|$)`, "g")
	cmAnsiEscapeRegex      = jsre.MustCompile(`\x1b\[[0-9;]*m|\x1b\]8;;[^\x1b\x07]*(?:\x1b\\|\x07)`, "g")
	cmOrderedRawRegex      = jsre.MustCompile(`^(?: {0,3})(\d{1,9}[.)])[ \t]+`, "")
	cmUnorderedRawRegex    = jsre.MustCompile(`^(?: {0,3})([-+*])(?:[ \t]+|(?=\r?\n|$))`, "")
	cmShellVarSpanRegex    = jsre.MustCompile(`^[A-Z_][A-Z0-9_]*[/:;,-]?$`, "")
)

func cmBuildRDelimAst(punct, punctSpace, notPunctSpace string) *jsre.Regexp {
	cjk := `[` + cmCjkRanges + `]`
	nonCjk := `[^*` + cmCjkRanges + `]`
	// Capture layout:
	//   [1],[2] pure right-flanking
	//   [3],[4] pure left-flanking (nested opener)
	//   [5]     same-side punct/CJK-CJK (Rule of 3 + prevChar == delim break)
	//   [6]     intraword non-CJK alnum (Rule of 3 + prevChar == delim break)
	//   [7]     CJK-nonCJK boundary (Rule of 3 without prevChar == delim break)
	pattern := strings.Join([]string{
		`^[^_*]*?__[^_*]*?\*[^_*]*?(?=__)`,
		`[^*]+(?=[^*])`,
		// Pure CJK-to-CJK uses Group 5 (actually [1] here? Wait — map groups explicitly via 1..7)
		// Group 1: punct (*+) ws/$
		`(?!\*)(?!` + cjk + `)` + punct + `(\*+)(?=[\s]|$)`,
		// Group 2: notPunctSpace (*+) punctSpace/$
		`(?!` + cjk + `)` + notPunctSpace + `(\*+)(?!\*)(?=(?!` + cjk + `)` + punctSpace + `|$)`,
		// Group 3: punctSpace (*+) notPunctSpace
		`(?!\*)(?!` + cjk + `)` + punctSpace + `(\*+)(?=(?!` + cjk + `)` + notPunctSpace + `)`,
		// Group 4: ws (*+) punct
		`[\s](\*+)(?!\*)(?=(?!` + cjk + `)` + punct + `)`,
		// Group 5: punct (*+) punct OR cjk (*+) cjk
		`(?:(?!\*)(?!` + cjk + `)` + punct + `|` + cjk + `)(\*+)(?!\*)(?=(?!` + cjk + `)` + punct + `|` + cjk + `)`,
		// Group 6: non-CJK alnum (*+) non-CJK alnum
		`(?!` + cjk + `)` + notPunctSpace + `(\*+)(?=(?!` + cjk + `)` + notPunctSpace + `)`,
		// Group 7: cross-script CJK <-> non-CJK
		`(?:` + cjk + `(\*+)(?!\*)(?!` + cjk + `)|` + nonCjk + `(\*+)(?!\*)(?=` + cjk + `))`,
	}, "|")
	return jsre.MustCompile(pattern, "gu")
}

func cmBuildRDelimUnd(punct, punctSpace, notPunctSpace string) *jsre.Regexp {
	cjk := `[` + cmCjkRanges + `]`
	nonCjk := `[^_` + cmCjkRanges + `]`
	// Capture layout matches cmBuildRDelimAst:
	//   [1],[2] pure right-flanking
	//   [3],[4] pure left-flanking
	//   [5]     same-side punct/CJK-CJK (Rule of 3 + d break)
	//   [6]     unused filler for index alignment with Ast? Avoid empty group by putting cross-CJK in [6]/[7]
	//   [6],[7] cross-script CJK <-> non-CJK
	pattern := strings.Join([]string{
		`^[^_*]*?\*\*[^_*]*?_[^_*]*?(?=\*\*)`,
		`[^_]+(?=[^_])`,
		`(?!_)(?!` + cjk + `)` + punct + `(_+)(?=[\s]|$)`,
		`(?!` + cjk + `)` + notPunctSpace + `(_+)(?!_)(?=(?!` + cjk + `)` + punctSpace + `|$)`,
		`(?!_)(?!` + cjk + `)` + punctSpace + `(_+)(?=(?!` + cjk + `)` + notPunctSpace + `)`,
		`[\s](_+)(?!_)(?=(?!` + cjk + `)` + punct + `)`,
		`(?:(?!_)(?!` + cjk + `)` + punct + `|` + cjk + `)(_+)(?!_)(?=(?!` + cjk + `)` + punct + `|` + cjk + `)`,
		// Group 6 & 7: cross-script CJK <-> non-CJK
		`(?:` + cjk + `(_+)(?!_)(?!` + cjk + `)|` + nonCjk + `(_+)(?!_)(?=` + cjk + `))`,
	}, "|")
	return jsre.MustCompile(pattern, "gu")
}

func cmNewStrictStrikethroughTokenizer() *marked.Tokenizer {
	tok := marked.NewTokenizer()
	tok.Del = func(src, maskedSrc, prevChar string) *marked.Token {
		return cmStrictDel(tok, src, maskedSrc, prevChar)
	}
	return tok
}

func cmNewMarkdownTokenizer() *marked.Tokenizer {
	tok := cmNewStrictStrikethroughTokenizer()
	tok.URL = cmMarkdownURL
	tok.EmStrong = func(src, maskedSrc, prevChar string) *marked.Token {
		return cmCjkFriendlyEmStrong(tok, src, maskedSrc, prevChar)
	}
	return tok
}

func cmStrictDel(tok *marked.Tokenizer, src, maskedSrc, prevChar string) *marked.Token {
	m := cmStrictStrikethroughRegex.Exec(src, 0)
	if m == nil {
		return nil
	}
	raw := m.Group(0)
	text := m.Group(2)
	var tokens []*marked.Token
	if tok != nil && tok.Lexer != nil {
		tokens = tok.Lexer.InlineTokens(text)
	}
	return &marked.Token{
		Type:   "del",
		Raw:    raw,
		Text:   text,
		Tokens: tokens,
	}
}

func cmBuiltinURL(src string) *marked.Token {
	m := cmGfmUrlRegex.Exec(src, 0)
	if m == nil {
		return nil
	}
	full := m.Group(0)
	var text, href string
	if m.Group(2) == "@" {
		text = full
		href = "mailto:" + text
	} else {
		cur := full
		for {
			prev := cur
			back := cmGfmBackpedalRegex.Exec(cur, 0)
			if back != nil {
				cur = back.Group(0)
			} else {
				cur = ""
			}
			if prev == cur {
				break
			}
		}
		if cur == "" {
			return nil
		}
		text = cur
		if m.Group(1) == "www." {
			href = "http://" + cur
		} else {
			href = cur
		}
	}
	return &marked.Token{
		Type: "link",
		Raw:  text,
		Text: text,
		Href: href,
		Tokens: []*marked.Token{
			{Type: "text", Raw: text, Text: text},
		},
	}
}

func cmMarkdownURL(src string) *marked.Token {
	token := cmBuiltinURL(src)
	if token == nil {
		return nil
	}
	punctIdx := CjkPunctuationRegex.Search(token.Raw)
	if punctIdx < 0 {
		return token
	}
	return cmBuiltinURL(js.Slice(token.Raw, 0, punctIdx))
}

func cmCjkFriendlyEmStrong(tok *marked.Tokenizer, src, maskedSrc, prevChar string) *marked.Token {
	m := cmLDelimGfmRegex.Exec(src, 0)
	if m == nil {
		return nil
	}
	g1 := m.Group(1)
	g2 := m.Group(2)
	g3 := m.Group(3)
	g4 := m.Group(4)
	if g1 == "" && g2 == "" && g3 == "" && g4 == "" {
		return nil
	}
	if g4 != "" && cmUnicodeAlphaNumericRegex.Test(prevChar) {
		return nil
	}
	punctAfter := g1
	if punctAfter == "" {
		punctAfter = g3
	}
	if punctAfter != "" && prevChar != "" &&
		!cmMarkedPunctuationRegex.Test(prevChar) &&
		!cmCjkRegexp.Test(prevChar) &&
		!cmCjkRegexp.Test(punctAfter) {
		return nil
	}

	nextChar := g1
	if nextChar == "" {
		nextChar = g2
	}
	if nextChar == "" {
		nextChar = g3
	}
	if nextChar == "" {
		nextChar = g4
	}
	fullMatch := m.Group(0)
	o := len(fullMatch) - len(nextChar)
	if o <= 0 {
		return nil
	}
	c := fullMatch[:1]
	d := prevChar == c
	rDelim := cmRDelimUnd
	if c == "*" {
		rDelim = cmRDelimAstGfm
	}

	maskedLen := js.Len(maskedSrc)
	srcLen := js.Len(src)
	suffixStart := maskedLen - srcLen + o
	if suffixStart < 0 {
		suffixStart = 0
	}
	t := js.SliceFrom(maskedSrc, suffixStart)
	if t == "" {
		return nil
	}

	a := o
	p := 0
	pos := 0
	tLen := js.Len(t)
	for pos <= tLen {
		rm := rDelim.Exec(t, pos)
		if rm == nil {
			break
		}
		if rm.End() == pos {
			pos = rDelim.AdvanceIndex(t, pos)
		} else {
			pos = rm.End()
		}

		// Pick the first participating delim capture among groups 1..8.
		var endDelim string
		groupIdx := 0
		maxG := rm.NumGroups()
		for gi := 1; gi < maxG; gi++ {
			if s, ok := rm.GroupOK(gi); ok && s != "" {
				endDelim = s
				groupIdx = gi
				break
			}
		}
		if endDelim == "" {
			continue
		}
		u := len(endDelim)

		if groupIdx == 3 || groupIdx == 4 {
			a += u
			continue
		}
		isCrossCjk := (c == "*" && (groupIdx == 7 || groupIdx == 8)) || (c == "_" && (groupIdx == 6 || groupIdx == 7))
		if groupIdx == 5 || (c == "*" && groupIdx == 6) || isCrossCjk {
			if o%3 != 0 && ((o+u)%3) == 0 {
				p += u
				continue
			}
			if !isCrossCjk && d {
				break
			}
		}

		a -= u
		if a > 0 {
			continue
		}
		if a+p < 0 {
			u = u + a + p
		}
		if u < 0 {
			u = 0
		}
		r, _ := utf8.DecodeRuneInString(rm.Group(0))
		b := utf8.RuneLen(r)
		if b < 0 {
			b = 1
		}
		bU16 := 1
		if r > 0xFFFF {
			bU16 = 2
		}
		g := js.Slice(src, 0, o+rm.Index+bU16+u)
		minLen := o
		if u < minLen {
			minLen = u
		}
		var inner string
		kind := "strong"
		if minLen%2 != 0 {
			kind = "em"
			inner = js.Slice(g, 1, -1)
		} else {
			inner = js.Slice(g, 2, -2)
		}
		var tokens []*marked.Token
		if tok != nil && tok.Lexer != nil {
			tokens = tok.Lexer.InlineTokens(inner)
		}
		return &marked.Token{
			Type:   kind,
			Raw:    g,
			Text:   inner,
			Tokens: tokens,
		}
	}
	return nil
}

func cmIsEscaped(source string, index int) bool {
	if index < 0 {
		return false
	}
	if index > len(source) {
		index = len(source)
	}
	backslashes := 0
	for i := index - 1; i >= 0 && source[i] == '\\'; i-- {
		backslashes++
	}
	return backslashes%2 == 1
}

func cmFindClosingDelimiter(source, closing string, start int) int {
	pos := start
	for pos <= len(source)-len(closing) {
		idx := strings.Index(source[pos:], closing)
		if idx < 0 {
			return -1
		}
		absIdx := pos + idx
		if !cmIsEscaped(source, absIdx) {
			return absIdx
		}
		pos = absIdx + 1
	}
	return -1
}

func cmContainsTableStart(content string) bool {
	lines := strings.Split(content, "\n")
	for i := 0; i+1 < len(lines); i++ {
		header := lines[i]
		delim := lines[i+1]
		if strings.TrimSpace(header) == "" {
			continue
		}
		if !cmTableDelimiterRegex.Test(delim) {
			continue
		}
		if !strings.Contains(header, "|") && !strings.Contains(delim, "|") {
			continue
		}
		// Ensure delimiter has at least one dash to avoid false positive on ":" alone.
		if !strings.Contains(delim, "-") {
			continue
		}
		return true
	}
	return false
}

func cmLooksLikePendingDollarMath(source string) bool {
	if strings.Contains(source, "\n") {
		return false
	}
	if source == "" {
		return false
	}
	if source[0] == ' ' || source[0] == '\t' {
		return false
	}
	// Heuristic: shell/currency amounts start with digits (unless followed by an operator or LaTeX macro).
	r, _ := utf8.DecodeRuneInString(source)
	if r >= '0' && r <= '9' {
		rest := strings.TrimSpace(source[1:])
		if !strings.ContainsAny(source, `\^_{}=+-*/`) || rest == "" {
			return false
		}
	}
	return true
}

func cmIsWordBoundaryDelimiterOK(src string, closeEnd int, openPrefixOK bool) bool {
	if !openPrefixOK {
		return false
	}
	if closeEnd < len(src) {
		next := src[closeEnd]
		if next >= '0' && next <= '9' {
			return false
		}
	}
	return true
}

func cmTokenizeInlineLatex(lx *marked.Lexer, src string, tokens []*marked.Token) *marked.Token {
	if strings.HasPrefix(src, `\(`) {
		closeIdx := cmFindClosingDelimiter(src, `\)`, 2)
		if closeIdx < 0 {
			// Pending inline \( ... preserves backslash while streaming.
			return &marked.Token{
				Type:    "latex",
				Raw:     src,
				Text:    src,
				Pending: true,
			}
		}
		raw := src[:closeIdx+2]
		text := strings.TrimSpace(src[2:closeIdx])
		return &marked.Token{
			Type: "latex",
			Raw:  raw,
			Text: text,
		}
	}

	if strings.HasPrefix(src, "$$") {
		closeIdx := cmFindClosingDelimiter(src, "$$", 2)
		if closeIdx < 0 {
			return nil
		}
		raw := src[:closeIdx+2]
		text := strings.TrimSpace(src[2:closeIdx])
		if text == "" || strings.Contains(text, "\n") {
			return nil
		}
		return &marked.Token{
			Type: "latex",
			Raw:  raw,
			Text: text,
		}
	}

	if strings.HasPrefix(src, "$") && !strings.HasPrefix(src, "$$") {
		if len(src) < 2 {
			return nil
		}
		first := src[1]
		if first == ' ' || first == '\t' || first == '\n' {
			return nil
		}
		closeIdx := cmFindClosingDelimiter(src, "$", 1)
		if closeIdx < 0 {
			body := src[1:]
			if cmLooksLikePendingDollarMath(body) {
				return &marked.Token{
					Type:    "latex",
					Raw:     src,
					Text:    body,
					Pending: true,
				}
			}
			return nil
		}
		if closeIdx == 1 {
			return nil
		}
		if src[closeIdx-1] == ' ' || src[closeIdx-1] == '\t' || src[closeIdx-1] == '\n' {
			return nil
		}
		if closeIdx+1 < len(src) && src[closeIdx+1] == '$' {
			return nil
		}
		if !cmIsWordBoundaryDelimiterOK(src, closeIdx+1, true) {
			return nil
		}
		body := src[1:closeIdx]
		if strings.Contains(body, "\n") {
			return nil
		}
		// Do not match if body starts with digit unless it looks math-y and closing isn't currency.
		if body[0] >= '0' && body[0] <= '9' && !strings.ContainsAny(body, `\^_{}=+-*/`) {
			return nil
		}
		// Reject adjacent shell variables like $HOME/$USER or $PATH:$HOME where another identifier follows $.
		if closeIdx+1 < len(src) {
			next := src[closeIdx+1]
			if ((next >= 'A' && next <= 'Z') || (next >= 'a' && next <= 'z') || next == '_') && cmShellVarSpanRegex.Test(body) {
				return nil
			}
		}
		raw := src[:closeIdx+1]
		return &marked.Token{
			Type: "latex",
			Raw:  raw,
			Text: strings.TrimSpace(body),
		}
	}

	return nil
}

func cmTokenizeBlockLatex(lx *marked.Lexer, src string, tokens []*marked.Token) *marked.Token {
	m := cmLatexBlockOpenRegex.Exec(src, 0)
	if m == nil {
		return nil
	}
	delim := m.Group(1)
	closing := "$$"
	if delim == `\[` {
		closing = `\]`
	}
	openEnd := len(m.Group(0))
	closeIdx := cmFindClosingDelimiter(src, closing, openEnd)
	if closeIdx < 0 {
		rest := src[openEnd:]
		if cmContainsTableStart(rest) {
			return nil
		}
		// Unclosed single-line orphan delimiter with no content shouldn't hijack following blocks unless non-empty or multiline.
		trimmedRest := strings.TrimSpace(rest)
		if trimmedRest == "" {
			return nil
		}
		return &marked.Token{
			Type:    "latexBlock",
			Raw:     src,
			Text:    trimmedRest,
			Pending: true,
		}
	}
	body := src[openEnd:closeIdx]
	if cmContainsTableStart(body) {
		return nil
	}
	end := closeIdx + len(closing)
	for end < len(src) && (src[end] == ' ' || src[end] == '\t') {
		end++
	}
	if end < len(src) && src[end] != '\n' {
		// Closing delimiter on the opening line must end the line (unless inline handled separately).
		return nil
	}
	for end < len(src) && src[end] == '\n' {
		end++
	}
	return &marked.Token{
		Type: "latexBlock",
		Raw:  src[:end],
		Text: strings.TrimSpace(body),
	}
}

func cmLatexBlockStart(src string) int {
	pos := 0
	for pos < len(src) {
		m := cmLatexBlockStartRegex.Exec(src[pos:], 0)
		if m == nil {
			return -1
		}
		delimByteOff := pos + js.U16ToByte(src[pos:], m.Index)
		delimStartU16, _ := m.GroupIndex(1)
		delimAbsByte := pos + js.U16ToByte(src[pos:], delimStartU16)
		if !cmIsEscaped(src, delimAbsByte) {
			return utf8.RuneCountInString(src[:delimByteOff])
		}
		adv := js.U16ToByte(src[pos:], m.End())
		if adv <= 0 {
			adv = 1
		}
		pos += adv
	}
	return -1
}

func cmLatexInlineStart(src string) int {
	for i := 0; i < len(src); i++ {
		if src[i] == '\\' && i+1 < len(src) && src[i+1] == '(' {
			if !cmIsEscaped(src, i) {
				return utf8.RuneCountInString(src[:i])
			}
		} else if src[i] == '$' {
			if !cmIsEscaped(src, i) {
				return utf8.RuneCountInString(src[:i])
			}
		}
	}
	return -1
}

var cmLatexMarkdownExtensions = []marked.TokenizerExtension{
	{
		Name:     "latexBlock",
		Level:    "block",
		Start:    cmLatexBlockStart,
		Tokenize: cmTokenizeBlockLatex,
	},
	{
		Name:     "latex",
		Level:    "inline",
		Start:    cmLatexInlineStart,
		Tokenize: cmTokenizeInlineLatex,
	},
}

func cmTrimPartialClosingFences(tokens []*marked.Token) {
	for _, tok := range tokens {
		if tok == nil {
			continue
		}
		switch tok.Type {
		case "code":
			if tok.CodeBlockStyle == "indented" {
				continue
			}
			m := cmFenceOpenRegex.Exec(tok.Raw, 0)
			if m == nil {
				continue
			}
			fence := m.Group(1)
			fenceChar := fence[0]
			fenceLen := len(fence)
			lines := strings.Split(tok.Raw, "\n")
			hasClosing := false
			if len(lines) > 1 {
				last := strings.TrimSpace(lines[len(lines)-1])
				if len(last) >= fenceLen && strings.TrimRight(last, string(fenceChar)) == "" {
					hasClosing = true
				}
			}
			if !hasClosing {
				textLines := strings.Split(tok.Text, "\n")
				if len(textLines) > 0 {
					lastText := strings.TrimSpace(textLines[len(textLines)-1])
					if len(lastText) > 0 && len(lastText) < fenceLen && strings.Trim(lastText, string(fenceChar)) == "" {
						tok.Text = strings.Join(textLines[:len(textLines)-1], "\n")
					}
				}
			}
		case "blockquote":
			cmTrimPartialClosingFences(tok.Tokens)
		case "list":
			for _, item := range tok.Items {
				if item != nil {
					cmTrimPartialClosingFences(item.Tokens)
				}
			}
		}
	}
}

func cmStripHtmlComments(raw string) string {
	return cmHtmlCommentRegex.Replace(raw, "")
}

func cmRemoveHtmlCommentBlocks(tokens []*marked.Token) []*marked.Token {
	if len(tokens) == 0 {
		return tokens
	}
	out := make([]*marked.Token, 0, len(tokens))
	removedComment := false
	for _, tok := range tokens {
		if tok == nil {
			continue
		}
		if tok.Type == "blockquote" {
			tok.Tokens = cmRemoveHtmlCommentBlocks(tok.Tokens)
			out = append(out, tok)
			removedComment = false
			continue
		}
		if tok.Type == "list" {
			for _, item := range tok.Items {
				if item != nil {
					item.Tokens = cmRemoveHtmlCommentBlocks(item.Tokens)
				}
			}
			out = append(out, tok)
			removedComment = false
			continue
		}
		if tok.Type == "html" && strings.Contains(tok.Raw, "<!--") {
			stripped := cmStripHtmlComments(tok.Raw)
			if strings.TrimSpace(stripped) == "" {
				removedComment = true
				continue
			}
			stripped = strings.TrimLeft(stripped, "\r\n")
			tok.Raw = stripped
			tok.Text = stripped
			out = append(out, tok)
			removedComment = false
			continue
		}
		if tok.Type == "space" {
			if len(out) == 0 {
				removedComment = false
				continue
			}
			if removedComment {
				// Drop the blank-line token immediately after a comment-only block if the previous block already ends with space or will space cleanly.
				removedComment = false
				if out[len(out)-1].Type == "space" {
					continue
				}
			}
			out = append(out, tok)
			continue
		}
		removedComment = false
		out = append(out, tok)
	}
	for len(out) > 0 && out[len(out)-1].Type == "space" {
		out = out[:len(out)-1]
	}
	return out
}

func cmLexMarkdown(src string) ([]*marked.Token, bool) {
	tok := cmNewMarkdownTokenizer()
	parser := marked.New(marked.WithTokenizer(tok))
	parser.Use(cmLatexMarkdownExtensions...)
	tokens := parser.Lexer(src)
	hasLinks := tok.Lexer != nil && len(tok.Lexer.Links) > 0
	return tokens, hasLinks
}

var cmMarkdownParser = cmNewMarkdownParser()

func cmNewMarkdownParser() *marked.Marked {
	m := marked.New(marked.WithTokenizer(cmNewMarkdownTokenizer()))
	m.Use(cmLatexMarkdownExtensions...)
	return m
}

// DefaultTextStyle holds optional default styling applied to Markdown body text.
type DefaultTextStyle struct {
	Color         func(text string) string
	BgColor       func(text string) string
	Bold          *bool
	Italic        *bool
	Strikethrough *bool
	Underline     *bool
}

// MarkdownTheme supplies styling functions for every Markdown construct.
type MarkdownTheme struct {
	Heading         func(text string) string
	Link            func(text string) string
	LinkUrl         func(text string) string
	Code            func(text string) string
	CodeBlock       func(text string) string
	CodeBlockBorder func(text string) string
	Quote           func(text string) string
	QuoteBorder     func(text string) string
	Hr              func(text string) string
	ListBullet      func(text string) string
	Bold            func(text string) string
	Italic          func(text string) string
	Strikethrough   func(text string) string
	Underline       func(text string) string
	HighlightCode   func(code string, lang *string) []string
	CodeBlockIndent *string
}

// MarkdownOptions configures optional behavior for the Markdown component.
type MarkdownOptions struct {
	// PreserveOrderedListMarkers preserves raw ordered-list numbers instead of renumbering from 1.
	PreserveOrderedListMarkers *bool
	// PreserveBackslashEscapes preserves source backslash escapes instead of unescaping punctuation.
	PreserveBackslashEscapes *bool
	// Transform transforms the Markdown source before parsing at the computed available width.
	Transform func(markdown string, availableWidth int) string
	// RenderLatex renders inline/display LaTeX math with unicode (default: true).
	RenderLatex *bool
}

type cmInlineStyleContext struct {
	applyText   func(text string) string
	stylePrefix string
}

type cmCachedTokens struct {
	source   string
	tokens   []*marked.Token
	hasLinks bool
}

type cmRenderedBlock struct {
	width           int
	nextTokenType   *string
	lines           []string
	highlightedCode map[string][]string
}

type cmBlockCache struct {
	source   string
	hasLinks bool
	blocks   map[string]*cmRenderedBlock
}

// Markdown renders CommonMark + GFM + LaTeX source into wrapped, padded ANSI lines.
type Markdown struct {
	text             string
	paddingX         int
	paddingY         int
	defaultTextStyle *DefaultTextStyle
	theme            MarkdownTheme
	options          MarkdownOptions

	defaultStylePrefix *string

	cachedText   *string
	cachedWidth  *int
	cachedLines  []string
	cachedTokens weak.Pointer[cmCachedTokens]
	blocks       weak.Pointer[cmBlockCache]

	blockHighlightedCode map[string][]string
}

// NewMarkdown creates a new Markdown component.
func NewMarkdown(
	text string,
	paddingX int,
	paddingY int,
	theme MarkdownTheme,
	defaultTextStyle *DefaultTextStyle,
	options *MarkdownOptions,
) *Markdown {
	var opts MarkdownOptions
	if options != nil {
		opts = *options
	}
	return &Markdown{
		text:             text,
		paddingX:         paddingX,
		paddingY:         paddingY,
		defaultTextStyle: defaultTextStyle,
		theme:            theme,
		options:          opts,
	}
}

func (m *Markdown) SetText(text string) {
	m.text = text
	m.cachedText = nil
	m.cachedWidth = nil
	m.cachedLines = nil
}

func (m *Markdown) Invalidate() {
	m.cachedText = nil
	m.cachedWidth = nil
	m.cachedLines = nil
	m.cachedTokens = weak.Pointer[cmCachedTokens]{}
	m.blocks = weak.Pointer[cmBlockCache]{}
	m.blockHighlightedCode = nil
	m.defaultStylePrefix = nil
}

func (m *Markdown) Render(width int) []string {
	if m.cachedLines != nil && m.cachedText != nil && *m.cachedText == m.text && m.cachedWidth != nil && *m.cachedWidth == width {
		return m.cachedLines
	}

	contentWidth := max(1, width-m.paddingX*2)

	if js.Trim(m.text) == "" {
		result := []string{}
		textCopy := m.text
		m.cachedText = &textCopy
		m.cachedWidth = &width
		m.cachedLines = result
		return result
	}

	sourceText := m.text
	if m.options.Transform != nil {
		sourceText = m.options.Transform(m.text, contentWidth)
	}
	if js.Trim(sourceText) == "" {
		result := []string{}
		textCopy := m.text
		m.cachedText = &textCopy
		m.cachedWidth = &width
		m.cachedLines = result
		return result
	}

	normalizedText := strings.ReplaceAll(sourceText, "\t", "   ")

	var tokens []*marked.Token
	var hasLinks bool
	tokensHolder := m.cachedTokens.Value()
	if tokensHolder != nil && tokensHolder.source == normalizedText {
		tokens = tokensHolder.tokens
		hasLinks = tokensHolder.hasLinks
	} else {
		tokens, hasLinks = cmLexMarkdown(normalizedText)
		cmTrimPartialClosingFences(tokens)
		tokens = cmRemoveHtmlCommentBlocks(tokens)
		tokensHolder = &cmCachedTokens{
			source:   normalizedText,
			tokens:   tokens,
			hasLinks: hasLinks,
		}
		m.cachedTokens = weak.Make(tokensHolder)
	}

	blockCache := m.blocks.Value()
	if blockCache == nil || ((hasLinks || blockCache.hasLinks) && blockCache.source != normalizedText) {
		blockCache = &cmBlockCache{
			source:   normalizedText,
			hasLinks: hasLinks,
			blocks:   make(map[string]*cmRenderedBlock),
		}
		m.blocks = weak.Make(blockCache)
	} else {
		blockCache.source = normalizedText
		blockCache.hasLinks = hasLinks
	}

	renderedLines := make([]string, 0, len(tokens)*2)
	for i, token := range tokens {
		var nextTokenType *string
		if i+1 < len(tokens) && tokens[i+1] != nil {
			nt := tokens[i+1].Type
			nextTokenType = &nt
		}
		renderedLines = append(renderedLines, m.renderBlock(token, contentWidth, nextTokenType, blockCache)...)
	}

	// Expand any wrapped lines containing embedded newlines (e.g. inline Kitty/iTerm2 sequences).
	expandedLines := make([]string, 0, len(renderedLines))
	for _, line := range renderedLines {
		if strings.Contains(line, "\n") {
			expandedLines = append(expandedLines, strings.Split(line, "\n")...)
		} else {
			expandedLines = append(expandedLines, line)
		}
	}

	leftMargin := strings.Repeat(" ", m.paddingX)
	rightMargin := strings.Repeat(" ", m.paddingX)
	var bgFn func(string) string
	if m.defaultTextStyle != nil {
		bgFn = m.defaultTextStyle.BgColor
	}

	contentLines := make([]string, 0, len(expandedLines))
	for _, line := range expandedLines {
		if IsImageLine(line) {
			contentLines = append(contentLines, line)
			continue
		}
		lineWithMargins := leftMargin + line + rightMargin
		if bgFn != nil {
			contentLines = append(contentLines, ApplyBackgroundToLine(lineWithMargins, width, bgFn))
		} else {
			visLen := VisibleWidth(lineWithMargins)
			padRight := strings.Repeat(" ", max(0, width-visLen))
			contentLines = append(contentLines, lineWithMargins+padRight)
		}
	}

	emptyLine := strings.Repeat(" ", max(0, width))
	if bgFn != nil {
		emptyLine = bgFn(emptyLine)
	}
	emptyLines := make([]string, 0, m.paddingY)
	for range m.paddingY {
		emptyLines = append(emptyLines, emptyLine)
	}

	result := make([]string, 0, len(emptyLines)*2+len(contentLines))
	result = append(result, emptyLines...)
	result = append(result, contentLines...)
	result = append(result, emptyLines...)
	FlattenLines(result)

	textCopy := m.text
	m.cachedText = &textCopy
	m.cachedWidth = &width
	m.cachedLines = result

	runtime.KeepAlive(tokensHolder)
	runtime.KeepAlive(blockCache)
	return result
}

func cmSameTokenTypePtr(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func (m *Markdown) renderBlock(
	token *marked.Token,
	contentWidth int,
	nextTokenType *string,
	blockCache *cmBlockCache,
) []string {
	if token == nil {
		return nil
	}
	key := token.Type + ":" + token.Raw
	cached := blockCache.blocks[key]
	if cached != nil && cached.width == contentWidth && cmSameTokenTypePtr(cached.nextTokenType, nextTokenType) {
		return cached.lines
	}

	if cached != nil && cached.highlightedCode != nil {
		m.blockHighlightedCode = cached.highlightedCode
	} else {
		m.blockHighlightedCode = make(map[string][]string)
	}
	lines := m.renderToken(token, contentWidth, nextTokenType)
	highlighted := m.blockHighlightedCode
	m.blockHighlightedCode = nil

	blockCache.blocks[key] = &cmRenderedBlock{
		width:           contentWidth,
		nextTokenType:   nextTokenType,
		lines:           lines,
		highlightedCode: highlighted,
	}
	return lines
}

func (m *Markdown) applyDefaultStyle(text string) string {
	if m.defaultTextStyle == nil {
		return text
	}
	styled := text
	if m.defaultTextStyle.Color != nil {
		styled = m.defaultTextStyle.Color(styled)
	}
	if m.defaultTextStyle.Bold != nil && *m.defaultTextStyle.Bold && m.theme.Bold != nil {
		styled = m.theme.Bold(styled)
	}
	if m.defaultTextStyle.Italic != nil && *m.defaultTextStyle.Italic && m.theme.Italic != nil {
		styled = m.theme.Italic(styled)
	}
	if m.defaultTextStyle.Strikethrough != nil && *m.defaultTextStyle.Strikethrough && m.theme.Strikethrough != nil {
		styled = m.theme.Strikethrough(styled)
	}
	if m.defaultTextStyle.Underline != nil && *m.defaultTextStyle.Underline && m.theme.Underline != nil {
		styled = m.theme.Underline(styled)
	}
	return styled
}

func (m *Markdown) getDefaultStylePrefix() string {
	if m.defaultTextStyle == nil {
		return ""
	}
	if m.defaultStylePrefix != nil {
		return *m.defaultStylePrefix
	}
	const marker = "\x00"
	styled := m.applyDefaultStyle(marker)
	idx := strings.Index(styled, marker)
	prefix := ""
	if idx >= 0 {
		prefix = styled[:idx]
	}
	m.defaultStylePrefix = &prefix
	return prefix
}

func (m *Markdown) getStylePrefix(styleFn func(text string) string) string {
	if styleFn == nil {
		return ""
	}
	const marker = "\x00"
	styled := styleFn(marker)
	idx := strings.Index(styled, marker)
	if idx < 0 {
		return ""
	}
	return styled[:idx]
}

func (m *Markdown) getDefaultInlineStyleContext() cmInlineStyleContext {
	return cmInlineStyleContext{
		applyText:   m.applyDefaultStyle,
		stylePrefix: m.getDefaultStylePrefix(),
	}
}

func (m *Markdown) applyThemeHeading(text string) string {
	if m.theme.Heading != nil {
		return m.theme.Heading(text)
	}
	return text
}

func (m *Markdown) applyThemeBold(text string) string {
	if m.theme.Bold != nil {
		return m.theme.Bold(text)
	}
	return text
}

func (m *Markdown) applyThemeUnderline(text string) string {
	if m.theme.Underline != nil {
		return m.theme.Underline(text)
	}
	return text
}

func (m *Markdown) applyThemeItalic(text string) string {
	if m.theme.Italic != nil {
		return m.theme.Italic(text)
	}
	return text
}

func (m *Markdown) renderToken(token *marked.Token, availableWidth int, nextTokenType *string) []string {
	if token == nil {
		return nil
	}
	lines := []string{}
	defaultStyleCtx := m.getDefaultInlineStyleContext()

	switch token.Type {
	case "heading":
		var styleFn func(string) string
		switch token.Depth {
		case 1:
			styleFn = func(t string) string {
				return m.applyThemeHeading(m.applyThemeBold(m.applyThemeUnderline(t)))
			}
		case 2:
			styleFn = func(t string) string {
				return m.applyThemeHeading(m.applyThemeBold(t))
			}
		default:
			styleFn = func(t string) string {
				return m.applyThemeHeading(m.applyThemeBold(t))
			}
		}
		headingStyleCtx := cmInlineStyleContext{
			applyText:   styleFn,
			stylePrefix: m.getStylePrefix(styleFn),
		}
		headingText := m.renderInlineTokens(token.Tokens, headingStyleCtx)
		if token.Depth >= 3 {
			prefix := strings.Repeat("#", token.Depth) + " "
			headingText = styleFn(prefix) + headingText
		}
		lines = append(lines, WrapTextWithAnsi(headingText, availableWidth)...)
		if nextTokenType != nil && *nextTokenType != "space" {
			lines = append(lines, "")
		}

	case "paragraph":
		paragraphText := m.renderInlineTokens(token.Tokens, defaultStyleCtx)
		lines = append(lines, WrapTextWithAnsi(paragraphText, availableWidth)...)
		if nextTokenType != nil && *nextTokenType != "space" {
			lines = append(lines, "")
		}

	case "code":
		indent := "  "
		if m.theme.CodeBlockIndent != nil {
			indent = *m.theme.CodeBlockIndent
		}
		lang := ""
		if token.Lang != nil {
			lang = *token.Lang
		}
		topBorder := "```" + lang
		if m.theme.CodeBlockBorder != nil {
			topBorder = m.theme.CodeBlockBorder(topBorder)
		}
		lines = append(lines, topBorder)

		var codeLines []string
		if m.theme.HighlightCode != nil {
			cacheKey := lang + "\x00" + token.Text
			if m.blockHighlightedCode != nil {
				if cachedLines, ok := m.blockHighlightedCode[cacheKey]; ok {
					codeLines = cachedLines
				}
			}
			if codeLines == nil {
				codeLines = m.theme.HighlightCode(token.Text, token.Lang)
				if m.blockHighlightedCode != nil {
					m.blockHighlightedCode[cacheKey] = codeLines
				}
			}
		} else {
			rawLines := strings.Split(token.Text, "\n")
			codeLines = make([]string, 0, len(rawLines))
			for _, cl := range rawLines {
				if m.theme.CodeBlock != nil {
					codeLines = append(codeLines, m.theme.CodeBlock(cl))
				} else {
					codeLines = append(codeLines, cl)
				}
			}
		}
		for _, cl := range codeLines {
			lines = append(lines, indent+cl)
		}

		bottomBorder := "```"
		if m.theme.CodeBlockBorder != nil {
			bottomBorder = m.theme.CodeBlockBorder(bottomBorder)
		}
		lines = append(lines, bottomBorder)
		if nextTokenType != nil && *nextTokenType != "space" {
			lines = append(lines, "")
		}

	case "list":
		lines = append(lines, m.renderList(token, 0, defaultStyleCtx, availableWidth)...)

	case "table":
		lines = append(lines, m.renderTable(token, availableWidth, defaultStyleCtx)...)
		if nextTokenType != nil && *nextTokenType != "space" {
			lines = append(lines, "")
		}

	case "blockquote":
		if availableWidth < 3 {
			// Guard against exponential nesting when availableWidth < 3 leaves no room for "│ ".
			fallback := strings.TrimSpace(token.Raw)
			if fallback != "" {
				lines = append(lines, WrapTextWithAnsi(fallback, availableWidth)...)
			}
		} else {
			quoteWidth := availableWidth - 2
			quoteStyleFn := func(t string) string {
				if m.theme.Quote != nil {
					return m.theme.Quote(m.applyThemeItalic(t))
				}
				return m.applyThemeItalic(t)
			}
			quoteStyleCtx := cmInlineStyleContext{
				applyText:   quoteStyleFn,
				stylePrefix: m.getStylePrefix(quoteStyleFn),
			}
			quoteBorder := "│ "
			if m.theme.QuoteBorder != nil {
				quoteBorder = m.theme.QuoteBorder(quoteBorder)
			}
			var innerLines []string
			for i, child := range token.Tokens {
				if child == nil {
					continue
				}
				var childNext *string
				if i+1 < len(token.Tokens) && token.Tokens[i+1] != nil {
					nt := token.Tokens[i+1].Type
					childNext = &nt
				}
				switch child.Type {
				case "paragraph":
					pText := m.renderInlineTokens(child.Tokens, quoteStyleCtx)
					innerLines = append(innerLines, WrapTextWithAnsi(pText, quoteWidth)...)
					if childNext != nil && *childNext != "space" {
						innerLines = append(innerLines, "")
					}
				case "text":
					var tText string
					if len(child.Tokens) > 0 {
						tText = m.renderInlineTokens(child.Tokens, quoteStyleCtx)
					} else {
						tText = quoteStyleCtx.applyText(child.Text)
					}
					innerLines = append(innerLines, WrapTextWithAnsi(tText, quoteWidth)...)
				case "space":
					if childNext != nil {
						innerLines = append(innerLines, "")
					}
				case "list":
					innerLines = append(innerLines, m.renderList(child, 0, quoteStyleCtx, quoteWidth)...)
				default:
					innerLines = append(innerLines, m.renderToken(child, quoteWidth, childNext)...)
				}
			}
			for _, ql := range innerLines {
				lines = append(lines, quoteBorder+ql)
			}
		}
		if nextTokenType != nil && *nextTokenType != "space" {
			lines = append(lines, "")
		}

	case "hr":
		hrLine := strings.Repeat("─", min(availableWidth, 80))
		if m.theme.Hr != nil {
			hrLine = m.theme.Hr(hrLine)
		}
		lines = append(lines, hrLine)
		if nextTokenType != nil && *nextTokenType != "space" {
			lines = append(lines, "")
		}

	case "latexBlock":
		renderLatex := m.options.RenderLatex == nil || *m.options.RenderLatex
		var rendered *string
		if renderLatex && !token.Pending {
			displayTrue := true
			rendered = RenderLatex(token.Text, &RenderLatexOptions{Display: &displayTrue})
		}
		if rendered != nil {
			renderedSplit := strings.Split(*rendered, "\n")
			fits := true
			for _, rl := range renderedSplit {
				if VisibleWidth(rl) > availableWidth {
					fits = false
					break
				}
			}
			if fits {
				for _, rl := range renderedSplit {
					lines = append(lines, m.applyDefaultStyle(rl))
				}
			} else {
				rawTrimmed := js.Trim(token.Raw)
				lines = append(lines, WrapTextWithAnsi(m.applyDefaultStyle(rawTrimmed), availableWidth)...)
			}
		} else {
			rawTrimmed := js.Trim(token.Raw)
			lines = append(lines, WrapTextWithAnsi(m.applyDefaultStyle(rawTrimmed), availableWidth)...)
		}
		if nextTokenType != nil && *nextTokenType != "space" {
			lines = append(lines, "")
		}

	case "html":
		rawTrimmed := strings.TrimRight(token.Raw, "\r\n")
		if rawTrimmed != "" {
			lines = append(lines, WrapTextWithAnsi(m.applyDefaultStyle(rawTrimmed), availableWidth)...)
		}
		if nextTokenType != nil && *nextTokenType != "space" {
			lines = append(lines, "")
		}

	case "space":
		if nextTokenType != nil {
			lines = append(lines, "")
		}

	case "def":
		return lines

	case "text":
		var tText string
		if len(token.Tokens) > 0 {
			tText = m.renderInlineTokens(token.Tokens, defaultStyleCtx)
		} else {
			tText = defaultStyleCtx.applyText(token.Text)
		}
		lines = append(lines, WrapTextWithAnsi(tText, availableWidth)...)

	default:
		if token.Text != "" {
			lines = append(lines, WrapTextWithAnsi(m.applyDefaultStyle(token.Text), availableWidth)...)
		}
	}

	return lines
}

func (m *Markdown) renderInlineTokens(tokens []*marked.Token, styleContext cmInlineStyleContext) string {
	var b strings.Builder
	dropLeadingSpace := false

	for _, token := range tokens {
		if token == nil {
			continue
		}
		switch token.Type {
		case "text":
			text := token.Text
			if dropLeadingSpace && strings.HasPrefix(text, " ") {
				text = text[1:]
				dropLeadingSpace = false
			}
			if len(token.Tokens) > 0 {
				b.WriteString(m.renderInlineTokens(token.Tokens, styleContext))
			} else {
				b.WriteString(styleContext.applyText(text))
			}

		case "escape":
			dropLeadingSpace = false
			if m.options.PreserveBackslashEscapes != nil && *m.options.PreserveBackslashEscapes {
				b.WriteString(styleContext.applyText(token.Raw))
			} else {
				b.WriteString(styleContext.applyText(token.Text))
			}

		case "html":
			if strings.Contains(token.Raw, "<!--") {
				stripped := cmStripHtmlComments(token.Raw)
				if stripped == "" {
					prev := b.String()
					if prev == "" || strings.HasSuffix(prev, " ") || strings.HasSuffix(prev, "\n") {
						dropLeadingSpace = true
					}
					continue
				}
				dropLeadingSpace = false
				b.WriteString(styleContext.applyText(stripped))
				continue
			}
			dropLeadingSpace = false
			b.WriteString(styleContext.applyText(token.Raw))

		case "paragraph":
			dropLeadingSpace = false
			b.WriteString(m.renderInlineTokens(token.Tokens, styleContext))

		case "strong":
			dropLeadingSpace = false
			inner := m.renderInlineTokens(token.Tokens, styleContext)
			b.WriteString(m.applyThemeBold(inner))
			b.WriteString(styleContext.stylePrefix)

		case "em":
			dropLeadingSpace = false
			inner := m.renderInlineTokens(token.Tokens, styleContext)
			b.WriteString(m.applyThemeItalic(inner))
			b.WriteString(styleContext.stylePrefix)

		case "del":
			dropLeadingSpace = false
			inner := m.renderInlineTokens(token.Tokens, styleContext)
			if m.theme.Strikethrough != nil {
				b.WriteString(m.theme.Strikethrough(inner))
			} else {
				b.WriteString(inner)
			}
			b.WriteString(styleContext.stylePrefix)

		case "codespan":
			dropLeadingSpace = false
			codeText := token.Text
			if m.theme.Code != nil {
				b.WriteString(m.theme.Code(codeText))
			} else {
				b.WriteString(codeText)
			}
			b.WriteString(styleContext.stylePrefix)

		case "br":
			dropLeadingSpace = false
			b.WriteByte('\n')

		case "link":
			dropLeadingSpace = false
			linkPlainText := cmPlainTextOfTokens(token.Tokens)
			if linkPlainText == "" {
				linkPlainText = token.Text
			}
			styledLink := m.renderInlineTokens(token.Tokens, styleContext)
			styled := styledLink
			if m.theme.Link != nil {
				styled = m.theme.Link(m.applyThemeUnderline(styledLink))
			} else {
				styled = m.applyThemeUnderline(styledLink)
			}
			if GetCapabilities().Hyperlinks {
				b.WriteString(Hyperlink(styled, token.Href))
				b.WriteString(styleContext.stylePrefix)
			} else {
				isBareMatch := linkPlainText == token.Href ||
					(token.Raw == token.Text && (token.Href == "mailto:"+linkPlainText || token.Href == "http://"+linkPlainText))
				if isBareMatch {
					b.WriteString(styled)
					b.WriteString(styleContext.stylePrefix)
				} else {
					urlPart := " (" + token.Href + ")"
					if m.theme.LinkUrl != nil {
						urlPart = m.theme.LinkUrl(urlPart)
					}
					b.WriteString(styled)
					b.WriteString(urlPart)
					b.WriteString(styleContext.stylePrefix)
				}
			}

		case "image":
			dropLeadingSpace = false
			alt := token.Text
			if alt == "" {
				alt = cmPlainTextOfTokens(token.Tokens)
			}
			b.WriteString(styleContext.applyText(alt))

		case "latex":
			dropLeadingSpace = false
			renderLatex := m.options.RenderLatex == nil || *m.options.RenderLatex
			if renderLatex && !token.Pending {
				displayFalse := false
				if rendered := RenderLatex(token.Text, &RenderLatexOptions{Display: &displayFalse}); rendered != nil {
					b.WriteString(styleContext.applyText(*rendered))
					continue
				}
			}
			b.WriteString(styleContext.applyText(token.Raw))

		default:
			dropLeadingSpace = false
			if len(token.Tokens) > 0 {
				b.WriteString(m.renderInlineTokens(token.Tokens, styleContext))
			} else if token.Text != "" {
				b.WriteString(styleContext.applyText(token.Text))
			} else if token.Raw != "" {
				b.WriteString(styleContext.applyText(token.Raw))
			}
		}
	}

	return b.String()
}

func cmPlainTextOfTokens(tokens []*marked.Token) string {
	if len(tokens) == 0 {
		return ""
	}
	var b strings.Builder
	for _, t := range tokens {
		if t == nil {
			continue
		}
		if len(t.Tokens) > 0 {
			b.WriteString(cmPlainTextOfTokens(t.Tokens))
		} else {
			b.WriteString(t.Text)
		}
	}
	return b.String()
}

func (m *Markdown) getOrderedListMarker(token *marked.Token, index int, item *marked.Token) string {
	start := 1
	if token.Start != nil {
		start = *token.Start
	}
	if m.options.PreserveOrderedListMarkers != nil && *m.options.PreserveOrderedListMarkers && item != nil {
		if rm := cmOrderedRawRegex.Exec(item.Raw, 0); rm != nil {
			return rm.Group(1) + " "
		}
	}
	return strconv.Itoa(start+index) + ". "
}

func (m *Markdown) getUnorderedListMarker(item *marked.Token) string {
	if m.options.PreserveOrderedListMarkers != nil && *m.options.PreserveOrderedListMarkers && item != nil {
		if rm := cmUnorderedRawRegex.Exec(item.Raw, 0); rm != nil {
			return rm.Group(1) + " "
		}
	}
	return "- "
}

func (m *Markdown) renderList(token *marked.Token, depth int, styleContext cmInlineStyleContext, availableWidth int) []string {
	if token == nil {
		return nil
	}
	lines := []string{}
	indent := strings.Repeat("    ", depth)

	for i, item := range token.Items {
		if item == nil {
			continue
		}
		var bullet string
		if token.Ordered {
			bullet = m.getOrderedListMarker(token, i, item)
		} else {
			bullet = m.getUnorderedListMarker(item)
		}
		taskMarker := ""
		if item.Task {
			if item.Checked != nil && *item.Checked {
				taskMarker = "[x] "
			} else {
				taskMarker = "[ ] "
			}
		}
		marker := bullet + taskMarker
		styledMarker := marker
		if m.theme.ListBullet != nil {
			styledMarker = m.theme.ListBullet(marker)
		}

		markerWidth := VisibleWidth(indent) + VisibleWidth(marker)
		itemContentWidth := max(1, availableWidth-markerWidth)
		contIndent := indent + strings.Repeat(" ", VisibleWidth(marker))

		var itemLines []string
		// First collect all child token lines belonging to this list item.
		pendingInline := []string{}
		flushInline := func() {
			if len(pendingInline) == 0 {
				return
			}
			combined := strings.Join(pendingInline, "")
			pendingInline = pendingInline[:0]
			itemLines = append(itemLines, WrapTextWithAnsi(combined, itemContentWidth)...)
		}

		for ci, child := range item.Tokens {
			if child == nil {
				continue
			}
			switch child.Type {
			case "text":
				var tStr string
				if len(child.Tokens) > 0 {
					tStr = m.renderInlineTokens(child.Tokens, styleContext)
				} else {
					tStr = styleContext.applyText(child.Text)
				}
				if len(pendingInline) > 0 {
					pendingInline = append(pendingInline, "\n"+tStr)
				} else {
					pendingInline = append(pendingInline, tStr)
				}
			case "paragraph":
				flushInline()
				pStr := m.renderInlineTokens(child.Tokens, styleContext)
				itemLines = append(itemLines, WrapTextWithAnsi(pStr, itemContentWidth)...)
				if ci+1 < len(item.Tokens) && item.Tokens[ci+1] != nil && item.Tokens[ci+1].Type != "list" && item.Tokens[ci+1].Type != "space" {
					itemLines = append(itemLines, "")
				}
			case "space":
				flushInline()
				if ci+1 < len(item.Tokens) {
					itemLines = append(itemLines, "")
				}
			case "list":
				flushInline()
				nestedLines := m.renderList(child, depth+1, styleContext, availableWidth)
				for _, nl := range nestedLines {
					lines = append(lines, nl)
				}
				// Append marker lines accumulated before nested list first.
				if len(itemLines) > 0 {
					// Emit item's own lines BEFORE nested list lines cleanly.
					preNested := make([]string, 0, len(itemLines))
					for li, il := range itemLines {
						if li == 0 {
							preNested = append(preNested, indent+styledMarker+il)
						} else if il == "" {
							preNested = append(preNested, "")
						} else {
							preNested = append(preNested, contIndent+il)
						}
					}
					// Move preNested before just-appended nestedLines.
					justAppended := len(nestedLines)
					prefixSlice := append([]string{}, lines[:len(lines)-justAppended]...)
					prefixSlice = append(prefixSlice, preNested...)
					prefixSlice = append(prefixSlice, lines[len(lines)-justAppended:]...)
					lines = prefixSlice
					itemLines = nil
					marker = ""
					styledMarker = ""
				}
			default:
				flushInline()
				childLines := m.renderToken(child, itemContentWidth, nil)
				for _, cl := range childLines {
					if cl == "" {
						itemLines = append(itemLines, "")
						continue
					}
					itemLines = append(itemLines, WrapTextWithAnsi(cl, itemContentWidth)...)
				}
			}
		}
		flushInline()

		if len(itemLines) > 0 {
			for li, il := range itemLines {
				if li == 0 && styledMarker != "" {
					lines = append(lines, indent+styledMarker+il)
				} else if il == "" {
					lines = append(lines, "")
				} else {
					lines = append(lines, contIndent+il)
				}
			}
		} else if styledMarker != "" && (len(item.Tokens) == 0 || item.Tokens[0].Type != "list") {
			lines = append(lines, indent+styledMarker)
		}

		if token.Loose && i < len(token.Items)-1 {
			lines = append(lines, "")
		}
	}

	return lines
}

func (m *Markdown) getLongestWordWidth(text string, maxWidth int) int {
	plain := cmAnsiEscapeRegex.Replace(text, "")
	if plain == "" {
		return 1
	}
	words := strings.Fields(plain)
	longest := 1
	for _, w := range words {
		wWidth := VisibleWidth(w)
		if wWidth > longest {
			longest = wWidth
		}
	}
	if maxWidth > 0 && longest > maxWidth {
		return maxWidth
	}
	return longest
}

func (m *Markdown) wrapCellText(text string, maxWidth int) []string {
	if maxWidth <= 0 {
		return []string{""}
	}
	wrapped := WrapTextWithAnsi(text, maxWidth)
	if len(wrapped) == 0 {
		return []string{""}
	}
	return wrapped
}

func (m *Markdown) renderTable(token *marked.Token, availableWidth int, styleContext cmInlineStyleContext) []string {
	if token == nil || len(token.Header) == 0 {
		return nil
	}
	numCols := len(token.Header)
	borderOverhead := numCols*3 + 1
	availForCols := max(numCols, availableWidth-borderOverhead)

	headerStyled := make([]string, numCols)
	for c, cell := range token.Header {
		inner := m.renderInlineTokens(cell.Tokens, styleContext)
		headerStyled[c] = m.applyThemeBold(inner)
	}

	rowsStyled := make([][]string, len(token.Rows))
	for r, row := range token.Rows {
		rowSlice := make([]string, numCols)
		for c := 0; c < numCols; c++ {
			if c < len(row) {
				rowSlice[c] = m.renderInlineTokens(row[c].Tokens, styleContext)
			}
		}
		rowsStyled[r] = rowSlice
	}

	naturalWidths := make([]int, numCols)
	minWidths := make([]int, numCols)
	for c := 0; c < numCols; c++ {
		w := max(1, VisibleWidth(headerStyled[c]))
		mw := m.getLongestWordWidth(headerStyled[c], availForCols)
		for _, row := range rowsStyled {
			cw := VisibleWidth(row[c])
			if cw > w {
				w = cw
			}
			cmw := m.getLongestWordWidth(row[c], availForCols)
			if cmw > mw {
				mw = cmw
			}
		}
		naturalWidths[c] = w
		minWidths[c] = mw
	}

	colWidths := make([]int, numCols)
	totalNatural := 0
	for _, w := range naturalWidths {
		totalNatural += w
	}

	if totalNatural <= availForCols {
		copy(colWidths, naturalWidths)
	} else {
		// Fit columns into availForCols, respecting word minimums where possible.
		totalMin := 0
		for c := 0; c < numCols; c++ {
			totalMin += minWidths[c]
		}
		if totalMin <= availForCols {
			remaining := availForCols - totalMin
			totalExcess := 0
			for c := 0; c < numCols; c++ {
				if naturalWidths[c] > minWidths[c] {
					totalExcess += naturalWidths[c] - minWidths[c]
				}
			}
			for c := 0; c < numCols; c++ {
				colWidths[c] = minWidths[c]
				if totalExcess > 0 && naturalWidths[c] > minWidths[c] {
					extra := remaining * (naturalWidths[c] - minWidths[c]) / totalExcess
					colWidths[c] += extra
				}
			}
			used := 0
			for _, w := range colWidths {
				used += w
			}
			for c := 0; used < availForCols; c = (c + 1) % numCols {
				if colWidths[c] < naturalWidths[c] {
					colWidths[c]++
					used++
				} else if used < availForCols && c == numCols-1 {
					break
				}
			}
		} else {
			base := availForCols / numCols
			rem := availForCols % numCols
			for c := 0; c < numCols; c++ {
				colWidths[c] = max(1, base)
				if c < rem {
					colWidths[c]++
				}
			}
		}
	}

	alignCell := func(cellLine string, colWidth int, align string) string {
		vis := VisibleWidth(cellLine)
		pad := max(0, colWidth-vis)
		switch align {
		case "right":
			return strings.Repeat(" ", pad) + cellLine
		case "center":
			leftPad := pad / 2
			rightPad := pad - leftPad
			return strings.Repeat(" ", leftPad) + cellLine + strings.Repeat(" ", rightPad)
		default:
			return cellLine + strings.Repeat(" ", pad)
		}
	}

	buildBorder := func(left, mid, right string) string {
		var b strings.Builder
		b.WriteString(left)
		for c, w := range colWidths {
			if c > 0 {
				b.WriteString(mid)
			}
			b.WriteString(strings.Repeat("─", w+2))
		}
		b.WriteString(right)
		return b.String()
	}

	renderRowLines := func(cells []string, isHeader bool) []string {
		wrappedCols := make([][]string, numCols)
		maxLines := 1
		for c := 0; c < numCols; c++ {
			wrapped := m.wrapCellText(cells[c], colWidths[c])
			wrappedCols[c] = wrapped
			if len(wrapped) > maxLines {
				maxLines = len(wrapped)
			}
		}
		out := make([]string, 0, maxLines)
		for li := 0; li < maxLines; li++ {
			var b strings.Builder
			b.WriteString("│ ")
			for c := 0; c < numCols; c++ {
				if c > 0 {
					b.WriteString(" │ ")
				}
				cellText := ""
				if li < len(wrappedCols[c]) {
					cellText = wrappedCols[c][li]
				}
				align := ""
				if isHeader {
					if c < len(token.Header) && token.Header[c].Align != nil {
						align = *token.Header[c].Align
					}
				} else if c < len(token.Header) && token.Header[c].Align != nil {
					align = *token.Header[c].Align
				}
				b.WriteString(alignCell(cellText, colWidths[c], align))
			}
			b.WriteString(" │")
			out = append(out, b.String())
		}
		return out
	}

	lines := make([]string, 0, len(rowsStyled)*2+5)
	lines = append(lines, buildBorder("┌", "┬", "┐"))
	lines = append(lines, renderRowLines(headerStyled, true)...)
	lines = append(lines, buildBorder("├", "┼", "┤"))
	for i, row := range rowsStyled {
		if i > 0 {
			lines = append(lines, buildBorder("├", "┼", "┤"))
		}
		lines = append(lines, renderRowLines(row, false)...)
	}
	lines = append(lines, buildBorder("└", "┴", "┘"))
	return lines
}

// Compile-time check that Markdown implements Component.
var _ Component = (*Markdown)(nil)

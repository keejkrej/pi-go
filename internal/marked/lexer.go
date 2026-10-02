package marked

import (
	"fmt"
	"os"
	"strings"
)

// TokenizerExtension is a marked block or inline tokenizer extension.
// Start, when set, returns the rune index of a possible match in src, or a
// negative index when there is none. Marked calls Start on src without its
// first rune so a paragraph or text token stops before the next extension.
// Tokenize returns nil when src does not open the extension. lx is the lexer
// marked passes as this.lexer; tokens is the list built so far.
type TokenizerExtension struct {
	Name        string
	Level       string
	Start       func(src string) int
	Tokenize    func(lx *Lexer, src string, tokens []*Token) *Token
	ChildTokens []string
}

type lexState struct {
	inLink      bool
	inRawBlock  bool
	linkEmitted bool
	top         bool
}

type inlineJob struct {
	src string
	dst *[]*Token
}

// Lexer is a marked block lexer. Construct one with Marked.Lexer; the
// exported methods exist so tokenizer overrides and extensions can re-enter
// lexing the way marked's Tokenizer.lexer does.
type Lexer struct {
	Links map[string]Link

	state       lexState
	inlineQueue []inlineJob
	tok         *Tokenizer
	rules       *ruleSet
	blockExt    []TokenizerExtension
	inlineExt   []TokenizerExtension
	startBlock  []func(string) int
	startInline []func(string) int
	silent      bool
	pedantic    bool
	gfm         bool
}

// Marked is a marked instance: options, tokenizer overrides, and extensions.
// The default is GFM on, breaks off, pedantic off, matching pi's markdown.ts
// (which only replaces the strikethrough tokenizer and adds LaTeX extensions).
type Marked struct {
	gfm         bool
	breaks      bool
	pedantic    bool
	silent      bool
	tokenizer   *Tokenizer
	blockExt    []TokenizerExtension
	inlineExt   []TokenizerExtension
	startBlock  []func(string) int
	startInline []func(string) int
	childTokens map[string][]string
}

// Option configures a Marked instance.
type Option func(*Marked)

// GFM toggles GitHub Flavored Markdown. The default is on.
func GFM(on bool) Option { return func(m *Marked) { m.gfm = on } }

// Breaks toggles GFM soft line breaks. The default is off. Ignored unless GFM is on.
func Breaks(on bool) Option { return func(m *Marked) { m.breaks = on } }

// Pedantic selects marked's original markdown.pl rules. It wins over GFM.
func Pedantic(on bool) Option { return func(m *Marked) { m.pedantic = on } }

// Silent makes an infinite-loop lex error print and stop instead of panicking.
func Silent(on bool) Option { return func(m *Marked) { m.silent = on } }

// WithTokenizer installs tok. Nil func fields keep the built-in tokenizers.
func WithTokenizer(tok *Tokenizer) Option {
	return func(m *Marked) { m.tokenizer = tok }
}

// New returns a Marked instance. GFM defaults on.
func New(opts ...Option) *Marked {
	m := &Marked{gfm: true}
	return m.SetOptions(opts...)
}

// SetOptions merges opts into m, like marked's setOptions.
func (m *Marked) SetOptions(opts ...Option) *Marked {
	for _, opt := range opts {
		if opt != nil {
			opt(m)
		}
	}
	return m
}

// Use registers tokenizer extensions. Later extensions of the same level run
// first, matching marked's unshift order. Name is required. Level is "block"
// or "inline".
func (m *Marked) Use(exts ...TokenizerExtension) *Marked {
	for _, ext := range exts {
		if ext.Name == "" {
			panic("extension name required")
		}
		if ext.Tokenize == nil {
			panic("extension tokenizer required")
		}
		switch ext.Level {
		case "block":
			m.blockExt = append([]TokenizerExtension{ext}, m.blockExt...)
			if ext.Start != nil {
				m.startBlock = append(m.startBlock, ext.Start)
			}
		case "inline":
			m.inlineExt = append([]TokenizerExtension{ext}, m.inlineExt...)
			if ext.Start != nil {
				m.startInline = append(m.startInline, ext.Start)
			}
		default:
			panic("extension level must be 'block' or 'inline'")
		}
		if len(ext.ChildTokens) > 0 {
			if m.childTokens == nil {
				m.childTokens = map[string][]string{}
			}
			m.childTokens[ext.Name] = append([]string(nil), ext.ChildTokens...)
		}
	}
	return m
}

// Lexer lexes Markdown with m's options, tokenizer, and extensions.
func (m *Marked) Lexer(src string) []*Token {
	if m.tokenizer == nil {
		m.tokenizer = NewTokenizer()
	}
	lx := &Lexer{
		Links:       map[string]Link{},
		tok:         m.tokenizer,
		rules:       pickRules(m.pedantic, m.gfm, m.breaks),
		blockExt:    m.blockExt,
		inlineExt:   m.inlineExt,
		startBlock:  m.startBlock,
		startInline: m.startInline,
		silent:      m.silent,
		pedantic:    m.pedantic,
		gfm:         m.gfm,
		state:       lexState{top: true},
	}
	lx.tok.rules = lx.rules
	lx.tok.Lexer = lx
	return lx.lex(src)
}

// Lex lexes src with marked's defaults (GFM on, breaks off).
func Lex(src string) []*Token { return New().Lexer(src) }

// WalkTokens calls fn for every token, then its children. Tables and lists
// are walked the way marked walks them. ChildTokens from Use are honored.
func (m *Marked) WalkTokens(tokens []*Token, fn func(*Token)) {
	if m == nil || fn == nil {
		return
	}
	for _, tok := range tokens {
		if tok == nil {
			continue
		}
		fn(tok)
		switch tok.Type {
		case "table":
			for i := range tok.Header {
				m.WalkTokens(tok.Header[i].Tokens, fn)
			}
			for _, row := range tok.Rows {
				for i := range row {
					m.WalkTokens(row[i].Tokens, fn)
				}
			}
		case "list":
			m.WalkTokens(tok.Items, fn)
		default:
			if names := m.childTokens[tok.Type]; len(names) > 0 {
				for _, name := range names {
					switch name {
					case "tokens":
						m.WalkTokens(tok.Tokens, fn)
					case "items":
						m.WalkTokens(tok.Items, fn)
					default:
						if tok.Extra != nil {
							m.WalkTokens(tok.Extra[name], fn)
						}
					}
				}
			} else if len(tok.Tokens) > 0 {
				m.WalkTokens(tok.Tokens, fn)
			}
		}
	}
}

// WalkTokens walks tokens with default child rules (no extensions).
func WalkTokens(tokens []*Token, fn func(*Token)) { New().WalkTokens(tokens, fn) }

func (lx *Lexer) lex(src string) []*Token {
	src = lx.rules.other.carriageReturn.replace(src, "\n")
	var tokens []*Token
	lx.blockTokens(src, &tokens, false)
	// Copy the queue length first: inline lexing does not append jobs, but
	// a tokenizer override might.
	n := len(lx.inlineQueue)
	for i := 0; i < n; i++ {
		job := lx.inlineQueue[i]
		*job.dst = lx.inlineTokens(job.src, *job.dst)
	}
	lx.inlineQueue = nil
	return tokens
}

// BlockTokens lexes src as block tokens. Extensions may call it.
func (lx *Lexer) BlockTokens(src string) []*Token {
	lx.tok.Lexer = lx
	return lx.blockTokens(src, nil, false)
}

// QueueInline is marked's lexer.inline. Inline tokens of src are appended to
// dst after the block pass so later link definitions are visible.
func (lx *Lexer) QueueInline(dst *[]*Token, src string) { lx.queueInline(dst, src) }

func (lx *Lexer) queueInline(dst *[]*Token, src string) {
	if dst == nil {
		panic("marked: QueueInline dst is nil")
	}
	if *dst == nil {
		*dst = []*Token{}
	}
	lx.inlineQueue = append(lx.inlineQueue, inlineJob{src: src, dst: dst})
}

func (lx *Lexer) popInline() {
	if len(lx.inlineQueue) == 0 {
		panic("marked: inline queue empty")
	}
	lx.inlineQueue = lx.inlineQueue[:len(lx.inlineQueue)-1]
}

func (lx *Lexer) lastInline() *inlineJob {
	if len(lx.inlineQueue) == 0 {
		panic("marked: inline queue empty")
	}
	return &lx.inlineQueue[len(lx.inlineQueue)-1]
}

func (lx *Lexer) blockTokens(src string, tokens *[]*Token, lastParagraphClipped bool) []*Token {
	lx.tok.Lexer = lx
	if tokens == nil {
		v := []*Token{}
		tokens = &v
	}
	if lx.pedantic {
		src = lx.rules.other.tabCharGlobal.replace(src, "    ")
		src = lx.rules.other.spaceLine.replace(src, "")
	}
	srcLen := int(^uint(0) >> 1)
	for src != "" {
		n := runeCount(src)
		if n < srcLen {
			srcLen = n
		} else {
			lx.infinite(src)
			break
		}
		if lx.consumeBlockExt(src, tokens, &src) {
			continue
		}
		if tok := lx.tok.callSpace(src); tok != nil {
			src = runeCut(src, runeCount(tok.Raw))
			if runeCount(tok.Raw) == 1 && len(*tokens) > 0 {
				(*tokens)[len(*tokens)-1].Raw += "\n"
			} else {
				*tokens = append(*tokens, tok)
			}
			continue
		}
		if tok := lx.tok.callCode(src); tok != nil {
			src = runeCut(src, runeCount(tok.Raw))
			if last := lastTok(*tokens); last != nil && (last.Type == "paragraph" || last.Type == "text") {
				last.Raw = joinNL(last.Raw, tok.Raw)
				last.Text += "\n" + tok.Text
				lx.lastInline().src = last.Text
			} else {
				*tokens = append(*tokens, tok)
			}
			continue
		}
		if tok := lx.tok.callFences(src); tok != nil {
			src = runeCut(src, runeCount(tok.Raw))
			*tokens = append(*tokens, tok)
			continue
		}
		if tok := lx.tok.callHeading(src); tok != nil {
			src = runeCut(src, runeCount(tok.Raw))
			*tokens = append(*tokens, tok)
			continue
		}
		if tok := lx.tok.callHr(src); tok != nil {
			src = runeCut(src, runeCount(tok.Raw))
			*tokens = append(*tokens, tok)
			continue
		}
		if tok := lx.tok.callBlockquote(src); tok != nil {
			src = runeCut(src, runeCount(tok.Raw))
			*tokens = append(*tokens, tok)
			continue
		}
		if tok := lx.tok.callList(src); tok != nil {
			src = runeCut(src, runeCount(tok.Raw))
			*tokens = append(*tokens, tok)
			continue
		}
		if tok := lx.tok.callHTML(src); tok != nil {
			src = runeCut(src, runeCount(tok.Raw))
			*tokens = append(*tokens, tok)
			continue
		}
		if tok := lx.tok.callDef(src); tok != nil {
			src = runeCut(src, runeCount(tok.Raw))
			if last := lastTok(*tokens); last != nil && (last.Type == "paragraph" || last.Type == "text") {
				last.Raw = joinNL(last.Raw, tok.Raw)
				last.Text += "\n" + tok.Raw
				lx.lastInline().src = last.Text
			} else if _, exists := lx.Links[tok.Tag]; !exists {
				lx.Links[tok.Tag] = Link{Href: tok.Href, Title: tok.Title}
				*tokens = append(*tokens, tok)
			}
			continue
		}
		if tok := lx.tok.callTable(src); tok != nil {
			src = runeCut(src, runeCount(tok.Raw))
			*tokens = append(*tokens, tok)
			continue
		}
		if tok := lx.tok.callLHeading(src); tok != nil {
			src = runeCut(src, runeCount(tok.Raw))
			*tokens = append(*tokens, tok)
			continue
		}
		cut := clipExtension(src, lx.startBlock)
		if lx.state.top {
			if tok := lx.tok.callParagraph(cut); tok != nil {
				if lastParagraphClipped {
					if last := lastTok(*tokens); last != nil && last.Type == "paragraph" {
						last.Raw = joinNL(last.Raw, tok.Raw)
						last.Text += "\n" + tok.Text
						lx.popInline()
						lx.lastInline().src = last.Text
					} else {
						*tokens = append(*tokens, tok)
					}
				} else {
					*tokens = append(*tokens, tok)
				}
				lastParagraphClipped = runeCount(cut) != runeCount(src)
				src = runeCut(src, runeCount(tok.Raw))
				continue
			}
		}
		if tok := lx.tok.callText(src); tok != nil {
			src = runeCut(src, runeCount(tok.Raw))
			if last := lastTok(*tokens); last != nil && last.Type == "text" {
				last.Raw = joinNL(last.Raw, tok.Raw)
				last.Text += "\n" + tok.Text
				lx.popInline()
				lx.lastInline().src = last.Text
			} else {
				*tokens = append(*tokens, tok)
			}
			continue
		}
		if src != "" {
			lx.infinite(src)
			break
		}
	}
	lx.state.top = true
	return *tokens
}

func lastTok(tokens []*Token) *Token {
	if len(tokens) == 0 {
		return nil
	}
	return tokens[len(tokens)-1]
}

func (lx *Lexer) consumeBlockExt(src string, tokens *[]*Token, srcOut *string) bool {
	for i := range lx.blockExt {
		ext := &lx.blockExt[i]
		tok := ext.Tokenize(lx, src, *tokens)
		if tok == nil {
			continue
		}
		*srcOut = runeCut(src, runeCount(tok.Raw))
		*tokens = append(*tokens, tok)
		return true
	}
	return false
}

func clipExtension(src string, starts []func(string) int) string {
	if len(starts) == 0 || src == "" {
		return src
	}
	temp := runeCut(src, 1)
	startIndex := int(^uint(0) >> 1)
	found := false
	for _, fn := range starts {
		if fn == nil {
			continue
		}
		idx := fn(temp)
		if idx >= 0 && idx < startIndex {
			startIndex = idx
			found = true
		}
	}
	if !found {
		return src
	}
	return jsSlice(src, 0, startIndex+1)
}

// InlineTokens lexes src as inline tokens immediately.
func (lx *Lexer) InlineTokens(src string) []*Token {
	lx.tok.Lexer = lx
	if lx.tok.rules == nil {
		lx.tok.rules = lx.rules
	}
	return lx.inlineTokens(src, nil)
}

func (lx *Lexer) inlineTokens(src string, into []*Token) []*Token {
	lx.tok.Lexer = lx
	if into == nil {
		into = []*Token{}
	}
	masked := src
	if len(lx.Links) > 0 && strings.Contains(src, "[") {
		masked = lx.rules.inline.reflinkSearch.replaceFunc(masked, func(c *caps) string {
			return lx.maskReflink(c.full)
		})
	}
	masked = lx.rules.inline.anyPunctuation.replaceFunc(masked, func(c *caps) string {
		return strings.Repeat("+", runeCount(c.full))
	})
	masked = lx.rules.inline.blockSkip.replaceFunc(masked, func(c *caps) string {
		context := c.group(2)
		offset := 0
		if context != "" {
			offset = runeCount(context)
		}
		n := runeCount(c.full) - offset - 2
		if n < 0 {
			n = 0
		}
		return jsSlice(c.full, 0, offset) + "[" + strings.Repeat("a", n) + "]"
	})

	keepPrev := false
	prevChar := ""
	srcLen := int(^uint(0) >> 1)
	for src != "" {
		n := runeCount(src)
		if n < srcLen {
			srcLen = n
		} else {
			lx.infinite(src)
			break
		}
		if !keepPrev {
			prevChar = ""
		}
		keepPrev = false

		if lx.consumeInlineExt(src, &into, &src) {
			continue
		}
		if tok := lx.tok.callEscape(src); tok != nil {
			src = runeCut(src, runeCount(tok.Raw))
			into = append(into, tok)
			continue
		}
		if tok := lx.tok.callTag(src); tok != nil {
			src = runeCut(src, runeCount(tok.Raw))
			into = append(into, tok)
			continue
		}
		if tok := lx.tok.callLink(src); tok != nil {
			src = runeCut(src, runeCount(tok.Raw))
			into = append(into, tok)
			continue
		}
		if tok := lx.tok.callReflink(src, lx.Links); tok != nil {
			src = runeCut(src, runeCount(tok.Raw))
			if tok.Type == "text" && len(into) > 0 && into[len(into)-1].Type == "text" {
				into[len(into)-1].Raw += tok.Raw
				into[len(into)-1].Text += tok.Text
			} else {
				into = append(into, tok)
			}
			continue
		}
		if tok := lx.tok.callEmStrong(src, masked, prevChar); tok != nil {
			src = runeCut(src, runeCount(tok.Raw))
			into = append(into, tok)
			continue
		}
		if tok := lx.tok.callCodespan(src); tok != nil {
			src = runeCut(src, runeCount(tok.Raw))
			into = append(into, tok)
			continue
		}
		if tok := lx.tok.callBr(src); tok != nil {
			src = runeCut(src, runeCount(tok.Raw))
			into = append(into, tok)
			continue
		}
		if tok := lx.tok.callDel(src, masked, prevChar); tok != nil {
			src = runeCut(src, runeCount(tok.Raw))
			into = append(into, tok)
			continue
		}
		if tok := lx.tok.callAutolink(src); tok != nil {
			src = runeCut(src, runeCount(tok.Raw))
			into = append(into, tok)
			continue
		}
		if !lx.state.inLink {
			if tok := lx.tok.callURL(src); tok != nil {
				src = runeCut(src, runeCount(tok.Raw))
				into = append(into, tok)
				continue
			}
		}
		cut := clipExtension(src, lx.startInline)
		if tok := lx.tok.callInlineText(cut); tok != nil {
			src = runeCut(src, runeCount(tok.Raw))
			if lastChar(tok.Raw) != "_" {
				prevChar = lastChar(tok.Raw)
			}
			keepPrev = true
			if len(into) > 0 && into[len(into)-1].Type == "text" {
				into[len(into)-1].Raw += tok.Raw
				into[len(into)-1].Text += tok.Text
			} else {
				into = append(into, tok)
			}
			continue
		}
		if src != "" {
			lx.infinite(src)
			break
		}
	}
	return into
}

func (lx *Lexer) consumeInlineExt(src string, into *[]*Token, srcOut *string) bool {
	for i := range lx.inlineExt {
		ext := &lx.inlineExt[i]
		tok := ext.Tokenize(lx, src, *into)
		if tok == nil {
			continue
		}
		*srcOut = runeCut(src, runeCount(tok.Raw))
		*into = append(*into, tok)
		return true
	}
	return false
}

func (lx *Lexer) maskReflink(match0 string) string {
	refStart := strings.LastIndex(match0, "[")
	key := jsSlice(match0, refStart+1, -1)
	if _, ok := lx.Links[key]; !ok {
		return match0
	}
	if refStart > 1 && charAt(match0, 0) != "!" {
		text := jsSlice(match0, 1, refStart-1)
		if lx.linkInText(text) {
			masked := lx.rules.inline.reflinkSearch.replaceFunc(text, func(c *caps) string {
				return lx.maskReflink(c.full)
			})
			return "[" + masked + "][" + strings.Repeat("a", runeCount(match0)-refStart-2) + "]"
		}
	}
	return "[" + strings.Repeat("a", runeCount(match0)-2) + "]"
}

func (lx *Lexer) linkInText(text string) bool {
	if !strings.Contains(text, "[") {
		return false
	}
	linkRule := lx.rules.inline.link
	for _, match := range lx.rules.inline.blockSkip.execAll(text) {
		if linkRule.test(match.full) && charAt(text, match.index-1) != "!" {
			return true
		}
	}
	for _, match := range lx.rules.inline.reflinkSearch.execAll(text) {
		match0 := match.full
		refStart := strings.LastIndex(match0, "[")
		key := jsSlice(match0, refStart+1, -1)
		if charAt(match0, 0) == "!" {
			continue
		}
		if _, ok := lx.Links[key]; !ok {
			continue
		}
		if refStart > 1 && lx.linkInText(jsSlice(match0, 1, refStart-1)) {
			continue
		}
		return true
	}
	return false
}

func (lx *Lexer) infinite(src string) {
	msg := fmt.Sprintf("Infinite loop on byte: %d", firstRune(src))
	if lx.silent {
		fmt.Fprintln(os.Stderr, msg)
		return
	}
	panic(msg)
}

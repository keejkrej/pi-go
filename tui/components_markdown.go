// Ported from packages/tui/src/components/markdown.ts (pi v1.0.0).

package tui

import (
	"weak"

	"github.com/keejkrej/pi-go/internal/jsre"
	"github.com/keejkrej/pi-go/internal/marked"
)

// cmStrictStrikethroughRegex matches ~~text~~ only when the inside does not start or end with whitespace.
var cmStrictStrikethroughRegex = jsre.MustCompile(`^(~~)(?=[^\s~])((?:\\.|[^\\])*?(?:\\.|[^\s~\\]))\1(?=[^~]|$)`, "")

// cmNewStrictStrikethroughTokenizer is Tokenizer with Del replaced.
// Nil func fields keep marked's built-in rules.
func cmNewStrictStrikethroughTokenizer() *marked.Tokenizer {
	tok := marked.NewTokenizer()
	tok.Del = func(src, maskedSrc, prevChar string) *marked.Token {
		return cmStrictDel(tok, src, maskedSrc, prevChar)
	}
	return tok
}

// cmStrictDel is Tokenizer.Del. Marked.Lexer sets tok.Lexer before Del runs.
// The TS override calls lexer.inlineTokens, which is Lexer.InlineTokens.
func cmStrictDel(tok *marked.Tokenizer, src, maskedSrc, prevChar string) *marked.Token {
	panic("unported: cmStrictDel")
}

func cmIsEscaped(source string, index int) bool {
	panic("unported: cmIsEscaped")
}

func cmFindClosingDelimiter(source, closing string, start int) int {
	panic("unported: cmFindClosingDelimiter")
}

func cmLooksLikePendingDollarMath(source string) bool {
	panic("unported: cmLooksLikePendingDollarMath")
}

// cmTokenizeInlineLatex is the inline latex tokenizer.
// lx and tokens are TokenizerExtension.Tokenize arguments; the TS function only reads source.
func cmTokenizeInlineLatex(lx *marked.Lexer, src string, tokens []*marked.Token) *marked.Token {
	panic("unported: cmTokenizeInlineLatex")
}

// cmTokenizeBlockLatex is the block latex tokenizer.
// lx and tokens are TokenizerExtension.Tokenize arguments; the TS function only reads source.
func cmTokenizeBlockLatex(lx *marked.Lexer, src string, tokens []*marked.Token) *marked.Token {
	panic("unported: cmTokenizeBlockLatex")
}

// cmLatexBlockStart returns a rune index, or a negative index when src has no block-math start.
func cmLatexBlockStart(src string) int {
	panic("unported: cmLatexBlockStart")
}

// cmLatexInlineStart returns a rune index, or a negative index when src has no inline-math start.
func cmLatexInlineStart(src string) int {
	panic("unported: cmLatexInlineStart")
}

// cmLatexMarkdownExtensions are the latexBlock and latex tokenizer extensions, in that order.
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
	panic("unported: cmTrimPartialClosingFences")
}

// cmMarkdownParser lexes Markdown with strict strikethrough and the LaTeX extensions.
var cmMarkdownParser = cmNewMarkdownParser()

func cmNewMarkdownParser() *marked.Marked {
	m := marked.New()
	m.SetOptions(marked.WithTokenizer(cmNewStrictStrikethroughTokenizer()))
	m.Use(cmLatexMarkdownExtensions...)
	return m
}

// DefaultTextStyle is the default text styling for markdown content.
// It applies to all text unless markdown formatting overrides it.
type DefaultTextStyle struct {
	// Color is the foreground color function.
	Color func(text string) string `json:"color,omitzero"`
	// BgColor is the background color function.
	BgColor func(text string) string `json:"bgColor,omitzero"`
	// Bold selects bold text.
	Bold *bool `json:"bold,omitzero"`
	// Italic selects italic text.
	Italic *bool `json:"italic,omitzero"`
	// Strikethrough selects strikethrough text.
	Strikethrough *bool `json:"strikethrough,omitzero"`
	// Underline selects underline text.
	Underline *bool `json:"underline,omitzero"`
}

// MarkdownTheme styles markdown elements.
// Each function takes text and returns styled text with ANSI codes.
type MarkdownTheme struct {
	Heading         func(text string) string                 `json:"heading"`
	Link            func(text string) string                 `json:"link"`
	LinkUrl         func(text string) string                 `json:"linkUrl"`
	Code            func(text string) string                 `json:"code"`
	CodeBlock       func(text string) string                 `json:"codeBlock"`
	CodeBlockBorder func(text string) string                 `json:"codeBlockBorder"`
	Quote           func(text string) string                 `json:"quote"`
	QuoteBorder     func(text string) string                 `json:"quoteBorder"`
	Hr              func(text string) string                 `json:"hr"`
	ListBullet      func(text string) string                 `json:"listBullet"`
	Bold            func(text string) string                 `json:"bold"`
	Italic          func(text string) string                 `json:"italic"`
	Strikethrough   func(text string) string                 `json:"strikethrough"`
	Underline       func(text string) string                 `json:"underline"`
	HighlightCode   func(code string, lang *string) []string `json:"highlightCode,omitzero"`
	// CodeBlockIndent prefixes each rendered code block line. Nil means "  ".
	CodeBlockIndent *string `json:"codeBlockIndent,omitzero"`
}

// MarkdownOptions configures Markdown parsing and rendering.
type MarkdownOptions struct {
	// PreserveOrderedListMarkers keeps source list markers instead of normalizing them.
	PreserveOrderedListMarkers *bool `json:"preserveOrderedListMarkers,omitzero"`
	// PreserveBackslashEscapes keeps source backslash escapes instead of normalizing escaped punctuation.
	PreserveBackslashEscapes *bool `json:"preserveBackslashEscapes,omitzero"`
	// Transform rewrites source Markdown before parsing. availableWidth is the width available for content.
	Transform func(markdown string, availableWidth int) string `json:"transform,omitzero"`
	// RenderLatex renders supported LaTeX math as Unicode text. Nil means true.
	RenderLatex *bool `json:"renderLatex,omitzero"`
}

// cmInlineStyleContext restores an outer ANSI prefix after an inline style resets it.
type cmInlineStyleContext struct {
	applyText   func(text string) string
	stylePrefix string
}

// cmCachedTokens is the weak-cache target: normalized source and its token tree.
type cmCachedTokens struct {
	source string
	tokens []*marked.Token
}

// Markdown renders Markdown as styled terminal lines. It implements Component.
type Markdown struct {
	text             string
	paddingX         int
	paddingY         int
	defaultTextStyle *DefaultTextStyle
	theme            MarkdownTheme
	options          MarkdownOptions
	// defaultStylePrefix is nil until computed. A computed empty prefix is non-nil.
	defaultStylePrefix *string
	// cachedText is nil when the line cache is cold. Empty string is a cached source.
	cachedText  *string
	cachedWidth *int
	// cachedLines is nil when uncached. An empty non-nil slice is a cached empty render.
	cachedLines []string
	// cachedTokens survives Invalidate. A collected or unset cache yields a nil pointer.
	cachedTokens weak.Pointer[cmCachedTokens]
}

// NewMarkdown returns a component for text with horizontal and vertical padding.
// Nil defaultTextStyle and options mean unset. options is shallow-copied.
func NewMarkdown(text string, paddingX, paddingY int, theme MarkdownTheme, defaultTextStyle *DefaultTextStyle, options *MarkdownOptions) *Markdown {
	panic("unported: NewMarkdown")
}

// SetText replaces the source and drops the line cache.
func (m *Markdown) SetText(text string) {
	panic("unported: Markdown.SetText")
}

// Invalidate drops the line cache. Parsed tokens stay cached.
func (m *Markdown) Invalidate() {
	panic("unported: Markdown.Invalidate")
}

// Render returns one string per line for the viewport width.
func (m *Markdown) Render(width int) []string {
	panic("unported: Markdown.Render")
}

func (m *Markdown) applyDefaultStyle(text string) string {
	panic("unported: Markdown.applyDefaultStyle")
}

func (m *Markdown) getDefaultStylePrefix() string {
	panic("unported: Markdown.getDefaultStylePrefix")
}

func (m *Markdown) getStylePrefix(styleFn func(text string) string) string {
	panic("unported: Markdown.getStylePrefix")
}

func (m *Markdown) getDefaultInlineStyleContext() cmInlineStyleContext {
	panic("unported: Markdown.getDefaultInlineStyleContext")
}

func (m *Markdown) renderToken(token *marked.Token, width int, nextTokenType *string, styleContext *cmInlineStyleContext) []string {
	panic("unported: Markdown.renderToken")
}

func (m *Markdown) renderInlineTokens(tokens []*marked.Token, styleContext *cmInlineStyleContext) string {
	panic("unported: Markdown.renderInlineTokens")
}

func (m *Markdown) getOrderedListMarker(item *marked.Token) *string {
	panic("unported: Markdown.getOrderedListMarker")
}

func (m *Markdown) getUnorderedListMarker(item *marked.Token) *string {
	panic("unported: Markdown.getUnorderedListMarker")
}

func (m *Markdown) renderList(token *marked.Token, depth, width int, styleContext *cmInlineStyleContext) []string {
	panic("unported: Markdown.renderList")
}

func (m *Markdown) getLongestWordWidth(text string, maxWidth *int) int {
	panic("unported: Markdown.getLongestWordWidth")
}

func (m *Markdown) wrapCellText(text string, maxWidth int, stylePrefix string) []string {
	panic("unported: Markdown.wrapCellText")
}

func (m *Markdown) renderTable(token *marked.Token, availableWidth int, nextTokenType *string, styleContext *cmInlineStyleContext) []string {
	panic("unported: Markdown.renderTable")
}

// Markdown implements Component.
var _ Component = (*Markdown)(nil)

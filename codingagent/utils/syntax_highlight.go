// Ported from packages/coding-agent/src/utils/syntax-highlight.ts (pi v1.5.2).

package utils

import (
	"regexp"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/keejkrej/pi-go/internal/highlight"
)

// HighlightTheme maps highlight.js scope names (e.g. "keyword", "string") to styling functions.
type HighlightTheme map[string]func(text string) string

// HighlightOptions configures syntax highlighting behavior.
type HighlightOptions struct {
	Language       string
	IgnoreIllegals bool
	Theme          HighlightTheme
}

const shEagerLanguageCount = 21

var (
	shClassAttrRe = regexp.MustCompile(`(?i)\bclass\s*=\s*(?:"([^"]*)"|'([^']*)')`)

	shAllLoaded     atomic.Bool
	shExtraOnce     sync.Once
	shExtraLexers   map[string]chroma.Lexer
	shTotalLangNums int
)

func shBuildExtraLexers() {
	shExtraLexers = make(map[string]chroma.Lexer)
	count := 0
	for _, lx := range lexers.GlobalLexerRegistry.Lexers {
		if lx == nil {
			continue
		}
		cfg := lx.Config()
		if cfg == nil || strings.EqualFold(cfg.Name, "plaintext") || strings.EqualFold(cfg.Name, "fallback") {
			continue
		}
		count++
		shExtraLexers[strings.ToLower(cfg.Name)] = lx
		for _, alias := range cfg.Aliases {
			key := strings.ToLower(alias)
			if _, exists := shExtraLexers[key]; !exists {
				shExtraLexers[key] = lx
			}
		}
	}
	if count < shEagerLanguageCount {
		count = shEagerLanguageCount
	}
	shTotalLangNums = count
}

func shLookupExtraLexer(language string) chroma.Lexer {
	shExtraOnce.Do(shBuildExtraLexers)
	return shExtraLexers[strings.ToLower(language)]
}

// SupportsLanguage reports whether syntax highlighting supports the given language or alias.
func SupportsLanguage(language string) bool {
	if highlight.SupportsLanguage(language) {
		return true
	}
	if !shAllLoaded.Load() {
		return false
	}
	return shLookupExtraLexer(language) != nil
}

// LoadAllHighlightLanguages enables deferred full-catalogue chroma lexers beyond the eager startup set.
func LoadAllHighlightLanguages() {
	shExtraOnce.Do(shBuildExtraLexers)
	shAllLoaded.Store(true)
}

// GetHighlightLanguageCount returns the number of currently active highlight languages.
func GetHighlightLanguageCount() int {
	if !shAllLoaded.Load() {
		return shEagerLanguageCount
	}
	shExtraOnce.Do(shBuildExtraLexers)
	return shTotalLangNums
}

func shResolveScopeFormatter(theme HighlightTheme, scope string) func(string) string {
	if theme == nil || scope == "" {
		return nil
	}
	if fn, ok := theme[scope]; ok && fn != nil {
		return fn
	}
	// Strip highlight.js subscope qualifiers (e.g. "title.function" -> "title", "selector-tag" -> "selector").
	if dot := strings.IndexByte(scope, '.'); dot > 0 {
		if fn, ok := theme[scope[:dot]]; ok && fn != nil {
			return fn
		}
	}
	if dash := strings.IndexByte(scope, '-'); dash > 0 {
		if fn, ok := theme[scope[:dash]]; ok && fn != nil {
			return fn
		}
	}
	if under := strings.TrimSuffix(scope, "_"); under != scope {
		if fn, ok := theme[under]; ok && fn != nil {
			return fn
		}
	}
	return nil
}

func shFormatText(fn func(string) string, text string) string {
	if fn == nil || text == "" {
		return text
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = fn(line)
		}
	}
	return strings.Join(lines, "\n")
}

func shExtractSpanScope(tag string) string {
	m := shClassAttrRe.FindStringSubmatch(tag)
	if len(m) == 0 {
		return ""
	}
	classList := m[1]
	if classList == "" {
		classList = m[2]
	}
	for _, cls := range strings.Fields(classList) {
		if strings.HasPrefix(cls, "hljs-") {
			return strings.TrimPrefix(cls, "hljs-")
		}
	}
	return ""
}

func shActiveFormatter(stack []string, theme HighlightTheme) func(string) string {
	if theme == nil {
		return nil
	}
	for i := len(stack) - 1; i >= 0; i-- {
		if fn := shResolveScopeFormatter(theme, stack[i]); fn != nil {
			return fn
		}
	}
	return theme["default"]
}

func shIsSpanOpenTagStart(html string, index int) bool {
	if !strings.HasPrefix(html[index:], "<span") {
		return false
	}
	if index+5 >= len(html) {
		return false
	}
	next := html[index+5]
	return next == '>' || next == ' ' || next == '\t' || next == '\n' || next == '\r'
}

// RenderHighlightedHtml renders highlight.js HTML (<span class="hljs-...">) using theme
// formatters, decoding HTML entities in a single pass and inheriting parent styles across
// unmapped or unscoped nested spans while preserving non-span literal angle brackets.
func RenderHighlightedHtml(html string, theme HighlightTheme) string {
	var out strings.Builder
	var textBuf strings.Builder
	var stack []string

	flushText := func() {
		if textBuf.Len() == 0 {
			return
		}
		txt := textBuf.String()
		textBuf.Reset()
		if fn := shActiveFormatter(stack, theme); fn != nil {
			out.WriteString(shFormatText(fn, txt))
		} else {
			out.WriteString(txt)
		}
	}

	for i := 0; i < len(html); {
		if html[i] == '<' {
			if shIsSpanOpenTagStart(html, i) {
				if closeRel := strings.IndexByte(html[i:], '>'); closeRel > 0 {
					flushText()
					tag := html[i : i+closeRel+1]
					stack = append(stack, shExtractSpanScope(tag))
					i += closeRel + 1
					continue
				}
			} else if strings.HasPrefix(html[i:], "</span>") {
				flushText()
				if len(stack) > 0 {
					stack = stack[:len(stack)-1]
				}
				i += len("</span>")
				continue
			}
		}
		if html[i] == '&' {
			if decoded := DecodeHtmlEntityAt(html, i); decoded != nil {
				textBuf.WriteString(decoded.Char)
				i += decoded.Length
				continue
			}
		}
		textBuf.WriteByte(html[i])
		i++
	}
	flushText()
	return out.String()
}

func shScopeOfChromaToken(t chroma.TokenType) string {
	switch t {
	case chroma.CommentPreproc, chroma.CommentPreprocFile, chroma.CommentHashbang:
		return "meta"
	case chroma.CommentSpecial:
		return "doctag"
	case chroma.KeywordType:
		return "type"
	case chroma.KeywordConstant, chroma.NameConstant:
		return "literal"
	case chroma.NameBuiltin, chroma.NameBuiltinPseudo, chroma.NamePseudo:
		return "built_in"
	case chroma.NameFunction, chroma.NameFunctionMagic:
		return "function"
	case chroma.NameClass, chroma.NameException:
		return "class"
	case chroma.NameAttribute, chroma.NameProperty:
		return "attr"
	case chroma.NameTag:
		return "tag"
	case chroma.NameDecorator:
		return "meta"
	case chroma.NameLabel, chroma.NameEntity, chroma.NameNamespace:
		return "name"
	case chroma.NameKeyword:
		return "keyword"
	case chroma.NameOperator:
		return "operator"
	case chroma.LiteralStringRegex:
		return "regexp"
	case chroma.LiteralStringInterpol:
		return ""
	case chroma.GenericInserted:
		return "addition"
	case chroma.GenericDeleted:
		return "deletion"
	case chroma.GenericEmph:
		return "emphasis"
	case chroma.GenericStrong:
		return "strong"
	case chroma.GenericUnderline:
		return "link"
	case chroma.GenericHeading, chroma.GenericSubheading, chroma.GenericPrompt:
		return "title"
	case chroma.Punctuation, chroma.TextPunctuation:
		return "punctuation"
	}
	switch {
	case t.InCategory(chroma.Comment):
		return "comment"
	case t.InCategory(chroma.Keyword):
		return "keyword"
	case t.InSubCategory(chroma.NameVariable):
		return "variable"
	case t.InSubCategory(chroma.LiteralString):
		return "string"
	case t.InSubCategory(chroma.LiteralNumber):
		return "number"
	case t.InCategory(chroma.Literal):
		return "literal"
	case t.InCategory(chroma.Operator):
		return "operator"
	case t.InCategory(chroma.Punctuation):
		return "punctuation"
	default:
		return ""
	}
}

func shHighlightExtraLexer(code, language string) []highlight.Token {
	lx := shLookupExtraLexer(language)
	if lx == nil {
		return []highlight.Token{{Text: code}}
	}
	it, err := chroma.Coalesce(lx).Tokenise(&chroma.TokeniseOptions{State: "root", EnsureLF: false}, code)
	if err != nil || it == nil {
		return []highlight.Token{{Text: code}}
	}
	var out []highlight.Token
	rest := code
	for {
		tok := it()
		if tok == chroma.EOF || rest == "" {
			break
		}
		text := tok.Value
		if text == "" {
			continue
		}
		switch {
		case strings.HasPrefix(rest, text):
			rest = rest[len(text):]
		case strings.HasPrefix(text, rest):
			text = rest
			rest = ""
		default:
			out = append(out, highlight.Token{Text: rest})
			rest = ""
			continue
		}
		out = append(out, highlight.Token{Text: text, Scope: shScopeOfChromaToken(tok.Type)})
	}
	if rest != "" {
		out = append(out, highlight.Token{Text: rest})
	}
	return out
}

// Highlight tokenizes code via internal/highlight (or deferred chroma lexers when loaded)
// and applies options.Theme formatters line-by-line so multi-line tokens keep lines independent.
func Highlight(code string, options HighlightOptions) string {
	var tokens []highlight.Token
	if highlight.SupportsLanguage(options.Language) {
		tokens = highlight.Highlight(code, options.Language)
	} else if shAllLoaded.Load() && options.Language != "" {
		tokens = shHighlightExtraLexer(code, options.Language)
	} else {
		return code
	}
	if len(tokens) == 0 {
		return code
	}
	var out strings.Builder
	for _, tok := range tokens {
		if tok.Scope != "" {
			if fn := shResolveScopeFormatter(options.Theme, tok.Scope); fn != nil {
				out.WriteString(shFormatText(fn, tok.Text))
				continue
			}
		}
		out.WriteString(tok.Text)
	}
	return out.String()
}

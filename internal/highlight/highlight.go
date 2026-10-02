// Package highlight lexes source with chroma and labels each run with a
// highlight.js scope name from the TUI theme (theme.ts buildCliHighlightTheme).
//
// Languages are the eager set syntax-highlight.ts registers, plus the aliases
// those highlight.js 10.7.3 grammars declare. chroma v2.27.0 has a lexer for
// every one of them, so UnsupportedLanguages is empty. The full catalogue
// loadAllHighlightLanguages pulls in is not supported. Token boundaries follow
// chroma, not highlight.js (PORTING.md §16).
package highlight

import (
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

// Token is a slice of source classified under one highlight.js scope.
// Scope is empty for text the theme leaves unstyled (hljs has no class).
type Token struct {
	Text  string
	Scope string
}

// UnsupportedLanguages lists eager syntax-highlight.ts languages that chroma
// cannot lex. It is empty: every registered grammar has a lexer. Names outside
// langLexer (the rest of highlight.js) are unsupported and are not listed here.
var UnsupportedLanguages []string

// langLexer maps a lowercased highlight.js language or alias to a chroma alias
// that is registered by name, not by filename. lexers.Get falls through to
// filename matching, so aliases chroma does not declare (h, jsx, gyp, pm, …)
// are resolved here and never passed to Get.
var langLexer = map[string]string{
	"python":     "python",
	"py":         "python",
	"gyp":        "python",
	"ipython":    "python",
	"java":       "java",
	"jsp":        "java",
	"go":         "go",
	"golang":     "go",
	"javascript": "javascript",
	"js":         "javascript",
	"jsx":        "javascript",
	"mjs":        "javascript",
	"cjs":        "javascript",
	"json":       "json",
	"cpp":        "cpp",
	"cc":         "cpp",
	"c++":        "cpp",
	"h++":        "cpp",
	"hpp":        "cpp",
	"hh":         "cpp",
	"hxx":        "cpp",
	"cxx":        "cpp",
	"typescript": "typescript",
	"ts":         "typescript",
	"tsx":        "typescript",
	"php":        "php",
	"php3":       "php",
	"php4":       "php",
	"php5":       "php",
	"php6":       "php",
	"php7":       "php",
	"php8":       "php",
	"ruby":       "ruby",
	"rb":         "ruby",
	"gemspec":    "ruby",
	"podspec":    "ruby",
	"thor":       "ruby",
	"irb":        "ruby",
	"c":          "c",
	"h":          "c",
	"csharp":     "csharp",
	"cs":         "csharp",
	"c#":         "csharp",
	"nix":        "nix",
	"nixos":      "nix",
	"bash":       "bash",
	"sh":         "bash",
	"zsh":        "bash",
	"rust":       "rust",
	"rs":         "rust",
	"scala":      "scala",
	"kotlin":     "kotlin",
	"kt":         "kotlin",
	"kts":        "kotlin",
	"swift":      "swift",
	"dart":       "dart",
	"groovy":     "groovy",
	"perl":       "perl",
	"pl":         "perl",
	"pm":         "perl",
	"lua":        "lua",
}

// SupportsLanguage reports whether lang is an eager highlight.js language or
// one of its aliases. Matching is case-insensitive, as hljs.getLanguage is.
func SupportsLanguage(lang string) bool {
	_, ok := langLexer[strings.ToLower(lang)]
	return ok
}

// Highlight tokenises code as lang. An unknown language, a lexer error, or a
// panic inside chroma yields the code as a single plain token. Empty code
// returns nil. Joined Text equals code: chroma's EnsureNL newline is dropped,
// and EnsureLF is not applied.
func Highlight(code, lang string) (out []Token) {
	if code == "" {
		return nil
	}
	defer func() {
		if recover() != nil {
			out = plain(code)
		}
	}()
	alias, ok := langLexer[strings.ToLower(lang)]
	if !ok {
		return plain(code)
	}
	lx := lexers.Get(alias)
	if lx == nil {
		return plain(code)
	}
	it, err := chroma.Coalesce(lx).Tokenise(&chroma.TokeniseOptions{
		State:    "root",
		EnsureLF: false,
	}, code)
	if err != nil || it == nil {
		return plain(code)
	}
	var raw []chroma.Token
	for {
		tok := it()
		if tok == chroma.EOF {
			break
		}
		raw = append(raw, tok)
	}
	return fit(code, raw)
}

func plain(code string) []Token {
	if code == "" {
		return nil
	}
	return []Token{{Text: code}}
}

// fit keeps only bytes from code. A token that runs past the leftover input
// (the EnsureNL newline) is cut to that leftover; anything else the lexer
// rewrote is emitted as plain text so the join still equals code.
func fit(code string, raw []chroma.Token) []Token {
	rest := code
	out := make([]Token, 0, len(raw))
	for _, tok := range raw {
		if rest == "" {
			break
		}
		text := tok.Value
		if text == "" {
			continue
		}
		scope := scopeOf(tok.Type)
		switch {
		case strings.HasPrefix(rest, text):
			rest = rest[len(text):]
		case strings.HasPrefix(text, rest):
			text = rest
			rest = ""
		default:
			out = append(out, Token{Text: rest})
			rest = ""
			continue
		}
		out = append(out, Token{Text: text, Scope: scope})
	}
	if rest != "" {
		out = append(out, Token{Text: rest})
	}
	return merge(out)
}

func merge(in []Token) []Token {
	if len(in) == 0 {
		return nil
	}
	out := make([]Token, 0, len(in))
	for _, tok := range in {
		if tok.Text == "" {
			continue
		}
		if n := len(out); n > 0 && out[n-1].Scope == tok.Scope {
			out[n-1].Text += tok.Text
			continue
		}
		out = append(out, tok)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// scopeOf maps a chroma token onto a key of buildCliHighlightTheme.
// HTML-only classes (subst, selector-tag, title.function_, …) are not emitted.
func scopeOf(t chroma.TokenType) string {
	switch t {
	case chroma.CommentPreproc, chroma.CommentPreprocFile, chroma.CommentHashbang:
		return "meta"
	case chroma.CommentSpecial:
		return "doctag"
	case chroma.KeywordType:
		return "type"
	case chroma.KeywordConstant:
		return "literal"
	case chroma.NameBuiltin, chroma.NameBuiltinPseudo, chroma.NamePseudo:
		return "built_in"
	case chroma.NameFunction, chroma.NameFunctionMagic:
		return "function"
	case chroma.NameClass, chroma.NameException:
		return "class"
	case chroma.NameAttribute, chroma.NameProperty:
		return "attr"
	case chroma.NameConstant:
		return "literal"
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

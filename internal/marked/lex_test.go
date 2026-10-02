package marked

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/google/go-cmp/cmp"
)

type lexCase struct {
	Name      string `json:"name"`
	Src       string `json:"src"`
	GFM       *bool  `json:"gfm,omitempty"`
	Breaks    bool   `json:"breaks,omitempty"`
	Pedantic  bool   `json:"pedantic,omitempty"`
	StrictDel bool   `json:"strictDel,omitempty"`
	Latex     bool   `json:"latex,omitempty"`
	Ext       bool   `json:"ext,omitempty"`
}

func TestLexerMatchesMarked(t *testing.T) {
	gfmOff := false
	cases := []lexCase{
		{Name: "paragraph", Src: "Hello world.\n"},
		{Name: "heading", Src: "# Title\n\n## Sub #"},
		{Name: "setext", Src: "Title\n====\n"},
		{Name: "fenced", Src: "```js\nlet x = 1;\n```\n"},
		{Name: "fenced-plain", Src: "```\nplain\n```\n"},
		{Name: "emphasis", Src: "a *em* b and a_b_\n"},
		{Name: "strong", Src: "**a *b* c** and a**b**\n"},
		{Name: "link", Src: "[text](https://ex.com \"t\")\n"},
		{Name: "image", Src: "![alt](img.png)\n"},
		{Name: "list", Src: "- one\n- two\n"},
		{Name: "ordered", Src: "1. one\n2. two\n"},
		{Name: "task", Src: "- [x] task\n- [ ] no\n"},
		{Name: "loose", Src: "- a\n\n- b\n"},
		{Name: "blockquote", Src: "> a\n> - b\n>   c\n"},
		{Name: "table", Src: "| h | i |\n| - | :-: |\n| 1 | 2 |\n"},
		{Name: "del", Src: "~~strike~~ and ~~ a ~~\n"},
		{Name: "autolink", Src: "https://ex.com and <https://ex.com>\n"},
		{Name: "reflink", Src: "[x][y]\n\n[y]: /z \"title\"\n"},
		{Name: "hr", Src: "---\n"},
		{Name: "indented", Src: "    code\n"},
		{Name: "html", Src: "<div>\n\nx\n\n</div>\n"},
		{Name: "crlf", Src: "a\r\nb\r\n"},
		{Name: "breaks", Src: "foo\nbar\n", Breaks: true},
		{Name: "pedantic", Src: "~~x~~\n\n| a |\n| - |\n| b |\n", Pedantic: true},
		{Name: "nogfm", Src: "~~x~~ and https://ex.com\n", GFM: &gfmOff},
		{Name: "combined", Src: "# Hi\n\npara **bold** and *em* and ~~del~~ and `code`.\n\n```js\nlet x = 1;\n```\n\n[link](https://ex.com \"t\")\n\n![alt](img.png)\n\n- a\n- [x] b\n\n> quote\n\n| h | i |\n| - | - |\n| 1 | 2 |\n\nhttps://ex.com and <https://ex.com>\n"},
		{Name: "strict", Src: "~~a~~ ~~ a ~~ ~~a b~~\n", StrictDel: true},
		{Name: "mention", Src: "See @ada in text.\n\n::note\nhello **world** @bee\n::\n", Ext: true},
		{Name: "latex", Src: "Keep ~~both~~ but not ~~ spaced ~~.\n\n$$\nE = mc^2\n$$\n\nSee $x+1$ now.\n\n$x+\n\n\\[\nalpha\n", StrictDel: true, Latex: true},
		{Name: "nested-list", Src: "- a\n  - b\n    c\n"},
		{Name: "mailto", Src: "<user@ex.com>\n"},
		{Name: "escape", Src: "\\*not em\\*\n"},
		{Name: "link-em", Src: "[*a*](https://x.com)\n"},
		{Name: "def-merge", Src: "[x][y]\n[y]: /z\n"},
		{Name: "continuation", Src: "* item\n  next\n"},
		{Name: "image-title", Src: "![alt](img.png \"t\")\n"},
		{Name: "del-em", Src: "~~*a*~~\n"},
		{Name: "dash-blank", Src: "-\n"},
		{Name: "nested-paren", Src: "[a](https://ex.com/a_(b) \"t\")\n"},
		{Name: "codespan-space", Src: "`` `code` ``\n"},
	}

	want := nodeLex(t, cases)
	if len(want) != len(cases) {
		t.Fatalf("node returned %d cases, want %d", len(want), len(cases))
	}
	for i, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			if want[i].Error != "" {
				t.Fatalf("node: %s", want[i].Error)
			}
			tokens, links := goLex(c)
			gotBody, err := json.Marshal(map[string]any{
				"tokens": projectAll(tokens),
				"links":  links,
			})
			if err != nil {
				t.Fatal(err)
			}
			wantBody, err := json.Marshal(map[string]any{
				"tokens": want[i].Tokens,
				"links":  want[i].Links,
			})
			if err != nil {
				t.Fatal(err)
			}
			var gotAny, wantAny any
			if err := json.Unmarshal(gotBody, &gotAny); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(wantBody, &wantAny); err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(wantAny, gotAny); diff != "" {
				t.Fatalf("token tree mismatch (-marked +go):\n%s", diff)
			}
		})
	}
}

type nodeResult struct {
	Name   string `json:"name"`
	Error  string `json:"error"`
	Tokens []any  `json:"tokens"`
	Links  []any  `json:"links"`
}

func nodeLex(t *testing.T, cases []lexCase) []nodeResult {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"cases": cases})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", "testdata/lex.mjs")
	cmd.Stdin = bytes.NewReader(payload)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node lex: %v\n%s", err, out)
	}
	var got []nodeResult
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("node json: %v\n%s", err, out)
	}
	return got
}

func goLex(c lexCase) ([]*Token, []any) {
	var opts []Option
	if c.GFM != nil {
		opts = append(opts, GFM(*c.GFM))
	}
	if c.Breaks {
		opts = append(opts, Breaks(true))
	}
	if c.Pedantic {
		opts = append(opts, Pedantic(true))
	}
	if c.StrictDel {
		tok := NewTokenizer()
		tok.Del = func(src, _, _ string) *Token {
			cap := strictDelRE.exec(src)
			if cap == nil {
				return nil
			}
			text := cap.group(2)
			return &Token{Type: "del", Raw: cap.full, Text: text, Tokens: tok.Lexer.InlineTokens(text)}
		}
		opts = append(opts, WithTokenizer(tok))
	}
	m := New(opts...)
	if c.Latex {
		m.Use(latexBlockExt(), latexInlineExt())
	}
	if c.Ext {
		m.Use(alertExt(), mentionExt())
	}
	tokens := m.Lexer(c.Src)
	var links []any
	m.WalkTokens(tokens, func(t *Token) {
		if t.Type == "link" && t.Href != "" {
			links = append(links, t.Href)
		}
	})
	if links == nil {
		links = []any{}
	}
	return tokens, links
}

var strictDelRE = mustCompile(`^(~~)(?=[^\s~])((?:\\.|[^\\])*?(?:\\.|[^\s~\\]))\1(?=[^~]|$)`, "")

var (
	textTypes = map[string]bool{
		"heading": true, "paragraph": true, "text": true, "code": true, "blockquote": true,
		"list_item": true, "html": true, "em": true, "strong": true, "del": true,
		"codespan": true, "link": true, "image": true, "escape": true, "mention": true,
		"alert": true, "latex": true, "latexBlock": true,
	}
)

func projectAll(tokens []*Token) []any {
	out := make([]any, len(tokens))
	for i, tok := range tokens {
		out[i] = projectTok(tok)
	}
	return out
}

func projectTok(tok *Token) map[string]any {
	o := map[string]any{"type": tok.Type}
	o["raw"] = tok.Raw
	if tok.Text != "" || textTypes[tok.Type] {
		o["text"] = tok.Text
	}
	if tok.Href != "" || tok.Type == "link" || tok.Type == "image" || tok.Type == "def" {
		o["href"] = tok.Href
	}
	if tok.Title != nil {
		o["title"] = *tok.Title
	}
	if tok.Depth > 0 {
		o["depth"] = tok.Depth
	}
	if tok.Lang != nil {
		o["lang"] = *tok.Lang
	}
	if tok.CodeBlockStyle != "" {
		o["codeBlockStyle"] = tok.CodeBlockStyle
	}
	if tok.Tokens != nil {
		o["tokens"] = projectAll(tok.Tokens)
	}
	if tok.Type == "list" {
		o["ordered"] = tok.Ordered
		if tok.Start != nil {
			o["start"] = *tok.Start
		} else {
			o["start"] = nil
		}
		o["loose"] = tok.Loose
		if tok.Items == nil {
			o["items"] = []any{}
		} else {
			o["items"] = projectAll(tok.Items)
		}
	}
	if tok.Type == "list_item" {
		o["task"] = tok.Task
		o["loose"] = tok.Loose
		if tok.Checked != nil {
			o["checked"] = *tok.Checked
		}
	}
	if tok.Type == "checkbox" && tok.Checked != nil {
		o["checked"] = *tok.Checked
	}
	if tok.Tag != "" {
		o["tag"] = tok.Tag
	}
	if tok.Pending {
		o["pending"] = true
	}
	if tok.Block {
		o["block"] = true
	}
	if tok.Pre {
		o["pre"] = true
	}
	if tok.Type == "table" {
		align := make([]any, len(tok.Align))
		for i, a := range tok.Align {
			if a != nil {
				align[i] = *a
			}
		}
		o["align"] = align
		header := make([]any, len(tok.Header))
		for i := range tok.Header {
			header[i] = projectCell(tok.Header[i])
		}
		o["header"] = header
		rows := make([]any, len(tok.Rows))
		for i, row := range tok.Rows {
			cells := make([]any, len(row))
			for j := range row {
				cells[j] = projectCell(row[j])
			}
			rows[i] = cells
		}
		o["rows"] = rows
	}
	return o
}

func projectCell(c TableCell) map[string]any {
	var align any
	if c.Align != nil {
		align = *c.Align
	}
	tokens := []any{}
	if c.Tokens != nil {
		tokens = projectAll(c.Tokens)
	}
	return map[string]any{
		"text":   c.Text,
		"header": c.Header,
		"align":  align,
		"tokens": tokens,
	}
}

func alertExt() TokenizerExtension {
	return TokenizerExtension{
		Name:  "alert",
		Level: "block",
		Start: func(src string) int {
			return runeIndex(src, "::")
		},
		Tokenize: func(lx *Lexer, src string, _ []*Token) *Token {
			m := alertRE.exec(src)
			if m == nil {
				return nil
			}
			text := m.group(2)
			return &Token{Type: "alert", Raw: m.full, Text: text, Tokens: lx.InlineTokens(text)}
		},
	}
}

func mentionExt() TokenizerExtension {
	return TokenizerExtension{
		Name:  "mention",
		Level: "inline",
		Start: func(src string) int {
			return runeIndex(src, "@")
		},
		Tokenize: func(_ *Lexer, src string, _ []*Token) *Token {
			m := mentionRE.exec(src)
			if m == nil {
				return nil
			}
			return &Token{Type: "mention", Raw: m.full, Text: m.group(1)}
		},
	}
}

var (
	alertRE   = mustCompile(`^::([A-Za-z0-9_]+)[ \t]*\n([\s\S]*?)\n::(?:\n|$)`, "")
	mentionRE = mustCompile(`^@([A-Za-z0-9_]+)`, "")
)

func latexBlockExt() TokenizerExtension {
	return TokenizerExtension{
		Name:  "latexBlock",
		Level: "block",
		Start: latexBlockStart,
		Tokenize: func(_ *Lexer, src string, _ []*Token) *Token {
			return tokenizeBlockLatex(src)
		},
	}
}

func latexInlineExt() TokenizerExtension {
	return TokenizerExtension{
		Name:  "latex",
		Level: "inline",
		Start: latexInlineStart,
		Tokenize: func(_ *Lexer, src string, _ []*Token) *Token {
			return tokenizeInlineLatex(src)
		},
	}
}

var (
	pendingMathRE   = mustCompile(`\\[A-Za-z]+|[_^=+*/<>()[\]|±≤≥≠≈∈→⇒∞∫∑√-]`, "")
	dollarSpaceRE   = mustCompile(`^\$\s`, "")
	trailingSpaceRE = mustCompile(`\s$`, "")
	leadingDigitRE  = mustCompile(`^\d`, "")
	identConstRE    = mustCompile(`^[A-Z_][A-Z0-9_]*(?:[^A-Za-z0-9_\s])?$`, "")
	identAfterRE    = mustCompile(`^[A-Za-z_][A-Za-z0-9_]*`, "")
	blockDollarRE   = mustCompile(`^ {0,3}\$\$[ \t]*(?:\n)?([\s\S]*?)\$\$[ \t]*(?:\n|$)`, "")
	blockBracketRE  = mustCompile(`^ {0,3}\\\[[ \t]*(?:\n)?([\s\S]*?)\\\][ \t]*(?:\n|$)`, "")
	pendBracketRE   = mustCompile(`^ {0,3}\\\[[ \t]*(?:\n)?([\s\S]*)$`, "")
	pendDollarRE    = mustCompile(`^ {0,3}\$\$[ \t]*(?:\n)?([\s\S]*)$`, "")
	blockStartRE    = mustCompile(`(?:^|\n) {0,3}(?:\$\$|\\\[)`, "")
)

func latexBlockStart(src string) int {
	m := blockStartRE.exec(src)
	if m == nil {
		return -1
	}
	extra := 0
	if strings.HasPrefix(m.full, "\n") {
		extra = 1
	}
	return m.index + extra
}

func latexInlineStart(src string) int {
	best := -1
	for _, sub := range []string{"$", `\(`, `\[`} {
		i := runeIndex(src, sub)
		if i >= 0 && (best < 0 || i < best) {
			best = i
		}
	}
	return best
}

func tokenizeBlockLatex(src string) *Token {
	if m := blockDollarRE.exec(src); m != nil && m.group(1) != "" {
		return &Token{Type: "latexBlock", Raw: m.full, Text: jsTrim(m.group(1))}
	}
	if m := blockBracketRE.exec(src); m != nil && m.group(1) != "" {
		return &Token{Type: "latexBlock", Raw: m.full, Text: jsTrim(m.group(1))}
	}
	if m := pendBracketRE.exec(src); m != nil {
		return &Token{Type: "latexBlock", Raw: m.full, Text: m.group(1), Pending: true}
	}
	if m := pendDollarRE.exec(src); m != nil && m.group(1) != "" && pendingMathRE.test(m.group(1)) {
		return &Token{Type: "latexBlock", Raw: m.full, Text: m.group(1), Pending: true}
	}
	return nil
}

func tokenizeInlineLatex(src string) *Token {
	opening, closing := "", ""
	switch {
	case strings.HasPrefix(src, "$$"):
		opening, closing = "$$", "$$"
	case strings.HasPrefix(src, `\(`):
		opening, closing = `\(`, `\)`
	case strings.HasPrefix(src, `\[`):
		opening, closing = `\[`, `\]`
	case strings.HasPrefix(src, "$") && !dollarSpaceRE.test(src):
		opening, closing = "$", "$"
	default:
		return nil
	}
	openLen := runeCount(opening)
	closingIndex := findClosingDelim(src, closing, openLen)
	if closingIndex >= 0 && opening == "$" {
		inner := jsSlice(src, openLen, closingIndex)
		after := jsSlice(src, closingIndex+runeCount(closing), runeCount(src))
		if trailingSpaceRE.test(inner) || leadingDigitRE.test(after) || (identConstRE.test(inner) && identAfterRE.test(after)) || strings.Contains(inner, "`") {
			return nil
		}
	}
	if closingIndex < 0 {
		pendingSource := jsSlice(src, openLen, runeCount(src))
		if strings.HasPrefix(opening, `\`) || pendingMathRE.test(pendingSource) {
			return &Token{Type: "latex", Raw: src, Text: pendingSource, Pending: true}
		}
		return nil
	}
	text := jsSlice(src, openLen, closingIndex)
	if text == "" || strings.Contains(text, "\n") {
		return nil
	}
	raw := jsSlice(src, 0, closingIndex+runeCount(closing))
	return &Token{Type: "latex", Raw: raw, Text: text}
}

func findClosingDelim(source, closing string, start int) int {
	restFrom := start
	for {
		rest := jsSlice(source, restFrom, runeCount(source))
		rel := runeIndex(rest, closing)
		if rel < 0 {
			return -1
		}
		abs := restFrom + rel
		if !latexEscaped(source, abs) {
			return abs
		}
		restFrom = abs + runeCount(closing)
	}
}

func latexEscaped(source string, index int) bool {
	backslashes := 0
	for pos := index - 1; pos >= 0 && charAt(source, pos) == `\`; pos-- {
		backslashes++
	}
	return backslashes%2 == 1
}

func runeIndex(s, sub string) int {
	i := strings.Index(s, sub)
	if i < 0 {
		return -1
	}
	return utf8.RuneCountInString(s[:i])
}

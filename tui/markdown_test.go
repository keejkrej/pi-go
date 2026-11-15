package tui

import (
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/internal/marked"
)

var tmarkAnsiStripRe = regexp.MustCompile("\x1b\\[[0-9;?]*[a-zA-Z]|\x1b\\]8;;[^\x07]*\x07")

func tmarkStripAnsi(s string) string {
	return tmarkAnsiStripRe.ReplaceAllString(s, "")
}

func tmarkBold(s string) string          { return "\x1b[1m" + s + "\x1b[22m" }
func tmarkDim(s string) string           { return "\x1b[2m" + s + "\x1b[22m" }
func tmarkItalic(s string) string        { return "\x1b[3m" + s + "\x1b[23m" }
func tmarkUnderline(s string) string     { return "\x1b[4m" + s + "\x1b[24m" }
func tmarkStrikethrough(s string) string { return "\x1b[9m" + s + "\x1b[29m" }
func tmarkYellow(s string) string        { return "\x1b[33m" + s + "\x1b[39m" }
func tmarkBlue(s string) string          { return "\x1b[34m" + s + "\x1b[39m" }
func tmarkGreen(s string) string         { return "\x1b[32m" + s + "\x1b[39m" }
func tmarkCyan(s string) string          { return "\x1b[36m" + s + "\x1b[39m" }
func tmarkGray(s string) string          { return "\x1b[90m" + s + "\x1b[39m" }
func tmarkBoldCyan(s string) string      { return "\x1b[1m\x1b[36m" + s + "\x1b[39m\x1b[22m" }

func tmarkDefaultTheme() MarkdownTheme {
	return MarkdownTheme{
		Heading:         tmarkBoldCyan,
		Link:            tmarkBlue,
		LinkUrl:         tmarkDim,
		Code:            tmarkYellow,
		CodeBlock:       tmarkGreen,
		CodeBlockBorder: tmarkDim,
		Quote:           tmarkItalic,
		QuoteBorder:     tmarkDim,
		Hr:              tmarkDim,
		ListBullet:      tmarkCyan,
		Bold:            tmarkBold,
		Italic:          tmarkItalic,
		Strikethrough:   tmarkStrikethrough,
		Underline:       tmarkUnderline,
	}
}

func tmarkBoolPtr(b bool) *bool { return &b }

func tmarkTrimEndLines(lines []string) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = strings.TrimRight(tmarkStripAnsi(line), " \t")
	}
	return out
}

func TestMarkdownUnit_EmptySetTextInvalidate(t *testing.T) {
	m := NewMarkdown("", 1, 0, tmarkDefaultTheme(), nil, nil)
	if got := m.Render(80); len(got) != 0 {
		t.Fatalf("empty markdown Render(80) = %#v, want empty slice", got)
	}

	m.SetText("   \n\t ")
	if got := m.Render(80); len(got) != 0 {
		t.Fatalf("whitespace-only markdown Render(80) = %#v, want empty slice", got)
	}

	m.SetText("hello")
	m.Invalidate()
	if m.cachedText != nil || m.cachedWidth != nil || m.cachedLines != nil {
		t.Fatalf("Invalidate did not clear render cache")
	}
	if m.cachedTokens.Value() != nil || m.blocks.Value() != nil {
		t.Fatalf("Invalidate did not clear token/block cache")
	}
}

func TestMarkdownUnit_URLStopAtCJKAndPunctuation(t *testing.T) {
	t.Run("stops before CJK punctuation", func(t *testing.T) {
		for _, tc := range []struct {
			input string
			want  string
		}{
			{"https://example.com/foo，bar", "https://example.com/foo"},
			{"https://example.com/foo。下一句", "https://example.com/foo"},
			{"https://example.com/foo、然后", "https://example.com/foo"},
			{"https://example.com/path)后文", "https://example.com/path)后文"},
			{"https://example.com/path）后文", "https://example.com/path"},
			{"https://example.com/path(a)后文", "https://example.com/path(a)后文"},
			{"https://example.com/path(a)", "https://example.com/path(a)"},
			{"https://example.com/path」结束", "https://example.com/path"},
			{"www.example.com/abc；说明", "www.example.com/abc"},
			{"https://example.com/a\u3000b", "https://example.com/a"},
		} {
			tok := cmMarkdownURL(tc.input)
			if tok == nil || tok.Type != "link" {
				t.Fatalf("cmMarkdownURL(%q) = %#v, want link token", tc.input, tok)
			}
			if tok.Text != tc.want {
				t.Errorf("cmMarkdownURL(%q).Text = %q, want %q", tc.input, tok.Text, tc.want)
			}
		}
	})

	t.Run("rejects non-URLs", func(t *testing.T) {
		for _, input := range []string{"hello world", "mailto:not-gfm", "example.com"} {
			if tok := cmMarkdownURL(input); tok != nil {
				t.Errorf("cmMarkdownURL(%q) = %#v, want nil", input, tok)
			}
		}
	})
}

func TestMarkdownUnit_CjkFriendlyEmphasisAndStrictDel(t *testing.T) {
	t.Run("CJK cross-script and same-script emphasis", func(t *testing.T) {
		for _, tc := range []struct {
			src       string
			wantTypes []string
		}{
			{"**日本語(にほんご)**です。", []string{"strong", "text"}},
			{"日本語の**『カッコ』**。", []string{"text", "strong", "text"}},
			{"**中文（括号）**测试", []string{"strong", "text"}},
			{"한국어**(괄호)**테스트", []string{"text", "strong", "text"}},
			{"ここは*「強調」*です", []string{"text", "em", "text"}},
			{"a**「b」**c", []string{"text", "strong", "text"}},
			{"foo * bar * baz", []string{"text"}},
			{"a_b_c", []string{"text"}},
		} {
			tokens, hasImages := cmLexMarkdown(tc.src)
			if hasImages {
				t.Fatalf("unexpected hasImages for %q", tc.src)
			}
			if len(tokens) == 0 || len(tokens[0].Tokens) == 0 {
				t.Fatalf("expected inline tokens for %q, got %#v", tc.src, tokens)
			}
			var gotTypes []string
			for _, tok := range tokens[0].Tokens {
				gotTypes = append(gotTypes, tok.Type)
			}
			if !reflect.DeepEqual(gotTypes, tc.wantTypes) {
				t.Errorf("inline token types for %q = %#v, want %#v", tc.src, gotTypes, tc.wantTypes)
			}
		}
	})

	t.Run("strict double-tilde strikethrough only", func(t *testing.T) {
		tokensSingle, _ := cmLexMarkdown("~one tilde~")
		for _, tok := range tokensSingle[0].Tokens {
			if tok.Type == "del" {
				t.Fatalf("single tilde produced del token: %#v", tok)
			}
		}

		tokensDouble, _ := cmLexMarkdown("~~two tildes~~")
		foundDel := false
		for _, tok := range tokensDouble[0].Tokens {
			if tok.Type == "del" && tok.Text == "two tildes" {
				foundDel = true
			}
		}
		if !foundDel {
			t.Fatalf("expected del token for ~~two tildes~~, got %#v", tokensDouble[0].Tokens)
		}
	})
}

func TestMarkdownUnit_LatexBlockAndInlineTokenizer(t *testing.T) {
	t.Run("tokenizes closed inline and display math", func(t *testing.T) {
		tokens, _ := cmLexMarkdown(`Before $\frac{1}{2}$ and \(x^2\)`)
		var latexTexts []string
		for _, tok := range tokens[0].Tokens {
			if tok.Type == "latex" {
				latexTexts = append(latexTexts, tok.Text)
			}
		}
		if !reflect.DeepEqual(latexTexts, []string{`\frac{1}{2}`, `x^2`}) {
			t.Fatalf("latex tokens = %#v, want [\\frac{1}{2}, x^2]", latexTexts)
		}

		blockTokens, _ := cmLexMarkdown("Before\n\n$$\nx^2 + y^2 = z^2\n$$\n\nafter")
		foundBlockLatex := false
		for _, tok := range blockTokens {
			if tok.Type == "latexBlock" && tok.Text == "x^2 + y^2 = z^2" && !tok.Pending {
				foundBlockLatex = true
			}
		}
		if !foundBlockLatex {
			t.Fatalf("expected closed latexBlock token, got %#v", blockTokens)
		}
	})

	t.Run("unclosed display math before tables (#11159)", func(t *testing.T) {
		src := "$$\n| Name | Value |\n| --- | --- |\n| a | 1 |"
		tokens, _ := cmLexMarkdown(src)
		foundTable := false
		for _, tok := range tokens {
			if tok.Type == "latexBlock" {
				t.Fatalf("table after orphan $$ should not be swallowed as latexBlock: %#v", tok)
			}
			if tok.Type == "table" {
				foundTable = true
			}
		}
		if !foundTable {
			t.Fatalf("expected table token after orphan $$, got %#v", tokens)
		}
	})

	t.Run("containsTableStart detects GFM pipe tables", func(t *testing.T) {
		if !cmContainsTableStart("text\n| A | B |\n| --- | --- |\n") {
			t.Fatal("expected cmContainsTableStart to detect table header+separator")
		}
		if cmContainsTableStart("x^2 + y^2") {
			t.Fatal("unexpected table start for math content")
		}
	})

	t.Run("currency and shell variables are not inline math", func(t *testing.T) {
		for _, src := range []string{
			"Costs $5 and $10 or $HOME/$USER",
			"Paths: $HOME/$USER and $XDG_CONFIG_HOME/$APP_CONFIG",
		} {
			tokens, _ := cmLexMarkdown(src)
			for _, tok := range tokens[0].Tokens {
				if tok.Type == "latex" && !tok.Pending {
					t.Fatalf("did not expect currency/envvar in %q to tokenize as closed latex: %#v", src, tok)
				}
			}
		}
	})

	t.Run("looksLikePendingDollarMath handles streaming formulas", func(t *testing.T) {
		if !cmLooksLikePendingDollarMath(`\frac{1`) {
			t.Fatal("expected backslash command after unclosed $ to look like pending dollar math")
		}
		if !cmLooksLikePendingDollarMath(`x^2 +`) {
			t.Fatal("expected math operators/sup after unclosed $ to look like pending dollar math")
		}
		if cmLooksLikePendingDollarMath("5 and 10") {
			t.Fatal("did not expect plain prose digits to look like pending dollar math")
		}
	})
}

func TestMarkdownUnit_StreamingFencesAndHtmlComments(t *testing.T) {
	t.Run("trimPartialClosingFences strips incomplete backtick/tilde closers", func(t *testing.T) {
		tokens := []*marked.Token{
			{Type: "code", Raw: "```ts\nconst x = 1\n``", Text: "const x = 1\n``"},
			{Type: "code", Raw: "~~~\nhello\n~", Text: "hello\n~"},
			{Type: "code", Raw: "```ts\nconst x = 1\n~~~", Text: "const x = 1\n~~~"},
		}
		cmTrimPartialClosingFences(tokens)
		if tokens[0].Text != "const x = 1" {
			t.Errorf("backtick fence text = %q, want %q", tokens[0].Text, "const x = 1")
		}
		if tokens[1].Text != "hello" {
			t.Errorf("tilde fence text = %q, want %q", tokens[1].Text, "hello")
		}
		if tokens[2].Text != "const x = 1\n~~~" {
			t.Errorf("mismatched fence should not strip, got %q", tokens[2].Text)
		}
	})

	t.Run("removeHtmlCommentBlocks drops comment-only html tokens and trailing space", func(t *testing.T) {
		tokens := []*marked.Token{
			{Type: "paragraph", Raw: "Before", Text: "Before"},
			{Type: "space", Raw: "\n\n"},
			{Type: "html", Raw: "<!-- secret -->\n", Text: "<!-- secret -->\n"},
			{Type: "space", Raw: "\n\n"},
			{Type: "paragraph", Raw: "After", Text: "After"},
			{Type: "space", Raw: "\n\n"},
		}
		cleaned := cmRemoveHtmlCommentBlocks(tokens)
		if len(cleaned) != 3 {
			t.Fatalf("cleaned token count = %d, want 3 (%#v)", len(cleaned), cleaned)
		}
		if cleaned[0].Raw != "Before" || cleaned[1].Type != "space" || cleaned[2].Raw != "After" {
			t.Fatalf("unexpected cleaned tokens: %#v", cleaned)
		}
	})

	t.Run("lexMarkdown tracks ref links and removeHtmlCommentBlocks strips HTML comments", func(t *testing.T) {
		tokens, hasLinks := cmLexMarkdown("<!-- note -->\n\nParagraph text\n\n[foo][ref]\n\n[ref]: https://example.com")
		if !hasLinks {
			t.Fatal("expected hasLinks true for reference link")
		}
		tokens = cmRemoveHtmlCommentBlocks(tokens)
		for _, tok := range tokens {
			if tok.Type == "html" {
				t.Fatalf("expected HTML comment block removed, got %#v", tok)
			}
		}
		if len(tokens) == 0 {
			t.Fatal("expected parsed tokens")
		}
	})
}

func TestMarkdownUnit_StylePrefixes(t *testing.T) {
	m := NewMarkdown("test", 0, 0, tmarkDefaultTheme(), &DefaultTextStyle{
		Color:         tmarkGray,
		Italic:        tmarkBoolPtr(true),
		Underline:     tmarkBoolPtr(true),
		Strikethrough: tmarkBoolPtr(true),
	}, nil)
	prefix := m.getDefaultStylePrefix()
	if !strings.Contains(prefix, "\x1b[90m") || !strings.Contains(prefix, "\x1b[3m") {
		t.Fatalf("unexpected default style prefix: %q", prefix)
	}
	styled := m.applyDefaultStyle("abc")
	if !strings.Contains(styled, "abc") || !strings.Contains(styled, "\x1b[90m") {
		t.Fatalf("applyDefaultStyle failed: %q", styled)
	}
}

func TestMarkdown_Transforms(t *testing.T) {
	t.Run("applies transform before parsing at available content width", func(t *testing.T) {
		seenWidth := -1
		md := NewMarkdown("placeholder", 2, 0, tmarkDefaultTheme(), nil, &MarkdownOptions{
			Transform: func(markdown string, availableWidth int) string {
				seenWidth = availableWidth
				return markdown + " **transformed**"
			},
		})
		lines := tmarkTrimEndLines(md.Render(50))
		if seenWidth != 46 {
			t.Fatalf("availableWidth = %d, want 46", seenWidth)
		}
		if len(lines) == 0 || !strings.Contains(lines[0], "placeholder transformed") {
			t.Fatalf("unexpected lines: %#v", lines)
		}
	})
}

func TestMarkdown_BlockReuseAndWeakCache(t *testing.T) {
	md := NewMarkdown("# Title\n\nFirst paragraph\n\nSecond paragraph", 0, 0, tmarkDefaultTheme(), nil, nil)
	lines1 := md.Render(80)
	if len(lines1) == 0 {
		t.Fatal("expected rendered lines")
	}

	md.SetText("# Title\n\nFirst paragraph\n\nSecond paragraph extended")
	lines2 := md.Render(80)
	if len(lines2) == 0 || !strings.Contains(tmarkStripAnsi(lines2[len(lines2)-1]), "Second paragraph extended") {
		t.Fatalf("unexpected lines after SetText: %#v", tmarkTrimEndLines(lines2))
	}

	md.Invalidate()
	lines3 := md.Render(80)
	if !reflect.DeepEqual(lines2, lines3) {
		t.Fatalf("Render after Invalidate differed: %#v vs %#v", lines3, lines2)
	}
}

func TestMarkdown_Lists(t *testing.T) {
	t.Run("nested ordered list", func(t *testing.T) {
		md := NewMarkdown("1. First\n   1. Nested first\n   2. Nested second\n2. Second", 0, 0, tmarkDefaultTheme(), nil, nil)
		lines := tmarkTrimEndLines(md.Render(80))
		joined := strings.Join(lines, "\n")
		for _, want := range []string{"1. First", "    1. Nested first", "    2. Nested second", "2. Second"} {
			if !strings.Contains(joined, want) {
				t.Errorf("missing %q in:\n%s", want, joined)
			}
		}
	})

	t.Run("normalizes ordered list markers by default", func(t *testing.T) {
		md := NewMarkdown("1. alpha\n1. beta\n1. gamma", 0, 0, tmarkDefaultTheme(), nil, nil)
		want := []string{"1. alpha", "2. beta", "3. gamma"}
		if got := tmarkTrimEndLines(md.Render(80)); !reflect.DeepEqual(got, want) {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	})

	t.Run("preserves source list markers when configured", func(t *testing.T) {
		md := NewMarkdown(
			"  4. forth\n  3. third\n\n10) ten\n7) seven\n\n+ plus\n* star\n- minus\n+",
			0, 0, tmarkDefaultTheme(), nil,
			&MarkdownOptions{PreserveOrderedListMarkers: tmarkBoolPtr(true)},
		)
		want := []string{
			"4. forth",
			"3. third",
			"",
			"10) ten",
			"7) seven",
			"",
			"+ plus",
			"* star",
			"- minus",
			"+",
		}
		if got := tmarkTrimEndLines(md.Render(80)); !reflect.DeepEqual(got, want) {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	})

	t.Run("loose list items", func(t *testing.T) {
		src := "1. Lorem ipsum dolor sit amet.\n\n   Ut enim ad minim veniam.\n\n2. Duis aute irure dolor.\n\n   Excepteur sint occaecat cupidatat.\n\n3. Beep boop"
		want := []string{
			"1. Lorem ipsum dolor sit amet.",
			"",
			"   Ut enim ad minim veniam.",
			"",
			"2. Duis aute irure dolor.",
			"",
			"   Excepteur sint occaecat cupidatat.",
			"",
			"3. Beep boop",
		}
		if got := tmarkTrimEndLines(NewMarkdown(src, 0, 0, tmarkDefaultTheme(), nil, nil).Render(80)); !reflect.DeepEqual(got, want) {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	})

	t.Run("task list markers", func(t *testing.T) {
		want := []string{"- [ ] beep", "- [x] boop"}
		if got := tmarkTrimEndLines(NewMarkdown("- [ ] beep\n- [x] boop", 0, 0, tmarkDefaultTheme(), nil, nil).Render(80)); !reflect.DeepEqual(got, want) {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	})

	t.Run("wrapping nested lists and blockquotes inside list items", func(t *testing.T) {
		got1 := tmarkTrimEndLines(NewMarkdown("- alpha beta gamma delta epsilon", 0, 0, tmarkDefaultTheme(), nil, nil).Render(20))
		if !reflect.DeepEqual(got1, []string{"- alpha beta gamma", "  delta epsilon"}) {
			t.Errorf("unordered wrap = %#v", got1)
		}
		got2 := tmarkTrimEndLines(NewMarkdown("1. alpha beta gamma delta epsilon", 0, 0, tmarkDefaultTheme(), nil, nil).Render(20))
		if !reflect.DeepEqual(got2, []string{"1. alpha beta gamma", "   delta epsilon"}) {
			t.Errorf("ordered wrap = %#v", got2)
		}
		got3 := tmarkTrimEndLines(NewMarkdown("- > alpha beta gamma delta epsilon zeta", 0, 0, tmarkDefaultTheme(), nil, nil).Render(24))
		if !reflect.DeepEqual(got3, []string{"- │ alpha beta gamma", "  │ delta epsilon zeta"}) {
			t.Errorf("quote in list wrap = %#v", got3)
		}
		got4 := tmarkTrimEndLines(NewMarkdown("- ```ts\n  alpha beta gamma delta epsilon zeta\n  ```", 0, 0, tmarkDefaultTheme(), nil, nil).Render(24))
		if !reflect.DeepEqual(got4, []string{"- ```ts", "    alpha beta gamma", "  delta epsilon zeta", "  ```"}) {
			t.Errorf("code block in list wrap = %#v", got4)
		}
	})
}

func TestMarkdown_Tables(t *testing.T) {
	t.Run("respects paddingX and aligns cells without trailing blank line", func(t *testing.T) {
		md := NewMarkdown("| Column One | Column Two |\n| --- | --- |\n| Data 1 | Data 2 |", 2, 0, tmarkDefaultTheme(), nil, nil)
		plain := tmarkTrimEndLines(md.Render(40))
		for _, line := range plain {
			if len([]rune(line)) > 40 {
				t.Errorf("line exceeds width 40: %q", line)
			}
		}
		if len(plain) == 0 || plain[len(plain)-1] == "" {
			t.Fatalf("table ended with blank line: %#v", plain)
		}
	})

	t.Run("wraps narrow cells and unbroken URLs without overflowing borders", func(t *testing.T) {
		SetCapabilities(TerminalCapabilities{Images: "", TrueColor: false, Hyperlinks: false})
		defer ResetCapabilitiesCache()
		url := "https://example.com/this/is/a/very/long/url/that/should/wrap"
		md := NewMarkdown("| Value |\n| --- |\n| prefix "+url+" |", 0, 0, tmarkDefaultTheme(), nil, nil)
		plain := tmarkTrimEndLines(md.Render(30))
		for _, line := range plain {
			if len([]rune(line)) > 30 {
				t.Errorf("line exceeds width 30: %q", line)
			}
			if strings.HasPrefix(line, "│") && strings.Count(line, "│") != 2 {
				t.Errorf("expected 2 vertical borders in %q", line)
			}
		}
	})
}

func TestMarkdown_LatexMath(t *testing.T) {
	t.Run("inline and display delimiters", func(t *testing.T) {
		md := NewMarkdown(`A map $\mathbb{C}^3 \to \mathbb{C}^3$, $xy$, $x-y$, $-x$, $\frac{1}{2}$, $\rightarrow$, and \(s \to \infty\).`, 0, 0, tmarkDefaultTheme(), nil, nil)
		want := []string{"A map ℂ³ → ℂ³, xy, x-y, -x, 1/2, →, and s → ∞."}
		if got := tmarkTrimEndLines(md.Render(80)); !reflect.DeepEqual(got, want) {
			t.Fatalf("got %#v, want %#v", got, want)
		}

		disp := NewMarkdown("Before\n\n$$\nE \\approx \\frac{0.1\\ \\text{lux}}{100\\ \\text{lm/W}}\n$$\n\nafter", 0, 0, tmarkDefaultTheme(), nil, nil)
		wantDisp := []string{"Before", "", "    0.1 lux", "E ≈ ────────", "    100 lm/W", "", "after"}
		if got := tmarkTrimEndLines(disp.Render(80)); !reflect.DeepEqual(got, wantDisp) {
			t.Fatalf("got %#v, want %#v", got, wantDisp)
		}
	})

	t.Run("unclosed display math before table (#11159)", func(t *testing.T) {
		md := NewMarkdown("$$\n| Name | Value |\n| --- | --- |\n| a | 1 |", 0, 0, tmarkDefaultTheme(), nil, nil)
		joined := strings.Join(tmarkTrimEndLines(md.Render(80)), "\n")
		if !strings.Contains(joined, "Name") || !strings.Contains(joined, "│") {
			t.Fatalf("expected rendered table, got:\n%s", joined)
		}
	})
}

func TestMarkdown_QuotesHeadingsEscapesAndStreaming(t *testing.T) {
	t.Run("deeply nested blockquotes capped cleanly", func(t *testing.T) {
		md := NewMarkdown("> > > deeply nested quote", 0, 0, tmarkDefaultTheme(), nil, nil)
		plain := tmarkTrimEndLines(md.Render(80))
		if len(plain) == 0 || !strings.Contains(plain[0], "│") || !strings.Contains(plain[0], "deeply nested quote") {
			t.Fatalf("unexpected blockquote output: %#v", plain)
		}
	})

	t.Run("pre-styled gray italic preserves inline code styling", func(t *testing.T) {
		md := NewMarkdown("This is thinking with `inline code` and **bold text**", 1, 0, tmarkDefaultTheme(), &DefaultTextStyle{
			Color:  tmarkGray,
			Italic: tmarkBoolPtr(true),
		}, nil)
		joined := strings.Join(md.Render(80), "\n")
		if !strings.Contains(joined, "\x1b[90m") || !strings.Contains(joined, "\x1b[3m") || !strings.Contains(joined, "\x1b[33m") {
			t.Fatalf("expected gray/italic/code styling preserved, got %q", joined)
		}
	})

	t.Run("CJK emphasis and HTML comment omission", func(t *testing.T) {
		md := NewMarkdown("<!-- comment -->\n\n**日本語(にほんご)**です。", 0, 0, tmarkDefaultTheme(), nil, nil)
		joined := strings.Join(md.Render(80), "\n")
		if strings.Contains(joined, "comment") {
			t.Fatalf("HTML comment leaked: %q", joined)
		}
		if !strings.Contains(joined, "\x1b[1m日本語(にほんご)\x1b[22m") {
			t.Fatalf("expected bold CJK segment, got %q", joined)
		}
	})

	t.Run("streaming code fence hides partial closing backticks", func(t *testing.T) {
		md := NewMarkdown("```ts\nconst x = 1\n``", 0, 0, tmarkDefaultTheme(), nil, nil)
		plain := tmarkTrimEndLines(md.Render(80))
		if strings.Contains(strings.Join(plain, "\n"), "``\n") {
			t.Fatalf("partial closing fence not trimmed: %#v", plain)
		}
	})
}

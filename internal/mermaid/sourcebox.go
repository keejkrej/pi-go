package mermaid

import "strings"

// SourceBox frames src in a titled box, hard-wrapping lines to maxWidth
// columns. A nil maxWidth does not wrap. The result can still exceed
// maxWidth: the title is never truncated.
func SourceBox(src string, maxWidth *int) MermaidArt {
	src = stripControls(src)
	header := "diagram"
	if ws := words(src); len(ws) > 0 {
		header = ws[0]
	}
	title := " mermaid: " + header + " "
	var limit *int
	if maxWidth != nil {
		v := max(8, sat(*maxWidth, 4))
		limit = &v
	}
	var body []string
	started := false
	for _, l := range srcLines(src) {
		l = jsTrimRight(l)
		if !started && l == "" {
			continue
		}
		started = true
		body = append(body, chunkLine(l, limit)...)
	}
	contentW := stringWidth(title)
	for _, l := range body {
		if w := stringWidth(l); w > contentW {
			contentW = w
		}
	}
	inner := contentW + 2
	rule := strings.Repeat("─", sat(inner, stringWidth(title)))

	plain := make([]string, 0, len(body)+2)
	styled := make([][]Span, 0, len(body)+2)

	plain = append(plain, "╭"+title+rule+"╮")
	styled = append(styled, []Span{
		{Text: "╭", Cls: ClsBorder},
		{Text: title, Cls: ClsTitle},
		{Text: rule + "╮", Cls: ClsBorder},
	})
	for _, line := range body {
		pad := strings.Repeat(" ", sat(contentW, stringWidth(line)))
		plain = append(plain, "│ "+line+pad+" │")
		styled = append(styled, []Span{
			{Text: "│ ", Cls: ClsBorder},
			{Text: line, Cls: ClsText},
			{Text: pad + " │", Cls: ClsBorder},
		})
	}
	bottom := "╰" + strings.Repeat("─", inner) + "╯"
	plain = append(plain, bottom)
	styled = append(styled, []Span{{Text: bottom, Cls: ClsBorder}})
	return MermaidArt{Plain: plain, Styled: styled, Width: inner + 2, Warnings: []string{}}
}

func chunkLine(line string, limit *int) []string {
	if limit == nil || stringWidth(line) <= *limit {
		return []string{line}
	}
	var out []string
	cur := ""
	curW := 0
	for _, c := range measured(line) {
		if curW+c.w > *limit && cur != "" {
			out = append(out, cur)
			cur = ""
			curW = 0
		}
		cur += c.s
		curW += c.w
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

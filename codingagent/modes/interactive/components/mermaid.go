// Ported from packages/coding-agent/src/modes/interactive/components/mermaid.ts (pi v1.5.2).

package components

import (
	"fmt"
	"strings"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/internal/marked"
	"github.com/keejkrej/pi-go/internal/mermaid"
)

// MermaidTransformerOptions configures the built-in Mermaid Markdown transformer.
type MermaidTransformerOptions struct {
	GetMode func() core.MermaidRenderingMode
	Theme   *theme.Theme
}

// CreateMermaidMarkdownTransformer returns a MarkdownTransformer that renders supported Mermaid fenced blocks
// into Unicode box-drawing diagrams when configured for the current message type and streaming phase.
func CreateMermaidMarkdownTransformer(options MermaidTransformerOptions) core.MarkdownTransformer {
	lexer := marked.New()
	return func(markdown string, rctx core.MarkdownTransformContext) string {
		if !mermSupportsMessageType(rctx.MessageType) {
			return markdown
		}

		mode := core.MermaidRenderingStreaming
		if options.GetMode != nil {
			if m := options.GetMode(); m != "" {
				mode = m
			}
		}
		if mode == core.MermaidRenderingOff || (mode == core.MermaidRenderingFinal && rctx.IsStreaming) {
			return markdown
		}

		if !strings.Contains(markdown, "```") && !strings.Contains(markdown, "~~~") {
			return markdown
		}

		tokens := lexer.Lexer(markdown)
		if len(tokens) == 0 {
			return markdown
		}

		ansiTheme := mermBuildAnsiTheme(options.Theme)
		var out strings.Builder
		cursor := 0
		changed := false

		for i, tok := range tokens {
			if tok == nil {
				continue
			}
			raw := tok.Raw
			idx := strings.Index(markdown[cursor:], raw)
			if idx < 0 {
				continue
			}
			start := cursor + idx
			if start > cursor {
				out.WriteString(markdown[cursor:start])
			}
			hasMore := start+len(raw) < len(markdown) || i+1 < len(tokens)

			if tok.Type == "code" && tok.Lang != nil && mermIsMermaidLang(*tok.Lang) {
				replacement, replaced := mermRenderBlock(tok, raw, rctx.AvailableWidth, rctx.IsStreaming, ansiTheme, hasMore)
				if replaced {
					changed = true
				}
				out.WriteString(replacement)
			} else {
				out.WriteString(raw)
			}
			cursor = start + len(raw)
		}

		if !changed {
			return markdown
		}
		if cursor < len(markdown) {
			out.WriteString(markdown[cursor:])
		}
		return out.String()
	}
}

func mermSupportsMessageType(msgType core.MarkdownMessageType) bool {
	switch msgType {
	case core.MarkdownMessageTypeUser, core.MarkdownMessageTypeAssistant, core.MarkdownMessageTypeSummary:
		return true
	default:
		return false
	}
}

func mermIsMermaidLang(lang string) bool {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(lang)))
	return len(fields) > 0 && fields[0] == "mermaid"
}

func mermBuildAnsiTheme(th *theme.Theme) map[mermaid.Cls]string {
	if th == nil {
		return map[mermaid.Cls]string{}
	}
	titleAnsi := mermBoldAnsi(th.GetFgAnsi(theme.FgAccent))
	return map[mermaid.Cls]string{
		mermaid.ClsBorder:    th.GetFgAnsi(theme.FgBorderMuted),
		mermaid.ClsText:      th.GetFgAnsi(theme.FgText),
		mermaid.ClsEdge:      th.GetFgAnsi(theme.FgAccent),
		mermaid.ClsEdgeLabel: th.GetFgAnsi(theme.FgText),
		mermaid.ClsTitle:     titleAnsi,
	}
}

func mermBoldAnsi(sgr string) string {
	if sgr == "" {
		return ""
	}
	if strings.HasPrefix(sgr, "\x1b[") {
		return "\x1b[1;" + sgr[2:]
	}
	return "\x1b[1m" + sgr
}

func mermRenderBlock(
	tok *marked.Token,
	raw string,
	availableWidth int,
	isStreaming bool,
	ansiTheme map[mermaid.Cls]string,
	hasMore bool,
) (string, bool) {
	art, err := mermaid.Render(tok.Text, mermaid.Options{})
	if err != nil || len(art.Plain) == 0 {
		return raw, false
	}
	if availableWidth > 0 && art.Width > availableWidth {
		return raw, false
	}

	if len(art.Warnings) > 0 && !isStreaming {
		first := art.Warnings[0]
		suffix := ""
		if len(art.Warnings) > 1 {
			suffix = fmt.Sprintf(" (+%d more)", len(art.Warnings)-1)
		}
		note := mermEscapeCodespan(fmt.Sprintf("Mermaid diagram not rendered: %s%s", first, suffix))
		hadTrailingNewline := strings.HasSuffix(raw, "\n")
		base := strings.TrimSuffix(raw, "\n")
		trailing := ""
		if hadTrailingNewline {
			if hasMore {
				trailing = "  \n"
			} else {
				trailing = "\n"
			}
		}
		return base + "\n" + note + trailing, true
	}

	lines := mermaid.ToAnsi(art, ansiTheme)
	wrapped := make([]string, len(lines))
	for i, line := range lines {
		wrapped[i] = mermEscapeCodespan(line)
	}
	rendered := strings.Join(wrapped, "  \n")
	if strings.HasSuffix(raw, "\n") {
		rendered += "\n"
	}
	return rendered, true
}

func mermEscapeCodespan(text string) string {
	maxRun := 0
	run := 0
	for i := 0; i < len(text); i++ {
		if text[i] == '`' {
			run++
			if run > maxRun {
				maxRun = run
			}
		} else {
			run = 0
		}
	}
	fence := strings.Repeat("`", maxRun+1)
	if len(text) > 0 && (text[0] == '`' || text[len(text)-1] == '`') {
		return fence + " " + text + " " + fence
	}
	return fence + text + fence
}

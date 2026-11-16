// Ported from packages/coding-agent/src/extensions/codemode/renderer.ts (pi v1.5.2).

package codemodeext

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"unicode"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/tui"
)

const (
	rendCodePreviewLines   = 10
	rendCallPreviewCount   = 8
	rendOutputPreviewLines = 5
	rendCollapsedArgsChars = 80
)

var rendScriptHeaderRe = regexp.MustCompile(`^Script (?:completed|failed)\nWall time [\d.]+\ seconds\nOutput:\n$`)

func rendResolveTheme(th *theme.Theme) *theme.Theme {
	if th != nil {
		return th
	}
	return theme.CurrentTheme()
}

func rendKeyHint(t *theme.Theme, rctx core.ToolRenderContext, keybinding tui.Keybinding, description string) string {
	if rctx.KeyHint != nil {
		return rctx.KeyHint(keybinding, description)
	}
	return theme.AppKeyHint(t, keybinding, description)
}

func rendExpandHint(t *theme.Theme, rctx core.ToolRenderContext, hidden int, noun string) string {
	return t.Fg(theme.FgMuted, fmt.Sprintf("... (%d more %s,", hidden, noun)) +
		" " + rendKeyHint(t, rctx, "app.tools.expand", "to expand") +
		t.Fg(theme.FgMuted, ")")
}

func rendFormatDuration(ms *float64) string {
	if ms == nil {
		return ""
	}
	v := *ms
	if v < 1000 {
		return fmt.Sprintf("%dms", int64(math.Round(v)))
	}
	return fmt.Sprintf("%.1fs", v/1000.0)
}

// rendFormatCost formats cents for amounts >= $0.01 and 2 significant digits for fractions of a cent.
func rendFormatCost(cost float64) string {
	if cost >= 0.01 {
		return fmt.Sprintf("$%.2f", cost)
	}
	return "$" + js.ToPrecision(cost, 2)
}

func rendStatusIcon(call CodemodeNestedCall, t *theme.Theme) string {
	switch call.Status {
	case CodemodeNestedCallRunning:
		return t.Fg(theme.FgWarning, "…")
	case CodemodeNestedCallOk:
		return t.Fg(theme.FgSuccess, "✓")
	case CodemodeNestedCallError:
		return t.Fg(theme.FgError, "✗")
	case CodemodeNestedCallCancelled:
		return t.Fg(theme.FgMuted, "⊘")
	default:
		return t.Fg(theme.FgMuted, "?")
	}
}

func rendFormatCall(call CodemodeNestedCall, t *theme.Theme, expanded bool) string {
	args := call.Args
	if !expanded && js.Len(args) > rendCollapsedArgsChars {
		args = core.SliceHead(args, rendCollapsedArgsChars-3) + "..."
	}
	duration := rendFormatDuration(call.DurationMs)
	line := rendStatusIcon(call, t) + " " + t.Fg(theme.FgToolTitle, call.Name)
	if args != "" {
		line += " " + t.Fg(theme.FgMuted, args)
	}
	if duration != "" {
		line += " " + t.Fg(theme.FgDim, duration)
	}
	if call.Cost != nil && *call.Cost != 0 {
		line += " " + t.Fg(theme.FgDim, rendFormatCost(*call.Cost))
	}
	if expanded && call.Error != nil && *call.Error != "" {
		indented := strings.Join(strings.Split(*call.Error, "\n"), "\n    ")
		line += "\n    " + t.Fg(theme.FgError, indented)
	}
	return line
}

func rendHighlighter(rctx core.ToolRenderContext) *theme.CodeHighlighter {
	if stateMap, ok := rctx.State.(map[string]any); ok && stateMap != nil {
		if h, ok := stateMap["scriptHighlighter"].(*theme.CodeHighlighter); ok && h != nil {
			return h
		}
		h := theme.NewCodeHighlighter()
		stateMap["scriptHighlighter"] = h
		return h
	}
	return theme.NewCodeHighlighter()
}

func rendTrimRightSpace(s string) string {
	return strings.TrimRightFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || r == '\ufeff'
	})
}

func rendDecodeDetails(details any) *CodemodeToolDetails {
	switch d := details.(type) {
	case *CodemodeToolDetails:
		return d
	case CodemodeToolDetails:
		cp := d
		return &cp
	case nil:
		return nil
	default:
		raw, err := jsonx.Marshal(d)
		if err != nil {
			return nil
		}
		var decoded CodemodeToolDetails
		if err := jsonx.Unmarshal(raw, &decoded); err != nil {
			return nil
		}
		return &decoded
	}
}

// CodemodeRenderers provides TUI RenderCall and RenderResult implementations for the codemode tool.
var CodemodeRenderers = core.ToolRenderers{
	RenderCall: func(args *jsonx.Object, th *theme.Theme, rctx core.ToolRenderContext) tui.Component {
		core.StartToolTimer(rctx)
		t := rendResolveTheme(th)

		var rawCode any
		if args != nil {
			rawCode, _ = args.Get("code")
		}
		code := core.Str(rawCode)

		title := t.Fg(theme.FgToolTitle, t.Bold("codemode"))
		component, ok := rctx.LastComponent.(*tui.Container)
		if !ok || component == nil {
			component = tui.NewContainer()
		}
		component.Clear()

		if code == nil {
			component.AddChild(tui.NewText(title+" "+t.Fg(theme.FgError, "[invalid arg]"), 0, 0, nil))
			return component
		}
		component.AddChild(tui.NewText(title, 0, 0, nil))
		if *code != "" {
			cleaned := core.ReplaceTabs(rendTrimRightSpace(strings.ReplaceAll(*code, "\r", "")))
			lang := "javascript"
			highlighted := strings.Join(rendHighlighter(rctx).Highlight(cleaned, &lang), "\n")
			if rctx.Expanded {
				component.AddChild(tui.NewText(highlighted, 0, 0, nil))
			} else {
				component.AddChild(theme.NewVisualLinePreview(theme.VisualLinePreviewOptions{
					Text:           highlighted,
					MaxVisualLines: rendCodePreviewLines,
					Keep:           "start",
					FormatHint: func(hidden int) string {
						return rendExpandHint(t, rctx, hidden, "lines")
					},
				}))
			}
		}
		return component
	},
	RenderResult: func(result agent.AgentToolResult, options core.ToolRenderResultOptions, th *theme.Theme, rctx core.ToolRenderContext) tui.Component {
		timerText := core.ToolTimerText(rctx, options.IsPartial)
		t := rendResolveTheme(th)
		component, ok := rctx.LastComponent.(*tui.Container)
		if !ok || component == nil {
			component = tui.NewContainer()
		}
		component.Clear()

		details := rendDecodeDetails(result.Details)
		var calls []CodemodeNestedCall
		if details != nil {
			calls = details.Calls
		}

		if len(calls) > 0 {
			shown := calls
			if !options.Expanded && len(calls) > rendCallPreviewCount {
				shown = calls[len(calls)-rendCallPreviewCount:]
			}
			lines := make([]string, 0, len(shown)+2)
			if len(shown) < len(calls) {
				hiddenCalls := len(calls) - len(shown)
				lines = append(lines,
					t.Fg(theme.FgMuted, fmt.Sprintf("... (%d earlier calls,", hiddenCalls))+
						" "+rendKeyHint(t, rctx, "app.tools.expand", "to expand")+
						t.Fg(theme.FgMuted, ")"),
				)
			}
			for _, call := range shown {
				lines = append(lines, rendFormatCall(call, t, options.Expanded))
			}
			// Collapsed rows hide earlier calls, so the total covers every call.
			pricedCount := 0
			totalCost := 0.0
			for _, call := range calls {
				if call.Cost != nil && *call.Cost != 0 {
					pricedCount++
					totalCost += *call.Cost
				}
			}
			if pricedCount > 1 {
				lines = append(lines, t.Fg(theme.FgMuted, "Model calls: "+rendFormatCost(totalCost)))
			}
			component.AddChild(tui.NewSpacer(1))
			component.AddChild(tui.NewText(strings.Join(lines, "\n"), 0, 0, nil))
		}

		// Drop the "Script completed\nWall time ...\nOutput:\n" header. Rejected input (invalid options)
		// has no header.
		hasHeader := false
		if len(result.Content) > 0 {
			if first, ok := result.Content[0].(*ai.TextContent); ok && first != nil && rendScriptHeaderRe.MatchString(first.Text) {
				hasHeader = true
			}
		}
		var output string
		if !options.IsPartial {
			trimmedResult := result
			if hasHeader {
				trimmedResult.Content = result.Content[1:]
			}
			output = js.Trim(core.GetTextOutput(&trimmedResult, rctx.ShowImages))
		}

		if output != "" {
			color := theme.FgToolOutput
			if rctx.IsError {
				color = theme.FgError
			}
			replaced := core.ReplaceTabs(output)
			rawLines := strings.Split(replaced, "\n")
			styledLines := make([]string, len(rawLines))
			for i, line := range rawLines {
				styledLines[i] = t.Fg(color, line)
			}
			styled := strings.Join(styledLines, "\n")

			component.AddChild(tui.NewSpacer(1))
			if options.Expanded {
				component.AddChild(tui.NewText(styled, 0, 0, nil))
			} else {
				// Limit wrapped lines, not logical ones: script output is often one long JSON line.
				component.AddChild(theme.NewVisualLinePreview(theme.VisualLinePreviewOptions{
					Text:           styled,
					MaxVisualLines: rendOutputPreviewLines,
					Keep:           "start",
					FormatHint: func(hidden int) string {
						return rendExpandHint(t, rctx, hidden, "lines")
					},
				}))
				// The collapsed preview hides the truncation notice at the end, so name the file here.
				if details != nil && details.FullOutputPath != nil && *details.FullOutputPath != "" {
					component.AddChild(tui.NewText(t.Fg(theme.FgMuted, "Full output: "+*details.FullOutputPath), 0, 0, nil))
				}
			}
		}

		if timerText != nil {
			component.AddChild(tui.NewSpacer(1))
			component.AddChild(tui.NewText(t.Fg(theme.FgMuted, *timerText), 0, 0, nil))
		}
		return component
	},
}

// Ported from packages/coding-agent/src/core/tools/renderers/find.ts (pi v1.5.2).

package core

import (
	"fmt"
	"strings"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/tui"
)

func trfFormatFindCall(args *jsonx.Object, th *theme.Theme) string {
	t := truResolveTheme(th)
	rawPattern, _ := truGetArg(args, "pattern")
	pattern := Str(rawPattern)
	rawPathArg, _ := truGetArg(args, "path")
	rawPath := Str(rawPathArg)

	invalidArg := InvalidArgText(t)
	patternPart := invalidArg
	if pattern != nil {
		patternPart = t.Fg(theme.FgAccent, *pattern)
	}

	pathPart := invalidArg
	if rawPath != nil {
		displayPath := *rawPath
		if displayPath == "" {
			displayPath = "."
		}
		pathPart = ShortenPath(displayPath)
	}

	text := t.Fg(theme.FgToolTitle, t.Bold("find")) +
		" " +
		patternPart +
		t.Fg(theme.FgToolOutput, " in "+pathPart)

	if rawLimit, ok := truGetArg(args, "limit"); ok && !truIsNilArg(rawLimit) {
		if n, valid := truParseLineNumber(rawLimit); valid {
			text += t.Fg(theme.FgToolOutput, fmt.Sprintf(" (limit %s)", truFormatNumber(n)))
		}
	}
	return text
}

func trfFormatFindResult(
	result agent.AgentToolResult,
	options ToolRenderResultOptions,
	th *theme.Theme,
	rctx ToolRenderContext,
) string {
	t := truResolveTheme(th)
	output := strings.TrimSpace(GetTextOutput(&result, rctx.ShowImages))
	text := ""
	if output != "" {
		lines := strings.Split(output, "\n")
		maxLines := len(lines)
		if !options.Expanded && maxLines > 20 {
			maxLines = 20
		}
		displayLines := lines[:maxLines]
		remaining := len(lines) - maxLines
		styled := make([]string, len(displayLines))
		for i, line := range displayLines {
			styled[i] = t.Fg(theme.FgToolOutput, line)
		}
		text += "\n" + strings.Join(styled, "\n")
		if remaining > 0 {
			text += t.Fg(theme.FgMuted, fmt.Sprintf("\n... (%d more lines,", remaining)) +
				" " + truKeyHint(t, rctx, "app.tools.expand", "to expand") +
				t.Fg(theme.FgMuted, ")")
		}
	}

	details := truDecodeDetails[FindToolDetails](result.Details)
	if details != nil {
		resultLimit := details.ResultLimitReached
		truncation := details.Truncation
		if (resultLimit != nil && *resultLimit > 0) || (truncation != nil && truncation.Truncated) {
			var warnings []string
			if resultLimit != nil && *resultLimit > 0 {
				warnings = append(warnings, fmt.Sprintf("%d results limit", *resultLimit))
			}
			if truncation != nil && truncation.Truncated {
				maxBytes := truncation.MaxBytes
				if maxBytes <= 0 {
					maxBytes = DefaultMaxBytes
				}
				warnings = append(warnings, FormatSize(maxBytes)+" limit")
			}
			text += "\n" + t.Fg(theme.FgWarning, fmt.Sprintf("[Truncated: %s]", strings.Join(warnings, ", ")))
		}
	}
	return text
}

// FindToolRenderers returns renderers for the built-in find tool.
func FindToolRenderers() ToolRenderers {
	return ToolRenderers{
		RenderCall: func(args *jsonx.Object, th *theme.Theme, rctx ToolRenderContext) tui.Component {
			text, ok := rctx.LastComponent.(*tui.Text)
			if !ok || text == nil {
				text = tui.NewText("", 0, 0, nil)
			}
			text.SetText(trfFormatFindCall(args, th))
			return text
		},
		RenderResult: func(result agent.AgentToolResult, options ToolRenderResultOptions, th *theme.Theme, rctx ToolRenderContext) tui.Component {
			text, ok := rctx.LastComponent.(*tui.Text)
			if !ok || text == nil {
				text = tui.NewText("", 0, 0, nil)
			}
			text.SetText(trfFormatFindResult(result, options, th, rctx))
			return text
		},
	}
}

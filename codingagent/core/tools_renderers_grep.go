// Ported from packages/coding-agent/src/core/tools/renderers/grep.ts (pi v1.5.2).

package core

import (
	"fmt"
	"strings"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/tui"
)

func trgFormatGrepCall(args *jsonx.Object, th *theme.Theme) string {
	t := truResolveTheme(th)
	rawPattern, _ := truGetArg(args, "pattern")
	pattern := Str(rawPattern)
	rawPathArg, _ := truGetArg(args, "path")
	rawPath := Str(rawPathArg)
	rawGlob, _ := truGetArg(args, "glob")
	glob := Str(rawGlob)

	invalidArg := InvalidArgText(t)
	patternPart := invalidArg
	if pattern != nil {
		patternPart = t.Fg(theme.FgAccent, fmt.Sprintf("/%s/", *pattern))
	}

	pathPart := invalidArg
	if rawPath != nil {
		displayPath := *rawPath
		if displayPath == "" {
			displayPath = "."
		}
		pathPart = ShortenPath(displayPath)
	}

	text := t.Fg(theme.FgToolTitle, t.Bold("grep")) +
		" " +
		patternPart +
		t.Fg(theme.FgToolOutput, " in "+pathPart)

	if glob != nil && *glob != "" {
		text += t.Fg(theme.FgToolOutput, fmt.Sprintf(" (%s)", *glob))
	}
	if rawLimit, ok := truGetArg(args, "limit"); ok && !truIsNilArg(rawLimit) {
		if n, valid := truParseLineNumber(rawLimit); valid {
			text += t.Fg(theme.FgToolOutput, fmt.Sprintf(" limit %s", truFormatNumber(n)))
		}
	}
	return text
}

func trgFormatGrepResult(
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
		if !options.Expanded && maxLines > 15 {
			maxLines = 15
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

	details := truDecodeDetails[GrepToolDetails](result.Details)
	if details != nil {
		matchLimit := details.MatchLimitReached
		truncation := details.Truncation
		linesTruncated := details.LinesTruncated != nil && *details.LinesTruncated
		if (matchLimit != nil && *matchLimit > 0) || (truncation != nil && truncation.Truncated) || linesTruncated {
			var warnings []string
			if matchLimit != nil && *matchLimit > 0 {
				warnings = append(warnings, fmt.Sprintf("%d matches limit", *matchLimit))
			}
			if truncation != nil && truncation.Truncated {
				maxBytes := truncation.MaxBytes
				if maxBytes <= 0 {
					maxBytes = DefaultMaxBytes
				}
				warnings = append(warnings, FormatSize(maxBytes)+" limit")
			}
			if linesTruncated {
				warnings = append(warnings, "some lines truncated")
			}
			text += "\n" + t.Fg(theme.FgWarning, fmt.Sprintf("[Truncated: %s]", strings.Join(warnings, ", ")))
		}
	}
	return text
}

// GrepToolRenderers returns renderers for the built-in grep tool.
func GrepToolRenderers() ToolRenderers {
	return ToolRenderers{
		RenderCall: func(args *jsonx.Object, th *theme.Theme, rctx ToolRenderContext) tui.Component {
			text, ok := rctx.LastComponent.(*tui.Text)
			if !ok || text == nil {
				text = tui.NewText("", 0, 0, nil)
			}
			text.SetText(trgFormatGrepCall(args, th))
			return text
		},
		RenderResult: func(result agent.AgentToolResult, options ToolRenderResultOptions, th *theme.Theme, rctx ToolRenderContext) tui.Component {
			text, ok := rctx.LastComponent.(*tui.Text)
			if !ok || text == nil {
				text = tui.NewText("", 0, 0, nil)
			}
			text.SetText(trgFormatGrepResult(result, options, th, rctx))
			return text
		},
	}
}

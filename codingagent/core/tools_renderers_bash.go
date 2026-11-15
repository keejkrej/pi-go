// Ported from packages/coding-agent/src/core/tools/renderers/bash.ts (pi v1.5.2).

package core

import (
	"fmt"
	"strings"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/tui"
)

const (
	trbBashPreviewLines = 5
	// BashUpdateThrottleMs is the minimum interval between live shell UI update callbacks.
	BashUpdateThrottleMs = 100
)

func trbFormatShellCall(args *jsonx.Object, th *theme.Theme, prompt string) string {
	t := truResolveTheme(th)
	rawCmd, _ := truGetArg(args, "command")
	command := Str(rawCmd)

	timeoutSuffix := ""
	if rawTimeout, ok := truGetArg(args, "timeout"); ok {
		if n, valid := truParseLineNumber(rawTimeout); valid && n != 0 {
			timeoutSuffix = t.Fg(theme.FgMuted, fmt.Sprintf(" (timeout %ss)", truFormatNumber(n)))
		}
	}

	var commandDisplay string
	if command == nil {
		commandDisplay = InvalidArgText(t)
	} else if *command != "" {
		commandDisplay = *command
	} else {
		commandDisplay = t.Fg(theme.FgToolOutput, "...")
	}

	return t.Fg(theme.FgToolTitle, t.Bold(prompt+" "+commandDisplay)) + timeoutSuffix
}

func trbRebuildBashResultRenderComponent(
	component *tui.Container,
	result agent.AgentToolResult,
	options ToolRenderResultOptions,
	th *theme.Theme,
	rctx ToolRenderContext,
	timerText *string,
) {
	t := truResolveTheme(th)
	component.Clear()

	output := strings.TrimSpace(GetTextOutput(&result, rctx.ShowImages))
	details := truDecodeDetails[BashToolDetails](result.Details)
	var truncation *TruncationResult
	fullOutputPath := ""
	if details != nil {
		truncation = details.Truncation
		if details.FullOutputPath != nil {
			fullOutputPath = *details.FullOutputPath
		}
	}

	if !options.IsPartial && truncation != nil && truncation.Truncated && fullOutputPath != "" && strings.HasSuffix(output, "]") {
		if footerStart := strings.LastIndex(output, "\n\n["); footerStart != -1 && strings.Contains(output[footerStart:], fullOutputPath) {
			output = strings.TrimRight(output[:footerStart], " \t\r\n")
		}
	}

	if output != "" {
		lines := strings.Split(output, "\n")
		styled := make([]string, len(lines))
		for i, line := range lines {
			styled[i] = t.Fg(theme.FgToolOutput, line)
		}
		styledOutput := strings.Join(styled, "\n")

		if options.Expanded {
			component.AddChild(tui.NewText("\n"+styledOutput, 0, 0, nil))
		} else {
			component.AddChild(tui.NewSpacer(1))
			component.AddChild(theme.NewVisualLinePreview(theme.VisualLinePreviewOptions{
				Text:           styledOutput,
				MaxVisualLines: trbBashPreviewLines,
				Keep:           "end",
				FormatHint: func(hidden int) string {
					return t.Fg(theme.FgMuted, fmt.Sprintf("... (%d earlier lines,", hidden)) +
						" " + truKeyHint(t, rctx, "app.tools.expand", "to expand") +
						t.Fg(theme.FgMuted, ")")
				},
			}))
		}
	}

	if (truncation != nil && truncation.Truncated) || fullOutputPath != "" {
		var warnings []string
		if fullOutputPath != "" {
			warnings = append(warnings, "Full output: "+fullOutputPath)
		}
		if truncation != nil && truncation.Truncated {
			if tb, ok := truncation.TruncatedBy.Get(); ok && tb == "lines" {
				warnings = append(warnings, fmt.Sprintf("Truncated: showing %d of %d lines", truncation.OutputLines, truncation.TotalLines))
			} else {
				maxBytes := truncation.MaxBytes
				if maxBytes <= 0 {
					maxBytes = DefaultMaxBytes
				}
				warnings = append(warnings, fmt.Sprintf("Truncated: %d lines shown (%s limit)", truncation.OutputLines, FormatSize(maxBytes)))
			}
		}
		component.AddChild(tui.NewText("\n"+t.Fg(theme.FgWarning, "["+strings.Join(warnings, ". ")+"]"), 0, 0, nil))
	}

	if timerText != nil {
		component.AddChild(tui.NewText("\n"+t.Fg(theme.FgMuted, *timerText), 0, 0, nil))
	}
}

// CreateShellRenderers constructs shell tool renderers parameterized by prompt prefix ("$" or "PS>").
func CreateShellRenderers(prompt string) ToolRenderers {
	return ToolRenderers{
		RenderCall: func(args *jsonx.Object, th *theme.Theme, rctx ToolRenderContext) tui.Component {
			StartToolTimer(rctx)
			text, ok := rctx.LastComponent.(*tui.Text)
			if !ok || text == nil {
				text = tui.NewText("", 0, 0, nil)
			}
			text.SetText(trbFormatShellCall(args, th, prompt))
			return text
		},
		RenderResult: func(result agent.AgentToolResult, options ToolRenderResultOptions, th *theme.Theme, rctx ToolRenderContext) tui.Component {
			timerText := ToolTimerText(rctx, options.IsPartial)
			component, ok := rctx.LastComponent.(*tui.Container)
			if !ok || component == nil {
				component = tui.NewContainer()
			}
			trbRebuildBashResultRenderComponent(component, result, options, th, rctx, timerText)
			component.Invalidate()
			return component
		},
	}
}

// Ported from packages/coding-agent/src/core/tools/renderers/read.ts (pi v1.5.2).

package core

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/tui"
)

type trrCompactReadClassification struct {
	kind  string
	label string
}

var trrCompactResourceFileNames = map[string]bool{
	"AGENTS.override.md": true,
	"AGENTS.md":          true,
	"AGENTS.MD":          true,
	"CLAUDE.md":          true,
	"CLAUDE.MD":          true,
}

func trrStyledReadLineRange(args *jsonx.Object, th *theme.Theme) string {
	offset, _ := truGetArg(args, "offset")
	limit, _ := truGetArg(args, "limit")
	rng := FormatReadLineRange(offset, limit)
	if rng == "" {
		return ""
	}
	return truResolveTheme(th).Fg(theme.FgWarning, rng)
}

func trrFormatReadCall(args *jsonx.Object, th *theme.Theme, cwd string) string {
	t := truResolveTheme(th)
	pathDisplay := RenderToolPath(truGetPathArg(args), t, cwd)
	return t.Fg(theme.FgToolTitle, t.Bold("read")) + " " + pathDisplay + trrStyledReadLineRange(args, t)
}

func trrGetPiDocsClassification(absolutePath string) *trrCompactReadClassification {
	readmePath := utils.GetReadmePath()
	if readmePath == "" {
		return nil
	}
	packageRoot, err := filepath.Abs(filepath.Dir(readmePath))
	if err != nil {
		return nil
	}
	resolvedTarget, err := filepath.Abs(absolutePath)
	if err != nil {
		resolvedTarget = filepath.Clean(absolutePath)
	}
	rel, err := filepath.Rel(packageRoot, resolvedTarget)
	sep := string(filepath.Separator)
	if err != nil || rel == "" || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+sep) || filepath.IsAbs(rel) {
		return nil
	}

	label := filepath.ToSlash(rel)
	if label == "README.md" || strings.HasPrefix(label, "docs/") || strings.HasPrefix(label, "examples/") {
		return &trrCompactReadClassification{kind: "docs", label: label}
	}
	return nil
}

func trrGetCompactReadClassification(args *jsonx.Object, cwd string) *trrCompactReadClassification {
	rawPathPtr := truGetPathArg(args)
	if rawPathPtr == nil || *rawPathPtr == "" {
		return nil
	}
	absolutePath := ResolveToCwd(*rawPathPtr, cwd)
	fileName := filepath.Base(absolutePath)
	if fileName == "SKILL.md" {
		parent := filepath.Base(filepath.Dir(absolutePath))
		if parent == "" || parent == "." || parent == string(filepath.Separator) || parent == "/" {
			parent = fileName
		}
		return &trrCompactReadClassification{kind: "skill", label: parent}
	}

	if docs := trrGetPiDocsClassification(absolutePath); docs != nil {
		return docs
	}

	if trrCompactResourceFileNames[fileName] {
		return &trrCompactReadClassification{
			kind:  "resource",
			label: utils.FormatPathRelativeToCwdOrAbsolute(absolutePath, cwd),
		}
	}
	return nil
}

func trrFormatCompactReadCall(
	classification trrCompactReadClassification,
	args *jsonx.Object,
	th *theme.Theme,
	rctx ToolRenderContext,
) string {
	t := truResolveTheme(th)
	expandHint := t.Fg(theme.FgDim, fmt.Sprintf(" (%s to expand)", truKeyText(rctx, "app.tools.expand")))
	if classification.kind == "skill" {
		return t.Fg(theme.FgCustomMessageLabel, "\x1b[1m[skill]\x1b[22m ") +
			t.Fg(theme.FgCustomMessageText, classification.label) +
			trrStyledReadLineRange(args, t) +
			expandHint
	}
	return t.Fg(theme.FgToolTitle, t.Bold("read "+classification.kind)) +
		" " +
		t.Fg(theme.FgAccent, classification.label) +
		trrStyledReadLineRange(args, t) +
		expandHint
}

func trrFormatReadResult(
	args *jsonx.Object,
	result agent.AgentToolResult,
	options ToolRenderResultOptions,
	th *theme.Theme,
	rctx ToolRenderContext,
) string {
	t := truResolveTheme(th)
	details := truDecodeDetails[ReadToolDetails](result.Details)

	imageWarning := ""
	if details != nil && details.ImageUnsupportedByModel != nil && *details.ImageUnsupportedByModel != "" {
		imageWarning = "\n" + t.Fg(theme.FgWarning, fmt.Sprintf("[%s does not support images; it saw a placeholder instead]", *details.ImageUnsupportedByModel))
	}
	if !options.Expanded && !rctx.IsError {
		return imageWarning
	}

	rawPath := truGetPathArg(args)
	output := GetTextOutput(&result, rctx.ShowImages)
	var lang *string
	if !rctx.IsError && rawPath != nil && *rawPath != "" {
		lang = theme.GetLanguageFromPath(*rawPath)
	}
	var renderedLines []string
	if lang != nil {
		renderedLines = theme.HighlightCode(ReplaceTabs(output), lang)
	} else {
		renderedLines = strings.Split(output, "\n")
	}
	lines := truTrimTrailingEmptyLines(renderedLines)
	maxLines := len(lines)
	if !options.Expanded && maxLines > 10 {
		maxLines = 10
	}
	displayLines := lines[:maxLines]
	remaining := len(lines) - maxLines
	styled := make([]string, len(displayLines))
	for i, line := range displayLines {
		tabbed := ReplaceTabs(line)
		if lang != nil {
			styled[i] = tabbed
		} else {
			styled[i] = t.Fg(theme.FgToolOutput, tabbed)
		}
	}
	text := "\n" + strings.Join(styled, "\n")
	if remaining > 0 {
		text += t.Fg(theme.FgMuted, fmt.Sprintf("\n... (%d more lines,", remaining)) +
			" " + truKeyHint(t, rctx, "app.tools.expand", "to expand") +
			t.Fg(theme.FgMuted, ")")
	}

	if details != nil && details.Truncation != nil && details.Truncation.Truncated {
		truncation := details.Truncation
		maxBytes := truncation.MaxBytes
		if maxBytes <= 0 {
			maxBytes = DefaultMaxBytes
		}
		if truncation.FirstLineExceedsLimit {
			text += "\n" + t.Fg(theme.FgWarning, fmt.Sprintf("[First line exceeds %s limit]", FormatSize(maxBytes)))
		} else if tb, ok := truncation.TruncatedBy.Get(); ok && tb == "lines" {
			maxLimitLines := truncation.MaxLines
			if maxLimitLines <= 0 {
				maxLimitLines = DefaultMaxLines
			}
			text += "\n" + t.Fg(theme.FgWarning, fmt.Sprintf("[Truncated: showing %d of %d lines (%d line limit)]", truncation.OutputLines, truncation.TotalLines, maxLimitLines))
		} else {
			text += "\n" + t.Fg(theme.FgWarning, fmt.Sprintf("[Truncated: %d lines shown (%s limit)]", truncation.OutputLines, FormatSize(maxBytes)))
		}
	}
	return text + imageWarning
}

// ReadToolRenderers returns renderers for the built-in read tool.
func ReadToolRenderers() ToolRenderers {
	return ToolRenderers{
		RenderCall: func(args *jsonx.Object, th *theme.Theme, rctx ToolRenderContext) tui.Component {
			text, ok := rctx.LastComponent.(*tui.Text)
			if !ok || text == nil {
				text = tui.NewText("", 0, 0, nil)
			}
			var classification *trrCompactReadClassification
			if !rctx.Expanded {
				classification = trrGetCompactReadClassification(args, rctx.Cwd)
			}
			if classification != nil {
				text.SetText(trrFormatCompactReadCall(*classification, args, th, rctx))
			} else {
				text.SetText(trrFormatReadCall(args, th, rctx.Cwd))
			}
			return text
		},
		RenderResult: func(result agent.AgentToolResult, options ToolRenderResultOptions, th *theme.Theme, rctx ToolRenderContext) tui.Component {
			text, ok := rctx.LastComponent.(*tui.Text)
			if !ok || text == nil {
				text = tui.NewText("", 0, 0, nil)
			}
			text.SetText(trrFormatReadResult(rctx.Args, result, options, th, rctx))
			return text
		},
	}
}

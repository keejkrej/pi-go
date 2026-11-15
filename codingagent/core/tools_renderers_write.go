// Ported from packages/coding-agent/src/core/tools/renderers/write.ts (pi v1.5.2).

package core

import (
	"fmt"
	"strings"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/tui"
)

const trwPartialFullHighlightLines = 50

type trwWriteHighlightCache struct {
	rawPath          *string
	lang             string
	rawContent       string
	normalizedLines  []string
	highlightedLines []string
}

type trwWriteCallRenderComponent struct {
	*tui.Text
	cache       *trwWriteHighlightCache
	highlighter *theme.CodeHighlighter
}

func trwNewWriteCallRenderComponent() *trwWriteCallRenderComponent {
	return &trwWriteCallRenderComponent{
		Text:        tui.NewText("", 0, 0, nil),
		highlighter: theme.NewCodeHighlighter(),
	}
}

func trwEqualRawPath(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func trwHighlightSingleLine(line, lang string) string {
	highlighted := theme.HighlightCode(line, &lang)
	if len(highlighted) == 0 {
		return ""
	}
	return highlighted[0]
}

func trwRefreshWriteHighlightPrefix(cache *trwWriteHighlightCache, highlighter *theme.CodeHighlighter) {
	prefixCount := len(cache.normalizedLines)
	if prefixCount > trwPartialFullHighlightLines {
		prefixCount = trwPartialFullHighlightLines
	}
	if prefixCount == 0 {
		return
	}
	prefixSource := strings.Join(cache.normalizedLines[:prefixCount], "\n")
	lang := cache.lang
	prefixHighlighted := highlighter.Highlight(prefixSource, &lang)
	for i := 0; i < prefixCount; i++ {
		if i < len(prefixHighlighted) {
			cache.highlightedLines[i] = prefixHighlighted[i]
		} else {
			cache.highlightedLines[i] = trwHighlightSingleLine(cache.normalizedLines[i], cache.lang)
		}
	}
}

func trwRebuildWriteHighlightCacheFull(rawPath *string, fileContent string) *trwWriteHighlightCache {
	if rawPath == nil || *rawPath == "" {
		return nil
	}
	lang := theme.GetLanguageFromPath(*rawPath)
	if lang == nil || *lang == "" {
		return nil
	}
	displayContent := NormalizeDisplayText(fileContent)
	normalized := ReplaceTabs(displayContent)
	return &trwWriteHighlightCache{
		rawPath:          rawPath,
		lang:             *lang,
		rawContent:       fileContent,
		normalizedLines:  strings.Split(normalized, "\n"),
		highlightedLines: theme.HighlightCode(normalized, lang),
	}
}

func trwUpdateWriteHighlightCacheIncremental(
	cache *trwWriteHighlightCache,
	rawPath *string,
	fileContent string,
	highlighter *theme.CodeHighlighter,
) *trwWriteHighlightCache {
	if rawPath == nil || *rawPath == "" {
		return nil
	}
	langPtr := theme.GetLanguageFromPath(*rawPath)
	if langPtr == nil || *langPtr == "" {
		return nil
	}
	lang := *langPtr
	if cache == nil {
		return trwRebuildWriteHighlightCacheFull(rawPath, fileContent)
	}
	if cache.lang != lang || !trwEqualRawPath(cache.rawPath, rawPath) || !strings.HasPrefix(fileContent, cache.rawContent) {
		return trwRebuildWriteHighlightCacheFull(rawPath, fileContent)
	}
	if len(fileContent) == len(cache.rawContent) {
		return cache
	}

	deltaRaw := fileContent[len(cache.rawContent):]
	deltaNormalized := ReplaceTabs(NormalizeDisplayText(deltaRaw))
	cache.rawContent = fileContent
	if len(cache.normalizedLines) == 0 {
		cache.normalizedLines = append(cache.normalizedLines, "")
		cache.highlightedLines = append(cache.highlightedLines, "")
	}

	segments := strings.Split(deltaNormalized, "\n")
	lastIndex := len(cache.normalizedLines) - 1
	cache.normalizedLines[lastIndex] += segments[0]
	cache.highlightedLines[lastIndex] = trwHighlightSingleLine(cache.normalizedLines[lastIndex], cache.lang)
	for i := 1; i < len(segments); i++ {
		cache.normalizedLines = append(cache.normalizedLines, segments[i])
		cache.highlightedLines = append(cache.highlightedLines, trwHighlightSingleLine(segments[i], cache.lang))
	}
	trwRefreshWriteHighlightPrefix(cache, highlighter)
	return cache
}

func trwFormatWriteCall(
	args *jsonx.Object,
	options ToolRenderResultOptions,
	th *theme.Theme,
	cache *trwWriteHighlightCache,
	highlighter *theme.CodeHighlighter,
	rctx ToolRenderContext,
) string {
	t := truResolveTheme(th)
	rawPath := truGetPathArg(args)
	rawContentArg, _ := truGetArg(args, "content")
	fileContent := Str(rawContentArg)
	pathDisplay := RenderToolPath(rawPath, t, rctx.Cwd)
	text := t.Fg(theme.FgToolTitle, t.Bold("write")) + " " + pathDisplay

	if fileContent == nil {
		text += "\n\n" + t.Fg(theme.FgError, "[invalid content arg - expected string]")
	} else if *fileContent != "" {
		var lang *string
		if rawPath != nil && *rawPath != "" {
			lang = theme.GetLanguageFromPath(*rawPath)
		}
		var renderedLines []string
		if lang != nil {
			if cache != nil {
				renderedLines = cache.highlightedLines
			} else {
				renderedLines = highlighter.Highlight(ReplaceTabs(NormalizeDisplayText(*fileContent)), lang)
			}
		} else {
			renderedLines = strings.Split(NormalizeDisplayText(*fileContent), "\n")
		}
		lines := truTrimTrailingEmptyLines(renderedLines)
		totalLines := len(lines)
		maxLines := len(lines)
		if !options.Expanded && maxLines > 10 {
			maxLines = 10
		}
		displayLines := lines[:maxLines]
		remaining := len(lines) - maxLines
		formatted := make([]string, len(displayLines))
		for i, line := range displayLines {
			if lang != nil {
				formatted[i] = line
			} else {
				formatted[i] = t.Fg(theme.FgToolOutput, ReplaceTabs(line))
			}
		}
		text += "\n\n" + strings.Join(formatted, "\n")
		if remaining > 0 {
			text += t.Fg(theme.FgMuted, fmt.Sprintf("\n... (%d more lines, %d total,", remaining, totalLines)) +
				" " + truKeyHint(t, rctx, "app.tools.expand", "to expand") +
				t.Fg(theme.FgMuted, ")")
		}
	}

	return text
}

func trwFormatWriteResult(result agent.AgentToolResult, th *theme.Theme, isError bool) string {
	if !isError {
		return ""
	}
	textBlocks, _ := truExtractTextAndImages(result.Content)
	output := strings.Join(textBlocks, "\n")
	if output == "" {
		return ""
	}
	return "\n" + truResolveTheme(th).Fg(theme.FgError, output)
}

// WriteToolRenderers returns renderers for the built-in write tool.
func WriteToolRenderers() ToolRenderers {
	return ToolRenderers{
		RenderCall: func(args *jsonx.Object, th *theme.Theme, rctx ToolRenderContext) tui.Component {
			rawPath := truGetPathArg(args)
			rawContentArg, _ := truGetArg(args, "content")
			fileContent := Str(rawContentArg)

			component, ok := rctx.LastComponent.(*trwWriteCallRenderComponent)
			if !ok || component == nil {
				component = trwNewWriteCallRenderComponent()
			}
			if fileContent != nil && !rctx.ArgsComplete {
				component.cache = trwUpdateWriteHighlightCacheIncremental(component.cache, rawPath, *fileContent, component.highlighter)
			} else {
				component.cache = nil
			}
			component.SetText(trwFormatWriteCall(
				args,
				ToolRenderResultOptions{Expanded: rctx.Expanded, IsPartial: rctx.IsPartial},
				th,
				component.cache,
				component.highlighter,
				rctx,
			))
			return component
		},
		RenderResult: func(result agent.AgentToolResult, _ ToolRenderResultOptions, th *theme.Theme, rctx ToolRenderContext) tui.Component {
			output := trwFormatWriteResult(result, th, rctx.IsError)
			if output == "" {
				component, ok := rctx.LastComponent.(*tui.Container)
				if !ok || component == nil {
					component = tui.NewContainer()
				}
				component.Clear()
				return component
			}
			text, ok := rctx.LastComponent.(*tui.Text)
			if !ok || text == nil {
				text = tui.NewText("", 0, 0, nil)
			}
			text.SetText(output)
			return text
		},
	}
}

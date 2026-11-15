// Ported from packages/coding-agent/src/core/export-html/tool-renderer.ts (pi v1.5.2).

package core

import (
	"strings"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/tui"
)

// ToolHtmlRendererDeps configures CreateToolHtmlRenderer.
type ToolHtmlRendererDeps struct {
	GetToolRenderers func(name string) *ToolRenderers
	Theme            *theme.Theme
	Cwd              string
	Width            *int
}

// ToolHtmlRenderedResult holds optional collapsed/expanded HTML strings returned by RenderResult.
type ToolHtmlRenderedResult struct {
	Collapsed *string
	Expanded  *string
}

// ToolHtmlRenderer renders custom tool calls and results to HTML strings.
type ToolHtmlRenderer interface {
	RenderCall(toolCallId, toolName string, args any) *string
	RenderResult(toolCallId, toolName string, result ai.ContentList, details any, isError bool) *ToolHtmlRenderedResult
}

type ehtrRenderer struct {
	getToolRenderers func(name string) *ToolRenderers
	theme            *theme.Theme
	cwd              string
	width            int

	callComponents   map[string]tui.Component
	resultComponents map[string]tui.Component
	states           map[string]any
	argsByCallId     map[string]*jsonx.Object
}

func ehtrIsBlankRenderedLine(line string) bool {
	return strings.TrimSpace(StripAnsiEscapes(line)) == ""
}

func ehtrTrimRenderedResultLines(lines []string) []string {
	start := 0
	end := len(lines)
	for start < end && ehtrIsBlankRenderedLine(lines[start]) {
		start++
	}
	for end > start && ehtrIsBlankRenderedLine(lines[end-1]) {
		end--
	}
	return lines[start:end]
}

func ehtrToJsonObject(args any) *jsonx.Object {
	if args == nil {
		return nil
	}
	switch v := args.(type) {
	case *jsonx.Object:
		return v
	case jsonx.Object:
		copied := v
		return &copied
	case map[string]any:
		obj := jsonx.NewObject()
		for k, val := range v {
			obj.Set(k, val)
		}
		return obj
	default:
		if raw, err := jsonx.Marshal(v); err == nil {
			var obj jsonx.Object
			if err := jsonx.Unmarshal(raw, &obj); err == nil {
				return &obj
			}
		}
		return nil
	}
}

func (r *ehtrRenderer) getState(toolCallId string) any {
	if st, ok := r.states[toolCallId]; ok {
		return st
	}
	st := make(map[string]any)
	r.states[toolCallId] = st
	return st
}

func (r *ehtrRenderer) createRenderContext(
	toolCallId string,
	lastComponent tui.Component,
	expanded bool,
	isPartial bool,
	isError bool,
) ToolRenderContext {
	return ToolRenderContext{
		KeybindingHints:  DefaultKeybindingHints(),
		Args:             r.argsByCallId[toolCallId],
		ToolCallId:       toolCallId,
		Invalidate:       func() {},
		LastComponent:    lastComponent,
		State:            r.getState(toolCallId),
		Cwd:              r.cwd,
		ExecutionStarted: true,
		ArgsComplete:     true,
		IsPartial:        isPartial,
		Expanded:         expanded,
		ShowImages:       false,
		IsError:          isError,
		DurationMs:       nil,
		OutputPad:        1,
	}
}

func (r *ehtrRenderer) RenderCall(toolCallId, toolName string, args any) (out *string) {
	defer func() {
		if recover() != nil {
			out = nil
		}
	}()
	if r.getToolRenderers == nil {
		return nil
	}
	objArgs := ehtrToJsonObject(args)
	r.argsByCallId[toolCallId] = objArgs

	toolDef := r.getToolRenderers(toolName)
	if toolDef == nil || toolDef.RenderCall == nil {
		return nil
	}

	comp := toolDef.RenderCall(
		objArgs,
		r.theme,
		r.createRenderContext(toolCallId, r.callComponents[toolCallId], false, true, false),
	)
	if comp == nil {
		return nil
	}
	r.callComponents[toolCallId] = comp
	rendered := AnsiLinesToHtml(comp.Render(r.width))
	return &rendered
}

func (r *ehtrRenderer) RenderResult(
	toolCallId, toolName string,
	result ai.ContentList,
	details any,
	isError bool,
) (out *ToolHtmlRenderedResult) {
	defer func() {
		if recover() != nil {
			out = nil
		}
	}()
	if r.getToolRenderers == nil {
		return nil
	}
	toolDef := r.getToolRenderers(toolName)
	if toolDef == nil || toolDef.RenderResult == nil {
		return nil
	}

	errFlag := isError
	agentResult := agent.AgentToolResult{
		Content: result,
		Details: details,
		IsError: &errFlag,
	}

	collapsedComp := toolDef.RenderResult(
		agentResult,
		ToolRenderResultOptions{Expanded: false, IsPartial: false},
		r.theme,
		r.createRenderContext(toolCallId, r.resultComponents[toolCallId], false, false, isError),
	)
	if collapsedComp == nil {
		return nil
	}
	r.resultComponents[toolCallId] = collapsedComp
	collapsed := AnsiLinesToHtml(ehtrTrimRenderedResultLines(collapsedComp.Render(r.width)))

	expandedComp := toolDef.RenderResult(
		agentResult,
		ToolRenderResultOptions{Expanded: true, IsPartial: false},
		r.theme,
		r.createRenderContext(toolCallId, r.resultComponents[toolCallId], true, false, isError),
	)
	if expandedComp == nil {
		return nil
	}
	r.resultComponents[toolCallId] = expandedComp
	expanded := AnsiLinesToHtml(ehtrTrimRenderedResultLines(expandedComp.Render(r.width)))

	res := &ToolHtmlRenderedResult{Expanded: &expanded}
	if collapsed != "" && collapsed != expanded {
		res.Collapsed = &collapsed
	}
	return res
}

// CreateToolHtmlRenderer creates a ToolHtmlRenderer backed by extension/builtin TUI tool renderers.
func CreateToolHtmlRenderer(deps ToolHtmlRendererDeps) ToolHtmlRenderer {
	width := 100
	if deps.Width != nil && *deps.Width > 0 {
		width = *deps.Width
	}
	return &ehtrRenderer{
		getToolRenderers: deps.GetToolRenderers,
		theme:            deps.Theme,
		cwd:              deps.Cwd,
		width:            width,
		callComponents:   make(map[string]tui.Component),
		resultComponents: make(map[string]tui.Component),
		states:           make(map[string]any),
		argsByCallId:     make(map[string]*jsonx.Object),
	}
}

// Ported from packages/coding-agent/src/core/tools/renderers/edit.ts (pi v1.5.2).

package core

import (
	"context"
	"strings"
	"sync"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/tui"
)

type treEditPreview struct {
	hasPreview       bool
	diff             string
	firstChangedLine *int
	errMsg           string
}

// EditRenderState stores shared call render state across RenderCall and RenderResult for the edit tool.
type EditRenderState struct {
	mu            sync.Mutex
	callComponent *treEditCallRenderComponent
}

type treEditCallRenderComponent struct {
	mu             sync.Mutex
	box            *tui.Box
	preview        *treEditPreview
	previewArgsKey *string
	previewPending bool
	settledError   bool
	dirty          bool
	lastArgs       *jsonx.Object
	lastTheme      *theme.Theme
	lastCwd        string
	lastOutputPad  int
}

func treNewEditCallRenderComponent() *treEditCallRenderComponent {
	return &treEditCallRenderComponent{
		box: tui.NewBox(1, 1, func(text string) string { return text }),
	}
}

// Render rebuilds pending async preview changes if needed and renders the edit header box.
func (c *treEditCallRenderComponent) Render(width int) []string {
	c.mu.Lock()
	if c.dirty {
		treBuildEditCallComponentLocked(c, c.lastArgs, c.lastTheme, c.lastCwd, c.lastOutputPad)
		c.dirty = false
	}
	lines := c.box.Render(width)
	c.mu.Unlock()
	return lines
}

// Invalidate drops the underlying box render cache.
func (c *treEditCallRenderComponent) Invalidate() {
	c.mu.Lock()
	c.box.Invalidate()
	c.mu.Unlock()
}

func treGetEditCallRenderComponent(state any, lastComponent tui.Component) *treEditCallRenderComponent {
	if existing, ok := lastComponent.(*treEditCallRenderComponent); ok && existing != nil {
		switch s := state.(type) {
		case *EditRenderState:
			if s != nil {
				s.mu.Lock()
				s.callComponent = existing
				s.mu.Unlock()
			}
		case map[string]any:
			s["editCallComponent"] = existing
		}
		return existing
	}

	switch s := state.(type) {
	case *EditRenderState:
		if s != nil {
			s.mu.Lock()
			if s.callComponent == nil {
				s.callComponent = treNewEditCallRenderComponent()
			}
			comp := s.callComponent
			s.mu.Unlock()
			return comp
		}
	case map[string]any:
		if comp, ok := s["editCallComponent"].(*treEditCallRenderComponent); ok && comp != nil {
			return comp
		}
		comp := treNewEditCallRenderComponent()
		s["editCallComponent"] = comp
		return comp
	}

	return treNewEditCallRenderComponent()
}

func treLookupEditCallRenderComponent(state any) *treEditCallRenderComponent {
	switch s := state.(type) {
	case *EditRenderState:
		if s == nil {
			return nil
		}
		s.mu.Lock()
		comp := s.callComponent
		s.mu.Unlock()
		return comp
	case map[string]any:
		comp, _ := s["editCallComponent"].(*treEditCallRenderComponent)
		return comp
	default:
		return nil
	}
}

type trePreviewInput struct {
	Path  string            `json:"path"`
	Edits []EditReplacement `json:"edits"`
}

func treGetRenderablePreviewInput(args *jsonx.Object) *trePreviewInput {
	if args == nil {
		return nil
	}
	path := ""
	if p, ok := args.GetString("path"); ok {
		path = p
	} else if fp, ok := args.GetString("file_path"); ok {
		path = fp
	}
	if path == "" {
		return nil
	}

	if rawEdits, ok := args.Get("edits"); ok {
		switch list := rawEdits.(type) {
		case []any:
			if len(list) > 0 {
				edits := make([]EditReplacement, 0, len(list))
				valid := true
				for _, item := range list {
					obj, isObj := item.(*jsonx.Object)
					if !isObj || obj == nil {
						valid = false
						break
					}
					oldText, okOld := obj.GetString("oldText")
					newText, okNew := obj.GetString("newText")
					if !okOld || !okNew {
						valid = false
						break
					}
					edits = append(edits, EditReplacement{OldText: oldText, NewText: newText})
				}
				if valid {
					return &trePreviewInput{Path: path, Edits: edits}
				}
			}
		case []EditReplacement:
			if len(list) > 0 {
				cp := make([]EditReplacement, len(list))
				copy(cp, list)
				return &trePreviewInput{Path: path, Edits: cp}
			}
		}
	}

	oldText, okOld := args.GetString("oldText")
	newText, okNew := args.GetString("newText")
	if okOld && okNew {
		return &trePreviewInput{
			Path:  path,
			Edits: []EditReplacement{{OldText: oldText, NewText: newText}},
		}
	}

	return nil
}

func treFormatEditCall(args *jsonx.Object, th *theme.Theme, cwd string) string {
	t := truResolveTheme(th)
	pathDisplay := RenderToolPath(truGetPathArg(args), t, cwd)
	return t.Fg(theme.FgToolTitle, t.Bold("edit")) + " " + pathDisplay
}

func treFormatEditResult(
	args *jsonx.Object,
	preview *treEditPreview,
	result agent.AgentToolResult,
	th *theme.Theme,
	isError bool,
) string {
	t := truResolveTheme(th)
	rawPath := truGetPathArg(args)
	previewDiff := ""
	previewError := ""
	if preview != nil && preview.hasPreview {
		previewDiff = preview.diff
		previewError = preview.errMsg
	}

	if isError {
		textBlocks, _ := truExtractTextAndImages(result.Content)
		errorText := strings.Join(textBlocks, "\n")
		if errorText == "" || errorText == previewError {
			return ""
		}
		return t.Fg(theme.FgError, errorText)
	}

	details := truDecodeDetails[EditToolDetails](result.Details)
	resultDiff := ""
	if details != nil {
		resultDiff = details.Diff
	}
	if resultDiff != "" && resultDiff != previewDiff {
		var filePath string
		if rawPath != nil {
			filePath = *rawPath
		}
		return theme.RenderDiff(resultDiff, theme.RenderDiffOptions{FilePath: filePath})
	}
	return ""
}

func treGetEditHeaderBg(preview *treEditPreview, settledError bool, th *theme.Theme) func(string) string {
	t := truResolveTheme(th)
	if preview != nil && preview.hasPreview {
		if preview.errMsg != "" {
			return func(text string) string { return t.Bg(theme.BgToolError, text) }
		}
		return func(text string) string { return t.Bg(theme.BgToolSuccess, text) }
	}
	if settledError {
		return func(text string) string { return t.Bg(theme.BgToolError, text) }
	}
	return func(text string) string { return t.Bg(theme.BgToolPending, text) }
}

func treBuildEditCallComponentLocked(
	component *treEditCallRenderComponent,
	args *jsonx.Object,
	th *theme.Theme,
	cwd string,
	outputPad int,
) {
	t := truResolveTheme(th)
	component.lastArgs = args
	component.lastTheme = t
	component.lastCwd = cwd
	component.lastOutputPad = outputPad

	component.box.SetBgFn(treGetEditHeaderBg(component.preview, component.settledError, t))
	component.box.SetPaddingX(outputPad)
	component.box.Clear()
	component.box.AddChild(tui.NewText(treFormatEditCall(args, t, cwd), 0, 0, nil))

	if component.preview == nil || !component.preview.hasPreview {
		return
	}

	var body string
	if component.preview.errMsg != "" {
		body = t.Fg(theme.FgError, component.preview.errMsg)
	} else {
		body = theme.RenderDiff(component.preview.diff)
	}
	component.box.AddChild(tui.NewSpacer(1))
	component.box.AddChild(tui.NewText(body, 0, 0, nil))
}

func trePtrIntEqual(a, b *int) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func trePtrStringEqual(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func treSetEditPreviewLocked(
	component *treEditCallRenderComponent,
	preview treEditPreview,
	argsKey *string,
) bool {
	current := component.preview
	changed := current == nil || !current.hasPreview
	if !changed {
		currIsErr := current.errMsg != ""
		nextIsErr := preview.errMsg != ""
		if currIsErr && nextIsErr {
			changed = current.errMsg != preview.errMsg
		} else if currIsErr != nextIsErr {
			changed = true
		} else {
			changed = current.diff != preview.diff || !trePtrIntEqual(current.firstChangedLine, preview.firstChangedLine)
		}
	}
	cp := preview
	cp.hasPreview = true
	component.preview = &cp
	component.previewArgsKey = argsKey
	component.previewPending = false
	component.dirty = true
	return changed
}

// EditToolRenderers returns renderers for the built-in edit tool.
func EditToolRenderers() ToolRenderers {
	return ToolRenderers{
		RenderCall: func(args *jsonx.Object, th *theme.Theme, rctx ToolRenderContext) tui.Component {
			component := treGetEditCallRenderComponent(rctx.State, rctx.LastComponent)
			previewInput := treGetRenderablePreviewInput(args)
			var argsKey *string
			if previewInput != nil {
				key := jsonx.MustStringify(previewInput)
				argsKey = &key
			}

			component.mu.Lock()
			if !trePtrStringEqual(component.previewArgsKey, argsKey) {
				component.preview = nil
				component.previewArgsKey = argsKey
				component.previewPending = false
				component.settledError = false
			}

			shouldCompute := rctx.ArgsComplete &&
				previewInput != nil &&
				(component.preview == nil || !component.preview.hasPreview) &&
				!component.previewPending
			var reqInput trePreviewInput
			var requestKey *string
			if shouldCompute {
				component.previewPending = true
				reqInput = *previewInput
				reqInput.Edits = append([]EditReplacement(nil), previewInput.Edits...)
				if argsKey != nil {
					k := *argsKey
					requestKey = &k
				}
			}

			treBuildEditCallComponentLocked(component, args, th, rctx.Cwd, rctx.OutputPad)
			component.dirty = false
			component.mu.Unlock()

			if shouldCompute {
				cwd := rctx.Cwd
				invalidate := rctx.Invalidate
				go func() {
					res := ComputeEditsDiff(context.Background(), reqInput.Path, reqInput.Edits, cwd)
					var next treEditPreview
					if res.Error != "" {
						next = treEditPreview{hasPreview: true, errMsg: res.Error}
					} else {
						next = treEditPreview{
							hasPreview:       true,
							diff:             res.Diff,
							firstChangedLine: res.FirstChangedLine,
						}
					}
					component.mu.Lock()
					matched := trePtrStringEqual(component.previewArgsKey, requestKey)
					if matched {
						treSetEditPreviewLocked(component, next, requestKey)
					}
					component.mu.Unlock()
					if matched && invalidate != nil {
						invalidate()
					}
				}()
			}

			return component
		},
		RenderResult: func(result agent.AgentToolResult, _ ToolRenderResultOptions, th *theme.Theme, rctx ToolRenderContext) tui.Component {
			callComponent := treLookupEditCallRenderComponent(rctx.State)
			previewInput := treGetRenderablePreviewInput(rctx.Args)
			var argsKey *string
			if previewInput != nil {
				key := jsonx.MustStringify(previewInput)
				argsKey = &key
			}

			details := truDecodeDetails[EditToolDetails](result.Details)
			var previewSnapshot *treEditPreview
			if callComponent != nil {
				callComponent.mu.Lock()
				changed := false
				if !rctx.IsError && details != nil && details.Diff != "" {
					changed = treSetEditPreviewLocked(callComponent, treEditPreview{
						hasPreview:       true,
						diff:             details.Diff,
						firstChangedLine: details.FirstChangedLine,
					}, argsKey) || changed
				}
				if callComponent.settledError != rctx.IsError {
					callComponent.settledError = rctx.IsError
					changed = true
				}
				if changed {
					treBuildEditCallComponentLocked(callComponent, rctx.Args, th, rctx.Cwd, rctx.OutputPad)
					callComponent.dirty = false
				}
				if callComponent.preview != nil {
					cp := *callComponent.preview
					previewSnapshot = &cp
				}
				callComponent.mu.Unlock()
			}

			output := treFormatEditResult(rctx.Args, previewSnapshot, result, th, rctx.IsError)
			component, ok := rctx.LastComponent.(*tui.Container)
			if !ok || component == nil {
				component = tui.NewContainer()
			}
			component.Clear()
			if output == "" {
				return component
			}
			component.AddChild(tui.NewSpacer(1))
			component.AddChild(tui.NewText(output, rctx.OutputPad, 0, nil))
			return component
		},
	}
}

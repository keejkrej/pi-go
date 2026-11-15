package components

import (
	"fmt"
	"strings"
	"sync"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/tui"
)

const (
	teDefaultFallbackPreviewLines = 10
	teDefaultImageWidthCells      = 40
)

var tePngOnce sync.Once

// ToolExecutionOptions configures initial behavior for ToolExecutionComponent.
type ToolExecutionOptions struct {
	ShowImages      *bool
	ImageWidthCells *int
	OutputPad       *int
}

// ToolExecutionResult holds the execution output rendered by ToolExecutionComponent.
type ToolExecutionResult struct {
	Content    ai.ContentList
	Details    any
	IsError    bool
	DurationMs *int64
}

type teImageSource struct {
	data       string
	mimeType   string
	widthCells int
}

// ToolRenderersFromDefinition extracts ToolRenderers from a ToolDefinition.
func ToolRenderersFromDefinition(def *core.ToolDefinition) *core.ToolRenderers {
	if def == nil {
		return nil
	}
	return &core.ToolRenderers{
		RenderShell:  def.RenderShell,
		RenderCall:   def.RenderCall,
		RenderResult: def.RenderResult,
	}
}

// ToolExecutionComponent renders a tool call header, streaming arguments/results,
// inline images, and mouse-clickable expand/collapse transitions.
type ToolExecutionComponent struct {
	*tui.Container

	mu sync.Mutex

	contentBox              *tui.Box
	contentText             *tui.Text
	contentTextRegion       *tui.MouseRegion
	selfRenderContainer     *tui.Container
	selfRenderHeight        int
	callRendererComponent   tui.Component
	resultRendererComponent tui.Component
	rendererState           map[string]any
	imageComponents         []*tui.Image
	imageSources            []teImageSource

	toolName         string
	toolCallId       string
	args             *jsonx.Object
	result           *ToolExecutionResult
	expanded         bool
	showImages       bool
	isPartial        bool
	imageWidthCells  int
	outputPad        int
	toolDefinition   *core.ToolRenderers
	ui               tui.TUI
	cwd              string
	hideComponent    bool
	executionStarted bool
	argsComplete     bool
	argsPending      bool
	needsUpdate      bool
}

// NewToolExecutionComponent creates a component that renders one tool call and its result.
func NewToolExecutionComponent(
	toolName string,
	toolCallId string,
	args *jsonx.Object,
	options *ToolExecutionOptions,
	toolDefinition *core.ToolRenderers,
	ui tui.TUI,
	cwd string,
) *ToolExecutionComponent {
	showImages := true
	imageWidthCells := teDefaultImageWidthCells
	outputPad := 0
	if options != nil {
		if options.ShowImages != nil {
			showImages = *options.ShowImages
		}
		if options.ImageWidthCells != nil && *options.ImageWidthCells > 0 {
			imageWidthCells = *options.ImageWidthCells
		}
		if options.OutputPad != nil && *options.OutputPad > 0 {
			outputPad = *options.OutputPad
		}
	}

	resolvedDef := core.WithBuiltInRenderers(toolName, toolDefinition)

	c := &ToolExecutionComponent{
		Container:           tui.NewContainer(),
		selfRenderContainer: tui.NewContainer(),
		rendererState:       make(map[string]any),
		toolName:            toolName,
		toolCallId:          toolCallId,
		args:                args,
		showImages:          showImages,
		imageWidthCells:     imageWidthCells,
		outputPad:           outputPad,
		toolDefinition:      resolvedDef,
		ui:                  ui,
		cwd:                 cwd,
	}

	c.Container.AddChild(tui.NewSpacer(1))

	th := theme.CurrentTheme()
	pendingBg := func(text string) string {
		return th.Bg(theme.BgToolPending, text)
	}
	c.contentBox = tui.NewBox(1+outputPad, 1, pendingBg)
	c.contentText = tui.NewText("", 1+outputPad, 1, pendingBg)
	c.contentTextRegion = c.teCreateResultRegion(c.contentText)

	if c.teUsesSelfShell(resolvedDef) {
		c.Container.AddChild(c.selfRenderContainer)
	} else if resolvedDef != nil {
		c.Container.AddChild(c.contentBox)
	} else {
		c.Container.AddChild(c.contentTextRegion)
	}

	c.updateDisplay()
	return c
}

// UpdateArgs stores streamed partial arguments for display at the next Render call.
func (c *ToolExecutionComponent) UpdateArgs(args *jsonx.Object) {
	c.mu.Lock()
	c.args = args
	c.argsPending = true
	c.mu.Unlock()
}

// MarkExecutionStarted records that tool execution has started and refreshes the header.
func (c *ToolExecutionComponent) MarkExecutionStarted() {
	c.mu.Lock()
	c.executionStarted = true
	c.argsPending = false
	c.mu.Unlock()
	c.updateDisplay()
}

// SetArgsComplete marks argument streaming complete and refreshes the display.
func (c *ToolExecutionComponent) SetArgsComplete() {
	c.mu.Lock()
	c.argsComplete = true
	c.argsPending = false
	c.mu.Unlock()
	c.updateDisplay()
}

// UpdateResult updates the tool result (partial or final) and refreshes the component.
func (c *ToolExecutionComponent) UpdateResult(result ToolExecutionResult, isPartial ...bool) {
	partial := false
	if len(isPartial) > 0 {
		partial = isPartial[0]
	}

	r := result
	c.mu.Lock()
	c.result = &r
	c.isPartial = partial
	c.argsPending = false
	var timer *core.ToolTimerState
	if !partial {
		if t, ok := c.rendererState["toolTimer"].(*core.ToolTimerState); ok {
			timer = t
		}
	}
	c.mu.Unlock()

	if timer != nil {
		timer.Stop()
	}
	c.updateDisplay()
}

// SetExpanded toggles collapsed vs expanded result display.
func (c *ToolExecutionComponent) SetExpanded(expanded bool) {
	c.mu.Lock()
	changed := c.expanded != expanded
	c.expanded = expanded
	c.mu.Unlock()
	if changed {
		c.updateDisplay()
	}
}

// SetOutputPad updates horizontal padding for tool output containers.
func (c *ToolExecutionComponent) SetOutputPad(pad int) {
	if pad < 0 {
		pad = 0
	}
	c.mu.Lock()
	changed := c.outputPad != pad
	c.outputPad = pad
	selfShell := c.teUsesSelfShell(c.toolDefinition)
	c.mu.Unlock()

	if !changed {
		return
	}
	if !selfShell {
		c.contentBox.SetPaddingX(1 + pad)
		c.contentText.SetPaddingX(1 + pad)
	}
	c.updateDisplay()
}

// SetShowImages toggles inline terminal image rendering.
func (c *ToolExecutionComponent) SetShowImages(show bool) {
	c.mu.Lock()
	changed := c.showImages != show
	c.showImages = show
	c.mu.Unlock()
	if changed {
		c.updateDisplay()
	}
}

// SetImageWidthCells updates maximum inline image cell width.
func (c *ToolExecutionComponent) SetImageWidthCells(widthCells int) {
	if widthCells < 1 {
		widthCells = teDefaultImageWidthCells
	}
	c.mu.Lock()
	changed := c.imageWidthCells != widthCells
	c.imageWidthCells = widthCells
	c.mu.Unlock()
	if changed {
		c.updateDisplay()
	}
}

// Dispose stops any background timer associated with this tool execution row.
func (c *ToolExecutionComponent) Dispose() {
	c.mu.Lock()
	timer, _ := c.rendererState["toolTimer"].(*core.ToolTimerState)
	c.mu.Unlock()
	if timer != nil {
		timer.Stop()
	}
}

// Invalidate marks cached layout dirty and notifies child components.
func (c *ToolExecutionComponent) Invalidate() {
	c.mu.Lock()
	c.needsUpdate = true
	c.mu.Unlock()
	c.Container.Invalidate()
}

// Render returns rendered ANSI lines for the tool execution component.
func (c *ToolExecutionComponent) Render(width int) []string {
	c.mu.Lock()
	need := c.argsPending || c.needsUpdate
	c.argsPending = false
	c.needsUpdate = false
	c.mu.Unlock()

	if need {
		c.updateDisplay()
	}

	c.mu.Lock()
	hidden := c.hideComponent
	selfShell := c.teUsesSelfShell(c.toolDefinition)
	c.mu.Unlock()

	if hidden {
		return []string{}
	}

	lines := c.Container.Render(width)
	if selfShell {
		h := max(0, len(lines)-1)
		c.mu.Lock()
		c.selfRenderHeight = h
		c.mu.Unlock()
	}
	return lines
}

// HandleMouse dispatches mouse events to tool shell children.
func (c *ToolExecutionComponent) HandleMouse(event tui.TuiMouseEvent) *tui.TuiMouseDispatchResult {
	c.mu.Lock()
	selfShell := c.teUsesSelfShell(c.toolDefinition)
	c.mu.Unlock()

	if !selfShell {
		return c.Container.HandleMouse(event)
	}
	if event.Y < 1 || event.Y >= event.Height {
		return nil
	}
	sub := event
	sub.Y = event.Y - 1
	sub.Height = event.Height - 1
	return c.selfRenderContainer.HandleMouse(sub)
}

func (c *ToolExecutionComponent) teUsesSelfShell(def *core.ToolRenderers) bool {
	return def != nil && def.RenderShell == core.ToolRenderShellSelf
}

func (c *ToolExecutionComponent) teCreateResultRegion(component tui.Component) *tui.MouseRegion {
	return tui.NewMouseRegion(component, func(event tui.TuiMouseEvent) *tui.TuiMouseEventResult {
		if event.Type != tui.TuiMouseEventTypeClick || event.Button != tui.TuiMouseButtonLeft {
			return nil
		}
		c.mu.Lock()
		hasResult := c.result != nil
		nextExpanded := !c.expanded
		c.mu.Unlock()
		if !hasResult {
			return nil
		}
		c.SetExpanded(nextExpanded)
		handled := true
		return &tui.TuiMouseEventResult{Handled: &handled}
	})
}

func (c *ToolExecutionComponent) updateDisplay() {
	c.mu.Lock()
	toolName := c.toolName
	toolCallId := c.toolCallId
	args := c.args
	var resultCopy *ToolExecutionResult
	if c.result != nil {
		r := *c.result
		resultCopy = &r
	}
	expanded := c.expanded
	showImages := c.showImages
	isPartial := c.isPartial
	imageWidthCells := c.imageWidthCells
	outputPad := c.outputPad
	def := c.toolDefinition
	ui := c.ui
	cwd := c.cwd
	executionStarted := c.executionStarted
	argsComplete := c.argsComplete
	lastCallComp := c.callRendererComponent
	lastResultComp := c.resultRendererComponent
	renderState := c.rendererState
	prevImages := c.imageComponents
	prevSources := c.imageSources
	c.argsPending = false
	c.needsUpdate = false
	c.mu.Unlock()

	th := theme.CurrentTheme()
	bgToken := theme.BgToolPending
	if !isPartial && resultCopy != nil {
		if resultCopy.IsError {
			bgToken = theme.BgToolError
		} else {
			bgToken = theme.BgToolSuccess
		}
	}
	bgFn := func(text string) string {
		return th.Bg(bgToken, text)
	}

	if def == nil {
		formatted := teFormatToolExecution(toolName, args, resultCopy, expanded, isPartial, showImages, th)
		c.contentText.SetCustomBgFn(bgFn)
		c.contentText.SetText(formatted)
		c.mu.Lock()
		c.hideComponent = false
		c.mu.Unlock()
		return
	}

	invalidateCb := func() {
		c.mu.Lock()
		c.needsUpdate = true
		uiRef := c.ui
		c.mu.Unlock()
		if uiRef != nil {
			uiRef.RequestRender(nil)
		}
	}

	baseCtx := core.ToolRenderContext{
		KeybindingHints:  core.DefaultKeybindingHints(),
		Args:             args,
		ToolCallId:       toolCallId,
		Invalidate:       invalidateCb,
		State:            renderState,
		Cwd:              cwd,
		ExecutionStarted: executionStarted,
		ArgsComplete:     argsComplete,
		IsPartial:        isPartial,
		Expanded:         expanded,
		ShowImages:       showImages,
		OutputPad:        outputPad,
	}

	var callComponent tui.Component
	if def.RenderCall != nil {
		callCtx := baseCtx
		callCtx.LastComponent = lastCallComp
		callComponent = def.RenderCall(args, th, callCtx)
	} else {
		callComponent = tui.NewText(core.FormatToolCallWithArgs(toolName, args, th, expanded), 0, 0, nil)
	}

	var resultComponent tui.Component
	if resultCopy != nil {
		agentRes := agent.AgentToolResult{
			Content: resultCopy.Content,
			Details: resultCopy.Details,
		}
		if def.RenderResult != nil {
			resCtx := baseCtx
			resCtx.LastComponent = lastResultComp
			resCtx.IsError = resultCopy.IsError
			resCtx.DurationMs = resultCopy.DurationMs
			resultComponent = def.RenderResult(
				agentRes,
				core.ToolRenderResultOptions{Expanded: expanded, IsPartial: isPartial},
				th,
				resCtx,
			)
		} else {
			resultComponent = teCreateDefaultResultComponent(&agentRes, expanded, showImages, th)
		}
	}

	var nextImages []*tui.Image
	var nextSources []teImageSource
	if resultCopy != nil && showImages && tui.GetCapabilities().Images != "" {
		var imgBlocks []*ai.ImageContent
		for _, block := range resultCopy.Content {
			if img, ok := block.(*ai.ImageContent); ok && img != nil {
				imgBlocks = append(imgBlocks, img)
			}
		}
		if len(imgBlocks) > 0 {
			tePngOnce.Do(utils.EnsurePngTranscoder)
			for i, img := range imgBlocks {
				src := teImageSource{
					data:       img.Data,
					mimeType:   img.MimeType,
					widthCells: imageWidthCells,
				}
				var imgComp *tui.Image
				if i < len(prevSources) && prevSources[i] == src && i < len(prevImages) && prevImages[i] != nil {
					imgComp = prevImages[i]
				} else {
					w := imageWidthCells
					imgComp = tui.NewImage(
						img.Data,
						img.MimeType,
						tui.ImageTheme{
							FallbackColor: func(s string) string {
								return th.Fg(theme.FgToolOutput, s)
							},
						},
						&tui.ImageOptions{MaxWidthCells: &w},
						nil,
					)
				}
				nextImages = append(nextImages, imgComp)
				nextSources = append(nextSources, src)
			}
		}
	}

	selfShell := c.teUsesSelfShell(def)
	hide := callComponent == nil && resultComponent == nil && len(nextImages) == 0

	c.mu.Lock()
	c.callRendererComponent = callComponent
	c.resultRendererComponent = resultComponent
	c.imageComponents = nextImages
	c.imageSources = nextSources
	c.hideComponent = hide
	c.mu.Unlock()

	if selfShell {
		c.selfRenderContainer.Clear()
		if !hide {
			if callComponent != nil {
				c.selfRenderContainer.AddChild(c.teCreateResultRegion(callComponent))
			}
			if resultComponent != nil {
				c.selfRenderContainer.AddChild(c.teCreateResultRegion(resultComponent))
			}
			for _, img := range nextImages {
				c.selfRenderContainer.AddChild(tui.NewSpacer(1))
				c.selfRenderContainer.AddChild(img)
			}
		}
	} else {
		c.contentBox.SetBgFn(bgFn)
		c.contentBox.Clear()
		if !hide {
			if callComponent != nil {
				c.contentBox.AddChild(c.teCreateResultRegion(callComponent))
			}
			if resultComponent != nil {
				c.contentBox.AddChild(c.teCreateResultRegion(resultComponent))
			}
			for _, img := range nextImages {
				c.contentBox.AddChild(tui.NewSpacer(1))
				c.contentBox.AddChild(img)
			}
		}
	}

	_ = ui
}

func teCreateDefaultResultComponent(result *agent.AgentToolResult, expanded, showImages bool, th *theme.Theme) tui.Component {
	raw := core.GetTextOutput(result, showImages)
	if raw == "" {
		return nil
	}
	lines := strings.Split(raw, "\n")
	maxLines := len(lines)
	if !expanded && maxLines > teDefaultFallbackPreviewLines {
		maxLines = teDefaultFallbackPreviewLines
	}
	display := lines[:maxLines]
	styled := make([]string, len(display))
	for i, line := range display {
		styled[i] = th.Fg(theme.FgToolOutput, line)
	}
	text := strings.Join(styled, "\n")
	if remaining := len(lines) - len(display); remaining > 0 {
		text += th.Fg(theme.FgMuted, fmt.Sprintf("\n... (%d more lines,", remaining)) +
			" " + theme.AppKeyHint(th, core.KeybindingAppToolsExpand, "to expand") +
			th.Fg(theme.FgMuted, ")")
	}
	return tui.NewText(text, 0, 0, nil)
}

func teFormatToolExecution(
	toolName string,
	args *jsonx.Object,
	result *ToolExecutionResult,
	expanded bool,
	isPartial bool,
	showImages bool,
	th *theme.Theme,
) string {
	text := th.Bold(th.Fg(theme.FgToolTitle, toolName)) + "\n"
	if args != nil {
		if formattedArgs, err := jsonx.MarshalIndent(args, "  "); err == nil && len(formattedArgs) > 0 {
			text += th.Fg(theme.FgDim, string(formattedArgs)) + "\n"
		}
	}

	if result == nil {
		return strings.TrimRight(text, "\n")
	}

	agentRes := agent.AgentToolResult{
		Content: result.Content,
		Details: result.Details,
	}
	output := core.GetTextOutput(&agentRes, showImages)
	if output == "" {
		if isPartial {
			text += th.Fg(theme.FgMuted, "...")
		}
		return strings.TrimRight(text, "\n")
	}

	lines := strings.Split(output, "\n")
	maxLines := len(lines)
	if !expanded && maxLines > teDefaultFallbackPreviewLines {
		maxLines = teDefaultFallbackPreviewLines
	}
	for _, line := range lines[:maxLines] {
		text += th.Fg(theme.FgToolOutput, line) + "\n"
	}
	if remaining := len(lines) - maxLines; remaining > 0 {
		text += th.Fg(theme.FgMuted, fmt.Sprintf("... (%d more lines,", remaining)) +
			" " + theme.AppKeyHint(th, core.KeybindingAppToolsExpand, "to expand") +
			th.Fg(theme.FgMuted, ")")
	}
	return strings.TrimRight(text, "\n")
}

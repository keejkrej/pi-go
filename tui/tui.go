// Ported from packages/tui/src/tui.ts (pi v1.5.2).

package tui

import (
	"math"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// TuiMouseEventType is the kind of a normalized mouse event.
type TuiMouseEventType string

const (
	TuiMouseEventTypePress   TuiMouseEventType = "press"
	TuiMouseEventTypeRelease TuiMouseEventType = "release"
	TuiMouseEventTypeMove    TuiMouseEventType = "move"
	TuiMouseEventTypeDrag    TuiMouseEventType = "drag"
	TuiMouseEventTypeClick   TuiMouseEventType = "click"
	TuiMouseEventTypeWheel   TuiMouseEventType = "wheel"
)

// TuiMouseButton is the button reported by a mouse event.
type TuiMouseButton string

const (
	TuiMouseButtonLeft   TuiMouseButton = "left"
	TuiMouseButtonMiddle TuiMouseButton = "middle"
	TuiMouseButtonRight  TuiMouseButton = "right"
	TuiMouseButtonNone   TuiMouseButton = "none"
)

// TuiMouseEvent is a normalized cell-based mouse event.
// Coordinates are zero-based. Field order matches the TuiMouseEvent interface.
type TuiMouseEvent struct {
	Type       TuiMouseEventType `json:"type"`
	Button     TuiMouseButton    `json:"button"`
	X          int               `json:"x"`
	Y          int               `json:"y"`
	ScreenX    int               `json:"screenX"`
	ScreenY    int               `json:"screenY"`
	Width      int               `json:"width"`
	Height     int               `json:"height"`
	Shift      bool              `json:"shift"`
	Alt        bool              `json:"alt"`
	Ctrl       bool              `json:"ctrl"`
	WheelDelta *int              `json:"wheelDelta,omitzero"`
	ClickCount *int              `json:"clickCount,omitzero"`
}

// TuiMouseEventResult is what a component's mouse handler returns.
// Nil means the handler did not handle the event.
// Field order matches the TuiMouseEventResult interface.
type TuiMouseEventResult struct {
	Handled *bool `json:"handled,omitzero"`
	Capture *bool `json:"capture,omitzero"`
	Focus   *bool `json:"focus,omitzero"`
	Render  *bool `json:"render,omitzero"`
}

// TuiMouseDispatchTarget is the component a container forwarded an event to.
// Field order matches the dispatchMouseEvent target literal.
type TuiMouseDispatchTarget struct {
	Component Component `json:"component"`
	OriginX   int       `json:"originX"`
	OriginY   int       `json:"originY"`
	Width     int       `json:"width"`
	Height    int       `json:"height"`
}

// TuiMouseDispatchResult is a mouse result that names its target.
// Handled is the TS literal true, so it is not the optional *bool of TuiMouseEventResult.
// Field order follows the dispatchMouseEvent object literal: result fields, focusTarget, then target.
type TuiMouseDispatchResult struct {
	Handled     bool                   `json:"handled"`
	Capture     *bool                  `json:"capture,omitzero"`
	Focus       *bool                  `json:"focus,omitzero"`
	Render      *bool                  `json:"render,omitzero"`
	FocusTarget Component              `json:"focusTarget,omitzero"`
	Target      TuiMouseDispatchTarget `json:"target"`
}

// DispatchMouseEvent delivers event to component and keeps the target and coordinate transform.
// It returns nil when the component does not handle the event.
func DispatchMouseEvent(component Component, event TuiMouseEvent) *TuiMouseDispatchResult {
	if tuiIsNilComponent(component) {
		return nil
	}
	if h, ok := component.(interface {
		HandleMouse(TuiMouseEvent) *TuiMouseDispatchResult
	}); ok {
		forwarded := h.HandleMouse(event)
		if forwarded == nil {
			return nil
		}
		res := *forwarded
		if tuiBoolTrue(res.Focus) && tuiHasInputHandler(component) {
			res.FocusTarget = component
		}
		return &res
	}
	var result *TuiMouseEventResult
	switch h := component.(type) {
	case interface {
		HandleMouse(TuiMouseEvent) *TuiMouseEventResult
	}:
		result = h.HandleMouse(event)
	case interface {
		HandleMouse(*TuiMouseEvent) *TuiMouseEventResult
	}:
		ev := event
		result = h.HandleMouse(&ev)
	default:
		return nil
	}
	if result == nil {
		return nil
	}
	if !tuiBoolTrue(result.Handled) && !tuiBoolTrue(result.Capture) && !tuiBoolTrue(result.Focus) {
		return nil
	}
	out := &TuiMouseDispatchResult{
		Handled: true,
		Capture: result.Capture,
		Focus:   result.Focus,
		Render:  result.Render,
		Target: TuiMouseDispatchTarget{
			Component: component,
			OriginX:   event.ScreenX - event.X,
			OriginY:   event.ScreenY - event.Y,
			Width:     event.Width,
			Height:    event.Height,
		},
	}
	if tuiBoolTrue(result.Focus) {
		out.FocusTarget = component
	}
	return out
}

// RetargetMouseEvent rebuilds local coordinates for a previously dispatched target.
func RetargetMouseEvent(event TuiMouseEvent, target TuiMouseDispatchTarget) TuiMouseEvent {
	out := event
	out.X = event.ScreenX - target.OriginX
	out.Y = event.ScreenY - target.OriginY
	out.Width = target.Width
	out.Height = target.Height
	return out
}

// Component is a renderable terminal widget.
// HandleInput(data string), HandleMouse(event TuiMouseEvent) *TuiMouseEventResult,
// and WantsKeyRelease() bool are optional in TypeScript and are not methods here.
// Callers type-assert for them. A nil HandleMouse result means the event was ignored.
type Component interface {
	// Render draws the component as lines for the viewport width.
	Render(width int) []string
	// Invalidate drops cached rendering state.
	Invalidate()
}

// TuiInputListenerResult is the optional result of an input listener.
// A nil pointer is the TS undefined result. Field order is consume, then data.
type TuiInputListenerResult struct {
	Consume *bool   `json:"consume,omitzero"`
	Data    *string `json:"data,omitzero"`
}

// TuiInputListener sees terminal input before the focused component.
// A nil result leaves the input unchanged.
type TuiInputListener func(data string) *TuiInputListenerResult

// tuiPendingTerminalColorQuery is one OSC color query waiting for its DA1 reply.
type tuiPendingTerminalColorQuery struct {
	foreground *RgbColor
	background *RgbColor
	palette    []*RgbColor
	// replied keys are "foreground", "background", or a palette index. Membership only; not iterated.
	replied map[string]struct{}
	deliver func(colors TerminalColors)
	timer   *time.Timer
}

const (
	tuiTerminalPaletteSize     = 16
	tuiTerminalColorReplyCount = 2 + tuiTerminalPaletteSize
	// tuiTerminalColorQuery is OSC 10, OSC 11, OSC 4 for palette 0-15, then a DA1 request.
	tuiTerminalColorQuery = "\x1b]10;?\x07\x1b]11;?\x07" +
		"\x1b]4;0;?\x07\x1b]4;1;?\x07\x1b]4;2;?\x07\x1b]4;3;?\x07" +
		"\x1b]4;4;?\x07\x1b]4;5;?\x07\x1b]4;6;?\x07\x1b]4;7;?\x07" +
		"\x1b]4;8;?\x07\x1b]4;9;?\x07\x1b]4;10;?\x07\x1b]4;11;?\x07" +
		"\x1b]4;12;?\x07\x1b]4;13;?\x07\x1b]4;14;?\x07\x1b]4;15;?\x07" +
		"\x1b[c"
	tuiSegmentReset              = "\x1b[0m\x1b]8;;\x07"
	tuiMinRenderIntervalMs int64 = 16
)

// tuiDeviceAttributesResponsePattern matches a primary device attributes (DA1) reply.
var tuiDeviceAttributesResponsePattern = regexp.MustCompile("^\x1b" + `\[\?[\d;]*c$`)

// Focusable is a component that can show the hardware cursor.
// The TUI sets focused. A focused component emits CursorMarker in its render output.
type Focusable interface {
	Focused() bool
	SetFocused(focused bool)
}

// IsFocusable reports whether component implements Focusable.
// A nil component is not focusable.
func IsFocusable(component Component) bool {
	if tuiIsNilComponent(component) {
		return false
	}
	_, ok := component.(Focusable)
	return ok
}

const (
	// CursorMarker is the zero-width APC sequence a focused component emits at its cursor.
	CursorMarker = "\x1b_pi:c\x07"
	// FakeCursorStart marks the beginning of an inline fake-cursor glyph span.
	FakeCursorStart = "\x1b_pi:fc\x07"
	// FakeCursorEnd marks the end of an inline fake-cursor glyph span.
	FakeCursorEnd = "\x1b_pi:/fc\x07"
)

var (
	tuiFocusedFakeCursor = regexp.MustCompile("(\x1b_pi:c\x07)\x1b_pi:fc\x07([\\s\\S]*?)(?:\x1b_pi:/fc\x07|$)")
	tuiSizePercentRe     = regexp.MustCompile(`^(\d+(?:\.\d+)?)%$`)
)

// RenderFakeCursor wraps text in fake-cursor markers resolved by TuiBase before compositing.
func RenderFakeCursor(text string) string {
	return FakeCursorStart + text + FakeCursorEnd
}

func tuiBoolTrue(p *bool) bool {
	return p != nil && *p
}

func tuiIsNilComponent(c Component) bool {
	if c == nil {
		return true
	}
	v := reflect.ValueOf(c)
	switch v.Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Slice, reflect.Map, reflect.Func, reflect.Chan:
		return v.IsNil()
	default:
		return false
	}
}

func tuiHasInputHandler(c Component) bool {
	if tuiIsNilComponent(c) {
		return false
	}
	_, ok := c.(interface{ HandleInput(string) })
	return ok
}

func tuiWantsKeyRelease(c Component) bool {
	if tuiIsNilComponent(c) {
		return false
	}
	if w, ok := c.(interface{ WantsKeyRelease() bool }); ok {
		return w.WantsKeyRelease()
	}
	return false
}

func tuiCapturesViewportInput(c Component) bool {
	if tuiIsNilComponent(c) {
		return false
	}
	if cv, ok := c.(interface{ CapturesViewportInput() bool }); ok {
		return cv.CapturesViewportInput()
	}
	return false
}

func tuiSameComponent(a, b Component) bool {
	if tuiIsNilComponent(a) && tuiIsNilComponent(b) {
		return true
	}
	if tuiIsNilComponent(a) || tuiIsNilComponent(b) {
		return false
	}
	ta, tb := reflect.TypeOf(a), reflect.TypeOf(b)
	if ta != tb {
		return false
	}
	if ta.Comparable() {
		return a == b
	}
	va, vb := reflect.ValueOf(a), reflect.ValueOf(b)
	if va.Kind() == reflect.Ptr && vb.Kind() == reflect.Ptr {
		return va.Pointer() == vb.Pointer()
	}
	return false
}

// OverlayAnchor is the anchor point for an overlay.
type OverlayAnchor string

const (
	OverlayAnchorCenter       OverlayAnchor = "center"
	OverlayAnchorTopLeft      OverlayAnchor = "top-left"
	OverlayAnchorTopRight     OverlayAnchor = "top-right"
	OverlayAnchorBottomLeft   OverlayAnchor = "bottom-left"
	OverlayAnchorBottomRight  OverlayAnchor = "bottom-right"
	OverlayAnchorTopCenter    OverlayAnchor = "top-center"
	OverlayAnchorBottomCenter OverlayAnchor = "bottom-center"
	OverlayAnchorLeftCenter   OverlayAnchor = "left-center"
	OverlayAnchorRightCenter  OverlayAnchor = "right-center"
)

// OverlayMargin is per-side overlay margin, in cells.
// It is also the object form of OverlayOptions.Margin.
// Field order matches the OverlayMargin interface.
type OverlayMargin struct {
	Top    *int `json:"top,omitzero"`
	Right  *int `json:"right,omitzero"`
	Bottom *int `json:"bottom,omitzero"`
	Left   *int `json:"left,omitzero"`
}

func (*OverlayMargin) isOverlayMarginSpec() {}

// OverlayMarginSpec is OverlayMargin or one number applied to every side.
type OverlayMarginSpec interface{ isOverlayMarginSpec() }

// OverlayMarginNumber is the number form of OverlayOptions.Margin.
type OverlayMarginNumber struct {
	Value int
}

func (*OverlayMarginNumber) isOverlayMarginSpec() {}

// SizeValue is an absolute cell count or a percentage string such as "50%".
type SizeValue interface{ isSizeValue() }

// SizeValueCells is an absolute SizeValue, in cells.
type SizeValueCells struct {
	Value int
}

func (*SizeValueCells) isSizeValue() {}

// SizeValuePercent is a percentage SizeValue. Text is the TS string, such as "50%".
type SizeValuePercent struct {
	Text string
}

func (*SizeValuePercent) isSizeValue() {}

// tuiParseSizeValue resolves value against referenceSize.
// A nil value or a percentage that does not match returns nil.
func tuiParseSizeValue(value SizeValue, referenceSize int) *int {
	if value == nil {
		return nil
	}
	switch v := value.(type) {
	case *SizeValueCells:
		if v == nil {
			return nil
		}
		res := v.Value
		return &res
	case *SizeValuePercent:
		if v == nil {
			return nil
		}
		m := tuiSizePercentRe.FindStringSubmatch(v.Text)
		if len(m) != 2 {
			return nil
		}
		pct, err := strconv.ParseFloat(m[1], 64)
		if err != nil {
			return nil
		}
		res := int(math.Floor(float64(referenceSize) * pct / 100.0))
		return &res
	default:
		return nil
	}
}

// OverlayOptions positions and sizes one overlay.
// Nil options mean the defaults. Field order matches the OverlayOptions interface.
type OverlayOptions struct {
	Width        SizeValue                                `json:"width,omitzero"`
	MinWidth     *int                                     `json:"minWidth,omitzero"`
	MaxHeight    SizeValue                                `json:"maxHeight,omitzero"`
	Anchor       *OverlayAnchor                           `json:"anchor,omitzero"`
	OffsetX      *int                                     `json:"offsetX,omitzero"`
	OffsetY      *int                                     `json:"offsetY,omitzero"`
	Row          SizeValue                                `json:"row,omitzero"`
	Col          SizeValue                                `json:"col,omitzero"`
	Margin       OverlayMarginSpec                        `json:"margin,omitzero"`
	Visible      func(termWidth int, termHeight int) bool `json:"-"`
	NonCapturing *bool                                    `json:"nonCapturing,omitzero"`
}

// OverlayUnfocusOptions chooses the component focused when an overlay releases focus.
type OverlayUnfocusOptions struct {
	Target Component `json:"target"`
}

// OverlayBounds is the last rendered terminal-relative overlay rectangle.
// Field order matches the OverlayBounds interface.
type OverlayBounds struct {
	Row    int `json:"row"`
	Col    int `json:"col"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// OverlayHandle controls one overlay returned by ShowOverlay.
type OverlayHandle interface {
	// Hide permanently removes the overlay.
	Hide()
	// SetHidden temporarily hides or shows the overlay.
	SetHidden(hidden bool)
	// IsHidden reports whether the overlay is temporarily hidden.
	IsHidden() bool
	// Focus focuses this overlay and brings it to the visual front.
	Focus()
	// Unfocus releases focus. Nil options use the next visible capturing overlay or the previous target.
	Unfocus(options *OverlayUnfocusOptions)
	// IsFocused reports whether this overlay currently has focus.
	IsFocused() bool
	// GetBounds returns the most recent rendered bounds of a visible overlay, or nil.
	GetBounds() *OverlayBounds
}

// tuiOverlayStackEntry is one entry on the overlay stack.
type tuiOverlayStackEntry struct {
	component  Component
	options    *OverlayOptions
	preFocus   Component
	hidden     bool
	focusOrder int
	bounds     *OverlayBounds
}

// tuiRenderedOverlayLayout is one overlay placed by the latest composite.
type tuiRenderedOverlayLayout struct {
	entry  *tuiOverlayStackEntry
	row    int
	col    int
	width  int
	height int
}

// tuiOverlayBlockedFocusResume is how a blocked overlay resumes focus.
type tuiOverlayBlockedFocusResume interface{ isTuiOverlayBlockedFocusResume() }

// tuiOverlayBlockedFocusResumeRestore resumes by focusing the overlay again.
type tuiOverlayBlockedFocusResumeRestore struct{}

func (*tuiOverlayBlockedFocusResumeRestore) isTuiOverlayBlockedFocusResume() {}

// tuiOverlayBlockedFocusResumeTarget resumes by focusing Target. Nil Target is TS null.
type tuiOverlayBlockedFocusResumeTarget struct {
	Target Component
}

func (*tuiOverlayBlockedFocusResumeTarget) isTuiOverlayBlockedFocusResume() {}

// tuiOverlayFocusRestoreState is the overlay focus-restore state machine.
// A nil value means status "inactive".
type tuiOverlayFocusRestoreState interface{ isTuiOverlayFocusRestoreState() }

type tuiOverlayFocusRestoreInactive struct{}

func (*tuiOverlayFocusRestoreInactive) isTuiOverlayFocusRestoreState() {}

type tuiOverlayFocusRestoreEligible struct {
	Overlay *tuiOverlayStackEntry
}

func (*tuiOverlayFocusRestoreEligible) isTuiOverlayFocusRestoreState() {}

type tuiOverlayFocusRestoreBlocked struct {
	Overlay   *tuiOverlayStackEntry
	BlockedBy Component
	Resume    tuiOverlayBlockedFocusResume
}

func (*tuiOverlayFocusRestoreBlocked) isTuiOverlayFocusRestoreState() {}

// tuiOverlayFocusRestorePolicy is the setFocusInternal overlay-restore policy.
type tuiOverlayFocusRestorePolicy string

const (
	tuiOverlayFocusRestorePolicyClear    tuiOverlayFocusRestorePolicy = "clear"
	tuiOverlayFocusRestorePolicyPreserve tuiOverlayFocusRestorePolicy = "preserve"
)

// tuiMouseLayoutChild is one child row recorded for mouse hit testing.
type tuiMouseLayoutChild struct {
	component Component
	height    int
}

// tuiMouseLayout is the child geometry from the last Container render.
type tuiMouseLayout struct {
	width    int
	children []tuiMouseLayoutChild
}

// tuiMouseOverlayHit is the result of dispatchMouseToOverlay.
type tuiMouseOverlayHit struct {
	Hit    bool
	Result *TuiMouseDispatchResult
}

// tuiResolvedOverlayLayout is the width, position, and optional max height of an overlay.
type tuiResolvedOverlayLayout struct {
	Width     int
	Row       int
	Col       int
	MaxHeight *int
}

// tuiCursorPosition is a hardware-cursor cell found from CursorMarker.
type tuiCursorPosition struct {
	Row int
	Col int
}

// Container is a component that stacks child components vertically.
// NewContainer sets children to an empty slice. The zero value is nil.
type Container struct {
	children    []Component
	mouseLayout *tuiMouseLayout
}

// NewContainer returns an empty container.
func NewContainer() *Container {
	return &Container{
		children: make([]Component, 0),
	}
}

// Children returns the child components. The slice is the live list.
func (c *Container) Children() []Component { return c.children }

// SetChildren replaces the child list. It is the TS children assignment.
func (c *Container) SetChildren(children []Component) { c.children = children }

// AddChild appends component.
func (c *Container) AddChild(component Component) {
	c.children = append(c.children, component)
}

// RemoveChild removes the first matching component.
func (c *Container) RemoveChild(component Component) {
	for i, existing := range c.children {
		if tuiSameComponent(existing, component) {
			c.children = append(c.children[:i], c.children[i+1:]...)
			return
		}
	}
}

// Clear removes every child.
func (c *Container) Clear() {
	c.children = make([]Component, 0)
}

// Invalidate invalidates every child.
func (c *Container) Invalidate() {
	for _, child := range c.children {
		if !tuiIsNilComponent(child) {
			child.Invalidate()
		}
	}
}

// HandleMouse hit-tests the last rendered child rows and forwards the event.
// It returns nil when no child handles the event.
func (c *Container) HandleMouse(event TuiMouseEvent) *TuiMouseDispatchResult {
	if event.Y < 0 || event.Y >= event.Height {
		return nil
	}
	var mouseChildren []tuiMouseLayoutChild
	if c.mouseLayout != nil && c.mouseLayout.width == event.Width {
		mouseChildren = c.mouseLayout.children
	} else {
		mouseChildren = make([]tuiMouseLayoutChild, len(c.children))
		for i, child := range c.children {
			mouseChildren[i] = tuiMouseLayoutChild{
				component: child,
				height:    len(child.Render(event.Width)),
			}
		}
	}
	childY := 0
	for _, mc := range mouseChildren {
		if event.Y >= childY && event.Y < childY+mc.height {
			childEvent := event
			childEvent.Y = event.Y - childY
			childEvent.Height = mc.height
			return DispatchMouseEvent(mc.component, childEvent)
		}
		childY += mc.height
	}
	return nil
}

// Render concatenates each child's lines.
func (c *Container) Render(width int) []string {
	lines := make([]string, 0)
	mouseChildren := make([]tuiMouseLayoutChild, 0, len(c.children))
	for _, child := range c.children {
		childLines := child.Render(width)
		mouseChildren = append(mouseChildren, tuiMouseLayoutChild{
			component: child,
			height:    len(childLines),
		})
		lines = append(lines, childLines...)
	}
	c.mouseLayout = &tuiMouseLayout{
		width:    width,
		children: mouseChildren,
	}
	return lines
}

// CompositeTuiLine paints overlayLine onto baseLine at startCol.
// overlayWidth and totalWidth are cell counts. Image lines are returned unchanged.
func CompositeTuiLine(baseLine string, overlayLine string, startCol int, overlayWidth int, totalWidth int) string {
	if IsImageLine(baseLine) {
		return baseLine
	}

	afterStart := startCol + overlayWidth
	base := ExtractSegments(baseLine, startCol, afterStart, totalWidth-afterStart, true)
	overlay := SliceWithWidth(overlayLine, 0, overlayWidth, true)
	beforePad := max(0, startCol-base.BeforeWidth)
	overlayPad := max(0, overlayWidth-overlay.Width)
	actualBeforeWidth := max(startCol, base.BeforeWidth)
	actualOverlayWidth := max(overlayWidth, overlay.Width)
	afterTarget := max(0, totalWidth-actualBeforeWidth-actualOverlayWidth)
	afterPad := max(0, afterTarget-base.AfterWidth)
	result := base.Before +
		strings.Repeat(" ", beforePad) +
		tuiSegmentReset +
		overlay.Text +
		strings.Repeat(" ", overlayPad) +
		tuiSegmentReset +
		base.After +
		strings.Repeat(" ", afterPad)

	if VisibleWidth(result) <= totalWidth {
		return result
	}
	return SliceByColumn(result, 0, totalWidth, true)
}

func tuiHideImagesUnderOverlay(lines []string, overlay OverlayBounds, viewportStart int) {
	if overlay.Height == 0 {
		return
	}
	startIdx := max(0, viewportStart-GetMaxKittyImageRows())
	for index := startIdx; index < len(lines); index++ {
		line := lines[index]
		extent := GetImageLineExtent(line)
		if extent == nil {
			continue
		}
		firstRow := index + extent.FirstRow - viewportStart
		lastRow := index + extent.LastRow - viewportStart
		if firstRow < overlay.Row+overlay.Height &&
			lastRow >= overlay.Row &&
			extent.StartCol < overlay.Col+overlay.Width &&
			extent.EndCol > overlay.Col {
			lines[index] = StripImageSequences(line)
		}
	}
}

// TuiMode is the screen mode of a TUI.
type TuiMode string

const (
	TuiModeRegular    TuiMode = "regular"
	TuiModeFullscreen TuiMode = "fullscreen"
)

// TuiStopOptions controls Stop. Nil options, or a nil PreserveScreen, leave the default.
// Field order matches the TuiStopOptions interface.
type TuiStopOptions struct {
	PreserveScreen *bool `json:"preserveScreen,omitzero"`
}

// TuiQueryTerminalColorsOptions is the argument of TUI.QueryTerminalColors.
// OnLateReply nil ignores replies that arrive after the timeout.
type TuiQueryTerminalColorsOptions struct {
	TimeoutMs   int64                       `json:"timeoutMs"`
	OnLateReply func(colors TerminalColors) `json:"-"`
}

// TUI is a terminal UI with differential rendering.
// Post is Go-only: goroutines other than the UI goroutine use it to mutate components.
// RequestRender is safe from any goroutine and coalesces.
type TUI interface {
	Component

	// Mode is "regular" or "fullscreen".
	Mode() TuiMode
	// Children returns the root child components.
	Children() []Component
	// SetChildren replaces the root child list.
	SetChildren(children []Component)
	// Terminal is the terminal this TUI drives.
	Terminal() Terminal
	// OnDebug is the Shift+Ctrl+D callback. Nil means unset.
	OnDebug() func()
	// SetOnDebug sets the Shift+Ctrl+D callback. Nil clears it.
	SetOnDebug(fn func())
	// FullRedraws is how many full redraws have run.
	FullRedraws() int

	AddChild(component Component)
	RemoveChild(component Component)
	Clear()
	GetShowHardwareCursor() bool
	SetShowHardwareCursor(enabled bool)
	GetClearOnShrink() bool
	SetClearOnShrink(enabled bool)
	// SetFocus moves keyboard focus. Nil clears it.
	SetFocus(component Component)
	// ShowOverlay shows component above the base content.
	// Nil options use the defaults. The handle controls that overlay.
	ShowOverlay(component Component, options *OverlayOptions) OverlayHandle
	// HideOverlay hides the topmost overlay and restores the previous focus.
	HideOverlay()
	// HasOverlay reports whether any overlay is visible.
	HasOverlay() bool
	Start()
	// Stop stops the terminal. Nil options mean {}.
	Stop(options *TuiStopOptions)
	// RenderNow draws immediately. Nil force means false.
	RenderNow(force *bool)
	// RequestRender schedules a draw. Nil force means false.
	RequestRender(force *bool)
	// AddInputListener registers listener and returns an unsubscribe function.
	AddInputListener(listener TuiInputListener) func()
	RemoveInputListener(listener TuiInputListener)
	// OnTerminalColorSchemeChange registers listener and returns an unsubscribe function.
	OnTerminalColorSchemeChange(listener func(scheme TerminalColorScheme)) func()
	SetTerminalColorSchemeNotifications(enabled bool)
	// QueryTerminalColors queries OSC 10, OSC 11, and palette 0-15.
	QueryTerminalColors(options TuiQueryTerminalColorsOptions) (TerminalColors, error)
	// Post runs fn on the UI goroutine.
	Post(fn func())
}

// ViewportTUI is a TUI that owns a scrollable viewport.
// ViewportTui replaces the TS VIEWPORT_TUI symbol.
type ViewportTUI interface {
	TUI
	// ViewportTui reports the viewport brand. It is always true.
	ViewportTui() bool
	// SetLayoutRoot replaces the viewport root. Nil uses the implicit document on the next render.
	SetLayoutRoot(component Component)
}

// IsViewportTui reports whether tui carries the viewport brand.
func IsViewportTui(tui TUI) bool {
	if tuiIsNilComponent(tui) {
		return false
	}
	vtui, ok := tui.(ViewportTUI)
	return ok && vtui.ViewportTui()
}

// tuiOverride is the TuiBase surface a concrete screen replaces.
// Calls from TuiBase go through TuiBase.override so the outer method runs.
// Nil means use the TuiBase method. *TuiMainScreen and *TuiAltScreen implement it.
type tuiOverride interface {
	doRender()
	resetRenderState()
	beforeTerminalStart()
	afterTerminalStart()
	beforeTerminalStop(options *TuiStopOptions)
	afterTerminalStop(options *TuiStopOptions)
	getMountedRoots() []Component
	getExitRestorePrefix() string
	getExitRestoreSuffix() string
}

type tuiInputListenerEntry struct {
	id uint64
	fn TuiInputListener
}

type tuiColorSchemeListenerEntry struct {
	id uint64
	fn func(scheme TerminalColorScheme)
}

// TuiBase is the shared differential-rendering core.
// mu guards state touched by RequestRender, timers, and the terminal input goroutine.
// Concrete screens embed TuiBase and call setOverride with the outer pointer.
// inputListeners and terminalColorSchemeListeners are slices, not sets: func values are not comparable.
type TuiBase struct {
	mu       sync.Mutex
	execMu   sync.Mutex
	renderMu sync.Mutex
	postMu   sync.Mutex
	Container
	override tuiOverride

	terminal                                Terminal
	focusedComponent                        Component
	inputListenerEntries                    []tuiInputListenerEntry
	nextListenerID                          uint64
	onDebug                                 func()
	renderRequested                         bool
	immediateRenderScheduled                bool
	renderTimer                             *time.Timer
	immediateTimer                          *time.Timer
	renderGen                               uint64
	lastRenderTime                          time.Time
	lastRenderAt                            float64
	showHardwareCursor                      bool
	clearOnShrink                           bool
	fullRedrawCount                         int
	started                                 bool
	stopped                                 bool
	exitRestored                            bool
	pendingTerminalColorQueries             []*tuiPendingTerminalColorQuery
	colorSchemeListenerEntries              []tuiColorSchemeListenerEntry
	terminalColorSchemeNotificationsEnabled bool
	// logDirectory nil disables debug logging. Crash dumps then use the OS temp directory.
	logDirectory           *string
	focusOrderCounter      int
	overlayStack           []*tuiOverlayStackEntry
	renderedOverlayLayouts []*tuiRenderedOverlayLayout
	// overlayFocusRestore nil means status "inactive".
	overlayFocusRestore tuiOverlayFocusRestoreState

	postQueue   []func()
	postRunning bool
}

// NewTuiBase constructs the shared core.
// showHardwareCursor nil leaves the cursor hidden. logDirectory nil disables debug logging.
func NewTuiBase(terminal Terminal, showHardwareCursor *bool, logDirectory *string) *TuiBase {
	b := &TuiBase{
		Container:           *NewContainer(),
		terminal:            terminal,
		logDirectory:        logDirectory,
		overlayFocusRestore: &tuiOverlayFocusRestoreInactive{},
	}
	if showHardwareCursor != nil {
		b.showHardwareCursor = *showHardwareCursor
	}
	return b
}

// setOverride installs the concrete screen used for overridable methods.
func (b *TuiBase) setOverride(override tuiOverride) { b.override = override }

// Mode is abstract. TuiAltScreen returns fullscreen. TuiMainScreen must override this with regular.
func (b *TuiBase) Mode() TuiMode {
	panic("TuiBase.Mode is abstract")
}

// Terminal returns the terminal this TUI drives.
func (b *TuiBase) Terminal() Terminal { return b.terminal }

// OnDebug returns the Shift+Ctrl+D callback. Nil means unset.
func (b *TuiBase) OnDebug() func() {
	b.mu.Lock()
	fn := b.onDebug
	b.mu.Unlock()
	return fn
}

// SetOnDebug sets the Shift+Ctrl+D callback. Nil clears it.
func (b *TuiBase) SetOnDebug(fn func()) {
	b.mu.Lock()
	b.onDebug = fn
	b.mu.Unlock()
}

// HasOverlayEntries reports whether the overlay stack is non-empty, including hidden entries.
func (b *TuiBase) HasOverlayEntries() bool {
	b.mu.Lock()
	has := len(b.overlayStack) > 0
	b.mu.Unlock()
	return has
}

// FullRedraws returns how many full redraws have run.
func (b *TuiBase) FullRedraws() int {
	b.mu.Lock()
	count := b.fullRedrawCount
	b.mu.Unlock()
	return count
}

// GetShowHardwareCursor reports whether the hardware cursor is shown while running.
func (b *TuiBase) GetShowHardwareCursor() bool {
	b.mu.Lock()
	v := b.showHardwareCursor
	b.mu.Unlock()
	return v
}

// SetShowHardwareCursor shows or hides the hardware cursor.
func (b *TuiBase) SetShowHardwareCursor(enabled bool) {
	b.mu.Lock()
	if b.showHardwareCursor == enabled {
		b.mu.Unlock()
		return
	}
	b.showHardwareCursor = enabled
	b.mu.Unlock()
	if !enabled {
		b.hideTerminalCursor()
	}
	b.RequestRender(nil)
}

// GetClearOnShrink reports whether a shorter frame clears the rows it vacated.
func (b *TuiBase) GetClearOnShrink() bool {
	b.mu.Lock()
	v := b.clearOnShrink
	b.mu.Unlock()
	return v
}

// SetClearOnShrink sets whether a shorter frame clears vacated rows.
// False, the default, leaves those rows in place.
func (b *TuiBase) SetClearOnShrink(enabled bool) {
	b.mu.Lock()
	b.clearOnShrink = enabled
	b.mu.Unlock()
}

// GetFocusedComponent returns the focused component, or nil.
func (b *TuiBase) GetFocusedComponent() Component {
	b.mu.Lock()
	c := b.focusedComponent
	b.mu.Unlock()
	return c
}

// Children returns a snapshot of the root child components.
func (b *TuiBase) Children() []Component {
	b.mu.Lock()
	c := slices.Clone(b.Container.children)
	b.mu.Unlock()
	return c
}

// SetChildren replaces the root child list.
func (b *TuiBase) SetChildren(children []Component) {
	b.mu.Lock()
	b.Container.SetChildren(children)
	b.mu.Unlock()
}

// AddChild appends component.
func (b *TuiBase) AddChild(component Component) {
	b.mu.Lock()
	b.Container.AddChild(component)
	b.mu.Unlock()
}

// RemoveChild removes the first matching component.
func (b *TuiBase) RemoveChild(component Component) {
	b.mu.Lock()
	b.Container.RemoveChild(component)
	b.mu.Unlock()
}

// Clear removes every child.
func (b *TuiBase) Clear() {
	b.mu.Lock()
	b.Container.Clear()
	b.mu.Unlock()
}

// Render concatenates each child's lines.
func (b *TuiBase) Render(width int) []string {
	b.mu.Lock()
	children := slices.Clone(b.Container.children)
	b.mu.Unlock()
	lines := make([]string, 0)
	mouseChildren := make([]tuiMouseLayoutChild, 0, len(children))
	for _, child := range children {
		childLines := child.Render(width)
		mouseChildren = append(mouseChildren, tuiMouseLayoutChild{
			component: child,
			height:    len(childLines),
		})
		lines = append(lines, childLines...)
	}
	b.mu.Lock()
	b.Container.mouseLayout = &tuiMouseLayout{
		width:    width,
		children: mouseChildren,
	}
	b.mu.Unlock()
	return lines
}

// HandleMouse hit-tests the last rendered child rows and forwards the event.
func (b *TuiBase) HandleMouse(event TuiMouseEvent) *TuiMouseDispatchResult {
	if event.Y < 0 || event.Y >= event.Height {
		return nil
	}
	b.mu.Lock()
	layout := b.Container.mouseLayout
	children := slices.Clone(b.Container.children)
	b.mu.Unlock()
	var mouseChildren []tuiMouseLayoutChild
	if layout != nil && layout.width == event.Width {
		mouseChildren = layout.children
	} else {
		mouseChildren = make([]tuiMouseLayoutChild, len(children))
		for i, child := range children {
			mouseChildren[i] = tuiMouseLayoutChild{
				component: child,
				height:    len(child.Render(event.Width)),
			}
		}
	}
	childY := 0
	for _, mc := range mouseChildren {
		if event.Y >= childY && event.Y < childY+mc.height {
			childEvent := event
			childEvent.Y = event.Y - childY
			childEvent.Height = mc.height
			return DispatchMouseEvent(mc.component, childEvent)
		}
		childY += mc.height
	}
	return nil
}

var tuiCellSizeResponsePattern = regexp.MustCompile("^\x1b" + `\[6;(\d+);(\d+)t$`)

// SetFocus moves keyboard focus. Nil clears it and clears overlay focus restore.
func (b *TuiBase) SetFocus(component Component) {
	b.setFocusInternal(component, tuiOverlayFocusRestorePolicyClear)
}

// ShowOverlay pushes component onto the overlay stack.
// Nil options use the defaults.
func (b *TuiBase) ShowOverlay(component Component, options *OverlayOptions) OverlayHandle {
	b.mu.Lock()
	b.focusOrderCounter++
	entry := &tuiOverlayStackEntry{
		component:  component,
		options:    options,
		preFocus:   b.focusedComponent,
		hidden:     false,
		focusOrder: b.focusOrderCounter,
	}
	b.overlayStack = append(b.overlayStack, entry)
	b.mu.Unlock()

	if (options == nil || !tuiBoolTrue(options.NonCapturing)) && b.isOverlayVisible(entry) {
		b.SetFocus(component)
	}
	b.hideTerminalCursor()
	b.RequestRender(nil)

	return &tuiOverlayHandle{
		tui:       b,
		entry:     entry,
		component: component,
		options:   options,
	}
}

type tuiOverlayHandle struct {
	tui       *TuiBase
	entry     *tuiOverlayStackEntry
	component Component
	options   *OverlayOptions
}

func (h *tuiOverlayHandle) Hide() {
	b := h.tui
	b.mu.Lock()
	index := slices.Index(b.overlayStack, h.entry)
	if index == -1 {
		b.mu.Unlock()
		return
	}
	b.clearOverlayFocusRestoreForLocked(h.entry)
	b.retargetOverlayPreFocusLocked(h.entry)
	b.overlayStack = append(b.overlayStack[:index], b.overlayStack[index+1:]...)
	hadFocus := tuiSameComponent(b.focusedComponent, h.component)
	empty := len(b.overlayStack) == 0
	var stackSnap []*tuiOverlayStackEntry
	if hadFocus {
		stackSnap = slices.Clone(b.overlayStack)
	}
	preFocus := h.entry.preFocus
	b.mu.Unlock()

	if hadFocus {
		topVisible := b.findTopmostVisibleOverlay(stackSnap)
		target := preFocus
		if topVisible != nil {
			target = topVisible.component
		}
		b.SetFocus(target)
	}
	if empty {
		b.hideTerminalCursor()
	}
	b.RequestRender(nil)
}

func (h *tuiOverlayHandle) SetHidden(hidden bool) {
	b := h.tui
	b.mu.Lock()
	if h.entry.hidden == hidden {
		b.mu.Unlock()
		return
	}
	h.entry.hidden = hidden
	if hidden {
		b.clearOverlayFocusRestoreForLocked(h.entry)
		hadFocus := tuiSameComponent(b.focusedComponent, h.component)
		var stackSnap []*tuiOverlayStackEntry
		if hadFocus {
			stackSnap = slices.Clone(b.overlayStack)
		}
		preFocus := h.entry.preFocus
		b.mu.Unlock()
		if hadFocus {
			topVisible := b.findTopmostVisibleOverlay(stackSnap)
			target := preFocus
			if topVisible != nil {
				target = topVisible.component
			}
			b.SetFocus(target)
		}
	} else {
		b.mu.Unlock()
		if (h.options == nil || !tuiBoolTrue(h.options.NonCapturing)) && b.isOverlayVisible(h.entry) {
			b.mu.Lock()
			b.focusOrderCounter++
			h.entry.focusOrder = b.focusOrderCounter
			b.mu.Unlock()
			b.SetFocus(h.component)
		}
	}
	b.RequestRender(nil)
}

func (h *tuiOverlayHandle) IsHidden() bool {
	b := h.tui
	b.mu.Lock()
	hidden := h.entry.hidden
	b.mu.Unlock()
	return hidden
}

func (h *tuiOverlayHandle) Focus() {
	b := h.tui
	b.mu.Lock()
	inStack := slices.Contains(b.overlayStack, h.entry)
	b.mu.Unlock()
	if !inStack || !b.isOverlayVisible(h.entry) {
		return
	}
	b.mu.Lock()
	b.focusOrderCounter++
	h.entry.focusOrder = b.focusOrderCounter
	b.mu.Unlock()
	b.SetFocus(h.component)
	b.RequestRender(nil)
}

func (h *tuiOverlayHandle) Unfocus(unfocusOptions *OverlayUnfocusOptions) {
	b := h.tui
	b.mu.Lock()
	isFocused := tuiSameComponent(b.focusedComponent, h.component)
	restoreState := b.overlayFocusRestore
	var blocked *tuiOverlayFocusRestoreBlocked
	var hasPendingRestore bool
	switch rs := restoreState.(type) {
	case *tuiOverlayFocusRestoreEligible:
		hasPendingRestore = rs.Overlay == h.entry
	case *tuiOverlayFocusRestoreBlocked:
		hasPendingRestore = rs.Overlay == h.entry
		blocked = rs
	}
	if !isFocused && !hasPendingRestore {
		b.mu.Unlock()
		return
	}
	if blocked != nil && blocked.Overlay == h.entry && tuiSameComponent(b.focusedComponent, blocked.BlockedBy) {
		if unfocusOptions != nil {
			b.overlayFocusRestore = &tuiOverlayFocusRestoreBlocked{
				Overlay:   h.entry,
				BlockedBy: blocked.BlockedBy,
				Resume:    &tuiOverlayBlockedFocusResumeTarget{Target: unfocusOptions.Target},
			}
		} else {
			b.clearOverlayFocusRestoreLocked()
		}
		b.mu.Unlock()
		b.RequestRender(nil)
		return
	}
	b.clearOverlayFocusRestoreForLocked(h.entry)
	stackSnap := slices.Clone(b.overlayStack)
	preFocus := h.entry.preFocus
	b.mu.Unlock()

	if isFocused || unfocusOptions != nil {
		var target Component
		if unfocusOptions != nil {
			target = unfocusOptions.Target
		} else {
			topVisible := b.findTopmostVisibleOverlay(stackSnap)
			if topVisible != nil && topVisible != h.entry {
				target = topVisible.component
			} else {
				target = preFocus
			}
		}
		b.SetFocus(target)
	}
	b.RequestRender(nil)
}

func (h *tuiOverlayHandle) IsFocused() bool {
	b := h.tui
	b.mu.Lock()
	focused := tuiSameComponent(b.focusedComponent, h.component)
	b.mu.Unlock()
	return focused
}

func (h *tuiOverlayHandle) GetBounds() *OverlayBounds {
	b := h.tui
	b.mu.Lock()
	inStack := slices.Contains(b.overlayStack, h.entry)
	b.mu.Unlock()
	if !inStack || !b.isOverlayVisible(h.entry) {
		return nil
	}
	b.mu.Lock()
	bounds := h.entry.bounds
	var cp *OverlayBounds
	if bounds != nil {
		v := *bounds
		cp = &v
	}
	b.mu.Unlock()
	return cp
}

// HideOverlay pops the topmost overlay and restores focus.
func (b *TuiBase) HideOverlay() {
	b.mu.Lock()
	if len(b.overlayStack) == 0 {
		b.mu.Unlock()
		return
	}
	overlay := b.overlayStack[len(b.overlayStack)-1]
	b.clearOverlayFocusRestoreForLocked(overlay)
	b.retargetOverlayPreFocusLocked(overlay)
	b.overlayStack = b.overlayStack[:len(b.overlayStack)-1]
	hadFocus := tuiSameComponent(b.focusedComponent, overlay.component)
	empty := len(b.overlayStack) == 0
	var stackSnap []*tuiOverlayStackEntry
	if hadFocus {
		stackSnap = slices.Clone(b.overlayStack)
	}
	b.mu.Unlock()

	if hadFocus {
		topVisible := b.findTopmostVisibleOverlay(stackSnap)
		target := overlay.preFocus
		if topVisible != nil {
			target = topVisible.component
		}
		b.SetFocus(target)
	}
	if empty {
		b.hideTerminalCursor()
	}
	b.RequestRender(nil)
}

// HasOverlay reports whether any overlay is currently visible.
func (b *TuiBase) HasOverlay() bool {
	b.mu.Lock()
	stackSnap := slices.Clone(b.overlayStack)
	b.mu.Unlock()
	for _, entry := range stackSnap {
		if b.isOverlayVisible(entry) {
			return true
		}
	}
	return false
}

// Invalidate invalidates mounted roots and overlay components.
func (b *TuiBase) Invalidate() {
	roots := b.dispatchGetMountedRoots()
	b.mu.Lock()
	overlays := slices.Clone(b.overlayStack)
	b.mu.Unlock()
	for _, root := range roots {
		if !tuiIsNilComponent(root) {
			root.Invalidate()
		}
	}
	for _, overlay := range overlays {
		if overlay != nil && !tuiIsNilComponent(overlay.component) {
			overlay.component.Invalidate()
		}
	}
}

// Start begins terminal input and the first render.
func (b *TuiBase) Start() {
	b.mu.Lock()
	b.started = true
	b.stopped = false
	b.exitRestored = false
	b.renderGen++
	notifyColors := b.terminalColorSchemeNotificationsEnabled
	term := b.terminal
	b.mu.Unlock()

	b.dispatchBeforeTerminalStart()
	term.Start(
		func(data string) { b.handleTerminalInput(data) },
		func() { b.RequestRender(nil) },
	)
	b.dispatchAfterTerminalStart()
	term.HideCursor()
	if notifyColors {
		term.Write("\x1b[?2031h")
	}
	b.queryCellSize()
	b.RequestRender(nil)
}

// AddInputListener registers listener and returns a function that removes it.
func (b *TuiBase) AddInputListener(listener TuiInputListener) func() {
	if listener == nil {
		return func() {}
	}
	b.mu.Lock()
	b.nextListenerID++
	id := b.nextListenerID
	b.inputListenerEntries = append(b.inputListenerEntries, tuiInputListenerEntry{id: id, fn: listener})
	b.mu.Unlock()
	return func() {
		b.mu.Lock()
		for i, entry := range b.inputListenerEntries {
			if entry.id == id {
				b.inputListenerEntries = append(b.inputListenerEntries[:i], b.inputListenerEntries[i+1:]...)
				break
			}
		}
		b.mu.Unlock()
	}
}

// RemoveInputListener removes a previously registered listener.
func (b *TuiBase) RemoveInputListener(listener TuiInputListener) {
	if listener == nil {
		return
	}
	targetPtr := reflect.ValueOf(listener).Pointer()
	b.mu.Lock()
	for i := len(b.inputListenerEntries) - 1; i >= 0; i-- {
		if reflect.ValueOf(b.inputListenerEntries[i].fn).Pointer() == targetPtr {
			b.inputListenerEntries = append(b.inputListenerEntries[:i], b.inputListenerEntries[i+1:]...)
			break
		}
	}
	b.mu.Unlock()
}

// OnTerminalColorSchemeChange registers listener and returns a function that removes it.
func (b *TuiBase) OnTerminalColorSchemeChange(listener func(scheme TerminalColorScheme)) func() {
	if listener == nil {
		return func() {}
	}
	b.mu.Lock()
	b.nextListenerID++
	id := b.nextListenerID
	b.colorSchemeListenerEntries = append(b.colorSchemeListenerEntries, tuiColorSchemeListenerEntry{id: id, fn: listener})
	b.mu.Unlock()
	return func() {
		b.mu.Lock()
		for i, entry := range b.colorSchemeListenerEntries {
			if entry.id == id {
				b.colorSchemeListenerEntries = append(b.colorSchemeListenerEntries[:i], b.colorSchemeListenerEntries[i+1:]...)
				break
			}
		}
		b.mu.Unlock()
	}
}

// SetTerminalColorSchemeNotifications enables or disables theme-report queries.
func (b *TuiBase) SetTerminalColorSchemeNotifications(enabled bool) {
	b.mu.Lock()
	if b.terminalColorSchemeNotificationsEnabled == enabled {
		b.mu.Unlock()
		return
	}
	b.terminalColorSchemeNotificationsEnabled = enabled
	stopped := b.stopped
	term := b.terminal
	b.mu.Unlock()
	if !stopped {
		if enabled {
			term.Write("\x1b[?2031h")
		} else {
			term.Write("\x1b[?2031l")
		}
	}
}

// Stop stops input and shows the cursor. Nil options mean {}.
func (b *TuiBase) Stop(options *TuiStopOptions) {
	b.mu.Lock()
	b.stopped = true
	b.renderGen++
	b.cancelRenderTimerLocked()
	b.cancelImmediateTimerLocked()
	b.renderRequested = false
	b.immediateRenderScheduled = false
	notifyColors := b.terminalColorSchemeNotificationsEnabled
	term := b.terminal
	b.mu.Unlock()

	if notifyColors {
		term.Write("\x1b[?2031l")
	}
	b.dispatchBeforeTerminalStop(options)
	term.ShowCursor()
	term.Stop()
	b.dispatchAfterTerminalStop(options)
}

// RestoreOnExit restores terminal modes if Stop has not run.
// Safe to call multiple times; restores at most once per active Start cycle.
func (b *TuiBase) RestoreOnExit() {
	b.mu.Lock()
	if !b.started || b.stopped || b.exitRestored {
		b.mu.Unlock()
		return
	}
	b.exitRestored = true
	term := b.terminal
	b.mu.Unlock()

	if restorer, ok := term.(ExitRestoreTerminal); ok {
		restorer.RestoreOnExit(b.dispatchGetExitRestorePrefix(), b.dispatchGetExitRestoreSuffix())
	}
}

// RenderNow draws immediately. Nil force means false. True resets differential render state first.
func (b *TuiBase) RenderNow(force *bool) {
	if tuiBoolTrue(force) {
		b.dispatchResetRenderState()
	}
	b.mu.Lock()
	b.renderRequested = false
	b.immediateRenderScheduled = false
	b.cancelRenderTimerLocked()
	b.cancelImmediateTimerLocked()
	b.lastRenderTime = time.Now()
	b.mu.Unlock()

	b.renderMu.Lock()
	defer b.renderMu.Unlock()
	b.dispatchDoRender()
}

// RequestRender schedules a draw. Nil force means false.
// It is safe to call from any goroutine.
func (b *TuiBase) RequestRender(force *bool) {
	if tuiBoolTrue(force) {
		b.dispatchResetRenderState()
		b.requestImmediateRender()
		return
	}
	b.mu.Lock()
	if b.renderRequested {
		b.mu.Unlock()
		return
	}
	b.renderRequested = true
	gen := b.renderGen
	b.mu.Unlock()

	time.AfterFunc(0, func() {
		b.Post(func() {
			b.scheduleRenderForGen(gen)
		})
	})
}

// Post runs fn on the UI goroutine. Go-only.
func (b *TuiBase) Post(fn func()) {
	if fn == nil {
		return
	}
	b.postMu.Lock()
	b.postQueue = append(b.postQueue, fn)
	if !b.postRunning {
		b.postRunning = true
		go b.drainPostQueue()
	}
	b.postMu.Unlock()
}

func (b *TuiBase) drainPostQueue() {
	for {
		b.postMu.Lock()
		if len(b.postQueue) == 0 {
			b.postRunning = false
			b.postMu.Unlock()
			return
		}
		fn := b.postQueue[0]
		b.postQueue[0] = nil
		b.postQueue = b.postQueue[1:]
		b.postMu.Unlock()

		b.execMu.Lock()
		fn()
		b.execMu.Unlock()
	}
}

// QueryTerminalColors queries default and palette colors.
// The error is nil unless the query is rejected; the TS promise does not reject on timeout.
func (b *TuiBase) QueryTerminalColors(options TuiQueryTerminalColorsOptions) (TerminalColors, error) {
	ch := make(chan TerminalColors, 1)
	query := &tuiPendingTerminalColorQuery{
		palette: make([]*RgbColor, tuiTerminalPaletteSize),
		replied: make(map[string]struct{}),
		deliver: func(colors TerminalColors) {
			select {
			case ch <- colors:
			default:
			}
		},
	}

	b.mu.Lock()
	b.pendingTerminalColorQueries = append(b.pendingTerminalColorQueries, query)
	query.timer = time.AfterFunc(time.Duration(options.TimeoutMs)*time.Millisecond, func() {
		b.mu.Lock()
		deliver := query.deliver
		if deliver == nil {
			b.mu.Unlock()
			return
		}
		query.deliver = options.OnLateReply
		query.timer = nil
		result := b.terminalColorQueryResult(query)
		b.mu.Unlock()
		deliver(result)
	})
	term := b.terminal
	b.mu.Unlock()

	term.Write(tuiTerminalColorQuery)
	colors := <-ch
	return colors, nil
}

func (b *TuiBase) doRender() {}

func (b *TuiBase) resetRenderState() {}

func (b *TuiBase) beforeTerminalStart() {}

func (b *TuiBase) afterTerminalStart() {}

func (b *TuiBase) beforeTerminalStop(_ *TuiStopOptions) {}

func (b *TuiBase) afterTerminalStop(_ *TuiStopOptions) {}

func (b *TuiBase) getExitRestorePrefix() string {
	b.mu.Lock()
	notify := b.terminalColorSchemeNotificationsEnabled
	b.mu.Unlock()
	if notify {
		return "\x1b[?2031l\x1b[?25h"
	}
	return "\x1b[?25h"
}

func (b *TuiBase) getExitRestoreSuffix() string { return "" }

func (b *TuiBase) dispatchDoRender() {
	if b.override != nil {
		b.override.doRender()
	} else {
		b.doRender()
	}
}

func (b *TuiBase) dispatchResetRenderState() {
	if b.override != nil {
		b.override.resetRenderState()
	} else {
		b.resetRenderState()
	}
}

func (b *TuiBase) dispatchBeforeTerminalStart() {
	if b.override != nil {
		b.override.beforeTerminalStart()
	} else {
		b.beforeTerminalStart()
	}
}

func (b *TuiBase) dispatchAfterTerminalStart() {
	if b.override != nil {
		b.override.afterTerminalStart()
	} else {
		b.afterTerminalStart()
	}
}

func (b *TuiBase) dispatchBeforeTerminalStop(options *TuiStopOptions) {
	if b.override != nil {
		b.override.beforeTerminalStop(options)
	} else {
		b.beforeTerminalStop(options)
	}
}

func (b *TuiBase) dispatchAfterTerminalStop(options *TuiStopOptions) {
	if b.override != nil {
		b.override.afterTerminalStop(options)
	} else {
		b.afterTerminalStop(options)
	}
}

func (b *TuiBase) dispatchGetMountedRoots() []Component {
	if b.override != nil {
		return b.override.getMountedRoots()
	}
	return b.getMountedRoots()
}

func (b *TuiBase) dispatchGetExitRestorePrefix() string {
	if b.override != nil {
		return b.override.getExitRestorePrefix()
	}
	return b.getExitRestorePrefix()
}

func (b *TuiBase) dispatchGetExitRestoreSuffix() string {
	if b.override != nil {
		return b.override.getExitRestoreSuffix()
	}
	return b.getExitRestoreSuffix()
}

func (b *TuiBase) setFocusInternal(component Component, overlayFocusRestore tuiOverlayFocusRestorePolicy) {
	// Snapshot mounted roots before taking b.mu to inspect/resolve blocked focus safely without holding b.mu across external Render/callbacks.
	b.mu.Lock()
	previousFocus := b.focusedComponent
	nextFocus := component
	stackSnap := slices.Clone(b.overlayStack)
	restoreState := b.overlayFocusRestore
	b.mu.Unlock()

	var previousFocusedOverlay *tuiOverlayStackEntry
	if !tuiIsNilComponent(previousFocus) {
		for _, entry := range stackSnap {
			if tuiSameComponent(entry.component, previousFocus) && b.isOverlayVisible(entry) {
				previousFocusedOverlay = entry
				break
			}
		}
	}
	nextFocusIsOverlay := false
	if !tuiIsNilComponent(nextFocus) {
		for _, entry := range stackSnap {
			if tuiSameComponent(entry.component, nextFocus) {
				nextFocusIsOverlay = true
				break
			}
		}
	}
	visibleRestore := b.resolveVisibleOverlayFocusRestore(restoreState, stackSnap)
	blockedRestore, isBlockedRestore := visibleRestore.(*tuiOverlayFocusRestoreBlocked)
	_, isInactiveRestore := visibleRestore.(*tuiOverlayFocusRestoreInactive)
	isActiveRestore := visibleRestore != nil && !isInactiveRestore

	b.mu.Lock()
	if !tuiIsNilComponent(nextFocus) && !nextFocusIsOverlay {
		if isBlockedRestore && tuiSameComponent(blockedRestore.BlockedBy, previousFocus) {
			if _, isTarget := blockedRestore.Resume.(*tuiOverlayBlockedFocusResumeTarget); isTarget || !b.isComponentMountedSnapshot(b.dispatchGetMountedRootsLockedSafe(), blockedRestore.BlockedBy) {
				nextFocus = b.resolveBlockedOverlayFocusResumeLocked(blockedRestore)
			} else {
				b.overlayFocusRestore = &tuiOverlayFocusRestoreBlocked{
					Overlay:   blockedRestore.Overlay,
					BlockedBy: nextFocus,
					Resume:    blockedRestore.Resume,
				}
			}
		} else if previousFocusedOverlay != nil &&
			isActiveRestore &&
			b.activeRestoreOverlay(visibleRestore) == previousFocusedOverlay &&
			!b.isOverlayFocusAncestorLocked(previousFocusedOverlay, nextFocus) {
			b.overlayFocusRestore = &tuiOverlayFocusRestoreBlocked{
				Overlay:   previousFocusedOverlay,
				BlockedBy: nextFocus,
				Resume:    &tuiOverlayBlockedFocusResumeRestore{},
			}
		}
	} else if tuiIsNilComponent(nextFocus) {
		if isBlockedRestore && tuiSameComponent(blockedRestore.BlockedBy, previousFocus) {
			nextFocus = b.resolveBlockedOverlayFocusResumeLocked(blockedRestore)
		} else if overlayFocusRestore == tuiOverlayFocusRestorePolicyClear {
			b.clearOverlayFocusRestoreLocked()
		}
	}

	b.focusedComponent = nextFocus

	if !tuiIsNilComponent(nextFocus) {
		for _, entry := range b.overlayStack {
			if tuiSameComponent(entry.component, nextFocus) && b.isOverlayVisibleNoTermLock(entry) {
				b.overlayFocusRestore = &tuiOverlayFocusRestoreEligible{Overlay: entry}
				break
			}
		}
	}
	b.mu.Unlock()

	if IsFocusable(previousFocus) {
		previousFocus.(Focusable).SetFocused(false)
	}
	if IsFocusable(nextFocus) {
		nextFocus.(Focusable).SetFocused(true)
	}
}

func (b *TuiBase) dispatchGetMountedRootsLockedSafe() []Component {
	if b.override != nil {
		// Note: b.mu may already be held by setFocusInternal; use children directly when override uses TuiAltScreen's layoutRoot or Container.children.
		if alt, ok := b.override.(*TuiAltScreen); ok {
			if alt.layoutRoot != nil {
				return []Component{alt.layoutRoot}
			}
			return slices.Clone(b.Container.children)
		}
	}
	return slices.Clone(b.Container.children)
}

func (b *TuiBase) activeRestoreOverlay(s tuiOverlayFocusRestoreState) *tuiOverlayStackEntry {
	switch v := s.(type) {
	case *tuiOverlayFocusRestoreEligible:
		return v.Overlay
	case *tuiOverlayFocusRestoreBlocked:
		return v.Overlay
	default:
		return nil
	}
}

func (b *TuiBase) resolveVisibleOverlayFocusRestore(restoreState tuiOverlayFocusRestoreState, stackSnap []*tuiOverlayStackEntry) tuiOverlayFocusRestoreState {
	if restoreState == nil {
		return &tuiOverlayFocusRestoreInactive{}
	}
	var overlay *tuiOverlayStackEntry
	switch v := restoreState.(type) {
	case *tuiOverlayFocusRestoreInactive:
		return v
	case *tuiOverlayFocusRestoreEligible:
		overlay = v.Overlay
	case *tuiOverlayFocusRestoreBlocked:
		overlay = v.Overlay
	}
	if overlay == nil || !slices.Contains(stackSnap, overlay) || !b.isOverlayVisible(overlay) {
		return &tuiOverlayFocusRestoreInactive{}
	}
	return restoreState
}

func (b *TuiBase) clearOverlayFocusRestoreLocked() {
	b.overlayFocusRestore = &tuiOverlayFocusRestoreInactive{}
}

func (b *TuiBase) clearOverlayFocusRestore() {
	b.mu.Lock()
	b.clearOverlayFocusRestoreLocked()
	b.mu.Unlock()
}

func (b *TuiBase) clearOverlayFocusRestoreForLocked(overlay *tuiOverlayStackEntry) {
	switch rs := b.overlayFocusRestore.(type) {
	case *tuiOverlayFocusRestoreEligible:
		if rs.Overlay == overlay {
			b.clearOverlayFocusRestoreLocked()
		}
	case *tuiOverlayFocusRestoreBlocked:
		if rs.Overlay == overlay {
			b.clearOverlayFocusRestoreLocked()
		}
	}
}

func (b *TuiBase) clearOverlayFocusRestoreFor(overlay *tuiOverlayStackEntry) {
	b.mu.Lock()
	b.clearOverlayFocusRestoreForLocked(overlay)
	b.mu.Unlock()
}

func (b *TuiBase) resolveBlockedOverlayFocusResumeLocked(restoreState *tuiOverlayFocusRestoreBlocked) Component {
	if _, ok := restoreState.Resume.(*tuiOverlayBlockedFocusResumeRestore); ok {
		return restoreState.Overlay.component
	}
	b.clearOverlayFocusRestoreLocked()
	if targetResume, ok := restoreState.Resume.(*tuiOverlayBlockedFocusResumeTarget); ok {
		return targetResume.Target
	}
	return nil
}

func (b *TuiBase) resolveBlockedOverlayFocusResume(restoreState *tuiOverlayFocusRestoreBlocked) Component {
	b.mu.Lock()
	res := b.resolveBlockedOverlayFocusResumeLocked(restoreState)
	b.mu.Unlock()
	return res
}

func (b *TuiBase) getVisibleOverlayFocusRestore() tuiOverlayFocusRestoreState {
	b.mu.Lock()
	restoreState := b.overlayFocusRestore
	stackSnap := slices.Clone(b.overlayStack)
	b.mu.Unlock()
	return b.resolveVisibleOverlayFocusRestore(restoreState, stackSnap)
}

func (b *TuiBase) isOverlayFocusAncestorLocked(entry *tuiOverlayStackEntry, component Component) bool {
	visited := make([]Component, 0, 4)
	current := entry.preFocus
	for !tuiIsNilComponent(current) {
		seen := false
		for _, v := range visited {
			if tuiSameComponent(v, current) {
				seen = true
				break
			}
		}
		if seen {
			break
		}
		visited = append(visited, current)
		if tuiSameComponent(current, component) {
			return true
		}
		var nextPre Component
		for _, overlay := range b.overlayStack {
			if tuiSameComponent(overlay.component, current) {
				nextPre = overlay.preFocus
				break
			}
		}
		current = nextPre
	}
	return false
}

func (b *TuiBase) isOverlayFocusAncestor(entry *tuiOverlayStackEntry, component Component) bool {
	b.mu.Lock()
	res := b.isOverlayFocusAncestorLocked(entry, component)
	b.mu.Unlock()
	return res
}

func (b *TuiBase) retargetOverlayPreFocusLocked(removed *tuiOverlayStackEntry) {
	for _, overlay := range b.overlayStack {
		if overlay != removed && tuiSameComponent(overlay.preFocus, removed.component) {
			overlay.preFocus = removed.preFocus
		}
	}
}

func (b *TuiBase) retargetOverlayPreFocus(removed *tuiOverlayStackEntry) {
	b.mu.Lock()
	b.retargetOverlayPreFocusLocked(removed)
	b.mu.Unlock()
}

func (b *TuiBase) getMountedRoots() []Component {
	b.mu.Lock()
	roots := slices.Clone(b.children)
	b.mu.Unlock()
	return roots
}

func (b *TuiBase) isComponentMountedSnapshot(roots []Component, component Component) bool {
	for _, root := range roots {
		if b.containsComponent(root, component) {
			return true
		}
	}
	return false
}

func (b *TuiBase) isComponentMounted(component Component) bool {
	return b.isComponentMountedSnapshot(b.dispatchGetMountedRoots(), component)
}

func (b *TuiBase) containsComponent(root Component, target Component) bool {
	if tuiSameComponent(root, target) {
		return true
	}
	container, ok := root.(interface{ Children() []Component })
	if !ok {
		// Only Containers recurse into children in TS (`if (!(root instanceof Container)) return false`).
		return false
	}
	if _, isContainer := root.(*Container); !isContainer {
		// Check embedded Container structs (Stack, VStack, HStack, ScrollView, TuiBase, etc.).
		v := reflect.ValueOf(root)
		for v.Kind() == reflect.Ptr {
			if v.IsNil() {
				return false
			}
			v = v.Elem()
		}
		if v.Kind() != reflect.Struct || !tuiHasEmbeddedContainer(v.Type()) {
			return false
		}
	}
	for _, child := range container.Children() {
		if b.containsComponent(child, target) {
			return true
		}
	}
	return false
}

func tuiHasEmbeddedContainer(t reflect.Type) bool {
	containerType := reflect.TypeFor[Container]()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.Anonymous {
			continue
		}
		ft := f.Type
		for ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
		}
		if ft == containerType || (ft.Kind() == reflect.Struct && tuiHasEmbeddedContainer(ft)) {
			return true
		}
	}
	return false
}

func (b *TuiBase) hideTerminalCursor() {
	b.mu.Lock()
	stopped := b.stopped
	term := b.terminal
	b.mu.Unlock()
	if !stopped {
		term.HideCursor()
	}
}

func (b *TuiBase) isOverlayFocused() bool {
	b.mu.Lock()
	focused := b.focusedComponent
	stackSnap := slices.Clone(b.overlayStack)
	b.mu.Unlock()
	if tuiIsNilComponent(focused) {
		return false
	}
	for _, entry := range stackSnap {
		if tuiSameComponent(entry.component, focused) && b.isOverlayVisible(entry) {
			return true
		}
	}
	return false
}

func (b *TuiBase) resolveMouseFocusTarget(component Component) Component {
	b.mu.Lock()
	stackSnap := slices.Clone(b.overlayStack)
	b.mu.Unlock()
	for i := len(stackSnap) - 1; i >= 0; i-- {
		overlay := stackSnap[i]
		if b.isOverlayVisible(overlay) && b.containsComponent(overlay.component, component) {
			return overlay.component
		}
	}
	return component
}

func (b *TuiBase) dispatchMouseToOverlay(event TuiMouseEvent) tuiMouseOverlayHit {
	b.mu.Lock()
	layouts := slices.Clone(b.renderedOverlayLayouts)
	b.mu.Unlock()
	for i := len(layouts) - 1; i >= 0; i-- {
		layout := layouts[i]
		if event.ScreenX < layout.col ||
			event.ScreenX >= layout.col+layout.width ||
			event.ScreenY < layout.row ||
			event.ScreenY >= layout.row+layout.height {
			continue
		}
		childEvent := event
		childEvent.X = event.ScreenX - layout.col
		childEvent.Y = event.ScreenY - layout.row
		childEvent.Width = layout.width
		childEvent.Height = layout.height
		result := DispatchMouseEvent(layout.entry.component, childEvent)
		if result != nil {
			res := *result
			if tuiBoolTrue(res.Focus) {
				res.FocusTarget = layout.entry.component
			}
			return tuiMouseOverlayHit{Hit: true, Result: &res}
		}
		return tuiMouseOverlayHit{Hit: true}
	}
	return tuiMouseOverlayHit{Hit: false}
}

func (b *TuiBase) isOverlayVisibleNoTermLock(entry *tuiOverlayStackEntry) bool {
	if entry == nil || entry.hidden {
		return false
	}
	if entry.options != nil && entry.options.Visible != nil {
		return entry.options.Visible(b.terminal.Columns(), b.terminal.Rows())
	}
	return true
}

func (b *TuiBase) isOverlayVisible(entry *tuiOverlayStackEntry) bool {
	if entry == nil {
		return false
	}
	b.mu.Lock()
	hidden := entry.hidden
	opts := entry.options
	term := b.terminal
	b.mu.Unlock()
	if hidden {
		return false
	}
	if opts != nil && opts.Visible != nil {
		return opts.Visible(term.Columns(), term.Rows())
	}
	return true
}

func (b *TuiBase) findTopmostVisibleOverlay(entries []*tuiOverlayStackEntry) *tuiOverlayStackEntry {
	var topmost *tuiOverlayStackEntry
	topmostOrder := 0
	for _, overlay := range entries {
		if overlay.options != nil && tuiBoolTrue(overlay.options.NonCapturing) {
			continue
		}
		if !b.isOverlayVisible(overlay) {
			continue
		}
		b.mu.Lock()
		order := overlay.focusOrder
		b.mu.Unlock()
		if topmost == nil || order > topmostOrder {
			topmost = overlay
			topmostOrder = order
		}
	}
	return topmost
}

func (b *TuiBase) getTopmostVisibleOverlay() *tuiOverlayStackEntry {
	b.mu.Lock()
	entries := slices.Clone(b.overlayStack)
	b.mu.Unlock()
	return b.findTopmostVisibleOverlay(entries)
}

func (b *TuiBase) queryCellSize() {
	if GetCapabilities().Images == "" {
		return
	}
	b.terminal.Write("\x1b[16t")
}

func (b *TuiBase) requestImmediateRender() {
	b.mu.Lock()
	b.cancelRenderTimerLocked()
	b.renderRequested = true
	if b.immediateRenderScheduled {
		b.mu.Unlock()
		return
	}
	b.immediateRenderScheduled = true
	gen := b.renderGen
	b.immediateTimer = time.AfterFunc(0, func() {
		b.Post(func() {
			b.flushImmediateRender(gen)
		})
	})
	b.mu.Unlock()
}

func (b *TuiBase) flushImmediateRender(gen uint64) {
	b.mu.Lock()
	b.immediateRenderScheduled = false
	b.immediateTimer = nil
	if gen != b.renderGen || b.stopped || !b.renderRequested {
		b.mu.Unlock()
		return
	}
	b.cancelRenderTimerLocked()
	b.renderRequested = false
	b.lastRenderTime = time.Now()
	b.mu.Unlock()

	b.renderMu.Lock()
	defer b.renderMu.Unlock()
	b.dispatchDoRender()
}

func (b *TuiBase) cancelRenderTimerLocked() {
	if b.renderTimer != nil {
		b.renderTimer.Stop()
		b.renderTimer = nil
	}
}

func (b *TuiBase) cancelImmediateTimerLocked() {
	if b.immediateTimer != nil {
		b.immediateTimer.Stop()
		b.immediateTimer = nil
	}
}

func (b *TuiBase) cancelRenderTimer() {
	b.mu.Lock()
	b.cancelRenderTimerLocked()
	b.mu.Unlock()
}

func (b *TuiBase) scheduleRender() {
	b.mu.Lock()
	gen := b.renderGen
	b.mu.Unlock()
	b.scheduleRenderForGen(gen)
}

func (b *TuiBase) scheduleRenderForGen(gen uint64) {
	b.mu.Lock()
	if gen != b.renderGen || b.stopped || b.renderTimer != nil || !b.renderRequested {
		b.mu.Unlock()
		return
	}
	var delay time.Duration
	if !b.lastRenderTime.IsZero() {
		target := time.Duration(tuiMinRenderIntervalMs) * time.Millisecond
		elapsed := time.Since(b.lastRenderTime)
		if elapsed < target {
			delay = target - elapsed
		}
	}
	b.renderTimer = time.AfterFunc(delay, func() {
		b.Post(func() {
			b.flushScheduledRender(gen)
		})
	})
	b.mu.Unlock()
}

func (b *TuiBase) flushScheduledRender(gen uint64) {
	b.mu.Lock()
	b.renderTimer = nil
	if gen != b.renderGen || b.stopped || !b.renderRequested {
		b.mu.Unlock()
		return
	}
	b.renderRequested = false
	b.lastRenderTime = time.Now()
	b.mu.Unlock()

	b.renderMu.Lock()
	b.dispatchDoRender()
	b.renderMu.Unlock()

	b.mu.Lock()
	needAnother := !b.stopped && b.renderRequested
	curGen := b.renderGen
	b.mu.Unlock()
	if needAnother {
		b.scheduleRenderForGen(curGen)
	}
}

func (b *TuiBase) handleTerminalInput(data string) {
	b.execMu.Lock()
	defer b.execMu.Unlock()

	if b.consumeTerminalColorResponse(data) {
		return
	}
	if b.consumeTerminalColorSchemeReport(data) {
		return
	}

	b.mu.Lock()
	listeners := slices.Clone(b.inputListenerEntries)
	b.mu.Unlock()
	if len(listeners) > 0 {
		current := data
		for _, entry := range listeners {
			res := entry.fn(current)
			if res != nil && tuiBoolTrue(res.Consume) {
				return
			}
			if res != nil && res.Data != nil {
				current = *res.Data
			}
		}
		if len(current) == 0 {
			return
		}
		data = current
	}

	if b.consumeCellSizeResponse(data) {
		return
	}

	b.mu.Lock()
	onDebug := b.onDebug
	b.mu.Unlock()
	if MatchesKey(data, KeyId("shift+ctrl+d")) && onDebug != nil {
		onDebug()
		return
	}

	b.mu.Lock()
	focused := b.focusedComponent
	stackSnap := slices.Clone(b.overlayStack)
	b.mu.Unlock()

	var focusedOverlay *tuiOverlayStackEntry
	for _, o := range stackSnap {
		if tuiSameComponent(o.component, focused) {
			focusedOverlay = o
			break
		}
	}
	if focusedOverlay != nil && !b.isOverlayVisible(focusedOverlay) {
		topVisible := b.findTopmostVisibleOverlay(stackSnap)
		if topVisible != nil {
			b.SetFocus(topVisible.component)
		} else {
			b.setFocusInternal(focusedOverlay.preFocus, tuiOverlayFocusRestorePolicyPreserve)
		}
	}

	b.mu.Lock()
	focused = b.focusedComponent
	stackSnap = slices.Clone(b.overlayStack)
	b.mu.Unlock()

	focusIsOverlay := false
	for _, o := range stackSnap {
		if tuiSameComponent(o.component, focused) {
			focusIsOverlay = true
			break
		}
	}
	if !focusIsOverlay {
		restoreState := b.getVisibleOverlayFocusRestore()
		switch rs := restoreState.(type) {
		case *tuiOverlayFocusRestoreEligible:
			b.SetFocus(rs.Overlay.component)
		case *tuiOverlayFocusRestoreBlocked:
			if !tuiSameComponent(rs.BlockedBy, focused) {
				switch res := rs.Resume.(type) {
				case *tuiOverlayBlockedFocusResumeRestore:
					b.SetFocus(rs.Overlay.component)
				case *tuiOverlayBlockedFocusResumeTarget:
					b.clearOverlayFocusRestore()
					b.SetFocus(res.Target)
				default:
					b.clearOverlayFocusRestore()
					b.SetFocus(rs.Overlay.component)
				}
			}
		}
	}

	b.mu.Lock()
	focused = b.focusedComponent
	b.mu.Unlock()
	if handler, ok := focused.(interface{ HandleInput(string) }); ok {
		if IsKeyRelease(data) && !tuiWantsKeyRelease(focused) {
			return
		}
		handler.HandleInput(data)
		b.requestImmediateRender()
	}
}

func (b *TuiBase) consumeTerminalColorResponse(data string) bool {
	b.mu.Lock()
	if len(b.pendingTerminalColorQueries) == 0 {
		b.mu.Unlock()
		return false
	}
	query := b.pendingTerminalColorQueries[0]
	if tuiDeviceAttributesResponsePattern.MatchString(data) {
		b.pendingTerminalColorQueries = b.pendingTerminalColorQueries[1:]
		deliver, result := b.completeTerminalColorQueryLocked(query)
		b.mu.Unlock()
		if deliver != nil {
			deliver(result)
		}
		return true
	}

	response := ParseOscColorResponse(data)
	if response == nil {
		b.mu.Unlock()
		return false
	}

	var key string
	var isFg, isBg bool
	palIdx := -1
	switch t := response.Target.(type) {
	case *OscColorTargetForeground:
		key = "foreground"
		isFg = true
	case *OscColorTargetBackground:
		key = "background"
		isBg = true
	case *OscColorTargetIndex:
		key = strconv.Itoa(t.Index)
		palIdx = t.Index
	default:
		b.mu.Unlock()
		return false
	}

	if query.deliver == nil {
		b.mu.Unlock()
		return true
	}
	if _, seen := query.replied[key]; seen {
		b.mu.Unlock()
		return true
	}
	query.replied[key] = struct{}{}
	if isFg {
		query.foreground = response.Rgb
	} else if isBg {
		query.background = response.Rgb
	} else if palIdx >= 0 && palIdx < tuiTerminalPaletteSize {
		query.palette[palIdx] = response.Rgb
	}
	var deliver func(TerminalColors)
	var result TerminalColors
	if len(query.replied) == tuiTerminalColorReplyCount {
		deliver, result = b.completeTerminalColorQueryLocked(query)
	}
	b.mu.Unlock()
	if deliver != nil {
		deliver(result)
	}
	return true
}

func (b *TuiBase) terminalColorQueryResult(query *tuiPendingTerminalColorQuery) TerminalColors {
	allPalette := true
	for _, p := range query.palette {
		if p == nil {
			allPalette = false
			break
		}
	}
	var palette []RgbColor
	if allPalette {
		palette = make([]RgbColor, len(query.palette))
		for i, p := range query.palette {
			palette[i] = *p
		}
	}
	return TerminalColors{
		Foreground: query.foreground,
		Background: query.background,
		Palette:    palette,
	}
}

func (b *TuiBase) completeTerminalColorQueryLocked(query *tuiPendingTerminalColorQuery) (func(TerminalColors), TerminalColors) {
	deliver := query.deliver
	query.deliver = nil
	if query.timer != nil {
		query.timer.Stop()
		query.timer = nil
	}
	var result TerminalColors
	if deliver != nil {
		result = b.terminalColorQueryResult(query)
	}
	return deliver, result
}

func (b *TuiBase) completeTerminalColorQuery(query *tuiPendingTerminalColorQuery) {
	b.mu.Lock()
	deliver, result := b.completeTerminalColorQueryLocked(query)
	b.mu.Unlock()
	if deliver != nil {
		deliver(result)
	}
}

func (b *TuiBase) consumeTerminalColorSchemeReport(data string) bool {
	scheme := ParseTerminalColorSchemeReport(data)
	if scheme == nil {
		return false
	}
	b.mu.Lock()
	listeners := slices.Clone(b.colorSchemeListenerEntries)
	b.mu.Unlock()
	for _, entry := range listeners {
		entry.fn(*scheme)
	}
	return true
}

func (b *TuiBase) consumeCellSizeResponse(data string) bool {
	match := tuiCellSizeResponsePattern.FindStringSubmatch(data)
	if len(match) != 3 {
		return false
	}
	heightPx, errH := strconv.Atoi(match[1])
	widthPx, errW := strconv.Atoi(match[2])
	if errH != nil || errW != nil || heightPx <= 0 || widthPx <= 0 {
		return true
	}
	SetCellDimensions(CellDimensions{WidthPx: widthPx, HeightPx: heightPx})
	b.Invalidate()
	b.RequestRender(nil)
	return true
}

func (b *TuiBase) resolveOverlayLayout(options *OverlayOptions, overlayHeight int, termWidth int, termHeight int) tuiResolvedOverlayLayout {
	var opt OverlayOptions
	if options != nil {
		opt = *options
	}

	var marginTop, marginRight, marginBottom, marginLeft int
	switch m := opt.Margin.(type) {
	case *OverlayMarginNumber:
		if m != nil {
			v := max(0, m.Value)
			marginTop, marginRight, marginBottom, marginLeft = v, v, v, v
		}
	case *OverlayMargin:
		if m != nil {
			if m.Top != nil {
				marginTop = max(0, *m.Top)
			}
			if m.Right != nil {
				marginRight = max(0, *m.Right)
			}
			if m.Bottom != nil {
				marginBottom = max(0, *m.Bottom)
			}
			if m.Left != nil {
				marginLeft = max(0, *m.Left)
			}
		}
	}

	availWidth := max(1, termWidth-marginLeft-marginRight)
	availHeight := max(1, termHeight-marginTop-marginBottom)

	width := min(80, availWidth)
	if parsedWidth := tuiParseSizeValue(opt.Width, termWidth); parsedWidth != nil {
		width = *parsedWidth
	}
	if opt.MinWidth != nil {
		width = max(width, *opt.MinWidth)
	}
	width = max(1, min(width, availWidth))

	var maxHeight *int
	if parsedMaxH := tuiParseSizeValue(opt.MaxHeight, termHeight); parsedMaxH != nil {
		clamped := max(1, min(*parsedMaxH, availHeight))
		maxHeight = &clamped
	}

	effectiveHeight := overlayHeight
	if maxHeight != nil {
		effectiveHeight = min(overlayHeight, *maxHeight)
	}

	anchor := OverlayAnchorCenter
	if opt.Anchor != nil {
		anchor = *opt.Anchor
	}

	var row int
	if opt.Row != nil {
		switch r := opt.Row.(type) {
		case *SizeValuePercent:
			if r != nil {
				if m := tuiSizePercentRe.FindStringSubmatch(r.Text); len(m) == 2 {
					if pct, err := strconv.ParseFloat(m[1], 64); err == nil {
						maxRow := max(0, availHeight-effectiveHeight)
						row = marginTop + int(math.Floor(float64(maxRow)*(pct/100.0)))
					} else {
						row = b.resolveAnchorRow(OverlayAnchorCenter, effectiveHeight, availHeight, marginTop)
					}
				} else {
					row = b.resolveAnchorRow(OverlayAnchorCenter, effectiveHeight, availHeight, marginTop)
				}
			} else {
				row = b.resolveAnchorRow(anchor, effectiveHeight, availHeight, marginTop)
			}
		case *SizeValueCells:
			if r != nil {
				row = r.Value
			} else {
				row = b.resolveAnchorRow(anchor, effectiveHeight, availHeight, marginTop)
			}
		default:
			row = b.resolveAnchorRow(anchor, effectiveHeight, availHeight, marginTop)
		}
	} else {
		row = b.resolveAnchorRow(anchor, effectiveHeight, availHeight, marginTop)
	}

	var col int
	if opt.Col != nil {
		switch c := opt.Col.(type) {
		case *SizeValuePercent:
			if c != nil {
				if m := tuiSizePercentRe.FindStringSubmatch(c.Text); len(m) == 2 {
					if pct, err := strconv.ParseFloat(m[1], 64); err == nil {
						maxCol := max(0, availWidth-width)
						col = marginLeft + int(math.Floor(float64(maxCol)*(pct/100.0)))
					} else {
						col = b.resolveAnchorCol(OverlayAnchorCenter, width, availWidth, marginLeft)
					}
				} else {
					col = b.resolveAnchorCol(OverlayAnchorCenter, width, availWidth, marginLeft)
				}
			} else {
				col = b.resolveAnchorCol(anchor, width, availWidth, marginLeft)
			}
		case *SizeValueCells:
			if c != nil {
				col = c.Value
			} else {
				col = b.resolveAnchorCol(anchor, width, availWidth, marginLeft)
			}
		default:
			col = b.resolveAnchorCol(anchor, width, availWidth, marginLeft)
		}
	} else {
		col = b.resolveAnchorCol(anchor, width, availWidth, marginLeft)
	}

	if opt.OffsetY != nil {
		row += *opt.OffsetY
	}
	if opt.OffsetX != nil {
		col += *opt.OffsetX
	}

	row = max(marginTop, min(row, termHeight-marginBottom-effectiveHeight))
	col = max(marginLeft, min(col, termWidth-marginRight-width))

	return tuiResolvedOverlayLayout{
		Width:     width,
		Row:       row,
		Col:       col,
		MaxHeight: maxHeight,
	}
}

func (b *TuiBase) resolveAnchorRow(anchor OverlayAnchor, height int, availHeight int, marginTop int) int {
	switch anchor {
	case OverlayAnchorTopLeft, OverlayAnchorTopCenter, OverlayAnchorTopRight:
		return marginTop
	case OverlayAnchorBottomLeft, OverlayAnchorBottomCenter, OverlayAnchorBottomRight:
		return marginTop + availHeight - height
	case OverlayAnchorLeftCenter, OverlayAnchorCenter, OverlayAnchorRightCenter:
		return marginTop + (availHeight-height)/2
	default:
		return marginTop + (availHeight-height)/2
	}
}

func (b *TuiBase) resolveAnchorCol(anchor OverlayAnchor, width int, availWidth int, marginLeft int) int {
	switch anchor {
	case OverlayAnchorTopLeft, OverlayAnchorLeftCenter, OverlayAnchorBottomLeft:
		return marginLeft
	case OverlayAnchorTopRight, OverlayAnchorRightCenter, OverlayAnchorBottomRight:
		return marginLeft + availWidth - width
	case OverlayAnchorTopCenter, OverlayAnchorCenter, OverlayAnchorBottomCenter:
		return marginLeft + (availWidth-width)/2
	default:
		return marginLeft + (availWidth-width)/2
	}
}

func (b *TuiBase) compositeOverlays(lines []string, termWidth int, termHeight int) []string {
	b.mu.Lock()
	if len(b.overlayStack) == 0 {
		b.renderedOverlayLayouts = nil
		b.mu.Unlock()
		return lines
	}
	type overlaySnapshot struct {
		entry      *tuiOverlayStackEntry
		focusOrder int
	}
	stackSnap := make([]overlaySnapshot, len(b.overlayStack))
	for i, entry := range b.overlayStack {
		entry.bounds = nil
		stackSnap[i] = overlaySnapshot{
			entry:      entry,
			focusOrder: entry.focusOrder,
		}
	}
	b.mu.Unlock()

	result := slices.Clone(lines)
	type renderedOverlay struct {
		entry        *tuiOverlayStackEntry
		overlayLines []string
		row          int
		col          int
		w            int
	}

	visibleEntries := make([]overlaySnapshot, 0, len(stackSnap))
	for _, snap := range stackSnap {
		if b.isOverlayVisible(snap.entry) {
			visibleEntries = append(visibleEntries, snap)
		}
	}
	slices.SortStableFunc(visibleEntries, func(a, c overlaySnapshot) int {
		if a.focusOrder < c.focusOrder {
			return -1
		}
		if a.focusOrder > c.focusOrder {
			return 1
		}
		return 0
	})

	rendered := make([]renderedOverlay, 0, len(visibleEntries))
	minLinesNeeded := len(result)
	for _, snap := range visibleEntries {
		entry := snap.entry
		firstLayout := b.resolveOverlayLayout(entry.options, 0, termWidth, termHeight)
		overlayLines := b.resolveFakeCursors(entry.component.Render(firstLayout.Width))
		if firstLayout.MaxHeight != nil && len(overlayLines) > *firstLayout.MaxHeight {
			overlayLines = overlayLines[:*firstLayout.MaxHeight]
		}
		posLayout := b.resolveOverlayLayout(entry.options, len(overlayLines), termWidth, termHeight)
		bounds := &OverlayBounds{
			Row:    posLayout.Row,
			Col:    posLayout.Col,
			Width:  firstLayout.Width,
			Height: len(overlayLines),
		}
		b.mu.Lock()
		entry.bounds = bounds
		b.mu.Unlock()

		rendered = append(rendered, renderedOverlay{
			entry:        entry,
			overlayLines: overlayLines,
			row:          posLayout.Row,
			col:          posLayout.Col,
			w:            firstLayout.Width,
		})
		if endRow := posLayout.Row + len(overlayLines); endRow > minLinesNeeded {
			minLinesNeeded = endRow
		}
	}

	renderedLayouts := make([]*tuiRenderedOverlayLayout, len(rendered))
	for i, r := range rendered {
		renderedLayouts[i] = &tuiRenderedOverlayLayout{
			entry:  r.entry,
			row:    r.row,
			col:    r.col,
			width:  r.w,
			height: len(r.overlayLines),
		}
	}
	b.mu.Lock()
	b.renderedOverlayLayouts = renderedLayouts
	b.mu.Unlock()

	workingHeight := max(len(result), termHeight, minLinesNeeded)
	for len(result) < workingHeight {
		result = append(result, "")
	}
	viewportStart := max(0, workingHeight-termHeight)

	for _, r := range rendered {
		tuiHideImagesUnderOverlay(result, OverlayBounds{
			Row:    r.row,
			Col:    r.col,
			Width:  r.w,
			Height: len(r.overlayLines),
		}, viewportStart)
		for i, overlayLine := range r.overlayLines {
			idx := viewportStart + r.row + i
			if idx >= 0 && idx < len(result) {
				truncated := overlayLine
				if VisibleWidth(truncated) > r.w {
					truncated = SliceByColumn(truncated, 0, r.w, true)
				}
				result[idx] = b.compositeLineAt(result[idx], truncated, r.col, r.w, termWidth)
			}
		}
	}
	return result
}

func (b *TuiBase) resolveFakeCursors(lines []string) []string {
	showHW := b.GetShowHardwareCursor()
	out := make([]string, len(lines))
	for i, line := range lines {
		if !strings.Contains(line, FakeCursorStart) {
			out[i] = line
			continue
		}
		resolved := line
		if showHW {
			resolved = tuiFocusedFakeCursor.ReplaceAllString(resolved, "$1$2")
		}
		resolved = strings.ReplaceAll(resolved, FakeCursorStart, "\x1b[7m")
		resolved = strings.ReplaceAll(resolved, FakeCursorEnd, "\x1b[27m")
		out[i] = resolved
	}
	return out
}

func (b *TuiBase) applyLineResets(lines []string) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		if IsImageLine(line) {
			out[i] = line
			continue
		}
		out[i] = NormalizeTerminalOutput(strings.ReplaceAll(line, CursorMarker, "")) + tuiSegmentReset
	}
	return out
}

func (b *TuiBase) compositeLineAt(baseLine string, overlayLine string, startCol int, overlayWidth int, totalWidth int) string {
	return CompositeTuiLine(baseLine, overlayLine, startCol, overlayWidth, totalWidth)
}

func (b *TuiBase) extractCursorPosition(lines []string, height int) *tuiCursorPosition {
	viewportTop := max(0, len(lines)-height)
	for row := len(lines) - 1; row >= viewportTop; row-- {
		line := lines[row]
		markerIndex := strings.Index(line, CursorMarker)
		if markerIndex != -1 {
			beforeMarker := line[:markerIndex]
			col := VisibleWidth(beforeMarker)
			lines[row] = strings.ReplaceAll(line, CursorMarker, "")
			return &tuiCursorPosition{Row: row, Col: col}
		}
	}
	return nil
}

var (
	_ Component   = (*Container)(nil)
	_ TUI         = (*TuiBase)(nil)
	_ tuiOverride = (*TuiBase)(nil)
	_ ViewportTUI = (*TuiAltScreen)(nil)
)

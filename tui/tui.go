// Ported from packages/tui/src/tui.ts (pi v1.0.0).

package tui

import (
	"regexp"
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
	panic("unported: DispatchMouseEvent")
}

// RetargetMouseEvent rebuilds local coordinates for a previously dispatched target.
func RetargetMouseEvent(event TuiMouseEvent, target TuiMouseDispatchTarget) TuiMouseEvent {
	panic("unported: RetargetMouseEvent")
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
	panic("unported: IsFocusable")
}

// CursorMarker is the zero-width APC sequence a focused component emits at its cursor.
const CursorMarker = "\x1b_pi:c\x07"

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
	panic("unported: tuiParseSizeValue")
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
	panic("unported: NewContainer")
}

// Children returns the child components. The slice is the live list.
func (c *Container) Children() []Component { return c.children }

// SetChildren replaces the child list. It is the TS children assignment.
func (c *Container) SetChildren(children []Component) { c.children = children }

// AddChild appends component.
func (c *Container) AddChild(component Component) {
	panic("unported: Container.AddChild")
}

// RemoveChild removes the first matching component.
func (c *Container) RemoveChild(component Component) {
	panic("unported: Container.RemoveChild")
}

// Clear removes every child.
func (c *Container) Clear() {
	panic("unported: Container.Clear")
}

// Invalidate invalidates every child.
func (c *Container) Invalidate() {
	panic("unported: Container.Invalidate")
}

// HandleMouse hit-tests the last rendered child rows and forwards the event.
// It returns nil when no child handles the event.
func (c *Container) HandleMouse(event TuiMouseEvent) *TuiMouseDispatchResult {
	panic("unported: Container.HandleMouse")
}

// Render concatenates each child's lines.
func (c *Container) Render(width int) []string {
	panic("unported: Container.Render")
}

// CompositeTuiLine paints overlayLine onto baseLine at startCol.
// overlayWidth and totalWidth are cell counts. Image lines are returned unchanged.
func CompositeTuiLine(baseLine string, overlayLine string, startCol int, overlayWidth int, totalWidth int) string {
	panic("unported: CompositeTuiLine")
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
	panic("unported: IsViewportTui")
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
}

// TuiBase is the shared differential-rendering core.
// mu guards state touched by RequestRender, timers, and the terminal input goroutine.
// Concrete screens embed TuiBase and call setOverride with the outer pointer.
// inputListeners and terminalColorSchemeListeners are slices, not sets: func values are not comparable.
type TuiBase struct {
	mu sync.Mutex
	Container
	override tuiOverride

	terminal                                Terminal
	focusedComponent                        Component
	inputListeners                          []TuiInputListener
	onDebug                                 func()
	renderRequested                         bool
	immediateRenderScheduled                bool
	renderTimer                             *time.Timer
	lastRenderAt                            float64
	showHardwareCursor                      bool
	clearOnShrink                           bool
	fullRedrawCount                         int
	stopped                                 bool
	pendingTerminalColorQueries             []*tuiPendingTerminalColorQuery
	terminalColorSchemeListeners            []func(scheme TerminalColorScheme)
	terminalColorSchemeNotificationsEnabled bool
	// logDirectory nil disables debug logging. Crash dumps then use the OS temp directory.
	logDirectory           *string
	focusOrderCounter      int
	overlayStack           []*tuiOverlayStackEntry
	renderedOverlayLayouts []*tuiRenderedOverlayLayout
	// overlayFocusRestore nil means status "inactive".
	overlayFocusRestore tuiOverlayFocusRestoreState
}

// NewTuiBase constructs the shared core.
// showHardwareCursor nil leaves the cursor hidden. logDirectory nil disables debug logging.
func NewTuiBase(terminal Terminal, showHardwareCursor *bool, logDirectory *string) *TuiBase {
	panic("unported: NewTuiBase")
}

// setOverride installs the concrete screen used for overridable methods.
func (b *TuiBase) setOverride(override tuiOverride) { b.override = override }

// Mode is abstract. TuiAltScreen returns fullscreen. TuiMainScreen must override this with regular.
func (b *TuiBase) Mode() TuiMode {
	panic("unported: TuiBase.Mode")
}

// Terminal returns the terminal this TUI drives.
func (b *TuiBase) Terminal() Terminal { return b.terminal }

// OnDebug returns the Shift+Ctrl+D callback. Nil means unset.
func (b *TuiBase) OnDebug() func() { return b.onDebug }

// SetOnDebug sets the Shift+Ctrl+D callback. Nil clears it.
func (b *TuiBase) SetOnDebug(fn func()) { b.onDebug = fn }

// HasOverlayEntries reports whether the overlay stack is non-empty, including hidden entries.
func (b *TuiBase) HasOverlayEntries() bool { return len(b.overlayStack) > 0 }

// FullRedraws returns how many full redraws have run.
func (b *TuiBase) FullRedraws() int { return b.fullRedrawCount }

// GetShowHardwareCursor reports whether the hardware cursor is shown while running.
func (b *TuiBase) GetShowHardwareCursor() bool { return b.showHardwareCursor }

// SetShowHardwareCursor shows or hides the hardware cursor.
func (b *TuiBase) SetShowHardwareCursor(enabled bool) {
	panic("unported: TuiBase.SetShowHardwareCursor")
}

// GetClearOnShrink reports whether a shorter frame clears the rows it vacated.
func (b *TuiBase) GetClearOnShrink() bool { return b.clearOnShrink }

// SetClearOnShrink sets whether a shorter frame clears vacated rows.
// False, the default, leaves those rows in place.
func (b *TuiBase) SetClearOnShrink(enabled bool) { b.clearOnShrink = enabled }

// GetFocusedComponent returns the focused component, or nil.
func (b *TuiBase) GetFocusedComponent() Component { return b.focusedComponent }

// SetFocus moves keyboard focus. Nil clears it and clears overlay focus restore.
func (b *TuiBase) SetFocus(component Component) {
	panic("unported: TuiBase.SetFocus")
}

// ShowOverlay pushes component onto the overlay stack.
// Nil options use the defaults.
func (b *TuiBase) ShowOverlay(component Component, options *OverlayOptions) OverlayHandle {
	panic("unported: TuiBase.ShowOverlay")
}

// HideOverlay pops the topmost overlay and restores focus.
func (b *TuiBase) HideOverlay() {
	panic("unported: TuiBase.HideOverlay")
}

// HasOverlay reports whether any overlay is currently visible.
func (b *TuiBase) HasOverlay() bool {
	panic("unported: TuiBase.HasOverlay")
}

// Invalidate invalidates mounted roots and overlay components.
func (b *TuiBase) Invalidate() {
	panic("unported: TuiBase.Invalidate")
}

// Start begins terminal input and the first render.
func (b *TuiBase) Start() {
	panic("unported: TuiBase.Start")
}

// AddInputListener registers listener and returns a function that removes it.
func (b *TuiBase) AddInputListener(listener TuiInputListener) func() {
	panic("unported: TuiBase.AddInputListener")
}

// RemoveInputListener removes a previously registered listener.
func (b *TuiBase) RemoveInputListener(listener TuiInputListener) {
	panic("unported: TuiBase.RemoveInputListener")
}

// OnTerminalColorSchemeChange registers listener and returns a function that removes it.
func (b *TuiBase) OnTerminalColorSchemeChange(listener func(scheme TerminalColorScheme)) func() {
	panic("unported: TuiBase.OnTerminalColorSchemeChange")
}

// SetTerminalColorSchemeNotifications enables or disables theme-report queries.
func (b *TuiBase) SetTerminalColorSchemeNotifications(enabled bool) {
	panic("unported: TuiBase.SetTerminalColorSchemeNotifications")
}

// Stop stops input and shows the cursor. Nil options mean {}.
func (b *TuiBase) Stop(options *TuiStopOptions) {
	panic("unported: TuiBase.Stop")
}

// RenderNow draws immediately. Nil force means false. True resets differential render state first.
func (b *TuiBase) RenderNow(force *bool) {
	panic("unported: TuiBase.RenderNow")
}

// RequestRender schedules a draw. Nil force means false.
// It is safe to call from any goroutine.
func (b *TuiBase) RequestRender(force *bool) {
	panic("unported: TuiBase.RequestRender")
}

// Post runs fn on the UI goroutine. Go-only.
func (b *TuiBase) Post(fn func()) {
	panic("unported: TuiBase.Post")
}

// QueryTerminalColors queries default and palette colors.
// The error is nil unless the query is rejected; the TS promise does not reject on timeout.
func (b *TuiBase) QueryTerminalColors(options TuiQueryTerminalColorsOptions) (TerminalColors, error) {
	panic("unported: TuiBase.QueryTerminalColors")
}

func (b *TuiBase) doRender() {
	panic("unported: TuiBase.doRender")
}

func (b *TuiBase) resetRenderState() {}

func (b *TuiBase) beforeTerminalStart() {}

func (b *TuiBase) afterTerminalStart() {}

func (b *TuiBase) beforeTerminalStop(_ *TuiStopOptions) {}

func (b *TuiBase) afterTerminalStop(_ *TuiStopOptions) {}

func (b *TuiBase) setFocusInternal(component Component, policy tuiOverlayFocusRestorePolicy) {
	panic("unported: TuiBase.setFocusInternal")
}

func (b *TuiBase) clearOverlayFocusRestore() {
	panic("unported: TuiBase.clearOverlayFocusRestore")
}

func (b *TuiBase) clearOverlayFocusRestoreFor(overlay *tuiOverlayStackEntry) {
	panic("unported: TuiBase.clearOverlayFocusRestoreFor")
}

func (b *TuiBase) resolveBlockedOverlayFocusResume(restoreState *tuiOverlayFocusRestoreBlocked) Component {
	panic("unported: TuiBase.resolveBlockedOverlayFocusResume")
}

func (b *TuiBase) getVisibleOverlayFocusRestore() tuiOverlayFocusRestoreState {
	panic("unported: TuiBase.getVisibleOverlayFocusRestore")
}

func (b *TuiBase) isOverlayFocusAncestor(entry *tuiOverlayStackEntry, component Component) bool {
	panic("unported: TuiBase.isOverlayFocusAncestor")
}

func (b *TuiBase) retargetOverlayPreFocus(removed *tuiOverlayStackEntry) {
	panic("unported: TuiBase.retargetOverlayPreFocus")
}

func (b *TuiBase) getMountedRoots() []Component { return b.children }

func (b *TuiBase) isComponentMounted(component Component) bool {
	panic("unported: TuiBase.isComponentMounted")
}

func (b *TuiBase) containsComponent(root Component, target Component) bool {
	panic("unported: TuiBase.containsComponent")
}

func (b *TuiBase) hideTerminalCursor() {
	panic("unported: TuiBase.hideTerminalCursor")
}

func (b *TuiBase) isOverlayFocused() bool {
	panic("unported: TuiBase.isOverlayFocused")
}

func (b *TuiBase) resolveMouseFocusTarget(component Component) Component {
	panic("unported: TuiBase.resolveMouseFocusTarget")
}

func (b *TuiBase) dispatchMouseToOverlay(event TuiMouseEvent) tuiMouseOverlayHit {
	panic("unported: TuiBase.dispatchMouseToOverlay")
}

func (b *TuiBase) isOverlayVisible(entry *tuiOverlayStackEntry) bool {
	panic("unported: TuiBase.isOverlayVisible")
}

func (b *TuiBase) getTopmostVisibleOverlay() *tuiOverlayStackEntry {
	panic("unported: TuiBase.getTopmostVisibleOverlay")
}

func (b *TuiBase) queryCellSize() {
	panic("unported: TuiBase.queryCellSize")
}

func (b *TuiBase) requestImmediateRender() {
	panic("unported: TuiBase.requestImmediateRender")
}

func (b *TuiBase) cancelRenderTimer() {
	panic("unported: TuiBase.cancelRenderTimer")
}

func (b *TuiBase) scheduleRender() {
	panic("unported: TuiBase.scheduleRender")
}

func (b *TuiBase) handleTerminalInput(data string) {
	panic("unported: TuiBase.handleTerminalInput")
}

func (b *TuiBase) consumeTerminalColorResponse(data string) bool {
	panic("unported: TuiBase.consumeTerminalColorResponse")
}

func (b *TuiBase) terminalColorQueryResult(query *tuiPendingTerminalColorQuery) TerminalColors {
	panic("unported: TuiBase.terminalColorQueryResult")
}

func (b *TuiBase) completeTerminalColorQuery(query *tuiPendingTerminalColorQuery) {
	panic("unported: TuiBase.completeTerminalColorQuery")
}

func (b *TuiBase) consumeTerminalColorSchemeReport(data string) bool {
	panic("unported: TuiBase.consumeTerminalColorSchemeReport")
}

func (b *TuiBase) consumeCellSizeResponse(data string) bool {
	panic("unported: TuiBase.consumeCellSizeResponse")
}

func (b *TuiBase) resolveOverlayLayout(options *OverlayOptions, overlayHeight int, termWidth int, termHeight int) tuiResolvedOverlayLayout {
	panic("unported: TuiBase.resolveOverlayLayout")
}

func (b *TuiBase) resolveAnchorRow(anchor OverlayAnchor, height int, availHeight int, marginTop int) int {
	panic("unported: TuiBase.resolveAnchorRow")
}

func (b *TuiBase) resolveAnchorCol(anchor OverlayAnchor, width int, availWidth int, marginLeft int) int {
	panic("unported: TuiBase.resolveAnchorCol")
}

func (b *TuiBase) compositeOverlays(lines []string, termWidth int, termHeight int) []string {
	panic("unported: TuiBase.compositeOverlays")
}

func (b *TuiBase) applyLineResets(lines []string) []string {
	panic("unported: TuiBase.applyLineResets")
}

func (b *TuiBase) compositeLineAt(baseLine string, overlayLine string, startCol int, overlayWidth int, totalWidth int) string {
	return CompositeTuiLine(baseLine, overlayLine, startCol, overlayWidth, totalWidth)
}

func (b *TuiBase) extractCursorPosition(lines []string, height int) *tuiCursorPosition {
	panic("unported: TuiBase.extractCursorPosition")
}

var (
	_ Component   = (*Container)(nil)
	_ TUI         = (*TuiBase)(nil)
	_ tuiOverride = (*TuiBase)(nil)
	_ ViewportTUI = (*TuiAltScreen)(nil)
)

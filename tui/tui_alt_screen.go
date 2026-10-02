// Ported from packages/tui/src/tui-alt-screen.ts (pi v1.0.0).

package tui

import (
	"regexp"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/internal/omap"
)

const (
	tasEnterAltScreen          = "\x1b[?1049h"
	tasExitAltScreen           = "\x1b[?1049l"
	tasDisableAutowrap         = "\x1b[?7l"
	tasEnableAutowrap          = "\x1b[?7h"
	tasEnableButtonMotionMouse = "\x1b[?1000h\x1b[?1002h\x1b[?1004h\x1b[?1006h"
	tasEnableAllMotionMouse    = "\x1b[?1000h\x1b[?1002h\x1b[?1003h\x1b[?1004h\x1b[?1006h"
	tasDisableMouse            = "\x1b[?1006l\x1b[?1004l\x1b[?1003l\x1b[?1002l\x1b[?1000l"
	tasFocusIn                 = "\x1b[I"
	tasFocusOut                = "\x1b[O"
	tasBeginSynchronizedOutput = "\x1b[?2026h"
	tasEndSynchronizedOutput   = "\x1b[?2026l"
)

const (
	tasPageScrollOverlap                        = 4
	tasAltWheelScrollMultiplier                 = 5
	tasMaxCachedOffscreenKittyImages            = 16
	tasMaxCachedOffscreenKittyTransmissionBytes = 32 * 1024 * 1024
	tasMaxCachedOffscreenKittyDecodedBytes      = 64 * 1024 * 1024
	tasDoubleClickIntervalMs               int64 = 500
	tasCopyErrorFlashDurationMs            int64 = 5000
)

var (
	tasOsc133ZonePrefix    = regexp.MustCompile("^(?:\x1b]133;[ABC](?:\x07|\x1b\\\\))+")
	tasOsc133PromptStart   = regexp.MustCompile("^\x1b]133;A(?:\x07|\x1b\\\\)")
	tasSgrMousePattern     = regexp.MustCompile("^\x1b\\[<(\\d+);(\\d+);(\\d+)[Mm]$")
	tasSgrMouseEventPattern = regexp.MustCompile("^\x1b\\[<(\\d+);(\\d+);(\\d+)([Mm])$")
)

// Regular mode delegates double-click selection to the terminal emulator. Fullscreen owns mouse selection,
// so mirror common terminal word-selection behavior by keeping paths and kebab-case tokens whole.
var tasTerminalWordSelectionJoiners = map[string]struct{}{
	"/": {},
	"-": {},
}

type tasCachedKittyImage struct {
	transmissionGeneration int
	transmissionBytes      int
	estimatedDecodedBytes  int
}

// tasSelectionPoint is a content or screen cell. boundary means the point lies between cells.
type tasSelectionPoint struct {
	row        int
	col        int
	scrollView *ScrollView
	boundary   bool
}

type tasSelectionRange struct {
	start tasSelectionPoint
	end   tasSelectionPoint
}

type tasSelectionGranularity string

const (
	tasSelectionGranularityCharacter tasSelectionGranularity = "character"
	tasSelectionGranularityWord      tasSelectionGranularity = "word"
	tasSelectionGranularityLine      tasSelectionGranularity = "line"
)

type tasClickTarget struct {
	timestamp  int64
	count      int
	row        int
	scrollView *ScrollView
	wordStart  int
	wordEnd    int
}

type tasSgrMouseEvent struct {
	button  int
	x       int
	y       int
	release bool
}

type tasWheelEvent struct {
	direction int // -1 or 1
	x         int
	y         int
	button    int
}

type tasScrollbarDrag struct {
	scrollView *ScrollView
	grabOffset int
}

type tasScrollbarTarget struct {
	scrollView *ScrollView
	geometry   ScrollbarGeometry
}

type tasScrollToEndIndicatorRect struct {
	row    int
	column int
	width  int
}

type tasSearchSelectionMode string

const (
	tasSearchSelectionModeQuery    tasSearchSelectionMode = "query"
	tasSearchSelectionModeRetain   tasSearchSelectionMode = "retain"
	tasSearchSelectionModeNext     tasSearchSelectionMode = "next"
	tasSearchSelectionModePrevious tasSearchSelectionMode = "previous"
)

type tasActiveSearch struct {
	component     *AltScreenSearchComponent
	index         *AltScreenSearchIndex
	overlay       OverlayHandle
	query         string
	matches       []AltScreenSearchMatch
	selectedIndex int
	selectedKey   *string
	anchorRow     int
	selectionMode tasSearchSelectionMode
}

type tasSearchHighlightRange struct {
	startCol int
	endCol   int
	current  bool
}

type tasCellPointer struct {
	x int
	y int
}

type tasComponentClick struct {
	timestamp int64
	count     int
	component Component
	x         int
	y         int
}

type tasMouseEventExtra struct {
	wheelDelta *int
	clickCount *int
}

type tasPreparedKittyScreen struct {
	lines                 []string
	evictedImageDeletion string
}

type tasSelectionColumns struct {
	start int
	end   int
}

// TuiAltScreenOptions configures NewTuiAltScreen.
type TuiAltScreenOptions struct {
	// WheelScrollLines is the logical lines moved for each mouse-wheel event.
	// Nil means 1. Auto accelerates fast wheel spins on terminals that send one event per notch.
	// Alt+wheel moves five times as far.
	WheelScrollLines *WheelScrollLines
	// Mouse captures mouse events for viewport scrolling and application-owned text selection.
	// Nil means true.
	Mouse *bool
	// SearchMatchStyle styles a non-current transcript search match.
	// Nil means underline.
	SearchMatchStyle func(text string) string
	// SearchCurrentMatchStyle styles the current transcript search match.
	// Nil means bold inverse.
	SearchCurrentMatchStyle func(text string) string
	// SearchNavigationButtonStyle styles a transcript search navigation button.
	// Nil leaves the text unchanged.
	SearchNavigationButtonStyle func(text string, hovered bool) string
	// ScrollToEndIndicator renders a clickable jump-to-end label.
	// It is centered on the last row of a follow-end primary scroll view while that view is scrolled away from its end.
	ScrollToEndIndicator func() string
	// OpenUrl opens an OSC 8 hyperlink activated with a primary-button click.
	OpenUrl func(url string)
	// OnRightClickPaste handles an unmodified secondary-button press for clipboard paste.
	// Currently enabled on Windows only.
	OnRightClickPaste func()
	// CopyOnSelect automatically copies selected text to the clipboard on mouse release.
	// Nil means true.
	CopyOnSelect *bool
	// CopySelection copies selected text to the system clipboard.
	// The result value is true on success, a string error message, or false for a generic error.
	// A non-nil error is a rejected promise. Nil means the selection is copied via an OSC 52 write.
	CopySelection func(text string) (any, error)
}

// TuiAltScreen is an alternate-screen TUI with a scrollable, application-owned viewport.
// mu guards state touched by the selection timer and by input.
type TuiAltScreen struct {
	mu sync.Mutex
	*TuiBase

	previousScreen             []string
	lastDocument               []string
	previousScreenWidth        int
	previousScreenHeight       int
	layoutRoot                 Component
	currentLayout              *LayoutFrame
	implicitDocument           Component
	implicitScrollView         *ScrollView
	flashes                    *AltScreenFlashContainer
	altScreenActive            bool
	imageProtocol              ImageProtocol // zero value is TS null
	savedCapabilities          *TerminalCapabilities
	uploadedKittyImages        *omap.Map[int, tasCachedKittyImage] // insertion order; delete+set moves an id to the end
	selectionAnchor            *tasSelectionPoint
	selectionFocus             *tasSelectionPoint
	selectionGranularity       tasSelectionGranularity // TS initial value "character"
	selectionInitialRange      *tasSelectionRange
	lastClick                  *tasClickTarget
	selectionDragPointer       *tasCellPointer
	selectionAutoScrollDirection int // -1, 0, or 1
	selectionAutoScrollTimer   *time.Ticker
	selectionPressActive       bool
	scrollbarDrag              *tasScrollbarDrag
	scrollbarHover             *ScrollView
	scrollToEndIndicatorRect   *tasScrollToEndIndicatorRect
	activeSearch               *tasActiveSearch
	pressedUrl                 *string
	selectionDragged           bool
	mouseCapture               *TuiMouseDispatchTarget
	mousePressTarget           *TuiMouseDispatchTarget
	mousePressPoint            *tasCellPointer
	mousePressMoved            bool
	lastComponentClick         *tasComponentClick
	wheelScroll                *WheelScrollAccelerator
	mouseEnabled               bool
	searchMatchStyle           func(text string) string
	searchCurrentMatchStyle    func(text string) string
	searchNavigationButtonStyle func(text string, hovered bool) string
	scrollToEndIndicator       func() string
	openUrl                    func(url string)
	onRightClickPaste          func()
	copyOnSelect               bool
	copySelection              func(text string) (any, error)
}

// NewTuiAltScreen builds an alternate-screen TUI.
// showHardwareCursor nil leaves the TuiBase default. logDirectory nil disables debug logging.
// options nil means the defaults.
func NewTuiAltScreen(terminal Terminal, showHardwareCursor *bool, logDirectory *string, options *TuiAltScreenOptions) *TuiAltScreen {
	panic("unported: NewTuiAltScreen")
}

// Mode is the TUI mode, always fullscreen.
func (t *TuiAltScreen) Mode() TuiMode { return "fullscreen" }

// ViewportTui reports the TS VIEWPORT_TUI brand.
func (t *TuiAltScreen) ViewportTui() bool { return true }

// ViewportTop is the primary scroll view's scroll top.
func (t *TuiAltScreen) ViewportTop() int {
	panic("unported: TuiAltScreen.ViewportTop")
}

// IsFollowingOutput reports whether the primary scroll view is following its end.
func (t *TuiAltScreen) IsFollowingOutput() bool {
	panic("unported: TuiAltScreen.IsFollowingOutput")
}

// SetWheelScrollLines changes the lines moved for each mouse-wheel event.
func (t *TuiAltScreen) SetWheelScrollLines(lines WheelScrollLines) {
	panic("unported: TuiAltScreen.SetWheelScrollLines")
}

// GetCopyOnSelect reports whether a selection is copied on mouse release.
func (t *TuiAltScreen) GetCopyOnSelect() bool { return t.copyOnSelect }

// SetCopyOnSelect enables or disables copying a selection on mouse release.
func (t *TuiAltScreen) SetCopyOnSelect(enabled bool) { t.copyOnSelect = enabled }

// HasActiveSelection reports whether the fullscreen viewport has a non-empty active text selection.
func (t *TuiAltScreen) HasActiveSelection() bool {
	panic("unported: TuiAltScreen.HasActiveSelection")
}

// CopyActiveSelectionToClipboard copies the active fullscreen text selection, if any,
// using the configured selection clipboard path.
// It reports false when there is no selection.
func (t *TuiAltScreen) CopyActiveSelectionToClipboard() (bool, error) {
	panic("unported: TuiAltScreen.CopyActiveSelectionToClipboard")
}

// GetScreenLines returns the lines of the last rendered frame, one per terminal row, as written to the terminal.
func (t *TuiAltScreen) GetScreenLines() []string {
	panic("unported: TuiAltScreen.GetScreenLines")
}

// SetLayoutRoot replaces the component laid out as the viewport root.
// Nil uses the implicit document on the next render.
func (t *TuiAltScreen) SetLayoutRoot(component Component) {
	panic("unported: TuiAltScreen.SetLayoutRoot")
}

// Render renders the layout root, or the TuiBase children when no layout root is set.
func (t *TuiAltScreen) Render(width int) []string {
	panic("unported: TuiAltScreen.Render")
}

// Flash shows a transient message in the alternate-screen flash stack.
// Nil durationMs means 1000.
func (t *TuiAltScreen) Flash(message string, durationMs *int64) {
	panic("unported: TuiAltScreen.Flash")
}

// ScrollBy moves the primary scroll view by lines and requests a render.
func (t *TuiAltScreen) ScrollBy(lines int) {
	panic("unported: TuiAltScreen.ScrollBy")
}

// ScrollToTop scrolls the primary scroll view to its start and requests a render.
func (t *TuiAltScreen) ScrollToTop() {
	panic("unported: TuiAltScreen.ScrollToTop")
}

// ScrollToBottom scrolls the primary scroll view to its end and requests a render.
func (t *TuiAltScreen) ScrollToBottom() {
	panic("unported: TuiAltScreen.ScrollToBottom")
}

func (t *TuiAltScreen) getMountedRoots() []Component {
	panic("unported: TuiAltScreen.getMountedRoots")
}

func (t *TuiAltScreen) getPrimaryScrollView() *ScrollView {
	panic("unported: TuiAltScreen.getPrimaryScrollView")
}

func (t *TuiAltScreen) beforeTerminalStart() {
	panic("unported: TuiAltScreen.beforeTerminalStart")
}

func (t *TuiAltScreen) beforeTerminalStop(options *TuiStopOptions) {
	panic("unported: TuiAltScreen.beforeTerminalStop")
}

func (t *TuiAltScreen) afterTerminalStop(options *TuiStopOptions) {
	panic("unported: TuiAltScreen.afterTerminalStop")
}

func (t *TuiAltScreen) deleteKittyImages() string {
	panic("unported: TuiAltScreen.deleteKittyImages")
}

func (t *TuiAltScreen) prepareKittyScreen(screen []string) tasPreparedKittyScreen {
	panic("unported: TuiAltScreen.prepareKittyScreen")
}

func (t *TuiAltScreen) resetRenderState() {
	panic("unported: TuiAltScreen.resetRenderState")
}

func (t *TuiAltScreen) scrollToPrompt(direction int) {
	panic("unported: TuiAltScreen.scrollToPrompt")
}

func (t *TuiAltScreen) toggleSearch() {
	panic("unported: TuiAltScreen.toggleSearch")
}

func (t *TuiAltScreen) closeSearch() {
	panic("unported: TuiAltScreen.closeSearch")
}

func (t *TuiAltScreen) updateSearchQuery(query string) {
	panic("unported: TuiAltScreen.updateSearchQuery")
}

func (t *TuiAltScreen) navigateSearch(direction int) {
	panic("unported: TuiAltScreen.navigateSearch")
}

func (t *TuiAltScreen) getSearchNavigationDirectionAt(x, y int) *int {
	panic("unported: TuiAltScreen.getSearchNavigationDirectionAt")
}

func (t *TuiAltScreen) handleSearchMouseEvent(event tasSgrMouseEvent) bool {
	panic("unported: TuiAltScreen.handleSearchMouseEvent")
}

func (t *TuiAltScreen) refreshSearch(layout *LayoutFrame) bool {
	panic("unported: TuiAltScreen.refreshSearch")
}

func (t *TuiAltScreen) shouldDeferViewportInputToOverlay() bool {
	panic("unported: TuiAltScreen.shouldDeferViewportInputToOverlay")
}

func (t *TuiAltScreen) clearComponentMouseGesture() {
	panic("unported: TuiAltScreen.clearComponentMouseGesture")
}

func (t *TuiAltScreen) handleViewportInput(data string) *TuiInputListenerResult {
	panic("unported: TuiAltScreen.handleViewportInput")
}

func (t *TuiAltScreen) decodeMouseButton(button int) TuiMouseButton {
	panic("unported: TuiAltScreen.decodeMouseButton")
}

func (t *TuiAltScreen) createMouseEvent(eventType TuiMouseEventType, button, x, y int, extra *tasMouseEventExtra) *TuiMouseEvent {
	panic("unported: TuiAltScreen.createMouseEvent")
}

func (t *TuiAltScreen) dispatchMouseToLayout(event *TuiMouseEvent) *TuiMouseDispatchResult {
	panic("unported: TuiAltScreen.dispatchMouseToLayout")
}

func (t *TuiAltScreen) applyMouseDispatchResult(event *TuiMouseEvent, result *TuiMouseDispatchResult) bool {
	panic("unported: TuiAltScreen.applyMouseDispatchResult")
}

func (t *TuiAltScreen) dispatchMouseToTarget(event *TuiMouseEvent, target *TuiMouseDispatchTarget) *TuiMouseDispatchResult {
	panic("unported: TuiAltScreen.dispatchMouseToTarget")
}

func (t *TuiAltScreen) getComponentClickCount(target *TuiMouseDispatchTarget, x, y int) int {
	panic("unported: TuiAltScreen.getComponentClickCount")
}

func (t *TuiAltScreen) clearTextSelection() {
	panic("unported: TuiAltScreen.clearTextSelection")
}

func (t *TuiAltScreen) handleMouseEvent(raw tasSgrMouseEvent) {
	panic("unported: TuiAltScreen.handleMouseEvent")
}

func (t *TuiAltScreen) parseWheelEvent(data string) *tasWheelEvent {
	panic("unported: TuiAltScreen.parseWheelEvent")
}

func (t *TuiAltScreen) routeWheel(event tasWheelEvent, delta int) {
	panic("unported: TuiAltScreen.routeWheel")
}

func (t *TuiAltScreen) parseSgrMouseEvent(data string) *tasSgrMouseEvent {
	panic("unported: TuiAltScreen.parseSgrMouseEvent")
}

func (t *TuiAltScreen) handleRightClickPaste(event tasSgrMouseEvent) bool {
	panic("unported: TuiAltScreen.handleRightClickPaste")
}

func (t *TuiAltScreen) handleScrollToEndIndicatorMouseEvent(event tasSgrMouseEvent) bool {
	panic("unported: TuiAltScreen.handleScrollToEndIndicatorMouseEvent")
}

func (t *TuiAltScreen) getScrollbarTargetAt(x, y int, includeHiddenAuto bool) *tasScrollbarTarget {
	panic("unported: TuiAltScreen.getScrollbarTargetAt")
}

func (t *TuiAltScreen) setScrollbarHover(scrollView *ScrollView) {
	panic("unported: TuiAltScreen.setScrollbarHover")
}

func (t *TuiAltScreen) updateScrollbarHover(x, y int) {
	panic("unported: TuiAltScreen.updateScrollbarHover")
}

func (t *TuiAltScreen) stopScrollbarHover() {
	panic("unported: TuiAltScreen.stopScrollbarHover")
}

func (t *TuiAltScreen) scrollScrollbarToPointer(scrollView *ScrollView, geometry ScrollbarGeometry, pointerY, grabOffset int) {
	panic("unported: TuiAltScreen.scrollScrollbarToPointer")
}

func (t *TuiAltScreen) handleScrollbarMouseEvent(event tasSgrMouseEvent) bool {
	panic("unported: TuiAltScreen.handleScrollbarMouseEvent")
}

func (t *TuiAltScreen) stopScrollbarDrag() {
	panic("unported: TuiAltScreen.stopScrollbarDrag")
}

func (t *TuiAltScreen) getScrollSelectionPoint(scrollView *ScrollView, x, y int) *tasSelectionPoint {
	panic("unported: TuiAltScreen.getScrollSelectionPoint")
}

func (t *TuiAltScreen) getSelectionPoint(event tasSgrMouseEvent, scrollView *ScrollView) tasSelectionPoint {
	panic("unported: TuiAltScreen.getSelectionPoint")
}

func (t *TuiAltScreen) getSelectionSourceLine(point tasSelectionPoint) string {
	panic("unported: TuiAltScreen.getSelectionSourceLine")
}

func (t *TuiAltScreen) getWordSelection(point tasSelectionPoint) *tasSelectionRange {
	panic("unported: TuiAltScreen.getWordSelection")
}

func (t *TuiAltScreen) getLineSelection(point tasSelectionPoint) tasSelectionRange {
	panic("unported: TuiAltScreen.getLineSelection")
}

func (t *TuiAltScreen) updateSelectionFocus(point tasSelectionPoint) {
	panic("unported: TuiAltScreen.updateSelectionFocus")
}

func (t *TuiAltScreen) getClickCount(point tasSelectionPoint, word *tasSelectionRange) int {
	panic("unported: TuiAltScreen.getClickCount")
}

func (t *TuiAltScreen) updateSelectionAutoScroll(event tasSgrMouseEvent) {
	panic("unported: TuiAltScreen.updateSelectionAutoScroll")
}

func (t *TuiAltScreen) autoScrollSelection() {
	panic("unported: TuiAltScreen.autoScrollSelection")
}

func (t *TuiAltScreen) stopSelectionAutoScroll() {
	panic("unported: TuiAltScreen.stopSelectionAutoScroll")
}

func (t *TuiAltScreen) handleSelectionMouseEvent(event tasSgrMouseEvent) {
	panic("unported: TuiAltScreen.handleSelectionMouseEvent")
}

func (t *TuiAltScreen) getSelectionBounds() *tasSelectionRange {
	panic("unported: TuiAltScreen.getSelectionBounds")
}

func (t *TuiAltScreen) getSelectionColumns(line string, row int, selection tasSelectionRange, minColumn, maxColumn int) tasSelectionColumns {
	panic("unported: TuiAltScreen.getSelectionColumns")
}

func (t *TuiAltScreen) getActiveSelectionText() *string {
	panic("unported: TuiAltScreen.getActiveSelectionText")
}

func (t *TuiAltScreen) copySelectionToClipboard() (bool, error) {
	panic("unported: TuiAltScreen.copySelectionToClipboard")
}

func (t *TuiAltScreen) copyTextToClipboard(text string) (bool, error) {
	panic("unported: TuiAltScreen.copyTextToClipboard")
}

func (t *TuiAltScreen) applySearchTextHighlight(text string, current bool) string {
	panic("unported: TuiAltScreen.applySearchTextHighlight")
}

func (t *TuiAltScreen) applySearchHighlights(screen []string, layout *LayoutFrame) []string {
	panic("unported: TuiAltScreen.applySearchHighlights")
}

func (t *TuiAltScreen) applySelectionHighlight(text string) string {
	panic("unported: TuiAltScreen.applySelectionHighlight")
}

func (t *TuiAltScreen) applySelection(screen []string, layout *LayoutFrame) []string {
	panic("unported: TuiAltScreen.applySelection")
}

func (t *TuiAltScreen) isMouseSequence(data string) bool {
	panic("unported: TuiAltScreen.isMouseSequence")
}

func (t *TuiAltScreen) compositeScrollToEndIndicator(screen []string, layout *LayoutFrame, width int) []string {
	panic("unported: TuiAltScreen.compositeScrollToEndIndicator")
}

func (t *TuiAltScreen) compositeFlashes(screen []string, width, height int) []string {
	panic("unported: TuiAltScreen.compositeFlashes")
}

func (t *TuiAltScreen) doRender() {
	panic("unported: TuiAltScreen.doRender")
}

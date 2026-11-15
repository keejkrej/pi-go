// Ported from packages/tui/src/tui-alt-screen.ts (pi v1.5.2).

package tui

import (
	"encoding/base64"
	"fmt"
	"math"
	"os"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

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
	tasPageScrollOverlap                              = 4
	tasAltWheelScrollMultiplier                       = 5
	tasMaxCachedOffscreenKittyImages                  = 16
	tasMaxCachedOffscreenKittyTransmissionBytes       = 32 * 1024 * 1024
	tasMaxCachedOffscreenKittyDecodedBytes            = 64 * 1024 * 1024
	tasDoubleClickIntervalMs                    int64 = 500
	tasCopyErrorFlashDurationMs                 int64 = 5000
)

var (
	tasOsc133ZonePrefix     = regexp.MustCompile("^(?:\x1b]133;[ABC](?:\x07|\x1b\\\\))+")
	tasOsc133PromptStart    = regexp.MustCompile("^\x1b]133;A(?:\x07|\x1b\\\\)")
	tasSgrMousePattern      = regexp.MustCompile("^\x1b\\[<(\\d+);(\\d+);(\\d+)[Mm]$")
	tasSgrMouseEventPattern = regexp.MustCompile("^\x1b\\[<(\\d+);(\\d+);(\\d+)([Mm])$")
)

// Regular mode delegates double-click selection to the terminal emulator. Fullscreen owns mouse selection,
// so it uses iTerm2's default "characters considered part of a word" set.
var tasTerminalWordSelectionJoiners = map[string]struct{}{
	"/":  {},
	"-":  {},
	"+":  {},
	"\\": {},
	"~":  {},
	"_":  {},
	".":  {},
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
	lines                []string
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

	previousScreen               []string
	lastDocument                 []string
	previousScreenWidth          int
	previousScreenHeight         int
	layoutRoot                   Component
	currentLayout                *LayoutFrame
	implicitDocument             Component
	implicitScrollView           *ScrollView
	flashes                      *AltScreenFlashContainer
	altScreenActive              bool
	imageProtocol                ImageProtocol // zero value is TS null
	savedCapabilities            *TerminalCapabilities
	uploadedKittyImages          *omap.Map[int, tasCachedKittyImage] // insertion order; delete+set moves an id to the end
	selectionAnchor              *tasSelectionPoint
	selectionFocus               *tasSelectionPoint
	selectionGranularity         tasSelectionGranularity // TS initial value "character"
	selectionInitialRange        *tasSelectionRange
	lastClick                    *tasClickTarget
	selectionDragPointer         *tasCellPointer
	selectionAutoScrollDirection int // -1, 0, or 1
	selectionAutoScrollTimer     *time.Ticker
	selectionAutoScrollStop      chan struct{}
	selectionPressActive         bool
	scrollbarDrag                *tasScrollbarDrag
	scrollbarHover               *ScrollView
	scrollToEndIndicatorRect     *tasScrollToEndIndicatorRect
	activeSearch                 *tasActiveSearch
	pressedUrl                   *string
	selectionDragged             bool
	mouseCapture                 *TuiMouseDispatchTarget
	mousePressTarget             *TuiMouseDispatchTarget
	mousePressPoint              *tasCellPointer
	mousePressMoved              bool
	lastComponentClick           *tasComponentClick
	wheelScroll                  *WheelScrollAccelerator
	mouseEnabled                 bool
	searchMatchStyle             func(text string) string
	searchCurrentMatchStyle      func(text string) string
	searchNavigationButtonStyle  func(text string, hovered bool) string
	scrollToEndIndicator         func() string
	openUrl                      func(url string)
	onRightClickPaste            func()
	copyOnSelect                 bool
	copySelection                func(text string) (any, error)
}

// NewTuiAltScreen builds an alternate-screen TUI.
// showHardwareCursor nil leaves the TuiBase default. logDirectory nil disables debug logging.
// options nil means the defaults.
type tasImplicitDocument struct {
	base *TuiBase
}

func (d *tasImplicitDocument) Render(width int) []string {
	return d.base.Render(width)
}

func (d *tasImplicitDocument) HandleMouse(event TuiMouseEvent) *TuiMouseDispatchResult {
	return d.base.HandleMouse(event)
}

func (d *tasImplicitDocument) Invalidate() {
	for _, child := range d.base.Children() {
		child.Invalidate()
	}
}

func tasSameComponent(a, b Component) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	va := reflect.ValueOf(a)
	vb := reflect.ValueOf(b)
	if va.Type() != vb.Type() {
		return false
	}
	if va.Type().Comparable() {
		return a == b
	}
	return false
}

func tasUsesInheritedContainerHandleMouse(comp Component) bool {
	if comp == nil {
		return false
	}
	switch comp.(type) {
	case *Container, *Stack, *VStack, *HStack, *ScrollView, *TuiBase:
		return true
	}
	// General fallback for any custom component embedding Container without overriding HandleMouse.
	if _, ok := comp.(interface {
		HandleMouse(TuiMouseEvent) *TuiMouseDispatchResult
	}); !ok {
		return false
	}
	typ := reflect.TypeOf(comp)
	elem := typ
	if elem.Kind() == reflect.Ptr {
		elem = elem.Elem()
	}
	if elem.Kind() != reflect.Struct {
		return false
	}
	hasEmbeddedContainer := false
	containerType := reflect.TypeOf(Container{})
	containerPtrType := reflect.TypeOf((*Container)(nil))
	for i := 0; i < elem.NumField(); i++ {
		f := elem.Field(i)
		if f.Anonymous && (f.Type == containerType || f.Type == containerPtrType) {
			hasEmbeddedContainer = true
			break
		}
	}
	if !hasEmbeddedContainer {
		return false
	}
	m, ok := typ.MethodByName("HandleMouse")
	if !ok {
		return false
	}
	fn := runtime.FuncForPC(m.Func.Pointer())
	if fn == nil {
		return false
	}
	file, _ := fn.FileLine(fn.Entry())
	return file == "<autogenerated>"
}

func tasScreenRowSame(a, b []string, row int) bool {
	inA := row < len(a)
	inB := row < len(b)
	if !inA && !inB {
		return true
	}
	if inA != inB {
		return false
	}
	return a[row] == b[row]
}

// NewTuiAltScreen builds an alternate-screen TUI.
// showHardwareCursor nil leaves the TuiBase default. logDirectory nil disables debug logging.
// options nil means the defaults.
func NewTuiAltScreen(terminal Terminal, showHardwareCursor *bool, logDirectory *string, options *TuiAltScreenOptions) *TuiAltScreen {
	base := NewTuiBase(terminal, showHardwareCursor, logDirectory)
	implicitDoc := &tasImplicitDocument{base: base}
	followEnd := ScrollViewFollowEnd
	primaryTrue := true
	sv, err := NewScrollView(implicitDoc, &ScrollViewOptions{
		Follow:  &followEnd,
		Primary: &primaryTrue,
	})
	if err != nil {
		panic(err)
	}

	wheelLines := WheelScrollLines{Lines: 1}
	mouseEnabled := true
	searchMatchStyle := func(text string) string { return "\x1b[4m" + text + "\x1b[24m" }
	searchCurrentMatchStyle := func(text string) string { return "\x1b[1;7m" + text + "\x1b[22;27m" }
	searchNavStyle := func(text string, _ bool) string { return text }
	copyOnSelect := true
	var scrollToEndIndicator func() string
	var openUrl func(string)
	var onRightClickPaste func()
	var copySelection func(string) (any, error)

	if options != nil {
		if options.WheelScrollLines != nil {
			wheelLines = *options.WheelScrollLines
		}
		if options.Mouse != nil {
			mouseEnabled = *options.Mouse
		}
		if options.SearchMatchStyle != nil {
			searchMatchStyle = options.SearchMatchStyle
		}
		if options.SearchCurrentMatchStyle != nil {
			searchCurrentMatchStyle = options.SearchCurrentMatchStyle
		}
		if options.SearchNavigationButtonStyle != nil {
			searchNavStyle = options.SearchNavigationButtonStyle
		}
		scrollToEndIndicator = options.ScrollToEndIndicator
		openUrl = options.OpenUrl
		onRightClickPaste = options.OnRightClickPaste
		if options.CopyOnSelect != nil {
			copyOnSelect = *options.CopyOnSelect
		}
		copySelection = options.CopySelection
	}

	t := &TuiAltScreen{
		TuiBase:                     base,
		previousScreen:              []string{},
		lastDocument:                []string{},
		implicitDocument:            implicitDoc,
		implicitScrollView:          sv,
		uploadedKittyImages:         omap.NewMap[int, tasCachedKittyImage](),
		selectionGranularity:        tasSelectionGranularityCharacter,
		wheelScroll:                 NewWheelScrollAccelerator(&wheelLines, nil),
		mouseEnabled:                mouseEnabled,
		searchMatchStyle:            searchMatchStyle,
		searchCurrentMatchStyle:     searchCurrentMatchStyle,
		searchNavigationButtonStyle: searchNavStyle,
		scrollToEndIndicator:        scrollToEndIndicator,
		openUrl:                     openUrl,
		onRightClickPaste:           onRightClickPaste,
		copyOnSelect:                copyOnSelect,
		copySelection:               copySelection,
	}
	t.flashes = NewAltScreenFlashContainer(func() { t.RequestRender(nil) })
	t.setOverride(t)
	t.AddInputListener(func(data string) *TuiInputListenerResult {
		return t.handleViewportInput(data)
	})
	return t
}

// Mode is the TUI mode, always fullscreen.
func (t *TuiAltScreen) Mode() TuiMode { return "fullscreen" }

// ViewportTui reports the TS VIEWPORT_TUI brand.
func (t *TuiAltScreen) ViewportTui() bool { return true }

// ViewportTop is the primary scroll view's scroll top.
func (t *TuiAltScreen) ViewportTop() int {
	return t.getPrimaryScrollView().ScrollTop()
}

// IsFollowingOutput reports whether the primary scroll view is following its end.
func (t *TuiAltScreen) IsFollowingOutput() bool {
	return t.getPrimaryScrollView().IsFollowingEnd()
}

// SetWheelScrollLines changes the lines moved for each mouse-wheel event.
func (t *TuiAltScreen) SetWheelScrollLines(lines WheelScrollLines) {
	t.mu.Lock()
	ws := t.wheelScroll
	t.mu.Unlock()
	if ws != nil {
		ws.SetLines(lines)
	}
}

// GetCopyOnSelect reports whether a selection is copied on mouse release.
func (t *TuiAltScreen) GetCopyOnSelect() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.copyOnSelect
}

// SetCopyOnSelect enables or disables copying a selection on mouse release.
func (t *TuiAltScreen) SetCopyOnSelect(enabled bool) {
	t.mu.Lock()
	t.copyOnSelect = enabled
	t.mu.Unlock()
}

// HasActiveSelection reports whether the fullscreen viewport has a non-empty active text selection.
func (t *TuiAltScreen) HasActiveSelection() bool {
	return t.getActiveSelectionText() != nil
}

// CopyActiveSelectionToClipboard copies the active fullscreen text selection, if any,
// using the configured selection clipboard path.
// It reports false when there is no selection.
func (t *TuiAltScreen) CopyActiveSelectionToClipboard() (bool, error) {
	return t.copySelectionToClipboard()
}

// ResetTextSelection drops the text selection and multi-click history, e.g. before the host replaces the transcript.
func (t *TuiAltScreen) ResetTextSelection() {
	t.clearTextSelection()
	t.mu.Lock()
	t.lastClick = nil
	t.mu.Unlock()
}

// GetScreenLines returns the lines of the last rendered frame, one per terminal row, as written to the terminal.
func (t *TuiAltScreen) GetScreenLines() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]string, len(t.previousScreen))
	copy(out, t.previousScreen)
	return out
}

// SetLayoutRoot replaces the component laid out as the viewport root.
// Nil uses the implicit document on the next render.
func (t *TuiAltScreen) SetLayoutRoot(component Component) {
	t.mu.Lock()
	if tasSameComponent(t.layoutRoot, component) {
		t.mu.Unlock()
		return
	}
	t.layoutRoot = component
	t.currentLayout = nil
	t.mu.Unlock()
	t.RequestRender(nil)
}

// Render renders the layout root, or the TuiBase children when no layout root is set.
func (t *TuiAltScreen) Render(width int) []string {
	t.mu.Lock()
	root := t.layoutRoot
	t.mu.Unlock()
	if root != nil {
		return root.Render(width)
	}
	return t.TuiBase.Render(width)
}

// Flash shows a transient message in the alternate-screen flash stack.
// Nil durationMs means 1000.
func (t *TuiAltScreen) Flash(message string, durationMs *int64) {
	t.flashes.Flash(message, durationMs)
}

// ScrollBy moves the primary scroll view by lines and requests a render.
func (t *TuiAltScreen) ScrollBy(lines int) {
	t.getPrimaryScrollView().ScrollBy(lines)
	t.RequestRender(nil)
}

// ScrollToTop scrolls the primary scroll view to its start and requests a render.
func (t *TuiAltScreen) ScrollToTop() {
	t.getPrimaryScrollView().ScrollToStart()
	t.RequestRender(nil)
}

// ScrollToBottom scrolls the primary scroll view to its end and requests a render.
func (t *TuiAltScreen) ScrollToBottom() {
	t.getPrimaryScrollView().ScrollToEnd()
	t.RequestRender(nil)
}

func (t *TuiAltScreen) getMountedRoots() []Component {
	t.mu.Lock()
	root := t.layoutRoot
	t.mu.Unlock()
	if root != nil {
		return []Component{root}
	}
	return t.TuiBase.Children()
}

func (t *TuiAltScreen) getPrimaryScrollView() *ScrollView {
	t.mu.Lock()
	layout := t.currentLayout
	sv := t.implicitScrollView
	t.mu.Unlock()
	if layout != nil && layout.PrimaryScrollView != nil {
		return layout.PrimaryScrollView
	}
	return sv
}

func (t *TuiAltScreen) beforeTerminalStart() {
	t.stopSelectionAutoScroll()
	t.stopScrollbarHover()
	t.stopScrollbarDrag()
	t.flashes.Dispose()
	t.clearComponentMouseGesture()

	t.mu.Lock()
	t.selectionPressActive = false
	t.altScreenActive = true
	capabilities := GetCapabilities()
	t.imageProtocol = capabilities.Images
	t.uploadedKittyImages.Clear()
	didStripITerm := false
	if capabilities.Images == ImageProtocolIterm2 {
		savedCopy := capabilities
		t.savedCapabilities = &savedCopy
		didStripITerm = true
	}
	t.lastDocument = []string{}
	t.selectionAnchor = nil
	t.selectionFocus = nil
	t.selectionGranularity = tasSelectionGranularityCharacter
	t.selectionInitialRange = nil
	t.lastClick = nil
	t.pressedUrl = nil
	t.selectionDragged = false
	t.lastComponentClick = nil
	mouseEnabled := t.mouseEnabled
	term := t.terminal
	t.mu.Unlock()

	if didStripITerm {
		updated := capabilities
		updated.Images = ""
		SetCapabilities(updated)
		t.Invalidate()
	}
	t.resetRenderState()

	mousePart := ""
	if mouseEnabled {
		mousePart = tasMouseTrackingSequence()
	}
	term.Write(tasEnterAltScreen + tasDisableAutowrap + mousePart + "\x1b[2J\x1b[H\x1b[?25l")
}

func tasMouseTrackingSequence() string {
	termName := strings.ToLower(os.Getenv("TERM"))
	_, hasTmux := os.LookupEnv("TMUX")
	_, hasZellij := os.LookupEnv("ZELLIJ")
	_, hasSty := os.LookupEnv("STY")
	if hasTmux || hasZellij || hasSty || strings.HasPrefix(termName, "tmux") || strings.HasPrefix(termName, "screen") {
		return tasEnableButtonMotionMouse
	}
	return tasEnableAllMotionMouse
}

func (t *TuiAltScreen) beforeTerminalStop(_ *TuiStopOptions) {
	t.closeSearch()
	t.stopSelectionAutoScroll()
	t.stopScrollbarHover()
	t.stopScrollbarDrag()
	t.clearComponentMouseGesture()
	t.flashes.Dispose()

	t.mu.Lock()
	t.selectionPressActive = false
	active := t.altScreenActive
	mouseEnabled := t.mouseEnabled
	imageProto := t.imageProtocol
	term := t.terminal
	t.mu.Unlock()

	if !active {
		return
	}
	delImages := ""
	if imageProto == ImageProtocolKitty {
		delImages = DeleteAllKittyImages()
	}
	mouseOff := ""
	if mouseEnabled {
		mouseOff = tasDisableMouse
	}
	term.Write(tasBeginSynchronizedOutput + delImages + mouseOff + tasEnableAutowrap + tasEndSynchronizedOutput)

	t.mu.Lock()
	t.uploadedKittyImages.Clear()
	t.mu.Unlock()
}

func (t *TuiAltScreen) afterTerminalStop(options *TuiStopOptions) {
	t.mu.Lock()
	if !t.altScreenActive {
		t.mu.Unlock()
		return
	}
	t.altScreenActive = false
	term := t.terminal
	savedCaps := t.savedCapabilities
	t.savedCapabilities = nil
	t.mu.Unlock()

	preserve := options != nil && options.PreserveScreen != nil && *options.PreserveScreen
	if preserve {
		term.Write(tasBeginSynchronizedOutput + tasExitAltScreen + "\x1b[?25h" + tasEndSynchronizedOutput)
	} else {
		width := max(1, term.Columns())
		rendered := t.Render(width)
		stripped := make([]string, len(rendered))
		for i, line := range rendered {
			stripped[i] = tasOsc133ZonePrefix.ReplaceAllString(line, "")
		}
		resolved := t.resolveFakeCursors(stripped)
		noCursorMarkers := make([]string, len(resolved))
		for i, line := range resolved {
			noCursorMarkers[i] = strings.ReplaceAll(line, CursorMarker, "")
		}
		docLines := t.applyLineResets(noCursorMarkers)
		for i, line := range docLines {
			if !IsImageLine(line) && VisibleWidth(line) > width {
				docLines[i] = SliceByColumn(line, 0, width, true)
			}
		}

		t.mu.Lock()
		t.lastDocument = docLines
		t.mu.Unlock()

		var buf strings.Builder
		buf.WriteString(tasBeginSynchronizedOutput + tasExitAltScreen + tasDisableAutowrap)
		for row, line := range docLines {
			if row > 0 {
				buf.WriteString("\r\n")
			}
			buf.WriteString("\r\x1b[2K")
			buf.WriteString(line)
		}
		buf.WriteString("\x1b[0m" + tasEnableAutowrap + "\r\n\x1b[?25h" + tasEndSynchronizedOutput)
		term.Write(buf.String())
	}

	if savedCaps != nil {
		SetCapabilities(*savedCaps)
	}
}

func (t *TuiAltScreen) getExitRestorePrefix() string {
	basePrefix := t.TuiBase.getExitRestorePrefix()
	t.mu.Lock()
	active := t.altScreenActive
	mouseEnabled := t.mouseEnabled
	t.mu.Unlock()
	if !active {
		return basePrefix
	}
	mouseOff := ""
	if mouseEnabled {
		mouseOff = tasDisableMouse
	}
	return tasEndSynchronizedOutput + mouseOff + tasEnableAutowrap + basePrefix
}

func (t *TuiAltScreen) getExitRestoreSuffix() string {
	baseSuffix := t.TuiBase.getExitRestoreSuffix()
	t.mu.Lock()
	active := t.altScreenActive
	t.mu.Unlock()
	if !active {
		return baseSuffix
	}
	// Kitty keyboard flags are per screen, so the terminal pops them before the alternate screen is left.
	return baseSuffix + tasExitAltScreen + "\x1b[?25h"
}

func (t *TuiAltScreen) deleteKittyImages() string {
	t.mu.Lock()
	proto := t.imageProtocol
	t.mu.Unlock()
	if proto == ImageProtocolKitty {
		return DeleteAllKittyImages()
	}
	return ""
}

func (t *TuiAltScreen) prepareKittyScreen(screen []string) tasPreparedKittyScreen {
	t.mu.Lock()
	defer t.mu.Unlock()

	visibleImageIds := make(map[int]struct{})
	lines := make([]string, len(screen))
	for i, line := range screen {
		placement := GetKittyImagePlacement(line)
		if placement == nil {
			lines[i] = line
			continue
		}
		visibleImageIds[placement.ImageId] = struct{}{}

		cachedImage, hasCached := t.uploadedKittyImages.Get(placement.ImageId)
		nextCachedImage := tasCachedKittyImage{
			transmissionGeneration: placement.TransmissionGeneration,
			transmissionBytes:      placement.TransmissionBytes,
			estimatedDecodedBytes:  placement.EstimatedDecodedBytes,
		}
		if hasCached {
			t.uploadedKittyImages.Delete(placement.ImageId)
		}
		t.uploadedKittyImages.Set(placement.ImageId, nextCachedImage)

		if hasCached && cachedImage.transmissionGeneration == placement.TransmissionGeneration {
			lines[i] = placement.ReplacementLine
		} else {
			lines[i] = line
		}
	}

	cachedOffscreenImageCount := 0
	cachedOffscreenTransmissionBytes := 0
	cachedOffscreenDecodedBytes := 0
	for _, cachedImage := range t.uploadedKittyImages.All() {
		_ = cachedImage
	}
	keys := t.uploadedKittyImages.Keys()
	for _, imageId := range keys {
		if _, visible := visibleImageIds[imageId]; visible {
			continue
		}
		cachedImage, ok := t.uploadedKittyImages.Get(imageId)
		if !ok {
			continue
		}
		cachedOffscreenImageCount++
		cachedOffscreenTransmissionBytes += cachedImage.transmissionBytes
		cachedOffscreenDecodedBytes += cachedImage.estimatedDecodedBytes
	}

	var evicted strings.Builder
	for _, imageId := range keys {
		if cachedOffscreenImageCount <= tasMaxCachedOffscreenKittyImages &&
			cachedOffscreenTransmissionBytes <= tasMaxCachedOffscreenKittyTransmissionBytes &&
			cachedOffscreenDecodedBytes <= tasMaxCachedOffscreenKittyDecodedBytes {
			break
		}
		if _, visible := visibleImageIds[imageId]; visible {
			continue
		}
		cachedImage, ok := t.uploadedKittyImages.Get(imageId)
		if !ok {
			continue
		}
		evicted.WriteString(DeleteKittyImage(imageId))
		t.uploadedKittyImages.Delete(imageId)
		cachedOffscreenImageCount--
		cachedOffscreenTransmissionBytes -= cachedImage.transmissionBytes
		cachedOffscreenDecodedBytes -= cachedImage.estimatedDecodedBytes
	}

	return tasPreparedKittyScreen{
		lines:                lines,
		evictedImageDeletion: evicted.String(),
	}
}

func (t *TuiAltScreen) resetRenderState() {
	t.mu.Lock()
	t.previousScreen = []string{}
	t.previousScreenWidth = 0
	t.previousScreenHeight = 0
	t.currentLayout = nil
	t.mu.Unlock()
}

func (t *TuiAltScreen) scrollToPrompt(direction int) {
	t.mu.Lock()
	layout := t.currentLayout
	sv := t.implicitScrollView
	t.mu.Unlock()
	if layout == nil {
		return
	}
	scrollView := layout.PrimaryScrollView
	if scrollView == nil {
		scrollView = sv
	}
	box := GetScrollViewBox(layout, scrollView)
	if box == nil || box.ScrollContentLines == nil {
		return
	}
	lines := box.ScrollContentLines
	for row := scrollView.ScrollTop() + direction; row >= 0 && row < len(lines); row += direction {
		if !tasOsc133PromptStart.MatchString(lines[row]) {
			continue
		}
		scrollView.ScrollTo(row, nil)
		t.RequestRender(nil)
		return
	}
}

func (t *TuiAltScreen) toggleSearch() {
	t.mu.Lock()
	existing := t.activeSearch
	navStyle := t.searchNavigationButtonStyle
	t.mu.Unlock()

	if existing != nil {
		t.closeSearch()
		return
	}

	component := NewAltScreenSearchComponent(
		func(query string) { t.updateSearchQuery(query) },
		navStyle,
	)
	search := &tasActiveSearch{
		component:     component,
		index:         NewAltScreenSearchIndex(),
		query:         "",
		matches:       []AltScreenSearchMatch{},
		selectedIndex: -1,
		anchorRow:     t.getPrimaryScrollView().ScrollTop(),
		selectionMode: tasSearchSelectionModeQuery,
	}

	t.mu.Lock()
	t.activeSearch = search
	t.mu.Unlock()

	anchor := OverlayAnchorTopRight
	minW := 32
	overlay := t.ShowOverlay(component, &OverlayOptions{
		Anchor:   &anchor,
		Width:    &SizeValuePercent{Text: "40%"},
		MinWidth: &minW,
		Margin:   &OverlayMarginNumber{Value: 1},
	})

	t.mu.Lock()
	if t.activeSearch == search {
		search.overlay = overlay
	} else {
		// Search was already closed synchronously during ShowOverlay.
		t.mu.Unlock()
		overlay.Hide()
		return
	}
	t.mu.Unlock()
}

func (t *TuiAltScreen) closeSearch() {
	t.mu.Lock()
	search := t.activeSearch
	t.activeSearch = nil
	t.mu.Unlock()
	if search == nil {
		return
	}
	if search.overlay != nil {
		search.overlay.Hide()
	}
	t.RequestRender(nil)
}

func (t *TuiAltScreen) updateSearchQuery(query string) {
	t.mu.Lock()
	search := t.activeSearch
	if search == nil || query == search.query {
		t.mu.Unlock()
		return
	}
	var anchorRowPtr *int
	if search.selectedIndex >= 0 && search.selectedIndex < len(search.matches) {
		selected := search.matches[search.selectedIndex]
		if len(selected.Segments) > 0 {
			r := selected.Segments[0].Row
			anchorRowPtr = &r
		}
	}
	comp := search.component
	t.mu.Unlock()

	if anchorRowPtr != nil {
		t.mu.Lock()
		if t.activeSearch == search {
			search.anchorRow = *anchorRowPtr
			search.query = query
			search.selectionMode = tasSearchSelectionModeQuery
		}
		t.mu.Unlock()
	} else {
		scrollTop := t.getPrimaryScrollView().ScrollTop()
		t.mu.Lock()
		if t.activeSearch == search {
			search.anchorRow = scrollTop
			search.query = query
			search.selectionMode = tasSearchSelectionModeQuery
		}
		t.mu.Unlock()
	}

	comp.SetResult(-1, 0)
	t.RequestRender(nil)
}

func (t *TuiAltScreen) navigateSearch(direction int) {
	t.mu.Lock()
	search := t.activeSearch
	if search == nil || search.query == "" {
		t.mu.Unlock()
		return
	}
	if direction < 0 {
		search.selectionMode = tasSearchSelectionModePrevious
	} else {
		search.selectionMode = tasSearchSelectionModeNext
	}
	t.mu.Unlock()
	t.RequestRender(nil)
}

func (t *TuiAltScreen) getSearchNavigationDirectionAt(x, y int) *int {
	t.mu.Lock()
	search := t.activeSearch
	var overlay OverlayHandle
	var comp *AltScreenSearchComponent
	if search != nil {
		overlay = search.overlay
		comp = search.component
	}
	t.mu.Unlock()
	if comp == nil || overlay == nil {
		return nil
	}
	bounds := overlay.GetBounds()
	if bounds == nil {
		return nil
	}
	if x < bounds.Col || x >= bounds.Col+bounds.Width || y < bounds.Row || y >= bounds.Row+bounds.Height {
		return nil
	}
	return comp.GetNavigationDirectionAt(y-bounds.Row, x-bounds.Col)
}

func (t *TuiAltScreen) handleSearchMouseEvent(event tasSgrMouseEvent) bool {
	t.mu.Lock()
	search := t.activeSearch
	var comp *AltScreenSearchComponent
	if search != nil {
		comp = search.component
	}
	t.mu.Unlock()
	if comp == nil {
		return false
	}
	direction := t.getSearchNavigationDirectionAt(event.x, event.y)
	if comp.SetHoveredNavigationDirection(direction) {
		t.RequestRender(nil)
	}
	if direction == nil || event.release || (event.button&32) != 0 || (event.button&3) != 0 {
		return false
	}
	t.navigateSearch(*direction)
	return true
}

func (t *TuiAltScreen) refreshSearch(layout *LayoutFrame) bool {
	t.mu.Lock()
	search := t.activeSearch
	if search == nil {
		t.mu.Unlock()
		return false
	}
	query := search.query
	selectionMode := search.selectionMode
	selectedIndex := search.selectedIndex
	selectedKey := search.selectedKey
	anchorRow := search.anchorRow
	comp := search.component
	index := search.index
	sv := t.implicitScrollView
	t.mu.Unlock()

	scrollView := layout.PrimaryScrollView
	if scrollView == nil {
		scrollView = sv
	}
	box := GetScrollViewBox(layout, scrollView)
	var lines []string
	if box != nil {
		lines = box.ScrollContentLines
	}
	if lines == nil || strings.TrimSpace(query) == "" {
		t.mu.Lock()
		if t.activeSearch == search {
			search.matches = []AltScreenSearchMatch{}
			search.selectedIndex = -1
			search.selectedKey = nil
			search.selectionMode = tasSearchSelectionModeRetain
		}
		t.mu.Unlock()
		comp.SetResult(-1, 0)
		return false
	}

	shouldRevealSelection := selectionMode != tasSearchSelectionModeRetain
	result := index.Search(lines, query)
	matches := result.Matches
	if !result.Changed && selectionMode == tasSearchSelectionModeRetain {
		t.mu.Lock()
		if t.activeSearch == search {
			search.matches = matches
		}
		t.mu.Unlock()
		return false
	}

	exactIndex := selectedIndex
	if result.Changed {
		exactIndex = -1
		if selectedKey != nil {
			for i, match := range matches {
				if GetAltScreenSearchMatchKey(match) == *selectedKey {
					exactIndex = i
					break
				}
			}
		}
	}

	nextSelectedIndex := -1
	if len(matches) > 0 {
		switch selectionMode {
		case tasSearchSelectionModeQuery:
			low := 0
			high := len(matches)
			for low < high {
				middle := low + (high-low)/2
				segRow := 0
				if len(matches[middle].Segments) > 0 {
					segRow = matches[middle].Segments[0].Row
				}
				if segRow < anchorRow {
					low = middle + 1
				} else {
					high = middle
				}
			}
			if low < len(matches) {
				nextSelectedIndex = low
			} else {
				nextSelectedIndex = 0
			}
		case tasSearchSelectionModeNext:
			baseIndex := exactIndex
			if baseIndex < 0 {
				baseIndex = min(selectedIndex, len(matches)-1)
			}
			if baseIndex < 0 {
				nextSelectedIndex = 0
			} else {
				nextSelectedIndex = (baseIndex + 1) % len(matches)
			}
		case tasSearchSelectionModePrevious:
			baseIndex := exactIndex
			if baseIndex < 0 {
				baseIndex = min(selectedIndex, len(matches)-1)
			}
			if baseIndex < 0 {
				nextSelectedIndex = len(matches) - 1
			} else {
				nextSelectedIndex = (baseIndex - 1 + len(matches)) % len(matches)
			}
		default:
			if exactIndex >= 0 {
				nextSelectedIndex = exactIndex
			} else {
				nextSelectedIndex = min(max(0, selectedIndex), len(matches)-1)
			}
		}
	}

	var nextSelectedKey *string
	if nextSelectedIndex >= 0 && nextSelectedIndex < len(matches) {
		k := GetAltScreenSearchMatchKey(matches[nextSelectedIndex])
		nextSelectedKey = &k
	}

	t.mu.Lock()
	if t.activeSearch == search {
		search.matches = matches
		search.selectedIndex = nextSelectedIndex
		search.selectedKey = nextSelectedKey
		search.selectionMode = tasSearchSelectionModeRetain
	}
	t.mu.Unlock()

	comp.SetResult(nextSelectedIndex, len(matches))
	if !shouldRevealSelection || nextSelectedIndex < 0 || nextSelectedIndex >= len(matches) {
		return false
	}

	selected := matches[nextSelectedIndex]
	if box == nil || len(selected.Segments) == 0 || scrollView.ViewportHeight() <= 0 {
		return false
	}
	firstSegment := selected.Segments[0]
	lastSegment := selected.Segments[len(selected.Segments)-1]
	before := scrollView.ScrollTop()
	vh := scrollView.ViewportHeight()
	visibleBottom := before + vh - 1
	target := before
	if firstSegment.Row < before || lastSegment.Row > visibleBottom {
		target = firstSegment.Row - vh/3
	}
	disableFollow := true
	scrollView.ScrollTo(target, &ScrollViewScrollToOptions{DisableFollow: &disableFollow})
	return scrollView.ScrollTop() != before
}

// Flash shows a transient message in the alternate-screen flash stack.
// (Comment preserved above; method defined earlier on TuiAltScreen.)

func (t *TuiAltScreen) shouldDeferViewportInputToOverlay() bool {
	if tuiCapturesViewportInput(t.GetFocusedComponent()) {
		return true
	}
	t.mu.Lock()
	search := t.activeSearch
	var searchOverlay OverlayHandle
	if search != nil {
		searchOverlay = search.overlay
	}
	t.mu.Unlock()
	searchFocused := searchOverlay != nil && searchOverlay.IsFocused()
	return t.isOverlayFocused() && !searchFocused
}

func (t *TuiAltScreen) clearComponentMouseGesture() {
	t.mu.Lock()
	t.mouseCapture = nil
	t.mousePressTarget = nil
	t.mousePressPoint = nil
	t.mousePressMoved = false
	t.mu.Unlock()
}

func (t *TuiAltScreen) handleViewportInput(data string) *TuiInputListenerResult {
	consume := true
	consumed := &TuiInputListenerResult{Consume: &consume}

	if data == tasFocusOut {
		t.stopSelectionAutoScroll()
		t.stopScrollbarHover()
		t.stopScrollbarDrag()
		t.clearComponentMouseGesture()

		t.mu.Lock()
		hadActiveSelection := t.selectionPressActive
		t.selectionPressActive = false
		search := t.activeSearch
		var searchComp *AltScreenSearchComponent
		if search != nil {
			searchComp = search.component
		}
		t.mu.Unlock()

		hadNonEmpty := hadActiveSelection && t.getSelectionBounds() != nil
		if searchComp != nil && searchComp.SetHoveredNavigationDirection(nil) {
			t.RequestRender(nil)
		}

		t.mu.Lock()
		t.pressedUrl = nil
		t.selectionDragged = false
		t.lastComponentClick = nil
		if hadActiveSelection {
			t.selectionAnchor = nil
			t.selectionFocus = nil
			t.selectionGranularity = tasSelectionGranularityCharacter
			t.selectionInitialRange = nil
		}
		t.lastClick = nil
		t.mu.Unlock()

		if hadActiveSelection && hadNonEmpty {
			t.RequestRender(nil)
		}
		return consumed
	}
	if data == tasFocusIn {
		return consumed
	}

	if wheelEvent := t.parseWheelEvent(data); wheelEvent != nil {
		nowMs := float64(time.Now().UnixNano()) / 1e6
		t.mu.Lock()
		lines := t.wheelScroll.Next(wheelEvent.direction, nowMs)
		t.mu.Unlock()

		step := lines
		if (wheelEvent.button & 8) != 0 {
			step = lines * tasAltWheelScrollMultiplier
		}
		wheelDelta := wheelEvent.direction * step
		event := t.createMouseEvent(TuiMouseEventTypeWheel, wheelEvent.button, wheelEvent.x, wheelEvent.y, &tasMouseEventExtra{
			wheelDelta: &wheelDelta,
		})
		overlay := t.dispatchMouseToOverlay(*event)
		result := overlay.Result
		if result == nil && !overlay.Hit {
			result = t.dispatchMouseToLayout(event)
		}
		if result != nil {
			if t.applyMouseDispatchResult(event, result) {
				t.RequestRender(nil)
			}
			return consumed
		}
		t.routeWheel(*wheelEvent, wheelDelta)
		return consumed
	}

	if mouseEvent := t.parseSgrMouseEvent(data); mouseEvent != nil {
		t.handleMouseEvent(*mouseEvent)
		return consumed
	}
	if t.isMouseSequence(data) {
		return consumed
	}

	keybindings := GetKeybindings()
	isRelease := IsKeyRelease(data)
	if keybindings.Matches(data, KeybindingTuiAltScreenSearch) {
		if !isRelease {
			t.toggleSearch()
		}
		return consumed
	}

	t.mu.Lock()
	search := t.activeSearch
	var searchOverlay OverlayHandle
	if search != nil {
		searchOverlay = search.overlay
	}
	t.mu.Unlock()

	if searchOverlay != nil && searchOverlay.IsFocused() {
		if keybindings.Matches(data, KeybindingTuiAltScreenSearchNext) {
			if !isRelease {
				t.navigateSearch(1)
			}
			return consumed
		}
		if keybindings.Matches(data, KeybindingTuiAltScreenSearchPrevious) {
			if !isRelease {
				t.navigateSearch(-1)
			}
			return consumed
		}
		if keybindings.Matches(data, KeybindingTuiAltScreenSearchClose) {
			if !isRelease {
				t.closeSearch()
			}
			return consumed
		}
	}

	if t.shouldDeferViewportInputToOverlay() {
		return nil
	}

	if keybindings.Matches(data, KeybindingTuiAltScreenPageUp) {
		if !isRelease {
			t.ScrollBy(-max(1, t.getPrimaryScrollView().ViewportHeight()-tasPageScrollOverlap))
		}
		return consumed
	}
	if keybindings.Matches(data, KeybindingTuiAltScreenPageDown) {
		if !isRelease {
			t.ScrollBy(max(1, t.getPrimaryScrollView().ViewportHeight()-tasPageScrollOverlap))
		}
		return consumed
	}
	if keybindings.Matches(data, KeybindingTuiAltScreenHalfPageUp) {
		if !isRelease {
			t.ScrollBy(-max(1, t.getPrimaryScrollView().ViewportHeight()/2))
		}
		return consumed
	}
	if keybindings.Matches(data, KeybindingTuiAltScreenHalfPageDown) {
		if !isRelease {
			t.ScrollBy(max(1, t.getPrimaryScrollView().ViewportHeight()/2))
		}
		return consumed
	}
	if keybindings.Matches(data, KeybindingTuiAltScreenLineUp) {
		if !isRelease {
			t.ScrollBy(-1)
		}
		return consumed
	}
	if keybindings.Matches(data, KeybindingTuiAltScreenLineDown) {
		if !isRelease {
			t.ScrollBy(1)
		}
		return consumed
	}
	if keybindings.Matches(data, KeybindingTuiAltScreenPreviousPrompt) {
		if !isRelease {
			t.scrollToPrompt(-1)
		}
		return consumed
	}
	if keybindings.Matches(data, KeybindingTuiAltScreenNextPrompt) {
		if !isRelease {
			t.scrollToPrompt(1)
		}
		return consumed
	}
	if keybindings.Matches(data, KeybindingTuiAltScreenTop) {
		if !isRelease {
			t.ScrollToTop()
		}
		return consumed
	}
	if keybindings.Matches(data, KeybindingTuiAltScreenBottom) {
		if !isRelease {
			t.ScrollToBottom()
		}
		return consumed
	}
	return nil
}

func (t *TuiAltScreen) decodeMouseButton(button int) TuiMouseButton {
	switch button & 3 {
	case 0:
		return TuiMouseButtonLeft
	case 1:
		return TuiMouseButtonMiddle
	case 2:
		return TuiMouseButtonRight
	default:
		return TuiMouseButtonNone
	}
}

func (t *TuiAltScreen) createMouseEvent(eventType TuiMouseEventType, button, x, y int, extra *tasMouseEventExtra) *TuiMouseEvent {
	btn := TuiMouseButtonNone
	if eventType != TuiMouseEventTypeWheel {
		btn = t.decodeMouseButton(button)
	}
	ev := &TuiMouseEvent{
		Type:    eventType,
		Button:  btn,
		X:       x,
		Y:       y,
		ScreenX: x,
		ScreenY: y,
		Width:   max(1, t.terminal.Columns()),
		Height:  max(1, t.terminal.Rows()),
		Shift:   (button & 4) != 0,
		Alt:     (button & 8) != 0,
		Ctrl:    (button & 16) != 0,
	}
	if extra != nil {
		ev.WheelDelta = extra.wheelDelta
		ev.ClickCount = extra.clickCount
	}
	return ev
}

func (t *TuiAltScreen) dispatchMouseToLayout(event *TuiMouseEvent) *TuiMouseDispatchResult {
	t.mu.Lock()
	layout := t.currentLayout
	t.mu.Unlock()
	if layout == nil {
		return nil
	}
	var visited []Component
	boxes := GetLayoutBoxesAt(layout, event.ScreenX, event.ScreenY)
	for _, box := range boxes {
		alreadySeen := false
		for _, prev := range visited {
			if tasSameComponent(prev, box.Component) {
				alreadySeen = true
				break
			}
		}
		if alreadySeen {
			continue
		}
		if GetLayoutNode(box.Component) != nil && tasUsesInheritedContainerHandleMouse(box.Component) {
			continue
		}
		visited = append(visited, box.Component)
		localEvent := *event
		localEvent.X = event.ScreenX - box.Rect.X
		localEvent.Y = event.ScreenY - box.Rect.Y
		localEvent.Width = box.Rect.Width
		localEvent.Height = box.Rect.Height
		if result := DispatchMouseEvent(box.Component, localEvent); result != nil {
			return result
		}
	}
	return nil
}

func (t *TuiAltScreen) applyMouseDispatchResult(event *TuiMouseEvent, result *TuiMouseDispatchResult) bool {
	targetComp := result.Target.Component
	if result.FocusTarget != nil {
		targetComp = result.FocusTarget
	}
	focusTarget := t.resolveMouseFocusTarget(targetComp)
	focusChanged := tuiBoolTrue(result.Focus) && !tasSameComponent(t.GetFocusedComponent(), focusTarget)
	if tuiBoolTrue(result.Focus) {
		t.SetFocus(focusTarget)
	}
	if tuiBoolTrue(result.Capture) {
		t.mu.Lock()
		targetCopy := result.Target
		t.mouseCapture = &targetCopy
		t.mu.Unlock()
	}
	if result.Render != nil {
		return *result.Render
	}
	return focusChanged ||
		event.Type == TuiMouseEventTypePress ||
		event.Type == TuiMouseEventTypeClick ||
		event.Type == TuiMouseEventTypeDrag ||
		event.Type == TuiMouseEventTypeWheel
}

func (t *TuiAltScreen) dispatchMouseToTarget(event *TuiMouseEvent, target *TuiMouseDispatchTarget) *TuiMouseDispatchResult {
	return DispatchMouseEvent(target.Component, RetargetMouseEvent(*event, *target))
}

func (t *TuiAltScreen) getComponentClickCount(target *TuiMouseDispatchTarget, x, y int) int {
	now := time.Now().UnixMilli()
	t.mu.Lock()
	defer t.mu.Unlock()
	prev := t.lastComponentClick
	count := 1
	if prev != nil &&
		now-prev.timestamp <= tasDoubleClickIntervalMs &&
		tasSameComponent(prev.component, target.Component) &&
		prev.x == x &&
		prev.y == y {
		count = (prev.count % 3) + 1
	}
	t.lastComponentClick = &tasComponentClick{
		timestamp: now,
		count:     count,
		component: target.Component,
		x:         x,
		y:         y,
	}
	return count
}

func (t *TuiAltScreen) clearTextSelection() {
	t.stopSelectionAutoScroll()
	t.mu.Lock()
	t.selectionPressActive = false
	t.selectionAnchor = nil
	t.selectionFocus = nil
	t.selectionGranularity = tasSelectionGranularityCharacter
	t.selectionInitialRange = nil
	t.pressedUrl = nil
	t.selectionDragged = false
	t.mu.Unlock()
}

func (t *TuiAltScreen) handleMouseEvent(raw tasSgrMouseEvent) {
	isMotion := (raw.button & 32) != 0
	var evType TuiMouseEventType
	switch {
	case raw.release:
		evType = TuiMouseEventTypeRelease
	case isMotion:
		if t.decodeMouseButton(raw.button) == TuiMouseButtonNone {
			evType = TuiMouseEventTypeMove
		} else {
			evType = TuiMouseEventTypeDrag
		}
	default:
		evType = TuiMouseEventTypePress
	}
	event := t.createMouseEvent(evType, raw.button, raw.x, raw.y, nil)

	t.mu.Lock()
	target := t.mouseCapture
	if target == nil {
		target = t.mousePressTarget
	}
	var pressPoint *tasCellPointer
	if t.mousePressPoint != nil {
		p := *t.mousePressPoint
		pressPoint = &p
	}
	if target != nil && pressPoint != nil && (raw.x != pressPoint.x || raw.y != pressPoint.y) {
		t.mousePressMoved = true
		t.lastComponentClick = nil
	}
	pressMoved := t.mousePressMoved
	t.mu.Unlock()

	if target != nil {
		render := false
		if targetResult := t.dispatchMouseToTarget(event, target); targetResult != nil {
			render = t.applyMouseDispatchResult(event, targetResult)
		}
		if raw.release {
			if !pressMoved && pressPoint != nil && pressPoint.x == raw.x && pressPoint.y == raw.y {
				clickCount := t.getComponentClickCount(target, raw.x, raw.y)
				clickEvent := t.createMouseEvent(TuiMouseEventTypeClick, raw.button, raw.x, raw.y, &tasMouseEventExtra{
					clickCount: &clickCount,
				})
				if clickResult := t.dispatchMouseToTarget(clickEvent, target); clickResult != nil {
					if t.applyMouseDispatchResult(clickEvent, clickResult) {
						render = true
					}
				}
			}
			t.clearComponentMouseGesture()
		}
		if render {
			t.RequestRender(nil)
		}
		return
	}

	if t.handleSearchMouseEvent(raw) {
		return
	}

	overlay := t.dispatchMouseToOverlay(*event)
	if !overlay.Hit {
		if t.handleScrollToEndIndicatorMouseEvent(raw) {
			return
		}
		scrollbarHandled := t.handleScrollbarMouseEvent(raw)
		t.mu.Lock()
		draggingScrollbar := t.scrollbarDrag != nil
		t.mu.Unlock()
		if !draggingScrollbar {
			t.updateScrollbarHover(raw.x, raw.y)
		}
		if scrollbarHandled {
			return
		}
	} else {
		t.stopScrollbarHover()
	}

	result := overlay.Result
	if result == nil && !overlay.Hit {
		result = t.dispatchMouseToLayout(event)
	}
	if result != nil {
		render := t.applyMouseDispatchResult(event, result)
		if evType == TuiMouseEventTypePress {
			t.clearTextSelection()
			t.mu.Lock()
			targetCopy := result.Target
			t.mousePressTarget = &targetCopy
			t.mousePressPoint = &tasCellPointer{x: raw.x, y: raw.y}
			t.mousePressMoved = false
			t.mu.Unlock()
		}
		if render {
			t.RequestRender(nil)
		}
		return
	}

	if t.handleRightClickPaste(raw) {
		return
	}
	t.handleSelectionMouseEvent(raw)
}

func (t *TuiAltScreen) parseWheelEvent(data string) *tasWheelEvent {
	if sgr := tasSgrMousePattern.FindStringSubmatch(data); sgr != nil {
		button, err1 := strconv.Atoi(sgr[1])
		x1, err2 := strconv.Atoi(sgr[2])
		y1, err3 := strconv.Atoi(sgr[3])
		if err1 != nil || err2 != nil || err3 != nil || (button&64) == 0 {
			return nil
		}
		dirBits := button & 3
		if dirBits != 0 && dirBits != 1 {
			return nil
		}
		dir := 1
		if dirBits == 0 {
			dir = -1
		}
		return &tasWheelEvent{
			direction: dir,
			x:         x1 - 1,
			y:         y1 - 1,
			button:    button,
		}
	}
	if strings.HasPrefix(data, "\x1b[M") {
		runes := []rune(data)
		if len(runes) == 6 {
			button := int(runes[3]) - 32
			if (button & 64) == 0 {
				return nil
			}
			dirBits := button & 3
			if dirBits != 0 && dirBits != 1 {
				return nil
			}
			dir := 1
			if dirBits == 0 {
				dir = -1
			}
			return &tasWheelEvent{
				direction: dir,
				x:         int(runes[4]) - 33,
				y:         int(runes[5]) - 33,
				button:    button,
			}
		}
	}
	return nil
}

func (t *TuiAltScreen) routeWheel(event tasWheelEvent, delta int) {
	t.mu.Lock()
	layout := t.currentLayout
	t.mu.Unlock()

	remaining := delta
	seen := make(map[*ScrollView]struct{})
	if layout != nil {
		for _, sv := range GetScrollViewsAt(layout, event.x, event.y) {
			seen[sv] = struct{}{}
			remaining = sv.ScrollBy(remaining)
			if remaining == 0 || sv.Overscroll() == ScrollLayoutOverscrollContain {
				break
			}
		}
	}
	primary := t.getPrimaryScrollView()
	if remaining != 0 {
		if _, ok := seen[primary]; !ok {
			primary.ScrollBy(remaining)
		}
	}
	t.updateScrollbarHover(event.x, event.y)
	t.RequestRender(nil)
}

func (t *TuiAltScreen) parseSgrMouseEvent(data string) *tasSgrMouseEvent {
	match := tasSgrMouseEventPattern.FindStringSubmatch(data)
	if match == nil {
		return nil
	}
	button, err1 := strconv.Atoi(match[1])
	x1, err2 := strconv.Atoi(match[2])
	y1, err3 := strconv.Atoi(match[3])
	if err1 != nil || err2 != nil || err3 != nil {
		return nil
	}
	return &tasSgrMouseEvent{
		button:  button,
		x:       x1 - 1,
		y:       y1 - 1,
		release: match[4] == "m",
	}
}

func (t *TuiAltScreen) handleRightClickPaste(event tasSgrMouseEvent) bool {
	t.mu.Lock()
	cb := t.onRightClickPaste
	t.mu.Unlock()
	if cb == nil ||
		runtime.GOOS != "windows" ||
		strings.ToLower(os.Getenv("TERM_PROGRAM")) == "vscode" ||
		event.release ||
		event.button != 2 {
		return false
	}
	cb()
	return true
}

func (t *TuiAltScreen) handleScrollToEndIndicatorMouseEvent(event tasSgrMouseEvent) bool {
	t.mu.Lock()
	rect := t.scrollToEndIndicatorRect
	t.mu.Unlock()
	if rect == nil || event.release || (event.button&32) != 0 || (event.button&3) != 0 {
		return false
	}
	if event.y != rect.row || event.x < rect.column || event.x >= rect.column+rect.width {
		return false
	}
	t.ScrollToBottom()
	return true
}

func (t *TuiAltScreen) getScrollbarTargetAt(x, y int, includeHiddenAuto bool) *tasScrollbarTarget {
	if t.HasOverlay() {
		return nil
	}
	t.mu.Lock()
	layout := t.currentLayout
	t.mu.Unlock()
	if layout == nil {
		return nil
	}
	for _, sv := range GetScrollViewsAt(layout, x, y) {
		box := GetScrollViewBox(layout, sv)
		if box == nil {
			continue
		}
		geometry := GetScrollbarGeometry(box, includeHiddenAuto)
		if geometry != nil &&
			x == geometry.Column &&
			y >= geometry.TrackTop &&
			y < geometry.TrackTop+geometry.TrackHeight {
			return &tasScrollbarTarget{
				scrollView: sv,
				geometry:   *geometry,
			}
		}
	}
	return nil
}

func (t *TuiAltScreen) setScrollbarHover(scrollView *ScrollView) {
	t.mu.Lock()
	prev := t.scrollbarHover
	if prev == scrollView {
		t.mu.Unlock()
		return
	}
	t.scrollbarHover = scrollView
	t.mu.Unlock()

	if prev != nil {
		prev.SetScrollbarActive(false)
	}
	if scrollView != nil {
		scrollView.SetScrollbarActive(true)
	}
}

func (t *TuiAltScreen) updateScrollbarHover(x, y int) {
	target := t.getScrollbarTargetAt(x, y, true)
	if target != nil {
		t.setScrollbarHover(target.scrollView)
	} else {
		t.setScrollbarHover(nil)
	}
}

func (t *TuiAltScreen) stopScrollbarHover() {
	t.setScrollbarHover(nil)
}

func (t *TuiAltScreen) scrollScrollbarToPointer(scrollView *ScrollView, geometry ScrollbarGeometry, pointerY, grabOffset int) {
	maxThumbOffset := geometry.TrackHeight - geometry.ThumbHeight
	thumbOffset := max(0, min(maxThumbOffset, pointerY-geometry.TrackTop-grabOffset))
	scrollTop := 0
	if maxThumbOffset != 0 {
		scrollTop = int(math.Round((float64(thumbOffset) / float64(maxThumbOffset)) * float64(geometry.MaxScrollTop)))
	}
	scrollView.ScrollTo(scrollTop, nil)
}

func (t *TuiAltScreen) handleScrollbarMouseEvent(event tasSgrMouseEvent) bool {
	t.mu.Lock()
	drag := t.scrollbarDrag
	layout := t.currentLayout
	t.mu.Unlock()

	if drag != nil {
		if event.release {
			t.stopScrollbarDrag()
			return true
		}
		if layout != nil {
			if box := GetScrollViewBox(layout, drag.scrollView); box != nil {
				if geometry := GetScrollbarGeometry(box, false); geometry != nil {
					t.scrollScrollbarToPointer(drag.scrollView, *geometry, event.y, drag.grabOffset)
				}
			}
		}
		return true
	}

	if event.release || (event.button&32) != 0 || (event.button&3) != 0 {
		return false
	}
	target := t.getScrollbarTargetAt(event.x, event.y, false)
	if target == nil {
		return false
	}

	t.stopSelectionAutoScroll()
	t.mu.Lock()
	t.selectionPressActive = false
	t.selectionAnchor = nil
	t.selectionFocus = nil
	t.selectionGranularity = tasSelectionGranularityCharacter
	t.selectionInitialRange = nil
	t.lastClick = nil
	t.pressedUrl = nil
	t.selectionDragged = false
	t.mu.Unlock()

	t.setScrollbarHover(target.scrollView)
	onThumb := event.y >= target.geometry.ThumbTop && event.y < target.geometry.ThumbTop+target.geometry.ThumbHeight
	grabOffset := target.geometry.ThumbHeight / 2
	if onThumb {
		grabOffset = event.y - target.geometry.ThumbTop
	} else {
		t.scrollScrollbarToPointer(target.scrollView, target.geometry, event.y, grabOffset)
	}

	t.mu.Lock()
	t.scrollbarDrag = &tasScrollbarDrag{
		scrollView: target.scrollView,
		grabOffset: grabOffset,
	}
	t.mu.Unlock()
	return true
}

func (t *TuiAltScreen) stopScrollbarDrag() {
	t.mu.Lock()
	t.scrollbarDrag = nil
	t.mu.Unlock()
}

func (t *TuiAltScreen) getScrollSelectionPoint(scrollView *ScrollView, x, y int) *tasSelectionPoint {
	t.mu.Lock()
	layout := t.currentLayout
	t.mu.Unlock()
	if layout == nil {
		return nil
	}
	box := GetScrollViewBox(layout, scrollView)
	if box == nil || box.Rect.Height <= 0 || box.Clip.Height <= 0 {
		return nil
	}
	visibleTop := max(0, box.Rect.Y, box.Clip.Y)
	visibleBottom := min(t.terminal.Rows()-1, box.Rect.Y+box.Rect.Height-1, box.Clip.Y+box.Clip.Height-1)
	if visibleBottom < visibleTop {
		return nil
	}
	pointerRow := max(visibleTop, min(visibleBottom, y))
	contentLen := max(1, len(box.ScrollContentLines))
	maxContentRow := contentLen - 1
	return &tasSelectionPoint{
		row:        max(0, min(maxContentRow, scrollView.ScrollTop()+pointerRow-box.Rect.Y)),
		col:        max(0, min(box.Rect.Width-1, x-box.Rect.X)),
		scrollView: scrollView,
	}
}

func (t *TuiAltScreen) getSelectionPoint(event tasSgrMouseEvent, scrollView *ScrollView) tasSelectionPoint {
	if scrollView != nil {
		if point := t.getScrollSelectionPoint(scrollView, event.x, event.y); point != nil {
			return *point
		}
	}
	return tasSelectionPoint{
		row: max(0, min(t.terminal.Rows()-1, event.y)),
		col: max(0, min(t.terminal.Columns()-1, event.x)),
	}
}

func (t *TuiAltScreen) getSelectionSourceLine(point tasSelectionPoint) string {
	t.mu.Lock()
	layout := t.currentLayout
	prevScreen := t.previousScreen
	t.mu.Unlock()

	if point.scrollView != nil && layout != nil {
		if box := GetScrollViewBox(layout, point.scrollView); box != nil && box.ScrollContentLines != nil {
			if point.row >= 0 && point.row < len(box.ScrollContentLines) {
				return box.ScrollContentLines[point.row]
			}
			return ""
		}
	}
	if point.row >= 0 && point.row < len(prevScreen) {
		return prevScreen[point.row]
	}
	return ""
}

func (t *TuiAltScreen) getWordSelection(point tasSelectionPoint) *tasSelectionRange {
	line := StripTerminalSequences(t.getSelectionSourceLine(point))
	type wordSegment struct {
		start      int
		end        int
		selectable bool
		joiner     bool
	}
	var segments []wordSegment
	start := 0
	for _, seg := range GetWordSegmenter().Segment(line) {
		end := start + VisibleWidth(seg.Segment)
		_, joiner := tasTerminalWordSelectionJoiners[seg.Segment]
		selectable := (seg.IsWordLike != nil && *seg.IsWordLike) || joiner
		segments = append(segments, wordSegment{
			start:      start,
			end:        end,
			selectable: selectable,
			joiner:     joiner,
		})
		start = end
	}

	clickedSegmentIndex := -1
	for i, seg := range segments {
		if point.col >= seg.start && point.col < seg.end {
			clickedSegmentIndex = i
			break
		}
	}
	if clickedSegmentIndex < 0 {
		return nil
	}

	canJoin := func(left, right wordSegment) bool {
		return left.selectable && right.selectable && (left.joiner || right.joiner)
	}
	selectionStart := segments[clickedSegmentIndex].start
	selectionEnd := segments[clickedSegmentIndex].end
	for index := clickedSegmentIndex; index > 0 && canJoin(segments[index-1], segments[index]); index-- {
		selectionStart = segments[index-1].start
	}
	for index := clickedSegmentIndex; index < len(segments)-1 && canJoin(segments[index], segments[index+1]); index++ {
		selectionEnd = segments[index+1].end
	}
	startPt := point
	startPt.col = selectionStart
	endPt := point
	endPt.col = selectionEnd
	endPt.boundary = true
	return &tasSelectionRange{
		start: startPt,
		end:   endPt,
	}
}

func (t *TuiAltScreen) getLineSelection(point tasSelectionPoint) tasSelectionRange {
	startPt := point
	startPt.col = 0
	endPt := point
	endPt.col = VisibleWidth(t.getSelectionSourceLine(point))
	endPt.boundary = true
	return tasSelectionRange{
		start: startPt,
		end:   endPt,
	}
}

func (t *TuiAltScreen) updateSelectionFocus(point tasSelectionPoint) {
	t.mu.Lock()
	granularity := t.selectionGranularity
	initialPtr := t.selectionInitialRange
	t.mu.Unlock()

	if granularity == tasSelectionGranularityCharacter || initialPtr == nil {
		t.mu.Lock()
		pCopy := point
		t.selectionFocus = &pCopy
		t.mu.Unlock()
		return
	}

	var r *tasSelectionRange
	if granularity == tasSelectionGranularityWord {
		r = t.getWordSelection(point)
	} else {
		lr := t.getLineSelection(point)
		r = &lr
	}
	if r == nil {
		return
	}

	initial := *initialPtr
	targetBeforeInitial := r.start.row < initial.start.row ||
		(r.start.row == initial.start.row && r.start.col < initial.start.col)

	t.mu.Lock()
	if targetBeforeInitial {
		a := initial.end
		f := r.start
		t.selectionAnchor = &a
		t.selectionFocus = &f
	} else {
		a := initial.start
		f := r.end
		t.selectionAnchor = &a
		t.selectionFocus = &f
	}
	t.mu.Unlock()
}

func (t *TuiAltScreen) getClickCount(point tasSelectionPoint, word *tasSelectionRange) int {
	now := time.Now().UnixMilli()
	t.mu.Lock()
	defer t.mu.Unlock()

	previous := t.lastClick
	count := 1
	if word != nil &&
		previous != nil &&
		now-previous.timestamp <= tasDoubleClickIntervalMs &&
		previous.row == point.row &&
		previous.scrollView == point.scrollView &&
		previous.wordStart == word.start.col &&
		previous.wordEnd == word.end.col {
		count = (previous.count % 3) + 1
	}
	if word != nil {
		t.lastClick = &tasClickTarget{
			timestamp:  now,
			count:      count,
			row:        point.row,
			scrollView: point.scrollView,
			wordStart:  word.start.col,
			wordEnd:    word.end.col,
		}
	} else {
		t.lastClick = nil
	}
	return count
}

func (t *TuiAltScreen) updateSelectionAutoScroll(event tasSgrMouseEvent) {
	t.mu.Lock()
	var scrollView *ScrollView
	if t.selectionAnchor != nil {
		scrollView = t.selectionAnchor.scrollView
	}
	layout := t.currentLayout
	t.mu.Unlock()

	if scrollView == nil || layout == nil {
		t.stopSelectionAutoScroll()
		return
	}
	box := GetScrollViewBox(layout, scrollView)
	if box == nil || box.Rect.Height <= 0 || box.Clip.Height <= 0 {
		t.stopSelectionAutoScroll()
		return
	}
	visibleTop := max(0, box.Rect.Y, box.Clip.Y)
	visibleBottom := min(t.terminal.Rows()-1, box.Rect.Y+box.Rect.Height-1, box.Clip.Y+box.Clip.Height-1)
	dir := 0
	if event.y <= visibleTop {
		dir = -1
	} else if event.y >= visibleBottom {
		dir = 1
	}

	t.mu.Lock()
	t.selectionDragPointer = &tasCellPointer{x: event.x, y: event.y}
	t.selectionAutoScrollDirection = dir
	alreadyRunning := t.selectionAutoScrollTimer != nil
	t.mu.Unlock()

	if dir == 0 {
		t.stopSelectionAutoScroll()
		return
	}
	if alreadyRunning {
		return
	}

	ticker := time.NewTicker(50 * time.Millisecond)
	stopCh := make(chan struct{})
	t.mu.Lock()
	if t.selectionAutoScrollTimer != nil || t.selectionAutoScrollDirection == 0 {
		t.mu.Unlock()
		ticker.Stop()
		close(stopCh)
		return
	}
	t.selectionAutoScrollTimer = ticker
	t.selectionAutoScrollStop = stopCh
	t.mu.Unlock()

	go func(tkr *time.Ticker, ch <-chan struct{}) {
		for {
			select {
			case <-ch:
				return
			case <-tkr.C:
				t.Post(func() { t.autoScrollSelection() })
			}
		}
	}(ticker, stopCh)
}

func (t *TuiAltScreen) autoScrollSelection() {
	t.mu.Lock()
	var scrollView *ScrollView
	if t.selectionAnchor != nil {
		scrollView = t.selectionAnchor.scrollView
	}
	pointer := t.selectionDragPointer
	direction := t.selectionAutoScrollDirection
	t.mu.Unlock()

	if scrollView == nil || pointer == nil || direction == 0 {
		t.stopSelectionAutoScroll()
		return
	}
	remaining := scrollView.ScrollBy(direction)
	if remaining == direction {
		t.stopSelectionAutoScroll()
		return
	}
	if point := t.getScrollSelectionPoint(scrollView, pointer.x, pointer.y); point != nil {
		t.updateSelectionFocus(*point)
	}
	t.RequestRender(nil)
}

func (t *TuiAltScreen) stopSelectionAutoScroll() {
	t.mu.Lock()
	ticker := t.selectionAutoScrollTimer
	stopCh := t.selectionAutoScrollStop
	t.selectionAutoScrollTimer = nil
	t.selectionAutoScrollStop = nil
	t.selectionAutoScrollDirection = 0
	t.selectionDragPointer = nil
	t.mu.Unlock()

	if ticker != nil {
		ticker.Stop()
	}
	if stopCh != nil {
		close(stopCh)
	}
}

func (t *TuiAltScreen) handleSelectionMouseEvent(event tasSgrMouseEvent) {
	button := event.button & 3
	if button != 0 && !(event.release && button == 3) {
		return
	}

	t.mu.Lock()
	var anchorScrollView *ScrollView
	if t.selectionAnchor != nil {
		anchorScrollView = t.selectionAnchor.scrollView
	}
	t.mu.Unlock()

	point := t.getSelectionPoint(event, anchorScrollView)
	if event.release {
		t.stopSelectionAutoScroll()

		t.mu.Lock()
		if !t.selectionPressActive {
			t.mu.Unlock()
			return
		}
		t.selectionPressActive = false
		anchorPtr := t.selectionAnchor
		t.mu.Unlock()

		if anchorPtr == nil {
			return
		}
		t.updateSelectionFocus(point)

		t.mu.Lock()
		anchor := *t.selectionAnchor
		isClick := !t.selectionDragged &&
			anchor.scrollView == point.scrollView &&
			anchor.row == point.row &&
			anchor.col == point.col
		var clickedUrl *string
		if isClick {
			clickedUrl = t.pressedUrl
		}
		t.pressedUrl = nil
		openUrlCb := t.openUrl
		var lastClickCount *int
		if t.lastClick != nil {
			c := t.lastClick.count
			lastClickCount = &c
		}
		copyOnSelect := t.copyOnSelect
		t.mu.Unlock()

		if clickedUrl != nil && openUrlCb != nil {
			t.mu.Lock()
			t.selectionAnchor = nil
			t.selectionFocus = nil
			t.mu.Unlock()
			openUrlCb(*clickedUrl)
			t.RequestRender(nil)
			return
		}
		if isClick {
			count := 1
			if lastClickCount != nil {
				count = *lastClickCount
			}
			clickEvent := t.createMouseEvent(TuiMouseEventTypeClick, event.button, event.x, event.y, &tasMouseEventExtra{
				clickCount: &count,
			})
			overlay := t.dispatchMouseToOverlay(*clickEvent)
			result := overlay.Result
			if result == nil && !overlay.Hit {
				result = t.dispatchMouseToLayout(clickEvent)
			}
			if result != nil {
				render := t.applyMouseDispatchResult(clickEvent, result)
				t.clearTextSelection()
				if render {
					t.RequestRender(nil)
				}
				return
			}
		}
		if copyOnSelect {
			_, _ = t.copySelectionToClipboard()
		}
		t.RequestRender(nil)
		return
	}

	if (event.button & 32) != 0 {
		t.mu.Lock()
		active := t.selectionPressActive && t.selectionAnchor != nil
		if active {
			t.selectionDragged = true
			t.lastClick = nil
			t.pressedUrl = nil
		}
		t.mu.Unlock()
		if !active {
			return
		}
		t.updateSelectionFocus(point)
		t.updateSelectionAutoScroll(event)
		t.RequestRender(nil)
		return
	}

	t.stopSelectionAutoScroll()
	t.mu.Lock()
	t.selectionPressActive = true
	layout := t.currentLayout
	t.mu.Unlock()

	var scrollView *ScrollView
	if !t.HasOverlay() && layout != nil {
		scrollViews := GetScrollViewsAt(layout, event.x, event.y)
		if len(scrollViews) > 0 {
			scrollView = scrollViews[0]
		}
	}
	anchor := t.getSelectionPoint(event, scrollView)
	word := t.getWordSelection(anchor)
	clickCount := t.getClickCount(anchor, word)

	var selRange *tasSelectionRange
	granularity := tasSelectionGranularityCharacter
	switch clickCount {
	case 2:
		selRange = word
		if selRange != nil {
			granularity = tasSelectionGranularityWord
		}
	case 3:
		lr := t.getLineSelection(anchor)
		selRange = &lr
		granularity = tasSelectionGranularityLine
	}

	anchorPoint := anchor
	focusPoint := anchor
	if selRange != nil {
		anchorPoint = selRange.start
		focusPoint = selRange.end
	}

	var pressedUrl *string
	if selRange == nil {
		t.mu.Lock()
		prevScreen := t.previousScreen
		t.mu.Unlock()
		rowIdx := max(0, min(t.terminal.Rows()-1, event.y))
		colIdx := max(0, min(t.terminal.Columns()-1, event.x))
		rowLine := ""
		if rowIdx < len(prevScreen) {
			rowLine = prevScreen[rowIdx]
		}
		pressedUrl = GetOsc8LinkAtColumn(rowLine, colIdx)
	}

	t.mu.Lock()
	t.selectionGranularity = granularity
	t.selectionInitialRange = selRange
	t.selectionAnchor = &anchorPoint
	t.selectionFocus = &focusPoint
	t.selectionDragged = false
	t.pressedUrl = pressedUrl
	t.mu.Unlock()

	t.RequestRender(nil)
}

func (t *TuiAltScreen) getSelectionBounds() *tasSelectionRange {
	t.mu.Lock()
	anchor := t.selectionAnchor
	focus := t.selectionFocus
	t.mu.Unlock()

	if anchor == nil || focus == nil {
		return nil
	}
	if anchor.scrollView != focus.scrollView {
		return nil
	}
	if anchor.row == focus.row && anchor.col == focus.col {
		return nil
	}
	anchorBeforeFocus := anchor.row < focus.row || (anchor.row == focus.row && anchor.col < focus.col)
	if anchorBeforeFocus {
		return &tasSelectionRange{start: *anchor, end: *focus}
	}
	return &tasSelectionRange{start: *focus, end: *anchor}
}

func (t *TuiAltScreen) getSelectionColumns(line string, row int, selection tasSelectionRange, minColumn, maxColumn int) tasSelectionColumns {
	lineWidth := VisibleWidth(line)
	start := max(0, minColumn)
	end := min(lineWidth, maxColumn)
	if row == selection.start.row {
		if gr := GetGraphemeCellRange(line, selection.start.col); gr != nil {
			start = gr.Start
		} else {
			start = min(selection.start.col, lineWidth)
		}
	}
	if row == selection.end.row {
		if selection.end.boundary {
			end = min(selection.end.col, lineWidth)
		} else if gr := GetGraphemeCellRange(line, selection.end.col); gr != nil {
			end = gr.End
		} else {
			end = min(selection.end.col+1, lineWidth)
		}
	}
	return tasSelectionColumns{
		start: max(minColumn, start),
		end:   min(maxColumn, end),
	}
}

func (t *TuiAltScreen) getActiveSelectionText() *string {
	selection := t.getSelectionBounds()
	if selection == nil {
		return nil
	}

	t.mu.Lock()
	sourceLines := t.previousScreen
	layout := t.currentLayout
	t.mu.Unlock()

	if selection.start.scrollView != nil {
		if layout == nil {
			return nil
		}
		box := GetScrollViewBox(layout, selection.start.scrollView)
		if box == nil || box.ScrollContentLines == nil {
			return nil
		}
		sourceLines = box.ScrollContentLines
	}

	var lines []string
	for row := selection.start.row; row <= selection.end.row; row++ {
		line := ""
		if row >= 0 && row < len(sourceLines) {
			line = sourceLines[row]
		}
		columns := t.getSelectionColumns(line, row, *selection, 0, VisibleWidth(line))
		sliced := SliceByColumn(line, columns.start, max(0, columns.end-columns.start), true)
		lines = append(lines, strings.TrimRight(StripTerminalSequences(sliced), " \t\n\r\v\f\u00a0"))
	}
	text := strings.Join(lines, "\n")
	if text == "" {
		return nil
	}
	return &text
}

func (t *TuiAltScreen) copySelectionToClipboard() (bool, error) {
	text := t.getActiveSelectionText()
	if text == nil || *text == "" {
		return false, nil
	}
	return t.copyTextToClipboard(*text)
}

func (t *TuiAltScreen) copyTextToClipboard(text string) (bool, error) {
	t.mu.Lock()
	copySelection := t.copySelection
	term := t.terminal
	t.mu.Unlock()

	if copySelection != nil {
		res, err := copySelection(text)
		if err != nil {
			return false, err
		}
		ok := false
		if b, isBool := res.(bool); isBool {
			ok = b
		}
		if ok {
			t.Flash("Copied!", nil)
			return true, nil
		}
		msg := "Copy failed"
		if s, isStr := res.(string); isStr {
			msg = s
		}
		dur := tasCopyErrorFlashDurationMs
		t.Flash(msg, &dur)
		return false, nil
	}

	encoded := base64.StdEncoding.EncodeToString([]byte(text))
	term.Write("\x1b]52;c;" + encoded + "\x07")
	t.Flash("Copied!", nil)
	return true, nil
}

func (t *TuiAltScreen) applySearchTextHighlight(text string, current bool) string {
	t.mu.Lock()
	style := t.searchMatchStyle
	if current {
		style = t.searchCurrentMatchStyle
	}
	t.mu.Unlock()

	var result strings.Builder
	plainStart := 0
	index := 0
	for index < len(text) {
		ansi := ExtractAnsiCode(text, index)
		if ansi == nil {
			_, size := utf8.DecodeRuneInString(text[index:])
			index += size
			continue
		}
		if index > plainStart {
			result.WriteString(style(text[plainStart:index]))
		}
		result.WriteString(ansi.Code)
		index += ansi.Length
		plainStart = index
	}
	if plainStart < len(text) {
		result.WriteString(style(text[plainStart:]))
	}
	return result.String()
}

func (t *TuiAltScreen) applySearchHighlights(screen []string, layout *LayoutFrame) []string {
	t.mu.Lock()
	search := t.activeSearch
	var matches []AltScreenSearchMatch
	selectedIndex := -1
	if search != nil {
		selectedIndex = search.selectedIndex
		matches = slices.Clone(search.matches)
	}
	sv := t.implicitScrollView
	t.mu.Unlock()

	if search == nil || selectedIndex < 0 || len(matches) == 0 {
		return screen
	}
	scrollView := layout.PrimaryScrollView
	if scrollView == nil {
		scrollView = sv
	}
	box := GetScrollViewBox(layout, scrollView)
	if box == nil {
		return screen
	}

	rangesByRow := make(map[int][]tasSearchHighlightRange)
	scrollbarGeom := GetScrollbarGeometry(box, false)
	minRow := max(0, box.Rect.Y, box.Clip.Y)
	maxRow := min(len(screen), box.Rect.Y+box.Rect.Height, box.Clip.Y+box.Clip.Height)
	minColumn := max(0, box.Rect.X, box.Clip.X)
	maxColumn := min(t.terminal.Columns(), box.Rect.X+box.Rect.Width, box.Clip.X+box.Clip.Width)
	if scrollbarGeom != nil {
		maxColumn = min(maxColumn, scrollbarGeom.Column)
	}
	scrollTop := scrollView.ScrollTop()
	minContentRow := scrollTop + minRow - box.Rect.Y
	maxContentRow := scrollTop + maxRow - box.Rect.Y - 1

	low := 0
	high := len(matches)
	for low < high {
		middle := low + (high-low)/2
		m := matches[middle]
		lastRow := -1
		if len(m.Segments) > 0 {
			lastRow = m.Segments[len(m.Segments)-1].Row
		}
		if lastRow < minContentRow {
			low = middle + 1
		} else {
			high = middle
		}
	}

	for matchIndex := low; matchIndex < len(matches); matchIndex++ {
		m := matches[matchIndex]
		firstRow := 0
		if len(m.Segments) > 0 {
			firstRow = m.Segments[0].Row
		}
		if firstRow > maxContentRow {
			break
		}
		for _, segment := range m.Segments {
			row := box.Rect.Y + segment.Row - scrollTop
			if row < minRow || row >= maxRow {
				continue
			}
			startCol := max(minColumn, box.Rect.X+segment.StartCol)
			endCol := min(maxColumn, box.Rect.X+segment.EndCol)
			if endCol <= startCol {
				continue
			}
			rangesByRow[row] = append(rangesByRow[row], tasSearchHighlightRange{
				startCol: startCol,
				endCol:   endCol,
				current:  matchIndex == selectedIndex,
			})
		}
	}

	result := slices.Clone(screen)
	for row, ranges := range rangesByRow {
		line := result[row]
		if IsImageLine(line) {
			continue
		}
		lineWidth := VisibleWidth(line)
		slices.SortStableFunc(ranges, func(a, b tasSearchHighlightRange) int {
			return b.startCol - a.startCol
		})
		for _, r := range ranges {
			startCol := min(r.startCol, lineWidth)
			endCol := min(r.endCol, lineWidth)
			if endCol <= startCol {
				continue
			}
			before := SliceByColumn(line, 0, startCol, true)
			highlighted := SliceByColumn(line, startCol, endCol-startCol, true)
			after := SliceByColumn(line, endCol, max(0, lineWidth-endCol), true)
			line = before + t.applySearchTextHighlight(highlighted, r.current) + after
		}
		result[row] = line
	}
	return result
}

func (t *TuiAltScreen) applySelectionHighlight(text string) string {
	var result strings.Builder
	result.WriteString("\x1b[7m")
	index := 0
	for index < len(text) {
		ansi := ExtractAnsiCode(text, index)
		if ansi == nil {
			_, size := utf8.DecodeRuneInString(text[index:])
			result.WriteString(text[index : index+size])
			index += size
			continue
		}
		result.WriteString(ansi.Code)
		if strings.HasSuffix(ansi.Code, "m") {
			result.WriteString("\x1b[7m")
		}
		index += ansi.Length
	}
	result.WriteString("\x1b[27m")
	return result.String()
}

func (t *TuiAltScreen) applySelection(screen []string, layout *LayoutFrame) []string {
	selection := t.getSelectionBounds()
	if selection == nil {
		return screen
	}

	if layout == nil {
		t.mu.Lock()
		layout = t.currentLayout
		t.mu.Unlock()
	}

	screenSelection := *selection
	minRow := 0
	maxRow := len(screen) - 1
	minColumn := 0
	maxColumn := t.terminal.Columns()

	if selection.start.scrollView != nil {
		if layout == nil {
			return screen
		}
		box := GetScrollViewBox(layout, selection.start.scrollView)
		if box == nil {
			return screen
		}
		minRow = max(0, box.Rect.Y, box.Clip.Y)
		maxRow = min(len(screen)-1, box.Rect.Y+box.Rect.Height-1, box.Clip.Y+box.Clip.Height-1)
		minColumn = max(0, box.Rect.X, box.Clip.X)
		maxColumn = min(t.terminal.Columns(), box.Rect.X+box.Rect.Width, box.Clip.X+box.Clip.Width)
		scrollTop := selection.start.scrollView.ScrollTop()
		screenSelection.start.row = box.Rect.Y + selection.start.row - scrollTop
		screenSelection.start.col = box.Rect.X + selection.start.col
		screenSelection.end.row = box.Rect.Y + selection.end.row - scrollTop
		screenSelection.end.col = box.Rect.X + selection.end.col
	}

	out := make([]string, len(screen))
	for row, line := range screen {
		if row < minRow ||
			row > maxRow ||
			row < screenSelection.start.row ||
			row > screenSelection.end.row ||
			IsImageLine(line) {
			out[row] = line
			continue
		}
		lineWidth := VisibleWidth(line)
		columns := t.getSelectionColumns(line, row, screenSelection, minColumn, maxColumn)
		if columns.end <= columns.start {
			out[row] = line
			continue
		}
		before := SliceByColumn(line, 0, columns.start, true)
		selected := SliceByColumn(line, columns.start, columns.end-columns.start, true)
		after := SliceByColumn(line, columns.end, max(0, lineWidth-columns.end), true)
		out[row] = before + t.applySelectionHighlight(selected) + after
	}
	return out
}

func (t *TuiAltScreen) isMouseSequence(data string) bool {
	if tasSgrMouseEventPattern.MatchString(data) {
		return true
	}
	return strings.HasPrefix(data, "\x1b[M") && utf8.RuneCountInString(data) == 6
}

func (t *TuiAltScreen) compositeScrollToEndIndicator(screen []string, layout *LayoutFrame, width int) []string {
	t.mu.Lock()
	t.scrollToEndIndicatorRect = nil
	indicatorFn := t.scrollToEndIndicator
	sv := t.implicitScrollView
	t.mu.Unlock()

	scrollView := layout.PrimaryScrollView
	if scrollView == nil {
		scrollView = sv
	}
	if indicatorFn == nil || !scrollView.FollowEnd() || scrollView.IsFollowingEnd() {
		return screen
	}
	box := GetScrollViewBox(layout, scrollView)
	if box == nil {
		return screen
	}
	clip := box.Clip
	if clip.Width <= 0 || clip.Height <= 0 {
		return screen
	}
	row := clip.Y + clip.Height - 1
	if row < 0 || row >= len(screen) || IsImageLine(screen[row]) {
		return screen
	}
	scrollbarGeom := GetScrollbarGeometry(box, false)
	label := TruncateToWidth(indicatorFn(), clip.Width, "", false)
	labelWidth := VisibleWidth(label)
	column := clip.X + (clip.Width-labelWidth)/2
	rightEdge := clip.X + clip.Width
	if scrollbarGeom != nil {
		rightEdge = scrollbarGeom.Column
	}
	availableWidth := max(0, rightEdge-column)
	text := TruncateToWidth(label, availableWidth, "", false)
	textWidth := VisibleWidth(text)
	if textWidth == 0 {
		return screen
	}
	result := slices.Clone(screen)
	result[row] = CompositeTuiLine(result[row], text, column, textWidth, width)

	t.mu.Lock()
	t.scrollToEndIndicatorRect = &tasScrollToEndIndicatorRect{
		row:    row,
		column: column,
		width:  textWidth,
	}
	t.mu.Unlock()
	return result
}

func (t *TuiAltScreen) compositeFlashes(screen []string, width, height int) []string {
	flashLines := t.flashes.Render(width)
	if len(flashLines) > height {
		flashLines = flashLines[len(flashLines)-height:]
	}
	if len(flashLines) == 0 {
		return screen
	}
	result := slices.Clone(screen)
	for len(result) < height {
		result = append(result, "")
	}
	for row, line := range flashLines {
		flashWidth := VisibleWidth(line)
		if flashWidth == 0 {
			continue
		}
		result[row] = CompositeTuiLine(result[row], line, width-flashWidth, flashWidth, width)
	}
	return result
}

func (t *TuiAltScreen) doRender() {
	t.TuiBase.mu.Lock()
	stopped := t.TuiBase.stopped
	t.TuiBase.mu.Unlock()

	t.mu.Lock()
	active := t.altScreenActive
	layoutRoot := t.layoutRoot
	sv := t.implicitScrollView
	imageProto := t.imageProtocol
	prevScreen := t.previousScreen
	prevWidth := t.previousScreenWidth
	prevHeight := t.previousScreenHeight
	hadUploadedKitty := t.uploadedKittyImages.Len() > 0
	term := t.terminal
	t.mu.Unlock()

	if stopped || !active {
		return
	}

	width := max(1, term.Columns())
	height := max(1, term.Rows())
	var root Component = sv
	if layoutRoot != nil {
		root = layoutRoot
	}
	reqRender := func() { t.RequestRender(nil) }
	nextLayout := RenderLayoutFrame(root, width, height, reqRender)
	if t.refreshSearch(nextLayout) {
		nextLayout = RenderLayoutFrame(root, width, height, reqRender)
	}

	// Resolve fake cursors before highlighting and compositing, which only track SGR codes.
	stripped := make([]string, len(nextLayout.Lines))
	for i, line := range nextLayout.Lines {
		stripped[i] = tasOsc133ZonePrefix.ReplaceAllString(line, "")
	}
	screen := t.resolveFakeCursors(stripped)
	screen = t.applySearchHighlights(screen, nextLayout)
	screen = t.compositeScrollToEndIndicator(screen, nextLayout, width)
	screen = t.compositeOverlays(screen, width, height)
	if len(screen) > height {
		screen = screen[len(screen)-height:]
	}
	screen = t.applySelection(screen, nextLayout)
	screen = t.compositeFlashes(screen, width, height)

	cursorPos := t.extractCursorPosition(screen, height)
	screen = t.applyLineResets(screen)
	for i, line := range screen {
		if !IsImageLine(line) && VisibleWidth(line) > width {
			screen[i] = SliceByColumn(line, 0, width, true)
		}
	}

	fullRedraw := len(prevScreen) == 0 || prevWidth != width || prevHeight != height
	changedRows := make([]bool, len(screen))
	anyChanged := false
	for row, line := range screen {
		changed := row >= len(prevScreen) || line != prevScreen[row]
		changedRows[row] = changed
		if changed {
			anyChanged = true
		}
	}
	imageAnchorsNeedRedraw := false
	for row, line := range screen {
		if !changedRows[row] {
			continue
		}
		prevLine := ""
		if row < len(prevScreen) {
			prevLine = prevScreen[row]
		}
		if IsImageLine(line) || IsImageLine(prevLine) {
			imageAnchorsNeedRedraw = true
			break
		}
	}
	imageCellsNeedRedraw := false
	if !imageAnchorsNeedRedraw && imageProto == ImageProtocolKitty && anyChanged {
		for row, line := range screen {
			placementRows := GetKittyImagePlacementRows(line)
			if placementRows == nil {
				continue
			}
			for coveredRow := row; coveredRow < row+*placementRows; coveredRow++ {
				if coveredRow < len(changedRows) && changedRows[coveredRow] {
					imageCellsNeedRedraw = true
					break
				}
			}
			if imageCellsNeedRedraw {
				break
			}
		}
	}
	imagesNeedRedraw := imageAnchorsNeedRedraw || imageCellsNeedRedraw
	redrawImages := fullRedraw || imagesNeedRedraw

	prepared := tasPreparedKittyScreen{lines: screen}
	if redrawImages && imageProto == ImageProtocolKitty {
		prepared = t.prepareKittyScreen(screen)
	}

	var buf strings.Builder
	buf.WriteString(tasBeginSynchronizedOutput)
	if fullRedraw {
		t.TuiBase.mu.Lock()
		t.TuiBase.fullRedrawCount++
		t.TuiBase.mu.Unlock()
		if imageProto == ImageProtocolKitty && hadUploadedKitty {
			buf.WriteString(DeleteAllKittyPlacements())
		} else {
			if imageProto == ImageProtocolKitty {
				buf.WriteString(DeleteAllKittyImages())
			}
			buf.WriteString("\x1b[2J")
		}
	} else if imagesNeedRedraw {
		switch imageProto {
		case ImageProtocolIterm2:
			buf.WriteString("\x1b[2J")
		case ImageProtocolKitty:
			buf.WriteString(DeleteAllKittyPlacements())
		}
	}
	buf.WriteString(prepared.evictedImageDeletion)

	hasImageLine := false
	for _, line := range screen {
		if IsImageLine(line) {
			hasImageLine = true
			break
		}
	}
	drawKittyImagesLast := redrawImages && imageProto == ImageProtocolKitty && hasImageLine
	rowLine := func(lines []string, row int) string {
		if row < len(lines) {
			return lines[row]
		}
		return ""
	}
	if drawKittyImagesLast {
		for row := 0; row < height; row++ {
			if !fullRedraw && !imagesNeedRedraw && tasScreenRowSame(screen, prevScreen, row) {
				continue
			}
			fmt.Fprintf(&buf, "\x1b[%d;1H\x1b[2K", row+1)
		}
		for row := 0; row < height; row++ {
			if !fullRedraw && !imagesNeedRedraw && tasScreenRowSame(screen, prevScreen, row) {
				continue
			}
			line := rowLine(prepared.lines, row)
			if IsImageLine(line) {
				continue
			}
			fmt.Fprintf(&buf, "\x1b[%d;1H%s", row+1, line)
		}
		for row := 0; row < height; row++ {
			if !fullRedraw && !imagesNeedRedraw && tasScreenRowSame(screen, prevScreen, row) {
				continue
			}
			line := rowLine(prepared.lines, row)
			if !IsImageLine(line) {
				continue
			}
			fmt.Fprintf(&buf, "\x1b[%d;1H%s", row+1, line)
		}
	} else {
		for row := 0; row < height; row++ {
			if !fullRedraw && !imagesNeedRedraw && tasScreenRowSame(screen, prevScreen, row) {
				continue
			}
			fmt.Fprintf(&buf, "\x1b[%d;1H\x1b[2K%s", row+1, rowLine(prepared.lines, row))
		}
	}

	if cursorPos != nil {
		fmt.Fprintf(&buf, "\x1b[%d;%dH", cursorPos.Row+1, min(width, cursorPos.Col)+1)
		if t.GetShowHardwareCursor() {
			buf.WriteString("\x1b[?25h")
		} else {
			buf.WriteString("\x1b[?25l")
		}
	} else {
		buf.WriteString("\x1b[?25l")
	}
	buf.WriteString(tasEndSynchronizedOutput)
	term.Write(buf.String())

	t.mu.Lock()
	t.previousScreen = screen
	t.previousScreenWidth = width
	t.previousScreenHeight = height
	t.currentLayout = nextLayout
	t.mu.Unlock()
}

var (
	_ ViewportTUI = (*TuiAltScreen)(nil)
	_ tuiOverride = (*TuiAltScreen)(nil)
)

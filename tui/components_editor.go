// Ported from packages/tui/src/components/editor.ts (pi v1.0.0 + v1.5.2 capturesViewportInput, fake cursor).

package tui

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsre"
	"github.com/keejkrej/pi-go/internal/omap"
)

// cePasteMarkerRegex matches paste markers such as "[paste #1 +123 lines]" or "[paste #2 1234 chars]".
var cePasteMarkerRegex = jsre.MustCompile(`\[paste #(\d+)( (\+\d+ lines|\d+ chars))?\]`, "g")

// cePasteMarkerSingle is the non-global form, for testing one segment.
var cePasteMarkerSingle = jsre.MustCompile(`^\[paste #(\d+)( (\+\d+ lines|\d+ chars))?\]$`, "")

var (
	ceCsiUCtrlRegex          = jsre.MustCompile(`\x1b\[(\d+);5u`, "g")
	ceWordCharTriggerRegex   = jsre.MustCompile(`[a-zA-Z0-9.\-_]`, "")
	ceAsciiWordCharRegex     = jsre.MustCompile(`\w`, "")
	ceNormalizer             = strings.NewReplacer("\r\n", "\n", "\r", "\n", "\t", "    ")
	ceEscapeCharClassMatcher = strings.NewReplacer(
		`\`, `\\`,
		`^`, `\^`,
		`$`, `\$`,
		`.`, `\.`,
		`*`, `\*`,
		`+`, `\+`,
		`?`, `\?`,
		`(`, `\(`,
		`)`, `\)`,
		`[`, `\[`,
		`]`, `\]`,
		`{`, `\{`,
		`}`, `\}`,
		`|`, `\|`,
		`-`, `\-`,
	)
)

// ceSegmenter is the Intl.Segmenter surface editor.ts uses (segment → segments).
type ceSegmenter interface {
	Segment(text string) []SegmentData
}

// TS const graphemeSegmenter = getGraphemeSegmenter(). Nil so package init does not call an unported function.
var ceGraphemeSegmenter ceSegmenter

// TS const wordSegmenter = getWordSegmenter(). Nil so package init does not call an unported function.
var ceWordSegmenter ceSegmenter

var ceSegmenterMu sync.Mutex

func ceGetGraphemeSegmenter() ceSegmenter {
	ceSegmenterMu.Lock()
	defer ceSegmenterMu.Unlock()
	if ceGraphemeSegmenter == nil {
		ceGraphemeSegmenter = GetGraphemeSegmenter()
	}
	return ceGraphemeSegmenter
}

func ceGetWordSegmenter() ceSegmenter {
	ceSegmenterMu.Lock()
	defer ceSegmenterMu.Unlock()
	if ceWordSegmenter == nil {
		ceWordSegmenter = GetWordSegmenter()
	}
	return ceWordSegmenter
}

func ceIsPasteMarker(segment string) bool {
	return js.Len(segment) >= 10 && cePasteMarkerSingle.Test(segment)
}

func ceSegmentWithMarkers(text string, base ceSegmenter, validIds *omap.Set[int]) []SegmentData {
	if validIds == nil || validIds.Len() == 0 || !strings.Contains(text, "[paste #") {
		return base.Segment(text)
	}

	type span struct {
		start int
		end   int
	}
	var markers []span
	for _, m := range cePasteMarkerRegex.MatchAll(text) {
		id, err := strconv.Atoi(m.Group(1))
		if err != nil || !validIds.Has(id) {
			continue
		}
		markers = append(markers, span{start: m.Index, end: m.End()})
	}
	if len(markers) == 0 {
		return base.Segment(text)
	}

	baseSegments := base.Segment(text)
	result := make([]SegmentData, 0, len(baseSegments))
	markerIdx := 0

	for _, seg := range baseSegments {
		for markerIdx < len(markers) && markers[markerIdx].end <= seg.Index {
			markerIdx++
		}

		var marker *span
		if markerIdx < len(markers) {
			marker = &markers[markerIdx]
		}

		if marker != nil && seg.Index >= marker.start && seg.Index < marker.end {
			if seg.Index == marker.start {
				result = append(result, SegmentData{
					Segment: js.Slice(text, marker.start, marker.end),
					Index:   marker.start,
					Input:   text,
				})
			}
		} else {
			result = append(result, seg)
		}
	}

	return result
}

// TextChunk is one word-wrapped piece of a logical line.
// StartIndex and EndIndex are indexes into that line (TS string indexes).
type TextChunk struct {
	Text       string `json:"text"`
	StartIndex int    `json:"startIndex"`
	EndIndex   int    `json:"endIndex"`
}

// WordWrapLine splits line into word-wrapped chunks of at most maxWidth visible columns.
// It breaks at word boundaries when it can, otherwise between graphemes.
// preSegmented is optional paste-marker-aware grapheme segments; nil uses the default grapheme segmenter.
// An empty non-nil slice is not the default (TS nullish only).
func WordWrapLine(line string, maxWidth int, preSegmented []SegmentData) []TextChunk {
	if line == "" || maxWidth <= 0 {
		return []TextChunk{{Text: "", StartIndex: 0, EndIndex: 0}}
	}

	if VisibleWidth(line) <= maxWidth {
		return []TextChunk{{Text: line, StartIndex: 0, EndIndex: js.Len(line)}}
	}

	chunks := []TextChunk{}
	segments := preSegmented
	if segments == nil {
		segments = ceGetGraphemeSegmenter().Segment(line)
	}

	currentWidth := 0
	chunkStart := 0
	wrapOppIndex := -1
	wrapOppWidth := 0

	for i := 0; i < len(segments); i++ {
		seg := segments[i]
		grapheme := seg.Segment
		gWidth := VisibleWidth(grapheme)
		charIndex := seg.Index
		isWs := !ceIsPasteMarker(grapheme) && IsWhitespaceChar(grapheme)

		if currentWidth+gWidth > maxWidth {
			if wrapOppIndex >= 0 && currentWidth-wrapOppWidth+gWidth <= maxWidth {
				chunks = append(chunks, TextChunk{
					Text:       js.Slice(line, chunkStart, wrapOppIndex),
					StartIndex: chunkStart,
					EndIndex:   wrapOppIndex,
				})
				chunkStart = wrapOppIndex
				currentWidth -= wrapOppWidth
			} else if chunkStart < charIndex {
				chunks = append(chunks, TextChunk{
					Text:       js.Slice(line, chunkStart, charIndex),
					StartIndex: chunkStart,
					EndIndex:   charIndex,
				})
				chunkStart = charIndex
				currentWidth = 0
			}
			wrapOppIndex = -1
		}

		if gWidth > maxWidth {
			subChunks := WordWrapLine(grapheme, maxWidth, nil)
			for j := 0; j < len(subChunks)-1; j++ {
				sc := subChunks[j]
				chunks = append(chunks, TextChunk{
					Text:       sc.Text,
					StartIndex: charIndex + sc.StartIndex,
					EndIndex:   charIndex + sc.EndIndex,
				})
			}
			last := subChunks[len(subChunks)-1]
			chunkStart = charIndex + last.StartIndex
			currentWidth = VisibleWidth(last.Text)
			wrapOppIndex = -1
			continue
		}

		currentWidth += gWidth

		if i+1 < len(segments) {
			next := segments[i+1]
			if isWs && (ceIsPasteMarker(next.Segment) || !IsWhitespaceChar(next.Segment)) {
				wrapOppIndex = next.Index
				wrapOppWidth = currentWidth
			} else if !isWs && !IsWhitespaceChar(next.Segment) {
				isCjk := !ceIsPasteMarker(grapheme) && CjkBreakRegex.Test(grapheme)
				nextIsCjk := !ceIsPasteMarker(next.Segment) && CjkBreakRegex.Test(next.Segment)
				if isCjk || nextIsCjk {
					wrapOppIndex = next.Index
					wrapOppWidth = currentWidth
				}
			}
		}
	}

	chunks = append(chunks, TextChunk{
		Text:       js.SliceFrom(line, chunkStart),
		StartIndex: chunkStart,
		EndIndex:   js.Len(line),
	})

	return chunks
}

// ceEditorState is the text and cursor. Cursor indexes are TS string indexes.
type ceEditorState struct {
	lines      []string
	cursorLine int
	cursorCol  int
}

func ceCloneState(state ceEditorState) ceEditorState {
	return ceEditorState{
		lines:      slices.Clone(state.lines),
		cursorLine: state.cursorLine,
		cursorCol:  state.cursorCol,
	}
}

// ceEditorSnapshot is an undo snapshot: text state plus the paste registry.
type ceEditorSnapshot struct {
	state        ceEditorState
	pastes       *omap.Map[int, string]
	pasteCounter int
}

func (s ceEditorSnapshot) Clone() ceEditorSnapshot {
	return ceCloneSnapshot(s)
}

func ceCloneSnapshot(snap ceEditorSnapshot) ceEditorSnapshot {
	pastes := omap.NewMap[int, string]()
	if snap.pastes != nil {
		pastes = snap.pastes.Clone()
	}
	return ceEditorSnapshot{
		state:        ceCloneState(snap.state),
		pastes:       pastes,
		pasteCounter: snap.pasteCounter,
	}
}

// ceLayoutLine is one visual row produced by layout.
// cursorPos is set only when hasCursor is true.
type ceLayoutLine struct {
	text      string
	hasCursor bool
	cursorPos *int
}

// ceVisualLine maps one wrapped row back to a logical line.
type ceVisualLine struct {
	logicalLine int
	startCol    int
	length      int
}

type ceCursorPlacement string

const (
	ceCursorPlacementStart ceCursorPlacement = "start"
	ceCursorPlacementEnd   ceCursorPlacement = "end"
)

type ceSegmentMode string

const (
	ceSegmentModeWord     ceSegmentMode = "word"
	ceSegmentModeGrapheme ceSegmentMode = "grapheme"
)

type ceLastAction string

const (
	ceLastActionKill     ceLastAction = "kill"
	ceLastActionYank     ceLastAction = "yank"
	ceLastActionTypeWord ceLastAction = "type-word"
)

type ceJumpMode string

const (
	ceJumpModeForward  ceJumpMode = "forward"
	ceJumpModeBackward ceJumpMode = "backward"
)

type ceAutocompleteState string

const (
	ceAutocompleteStateRegular ceAutocompleteState = "regular"
	ceAutocompleteStateForce   ceAutocompleteState = "force"
)

type ceScrollDirection string

const (
	ceScrollDirectionUp   ceScrollDirection = "↑"
	ceScrollDirectionDown ceScrollDirection = "↓"
)

type ceAutocompleteRequestOptions struct {
	force       bool
	explicitTab bool
}

// EditorTheme is the editor's border and autocomplete-list styling.
type EditorTheme struct {
	BorderColor func(str string) string `json:"borderColor"`
	SelectList  SelectListTheme         `json:"selectList"`
}

// EditorOptions configures padding and the autocomplete list height.
// A nil *EditorOptions means the TS default {}.
type EditorOptions struct {
	PaddingX               *int `json:"paddingX,omitzero"`
	AutocompleteMaxVisible *int `json:"autocompleteMaxVisible,omitzero"`
}

// EditorCursor is the position returned by Editor.GetCursor.
// Line is a logical line index. Col is a TS string index on that line.
type EditorCursor struct {
	Line int `json:"line"`
	Col  int `json:"col"`
}

// EditorOverrides holds the virtual methods an embedding wrapper (such as CustomEditor)
// replaces so Editor.Render and bracketed-paste leftovers invoke the outermost override.
type EditorOverrides interface {
	HandleInput(data string)
	RenderTopBorder(width int, hiddenLineCount int) string
	RenderBottomBorder(width int, hiddenLineCount int) string
}

var ceSlashCommandMinPrimaryColumnWidth = 12
var ceSlashCommandMaxPrimaryColumnWidth = 32

var ceSlashCommandSelectListLayout = SelectListLayoutOptions{
	MinPrimaryColumnWidth: &ceSlashCommandMinPrimaryColumnWidth,
	MaxPrimaryColumnWidth: &ceSlashCommandMaxPrimaryColumnWidth,
}

const ceAttachmentAutocompleteDebounceMs int64 = 20

var ceDefaultAutocompleteTriggerCharacters = []string{"@", "#"}

// Suffix of an unquoted completion token. Depends on AutocompleteSeparatorRegex (utils.ts).
var ceUnquotedAutocompleteSuffixRegex = jsre.MustCompile(`(?:(?!`+AutocompleteSeparatorRegex.Source()+`).)*`, "u")

// Prefix of a trigger token, including an opening bracket or quote before the trigger.
var ceAutocompleteTokenStartSource = AutocompleteBoundaryRegex.Source() + "[([{<`]*"

func ceEscapeCharacterClass(value string) string {
	return ceEscapeCharClassMatcher.Replace(value)
}

func ceBuildTriggerPattern(triggerCharacters []string) *jsre.Regexp {
	escaped := make([]string, len(triggerCharacters))
	for i, c := range triggerCharacters {
		escaped[i] = ceEscapeCharacterClass(c)
	}
	return jsre.MustCompile(
		ceAutocompleteTokenStartSource+`(?:@"[^"]*|[`+strings.Join(escaped, "")+`]`+ceUnquotedAutocompleteSuffixRegex.Source()+`)$`,
		"u",
	)
}

func ceBuildDebouncePattern(triggerCharacters []string) *jsre.Regexp {
	var escapedWithoutAt []string
	for _, c := range triggerCharacters {
		if c == "@" {
			continue
		}
		escapedWithoutAt = append(escapedWithoutAt, ceEscapeCharacterClass(c))
	}
	return jsre.MustCompile(
		ceAutocompleteTokenStartSource+`(?:@(?:"[^"]*|`+ceUnquotedAutocompleteSuffixRegex.Source()+`)|[`+strings.Join(escapedWithoutAt, "")+`]`+ceUnquotedAutocompleteSuffixRegex.Source()+`)$`,
		"u",
	)
}

func ceCreateScrollBorder(direction ceScrollDirection, hiddenLineCount int, width int) string {
	availableWidth := max(0, width)
	label := fmt.Sprintf(" %s %d more ", direction, hiddenLineCount)
	labelWidth := VisibleWidth(label)
	if labelWidth+2 <= availableWidth {
		leftWidth := (availableWidth - labelWidth) / 2
		return strings.Repeat("─", leftWidth) + label + strings.Repeat("─", availableWidth-leftWidth-labelWidth)
	}

	indicator := fmt.Sprintf("─── %s %d more ", direction, hiddenLineCount)
	remaining := availableWidth - VisibleWidth(indicator)
	if remaining >= 0 {
		return indicator + strings.Repeat("─", remaining)
	}

	ellipsis := js.Slice("...", 0, availableWidth)
	indicatorWidth := availableWidth - VisibleWidth(ellipsis)
	return SliceByColumn(indicator, 0, indicatorWidth, true) + ellipsis
}

// Editor is the multiline text input. It implements Component and Focusable.
// CustomEditor embeds *Editor and overrides HandleInput and RenderTopBorder.
// Editor.Render and the paste-tail path dispatch through overrides registered via SetOverrides
// (TS this.renderTopBorder / this.renderBottomBorder / this.handleInput).
type Editor struct {
	mu sync.Mutex

	state   ceEditorState
	focused bool

	// Tui is the owning screen (TS protected).
	Tui      TUI
	theme    EditorTheme
	paddingX int

	overrides EditorOverrides

	// lastWidth defaults to 80. NewEditor must set it; the zero value is not 80.
	lastWidth                  int
	renderedVisibleLineCount   int // NewEditor must set 1
	renderedAutocompleteHeight int
	scrollOffset               int

	BorderColor func(str string) string

	autocompleteProvider          AutocompleteProvider
	autocompleteTriggerCharacters []string
	autocompleteTriggerPattern    *jsre.Regexp
	autocompleteDebouncePattern   *jsre.Regexp
	autocompleteList              *SelectList
	autocompleteState             *ceAutocompleteState
	autocompletePrefix            string
	autocompleteMaxVisible        int // NewEditor must set 5
	autocompleteAbort             context.CancelFunc
	autocompleteDebounceTimer     *time.Timer
	autocompleteRequestTask       func() error // nil is the idle Promise.resolve() chain
	autocompleteStartToken        int
	autocompleteRequestId         int

	pastes       *omap.Map[int, string]
	pasteCounter int

	pasteBuffer string
	isInPaste   bool

	history []string
	// historyIndex -1 means not browsing. NewEditor must set it; the zero value is not -1.
	historyIndex int
	historyDraft *ceEditorState

	killRing   *KillRing
	lastAction *ceLastAction

	jumpMode *ceJumpMode

	preferredVisualCol   *int
	snappedFromCursorCol *int

	undoStack *UndoStack[ceEditorSnapshot]

	OnSubmit      func(text string)
	OnChange      func(text string)
	DisableSubmit bool
}

// NewEditor builds an editor. Nil options use paddingX 0 and autocompleteMaxVisible 5.
func NewEditor(tui TUI, theme EditorTheme, options *EditorOptions) *Editor {
	paddingX := 0
	maxVisible := 5
	if options != nil {
		if options.PaddingX != nil {
			paddingX = max(0, *options.PaddingX)
		}
		if options.AutocompleteMaxVisible != nil {
			maxVisible = max(3, min(20, *options.AutocompleteMaxVisible))
		}
	}
	triggers := slices.Clone(ceDefaultAutocompleteTriggerCharacters)
	return &Editor{
		state: ceEditorState{
			lines:      []string{""},
			cursorLine: 0,
			cursorCol:  0,
		},
		Tui:                           tui,
		theme:                         theme,
		paddingX:                      paddingX,
		lastWidth:                     80,
		renderedVisibleLineCount:      1,
		BorderColor:                   theme.BorderColor,
		autocompleteTriggerCharacters: triggers,
		autocompleteTriggerPattern:    ceBuildTriggerPattern(triggers),
		autocompleteDebouncePattern:   ceBuildDebouncePattern(triggers),
		autocompleteMaxVisible:        maxVisible,
		pastes:                        omap.NewMap[int, string](),
		historyIndex:                  -1,
		killRing:                      NewKillRing(),
		undoStack:                     NewUndoStack[ceEditorSnapshot](),
	}
}

// Focused reports whether the TUI has given this editor keyboard focus.
func (e *Editor) Focused() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.focused
}

// SetFocused sets keyboard focus. When true, Render emits CursorMarker.
func (e *Editor) SetFocused(focused bool) {
	e.mu.Lock()
	e.focused = focused
	e.mu.Unlock()
}

// CapturesViewportInput reports whether an open autocomplete menu should intercept fullscreen viewport keys.
func (e *Editor) CapturesViewportInput() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.autocompleteState != nil
}

// SetOverrides registers the outermost wrapper so Render and bracketed-paste
// leftovers call CustomEditor overrides instead of *Editor's base methods.
func (e *Editor) SetOverrides(overrides EditorOverrides) {
	e.mu.Lock()
	e.overrides = overrides
	e.mu.Unlock()
}

func (e *Editor) validPasteIdsLocked() *omap.Set[int] {
	ids := omap.NewSet[int]()
	if e.pastes != nil {
		for _, k := range e.pastes.Keys() {
			ids.Add(k)
		}
	}
	return ids
}

func (e *Editor) validPasteIds() *omap.Set[int] {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.validPasteIdsLocked()
}

func (e *Editor) segmentLocked(text string, mode ceSegmentMode) []SegmentData {
	base := ceGetGraphemeSegmenter()
	if mode == ceSegmentModeWord {
		base = ceGetWordSegmenter()
	}
	return ceSegmentWithMarkers(text, base, e.validPasteIdsLocked())
}

func (e *Editor) segment(text string, mode ceSegmentMode) []SegmentData {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.segmentLocked(text, mode)
}

func (e *Editor) GetPaddingX() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.paddingX
}

func (e *Editor) SetPaddingX(padding int) {
	newPadding := max(0, padding)
	e.mu.Lock()
	changed := e.paddingX != newPadding
	if changed {
		e.paddingX = newPadding
	}
	t := e.Tui
	e.mu.Unlock()
	if changed && t != nil {
		t.RequestRender(nil)
	}
}

func (e *Editor) GetAutocompleteMaxVisible() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.autocompleteMaxVisible
}

func (e *Editor) SetAutocompleteMaxVisible(maxVisible int) {
	newMaxVisible := max(3, min(20, maxVisible))
	e.mu.Lock()
	changed := e.autocompleteMaxVisible != newMaxVisible
	if changed {
		e.autocompleteMaxVisible = newMaxVisible
	}
	t := e.Tui
	e.mu.Unlock()
	if changed && t != nil {
		t.RequestRender(nil)
	}
}

func (e *Editor) SetAutocompleteProvider(provider AutocompleteProvider) {
	var triggers []string
	if provider != nil {
		triggers = provider.TriggerCharacters()
	}
	e.mu.Lock()
	e.cancelAutocompleteLocked()
	e.autocompleteProvider = provider
	e.setAutocompleteTriggerCharactersLocked(triggers)
	e.mu.Unlock()
}

// AddToHistory records a submitted prompt for up/down navigation.
func (e *Editor) AddToHistory(text string) {
	trimmed := js.Trim(text)
	if trimmed == "" {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.history) > 0 && e.history[0] == trimmed {
		return
	}
	e.history = append([]string{trimmed}, e.history...)
	if len(e.history) > 100 {
		e.history = e.history[:100]
	}
}

func (e *Editor) isEditorEmptyLocked() bool {
	return len(e.state.lines) == 1 && e.state.lines[0] == ""
}

func (e *Editor) isEditorEmpty() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.isEditorEmptyLocked()
}

func (e *Editor) isOnFirstVisualLineLocked() bool {
	visualLines := e.buildVisualLineMapLocked(e.lastWidth)
	return e.findCurrentVisualLineLocked(visualLines) == 0
}

func (e *Editor) isOnFirstVisualLine() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.isOnFirstVisualLineLocked()
}

func (e *Editor) isOnLastVisualLineLocked() bool {
	visualLines := e.buildVisualLineMapLocked(e.lastWidth)
	return e.findCurrentVisualLineLocked(visualLines) == len(visualLines)-1
}

func (e *Editor) isOnLastVisualLine() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.isOnLastVisualLineLocked()
}

func (e *Editor) navigateHistoryLocked(direction int, cb *[]func()) {
	e.lastAction = nil
	if len(e.history) == 0 {
		return
	}

	newIndex := e.historyIndex - direction
	if newIndex < -1 || newIndex >= len(e.history) {
		return
	}

	if e.historyIndex == -1 && newIndex >= 0 {
		e.pushUndoSnapshotLocked()
		draft := ceCloneState(e.state)
		e.historyDraft = &draft
	}

	e.historyIndex = newIndex

	if e.historyIndex == -1 {
		draft := e.historyDraft
		e.historyDraft = nil
		if draft != nil {
			e.state = ceCloneState(*draft)
			e.preferredVisualCol = nil
			e.snappedFromCursorCol = nil
			e.scrollOffset = 0
			e.enqueueOnChangeLocked(cb)
		} else {
			e.setTextInternalLocked("", ceCursorPlacementEnd, cb)
		}
	} else {
		placement := ceCursorPlacementEnd
		if direction == -1 {
			placement = ceCursorPlacementStart
		}
		entry := ""
		if e.historyIndex < len(e.history) {
			entry = e.history[e.historyIndex]
		}
		e.setTextInternalLocked(entry, placement, cb)
	}
}

func (e *Editor) navigateHistory(direction int) {
	var cb []func()
	e.mu.Lock()
	e.navigateHistoryLocked(direction, &cb)
	e.mu.Unlock()
	ceRunCallbacks(cb)
}

func (e *Editor) exitHistoryBrowsingLocked() {
	e.historyIndex = -1
	e.historyDraft = nil
}

func (e *Editor) exitHistoryBrowsing() {
	e.mu.Lock()
	e.exitHistoryBrowsingLocked()
	e.mu.Unlock()
}

func (e *Editor) setTextInternalLocked(text string, cursorPlacement ceCursorPlacement, cb *[]func()) {
	lines := js.Split(text, "\n")
	if len(lines) == 0 {
		e.state.lines = []string{""}
	} else {
		e.state.lines = lines
	}
	if cursorPlacement == ceCursorPlacementStart {
		e.state.cursorLine = 0
		e.setCursorColLocked(0)
	} else {
		e.state.cursorLine = len(e.state.lines) - 1
		e.setCursorColLocked(js.Len(e.state.lines[e.state.cursorLine]))
	}
	e.scrollOffset = 0
	e.enqueueOnChangeLocked(cb)
}

func (e *Editor) setTextInternal(text string, cursorPlacement ceCursorPlacement) {
	var cb []func()
	e.mu.Lock()
	e.setTextInternalLocked(text, cursorPlacement, &cb)
	e.mu.Unlock()
	ceRunCallbacks(cb)
}

func (e *Editor) enqueueOnChangeLocked(cb *[]func()) {
	if e.OnChange != nil {
		fn := e.OnChange
		text := e.getTextLocked()
		*cb = append(*cb, func() { fn(text) })
	}
}

func ceRunCallbacks(cb []func()) {
	for _, fn := range cb {
		fn()
	}
}

// Invalidate drops cached render state. Editor currently has none.
func (e *Editor) Invalidate() {}

// RenderTopBorder draws the top border, with a scroll indicator when lines are hidden above.
// Editor.Render dispatches to the outermost override if registered.
func (e *Editor) RenderTopBorder(width int, hiddenLineCount int) string {
	var border string
	if hiddenLineCount > 0 {
		border = ceCreateScrollBorder(ceScrollDirectionUp, hiddenLineCount, width)
	} else {
		border = strings.Repeat("─", max(0, width))
	}
	e.mu.Lock()
	bc := e.BorderColor
	e.mu.Unlock()
	if bc != nil {
		return bc(border)
	}
	return border
}

// RenderBottomBorder draws the bottom border, with a scroll indicator when lines are hidden below.
// Editor.Render dispatches to the outermost override if registered.
func (e *Editor) RenderBottomBorder(width int, hiddenLineCount int) string {
	var border string
	if hiddenLineCount > 0 {
		border = ceCreateScrollBorder(ceScrollDirectionDown, hiddenLineCount, width)
	} else {
		border = strings.Repeat("─", max(0, width))
	}
	e.mu.Lock()
	bc := e.BorderColor
	e.mu.Unlock()
	if bc != nil {
		return bc(border)
	}
	return border
}

func (e *Editor) Render(width int) []string {
	e.mu.Lock()
	maxPadding := max(0, (width-1)/2)
	paddingX := min(e.paddingX, maxPadding)
	contentWidth := max(1, width-paddingX*2)

	padReserve := 1
	if paddingX > 0 {
		padReserve = 0
	}
	layoutWidth := max(1, contentWidth-padReserve)
	e.lastWidth = layoutWidth

	layoutLines := e.layoutTextLocked(layoutWidth)
	t := e.Tui
	ov := e.overrides
	e.mu.Unlock()

	terminalRows := 24
	if t != nil {
		if term := t.Terminal(); term != nil {
			terminalRows = term.Rows()
		}
	}
	maxVisibleLines := max(5, (terminalRows*3)/10)

	e.mu.Lock()
	cursorLineIndex := -1
	for i, line := range layoutLines {
		if line.hasCursor {
			cursorLineIndex = i
			break
		}
	}
	if cursorLineIndex == -1 {
		cursorLineIndex = 0
	}

	if cursorLineIndex < e.scrollOffset {
		e.scrollOffset = cursorLineIndex
	} else if cursorLineIndex >= e.scrollOffset+maxVisibleLines {
		e.scrollOffset = cursorLineIndex - maxVisibleLines + 1
	}

	maxScrollOffset := max(0, len(layoutLines)-maxVisibleLines)
	e.scrollOffset = max(0, min(e.scrollOffset, maxScrollOffset))

	endIdx := min(len(layoutLines), e.scrollOffset+maxVisibleLines)
	visibleLines := layoutLines[e.scrollOffset:endIdx]
	e.renderedVisibleLineCount = len(visibleLines)
	scrollOffset := e.scrollOffset
	emitCursorMarker := e.focused
	validIds := e.validPasteIdsLocked()

	linesBelow := len(layoutLines) - (scrollOffset + len(visibleLines))

	var acList *SelectList
	if e.autocompleteState != nil && e.autocompleteList != nil {
		acList = e.autocompleteList
	}
	e.mu.Unlock()

	var topBorder string
	if ov != nil {
		topBorder = ov.RenderTopBorder(width, scrollOffset)
	} else {
		topBorder = e.RenderTopBorder(width, scrollOffset)
	}

	result := make([]string, 0, len(visibleLines)+2)
	leftPadding := strings.Repeat(" ", paddingX)
	rightPadding := leftPadding
	result = append(result, topBorder)

	baseSeg := ceGetGraphemeSegmenter()
	for _, layoutLine := range visibleLines {
		displayText := layoutLine.text
		lineVisibleWidth := VisibleWidth(layoutLine.text)
		cursorInPadding := false

		if layoutLine.hasCursor && layoutLine.cursorPos != nil {
			cursorPos := *layoutLine.cursorPos
			before := js.Slice(displayText, 0, cursorPos)
			after := js.SliceFrom(displayText, cursorPos)

			marker := ""
			if emitCursorMarker {
				marker = CursorMarker
			}

			if js.Len(after) > 0 {
				afterGraphemes := ceSegmentWithMarkers(after, baseSeg, validIds)
				firstGrapheme := ""
				if len(afterGraphemes) > 0 {
					firstGrapheme = afterGraphemes[0].Segment
				}
				restAfter := js.SliceFrom(after, js.Len(firstGrapheme))
				cursor := RenderFakeCursor(firstGrapheme)
				displayText = before + marker + cursor + restAfter
			} else {
				cursor := RenderFakeCursor(" ")
				displayText = before + marker + cursor
				lineVisibleWidth++
				if lineVisibleWidth > contentWidth && paddingX > 0 {
					cursorInPadding = true
				}
			}
		}

		padding := strings.Repeat(" ", max(0, contentWidth-lineVisibleWidth))
		lineRightPadding := rightPadding
		if cursorInPadding && len(rightPadding) > 0 {
			lineRightPadding = rightPadding[1:]
		}
		result = append(result, leftPadding+displayText+padding+lineRightPadding)
	}

	var bottomBorder string
	if ov != nil {
		bottomBorder = ov.RenderBottomBorder(width, linesBelow)
	} else {
		bottomBorder = e.RenderBottomBorder(width, linesBelow)
	}
	result = append(result, bottomBorder)

	acHeight := 0
	if acList != nil {
		autocompleteResult := acList.Render(contentWidth)
		acHeight = len(autocompleteResult)
		for _, line := range autocompleteResult {
			lineWidth := VisibleWidth(line)
			linePadding := strings.Repeat(" ", max(0, contentWidth-lineWidth))
			result = append(result, leftPadding+line+linePadding+rightPadding)
		}
	}

	e.mu.Lock()
	e.renderedAutocompleteHeight = acHeight
	e.mu.Unlock()

	return result
}

func (e *Editor) HandleMouse(event TuiMouseEvent) *TuiMouseEventResult {
	e.mu.Lock()
	autocompleteStartRow := e.renderedVisibleLineCount + 2
	acActive := e.autocompleteState != nil && e.autocompleteList != nil
	acList := e.autocompleteList
	acHeight := e.renderedAutocompleteHeight
	paddingSetting := e.paddingX
	e.mu.Unlock()

	if acActive && event.Y >= autocompleteStartRow && event.Y < autocompleteStartRow+acHeight {
		maxPadding := max(0, (event.Width-1)/2)
		paddingX := min(paddingSetting, maxPadding)
		contentWidth := max(1, event.Width-paddingX*2)
		subEvent := event
		subEvent.X = event.X - paddingX
		subEvent.Y = event.Y - autocompleteStartRow
		subEvent.Width = contentWidth
		subEvent.Height = acHeight
		result := acList.HandleMouse(subEvent)
		if result == nil {
			return nil
		}
		focusTrue := true
		res := *result
		res.Focus = &focusTrue
		return &res
	}

	if event.Type != TuiMouseEventTypeClick || event.Button != TuiMouseButtonLeft {
		return nil
	}

	handledTrue := true
	focusTrue := true
	handledResult := &TuiMouseEventResult{Handled: &handledTrue, Focus: &focusTrue}

	e.mu.Lock()
	if event.Y <= 0 || event.Y > e.renderedVisibleLineCount {
		e.mu.Unlock()
		return handledResult
	}

	visualLines := e.buildVisualLineMapLocked(e.lastWidth)
	visualLineIndex := e.scrollOffset + event.Y - 1
	if visualLineIndex < 0 || visualLineIndex >= len(visualLines) {
		e.mu.Unlock()
		return handledResult
	}
	visualLine := visualLines[visualLineIndex]
	logicalLine := ""
	if visualLine.logicalLine >= 0 && visualLine.logicalLine < len(e.state.lines) {
		logicalLine = e.state.lines[visualLine.logicalLine]
	}
	chunkEnd := visualLine.startCol + visualLine.length
	chunk := js.Slice(logicalLine, visualLine.startCol, chunkEnd)
	maxPadding := max(0, (event.Width-1)/2)
	paddingX := min(e.paddingX, maxPadding)
	targetColumn := max(0, event.X-paddingX)

	visibleColumn := 0
	chunkLen := js.Len(chunk)
	targetIndex := chunkLen
	lastGraphemeIndex := 0
	for _, grapheme := range e.segmentLocked(chunk, ceSegmentModeGrapheme) {
		nextColumn := visibleColumn + VisibleWidth(grapheme.Segment)
		lastGraphemeIndex = grapheme.Index
		if targetColumn < nextColumn {
			targetIndex = grapheme.Index
			break
		}
		visibleColumn = nextColumn
	}

	isLastSegment := visualLineIndex == len(visualLines)-1 || visualLines[visualLineIndex+1].logicalLine != visualLine.logicalLine
	if !isLastSegment && targetIndex == chunkLen && chunkLen > 0 {
		targetIndex = lastGraphemeIndex
	}

	e.state.cursorLine = visualLine.logicalLine
	e.setCursorColLocked(visualLine.startCol + targetIndex)
	e.lastAction = nil
	e.exitHistoryBrowsingLocked()
	needUpdateAC := e.autocompleteState != nil
	e.mu.Unlock()

	if needUpdateAC {
		e.updateAutocomplete()
	}
	return handledResult
}

// HandleInput applies one terminal input chunk.
// Bracketed-paste leftovers are fed back through the outermost HandleInput override if registered.
func (e *Editor) HandleInput(data string) {
	kb := GetKeybindings()

	e.mu.Lock()
	if e.jumpMode != nil {
		if kb.Matches(data, KeybindingTuiEditorJumpForward) || kb.Matches(data, KeybindingTuiEditorJumpBackward) {
			e.jumpMode = nil
			e.mu.Unlock()
			return
		}
		var printable *string
		if p := DecodePrintableKey(data); p != nil {
			printable = p
		} else if js.CharCodeAt(data, 0) >= 32 {
			d := data
			printable = &d
		}
		if printable != nil {
			dir := *e.jumpMode
			e.jumpMode = nil
			e.jumpToCharLocked(*printable, dir)
			e.mu.Unlock()
			return
		}
		e.jumpMode = nil
	}

	if strings.Contains(data, "\x1b[200~") {
		e.isInPaste = true
		e.pasteBuffer = ""
		data = strings.Replace(data, "\x1b[200~", "", 1)
	}

	if e.isInPaste {
		e.pasteBuffer += data
		endIndex := strings.Index(e.pasteBuffer, "\x1b[201~")
		if endIndex != -1 {
			pasteContent := e.pasteBuffer[:endIndex]
			var pasteCB []func()
			if pasteContent != "" {
				e.handlePasteLocked(pasteContent, &pasteCB)
			}
			e.isInPaste = false
			remaining := e.pasteBuffer[endIndex+len("\x1b[201~"):]
			e.pasteBuffer = ""
			ov := e.overrides
			e.mu.Unlock()
			ceRunCallbacks(pasteCB)
			if remaining != "" {
				if ov != nil {
					ov.HandleInput(remaining)
				} else {
					e.HandleInput(remaining)
				}
			}
			return
		}
		e.mu.Unlock()
		return
	}

	if kb.Matches(data, KeybindingTuiInputCopy) {
		e.mu.Unlock()
		return
	}

	if kb.Matches(data, KeybindingTuiEditorUndo) {
		var cb []func()
		e.undoLocked(&cb)
		e.mu.Unlock()
		ceRunCallbacks(cb)
		return
	}

	if e.autocompleteState != nil && e.autocompleteList != nil {
		if kb.Matches(data, KeybindingTuiSelectCancel) {
			e.cancelAutocompleteLocked()
			e.mu.Unlock()
			return
		}

		if kb.Matches(data, KeybindingTuiSelectUp) ||
			kb.Matches(data, KeybindingTuiSelectDown) ||
			kb.Matches(data, KeybindingTuiSelectPageUp) ||
			kb.Matches(data, KeybindingTuiSelectPageDown) {
			acList := e.autocompleteList
			e.mu.Unlock()
			acList.HandleInput(data)
			return
		}

		if kb.Matches(data, KeybindingTuiInputTab) {
			selected := e.autocompleteList.GetSelectedItem()
			provider := e.autocompleteProvider
			if selected != nil && provider != nil {
				e.pushUndoSnapshotLocked()
				e.lastAction = nil
				linesCopy := slices.Clone(e.state.lines)
				curLine := e.state.cursorLine
				curCol := e.state.cursorCol
				prefix := e.autocompletePrefix
				e.mu.Unlock()

				item := AutocompleteItem{
					Value:       selected.Value,
					Label:       selected.Label,
					Description: selected.Description,
				}
				result := provider.ApplyCompletion(linesCopy, curLine, curCol, item, prefix)

				var cb []func()
				e.mu.Lock()
				e.state.lines = result.Lines
				e.state.cursorLine = result.CursorLine
				e.setCursorColLocked(result.CursorCol)
				e.cancelAutocompleteLocked()
				e.enqueueOnChangeLocked(&cb)
				e.mu.Unlock()
				ceRunCallbacks(cb)
				return
			}
			e.mu.Unlock()
			return
		}

		if kb.Matches(data, KeybindingTuiSelectConfirm) {
			selected := e.autocompleteList.GetSelectedItem()
			provider := e.autocompleteProvider
			if selected != nil && provider != nil {
				e.pushUndoSnapshotLocked()
				e.lastAction = nil
				linesCopy := slices.Clone(e.state.lines)
				curLine := e.state.cursorLine
				curCol := e.state.cursorCol
				prefix := e.autocompletePrefix
				e.mu.Unlock()

				item := AutocompleteItem{
					Value:       selected.Value,
					Label:       selected.Label,
					Description: selected.Description,
				}
				result := provider.ApplyCompletion(linesCopy, curLine, curCol, item, prefix)

				var cb []func()
				e.mu.Lock()
				e.state.lines = result.Lines
				e.state.cursorLine = result.CursorLine
				e.setCursorColLocked(result.CursorCol)
				isSlash := strings.HasPrefix(prefix, "/")
				e.cancelAutocompleteLocked()
				if !isSlash {
					e.enqueueOnChangeLocked(&cb)
					e.mu.Unlock()
					ceRunCallbacks(cb)
					return
				}
				// Slash command falls through to submit below under re-acquired lock.
			}
		}
	}

	if kb.Matches(data, KeybindingTuiInputTab) && e.autocompleteState == nil {
		e.mu.Unlock()
		e.handleTabCompletion()
		return
	}

	if kb.Matches(data, KeybindingTuiEditorDeleteToLineEnd) {
		var cb []func()
		e.deleteToEndOfLineLocked(&cb)
		e.mu.Unlock()
		ceRunCallbacks(cb)
		return
	}
	if kb.Matches(data, KeybindingTuiEditorDeleteToLineStart) {
		var cb []func()
		e.deleteToStartOfLineLocked(&cb)
		e.mu.Unlock()
		ceRunCallbacks(cb)
		return
	}
	if kb.Matches(data, KeybindingTuiEditorDeleteWordBackward) {
		var cb []func()
		e.deleteWordBackwardsLocked(&cb)
		e.mu.Unlock()
		ceRunCallbacks(cb)
		return
	}
	if kb.Matches(data, KeybindingTuiEditorDeleteWordForward) {
		var cb []func()
		e.deleteWordForwardLocked(&cb)
		e.mu.Unlock()
		ceRunCallbacks(cb)
		return
	}
	if kb.Matches(data, KeybindingTuiEditorDeleteCharBackward) || MatchesKey(data, "shift+backspace") {
		e.mu.Unlock()
		e.handleBackspace()
		return
	}
	if kb.Matches(data, KeybindingTuiEditorDeleteCharForward) || MatchesKey(data, "shift+delete") {
		e.mu.Unlock()
		e.handleForwardDelete()
		return
	}

	if kb.Matches(data, KeybindingTuiEditorYank) {
		var cb []func()
		e.yankLocked(&cb)
		e.mu.Unlock()
		ceRunCallbacks(cb)
		return
	}
	if kb.Matches(data, KeybindingTuiEditorYankPop) {
		var cb []func()
		e.yankPopLocked(&cb)
		e.mu.Unlock()
		ceRunCallbacks(cb)
		return
	}

	if kb.Matches(data, KeybindingTuiEditorHistoryPrevious) {
		var cb []func()
		e.cancelAutocompleteLocked()
		e.navigateHistoryLocked(-1, &cb)
		e.mu.Unlock()
		ceRunCallbacks(cb)
		return
	}
	if kb.Matches(data, KeybindingTuiEditorHistoryNext) {
		var cb []func()
		e.cancelAutocompleteLocked()
		e.navigateHistoryLocked(1, &cb)
		e.mu.Unlock()
		ceRunCallbacks(cb)
		return
	}

	if kb.Matches(data, KeybindingTuiEditorCursorLineStart) {
		e.moveToLineStartLocked()
		e.mu.Unlock()
		return
	}
	if kb.Matches(data, KeybindingTuiEditorCursorLineEnd) {
		e.moveToLineEndLocked()
		e.mu.Unlock()
		return
	}
	if kb.Matches(data, KeybindingTuiEditorCursorWordLeft) {
		e.moveWordBackwardsLocked()
		e.mu.Unlock()
		return
	}
	if kb.Matches(data, KeybindingTuiEditorCursorWordRight) {
		e.moveWordForwardsLocked()
		e.mu.Unlock()
		return
	}

	dataLen := js.Len(data)
	if kb.Matches(data, KeybindingTuiInputNewLine) ||
		(js.CharCodeAt(data, 0) == 10 && dataLen > 1) ||
		data == "\x1b\r" ||
		data == "\x1b[13;2~" ||
		(dataLen > 1 && strings.Contains(data, "\x1b") && strings.Contains(data, "\r")) ||
		data == "\n" {
		if e.shouldSubmitOnBackslashEnterLocked(data, kb) {
			e.mu.Unlock()
			e.handleBackspace()
			e.submitValue()
			return
		}
		var cb []func()
		e.addNewLineLocked(&cb)
		e.mu.Unlock()
		ceRunCallbacks(cb)
		return
	}

	if kb.Matches(data, KeybindingTuiInputSubmit) {
		if e.disableSubmitLocked() {
			e.mu.Unlock()
			return
		}
		currentLine := e.currentLineLocked()
		if e.state.cursorCol > 0 && js.CharCodeAt(currentLine, e.state.cursorCol-1) == '\\' {
			e.mu.Unlock()
			e.handleBackspace()
			e.addNewLine()
			return
		}
		var cb []func()
		e.submitValueLocked(&cb)
		e.mu.Unlock()
		ceRunCallbacks(cb)
		return
	}

	if kb.Matches(data, KeybindingTuiEditorCursorUp) {
		if e.isOnFirstVisualLineLocked() && (e.isEditorEmptyLocked() || e.historyIndex > -1 || e.state.cursorCol == 0) {
			var cb []func()
			e.navigateHistoryLocked(-1, &cb)
			e.mu.Unlock()
			ceRunCallbacks(cb)
			return
		}
		if e.isOnFirstVisualLineLocked() {
			e.moveToLineStartLocked()
			e.mu.Unlock()
			return
		}
		needUpdateAC := e.moveCursorLocked(-1, 0)
		e.mu.Unlock()
		if needUpdateAC {
			e.updateAutocomplete()
		}
		return
	}
	if kb.Matches(data, KeybindingTuiEditorCursorDown) {
		if e.historyIndex > -1 && e.isOnLastVisualLineLocked() {
			var cb []func()
			e.navigateHistoryLocked(1, &cb)
			e.mu.Unlock()
			ceRunCallbacks(cb)
			return
		}
		if e.isOnLastVisualLineLocked() {
			e.moveToLineEndLocked()
			e.mu.Unlock()
			return
		}
		needUpdateAC := e.moveCursorLocked(1, 0)
		e.mu.Unlock()
		if needUpdateAC {
			e.updateAutocomplete()
		}
		return
	}
	if kb.Matches(data, KeybindingTuiEditorCursorRight) {
		needUpdateAC := e.moveCursorLocked(0, 1)
		e.mu.Unlock()
		if needUpdateAC {
			e.updateAutocomplete()
		}
		return
	}
	if kb.Matches(data, KeybindingTuiEditorCursorLeft) {
		needUpdateAC := e.moveCursorLocked(0, -1)
		e.mu.Unlock()
		if needUpdateAC {
			e.updateAutocomplete()
		}
		return
	}

	if kb.Matches(data, KeybindingTuiEditorPageUp) {
		e.mu.Unlock()
		e.pageScroll(-1)
		return
	}
	if kb.Matches(data, KeybindingTuiEditorPageDown) {
		e.mu.Unlock()
		e.pageScroll(1)
		return
	}

	if kb.Matches(data, KeybindingTuiEditorJumpForward) {
		mode := ceJumpModeForward
		e.jumpMode = &mode
		e.mu.Unlock()
		return
	}
	if kb.Matches(data, KeybindingTuiEditorJumpBackward) {
		mode := ceJumpModeBackward
		e.jumpMode = &mode
		e.mu.Unlock()
		return
	}

	if MatchesKey(data, "shift+space") {
		e.mu.Unlock()
		e.insertCharacter(" ", false)
		return
	}

	if printable := DecodePrintableKey(data); printable != nil {
		e.mu.Unlock()
		e.insertCharacter(*printable, false)
		return
	}

	e.mu.Unlock()
	if js.CharCodeAt(data, 0) >= 32 {
		e.insertCharacter(data, false)
	}
}

func (e *Editor) currentLineLocked() string {
	if e.state.cursorLine >= 0 && e.state.cursorLine < len(e.state.lines) {
		return e.state.lines[e.state.cursorLine]
	}
	return ""
}

func (e *Editor) disableSubmitLocked() bool {
	return e.DisableSubmit
}

func (e *Editor) layoutTextLocked(contentWidth int) []ceLayoutLine {
	layoutLines := []ceLayoutLine{}

	if len(e.state.lines) == 0 || (len(e.state.lines) == 1 && e.state.lines[0] == "") {
		zero := 0
		layoutLines = append(layoutLines, ceLayoutLine{
			text:      "",
			hasCursor: true,
			cursorPos: &zero,
		})
		return layoutLines
	}

	for i, line := range e.state.lines {
		isCurrentLine := i == e.state.cursorLine
		lineVisibleWidth := VisibleWidth(line)

		if lineVisibleWidth <= contentWidth {
			if isCurrentLine {
				pos := e.state.cursorCol
				layoutLines = append(layoutLines, ceLayoutLine{
					text:      line,
					hasCursor: true,
					cursorPos: &pos,
				})
			} else {
				layoutLines = append(layoutLines, ceLayoutLine{
					text:      line,
					hasCursor: false,
				})
			}
		} else {
			chunks := WordWrapLine(line, contentWidth, e.segmentLocked(line, ceSegmentModeGrapheme))
			for chunkIndex, chunk := range chunks {
				cursorPos := e.state.cursorCol
				isLastChunk := chunkIndex == len(chunks)-1

				hasCursorInChunk := false
				adjustedCursorPos := 0

				if isCurrentLine {
					if isLastChunk {
						hasCursorInChunk = cursorPos >= chunk.StartIndex
						adjustedCursorPos = cursorPos - chunk.StartIndex
					} else {
						hasCursorInChunk = cursorPos >= chunk.StartIndex && cursorPos < chunk.EndIndex
						if hasCursorInChunk {
							adjustedCursorPos = cursorPos - chunk.StartIndex
							chunkTextLen := js.Len(chunk.Text)
							if adjustedCursorPos > chunkTextLen {
								adjustedCursorPos = chunkTextLen
							}
						}
					}
				}

				if hasCursorInChunk {
					pos := adjustedCursorPos
					layoutLines = append(layoutLines, ceLayoutLine{
						text:      chunk.Text,
						hasCursor: true,
						cursorPos: &pos,
					})
				} else {
					layoutLines = append(layoutLines, ceLayoutLine{
						text:      chunk.Text,
						hasCursor: false,
					})
				}
			}
		}
	}

	return layoutLines
}

func (e *Editor) layoutText(contentWidth int) []ceLayoutLine {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.layoutTextLocked(contentWidth)
}

func (e *Editor) getTextLocked() string {
	return strings.Join(e.state.lines, "\n")
}

func (e *Editor) GetText() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.getTextLocked()
}

func (e *Editor) expandPasteMarkersLocked(text string) string {
	result := text
	if e.pastes == nil {
		return result
	}
	for pasteId, pasteContent := range e.pastes.All() {
		markerRegex := jsre.MustCompile(fmt.Sprintf(`\[paste #%d( (\+\d+ lines|\d+ chars))?\]`, pasteId), "g")
		result = markerRegex.ReplaceFunc(result, func(*jsre.Match) string {
			return pasteContent
		})
	}
	return result
}

func (e *Editor) expandPasteMarkers(text string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.expandPasteMarkersLocked(text)
}

// GetExpandedText returns the text with paste markers replaced by their stored content.
func (e *Editor) GetExpandedText() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.expandPasteMarkersLocked(e.getTextLocked())
}

func (e *Editor) GetLines() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.state.lines)
}

func (e *Editor) GetCursor() EditorCursor {
	e.mu.Lock()
	defer e.mu.Unlock()
	return EditorCursor{Line: e.state.cursorLine, Col: e.state.cursorCol}
}

func (e *Editor) SetText(text string) {
	var cb []func()
	e.mu.Lock()
	e.cancelAutocompleteLocked()
	e.lastAction = nil
	e.exitHistoryBrowsingLocked()
	normalized := e.normalizeText(text)
	if e.getTextLocked() != normalized {
		e.pushUndoSnapshotLocked()
	}
	if e.pastes == nil {
		e.pastes = omap.NewMap[int, string]()
	} else {
		e.pastes.Clear()
	}
	e.pasteCounter = 0
	e.setTextInternalLocked(normalized, ceCursorPlacementEnd, &cb)
	e.mu.Unlock()
	ceRunCallbacks(cb)
}

// InsertTextAtCursor inserts text at the cursor as one undo step.
func (e *Editor) InsertTextAtCursor(text string) {
	if text == "" {
		return
	}
	var cb []func()
	e.mu.Lock()
	e.cancelAutocompleteLocked()
	e.pushUndoSnapshotLocked()
	e.lastAction = nil
	e.exitHistoryBrowsingLocked()
	e.insertTextAtCursorInternalLocked(text, &cb)
	e.mu.Unlock()
	ceRunCallbacks(cb)
}

func (e *Editor) normalizeText(text string) string {
	return ceNormalizer.Replace(text)
}

func (e *Editor) insertTextAtCursorInternalLocked(text string, cb *[]func()) {
	if text == "" {
		return
	}

	normalized := e.normalizeText(text)
	insertedLines := js.Split(normalized, "\n")

	currentLine := e.currentLineLocked()
	beforeCursor := js.Slice(currentLine, 0, e.state.cursorCol)
	afterCursor := js.SliceFrom(currentLine, e.state.cursorCol)

	if len(insertedLines) == 1 {
		e.state.lines[e.state.cursorLine] = beforeCursor + normalized + afterCursor
		e.setCursorColLocked(e.state.cursorCol + js.Len(normalized))
	} else {
		newLines := make([]string, 0, len(e.state.lines)+len(insertedLines)-1)
		newLines = append(newLines, e.state.lines[:e.state.cursorLine]...)
		newLines = append(newLines, beforeCursor+insertedLines[0])
		if len(insertedLines) > 2 {
			newLines = append(newLines, insertedLines[1:len(insertedLines)-1]...)
		}
		lastInserted := insertedLines[len(insertedLines)-1]
		newLines = append(newLines, lastInserted+afterCursor)
		newLines = append(newLines, e.state.lines[e.state.cursorLine+1:]...)

		e.state.lines = newLines
		e.state.cursorLine += len(insertedLines) - 1
		e.setCursorColLocked(js.Len(lastInserted))
	}

	e.enqueueOnChangeLocked(cb)
}

func (e *Editor) insertTextAtCursorInternal(text string) {
	var cb []func()
	e.mu.Lock()
	e.insertTextAtCursorInternalLocked(text, &cb)
	e.mu.Unlock()
	ceRunCallbacks(cb)
}

func (e *Editor) insertCharacter(char string, skipUndoCoalescing bool) {
	var cb []func()
	e.mu.Lock()
	e.exitHistoryBrowsingLocked()

	if !skipUndoCoalescing {
		if IsWhitespaceChar(char) || e.lastAction == nil || *e.lastAction != ceLastActionTypeWord {
			e.pushUndoSnapshotLocked()
		}
		act := ceLastActionTypeWord
		e.lastAction = &act
	}

	line := e.currentLineLocked()
	before := js.Slice(line, 0, e.state.cursorCol)
	after := js.SliceFrom(line, e.state.cursorCol)

	e.state.lines[e.state.cursorLine] = before + char + after
	e.setCursorColLocked(e.state.cursorCol + js.Len(char))
	e.enqueueOnChangeLocked(&cb)

	action := 0 // 0=none, 1=tryTrigger, 2=update
	if e.autocompleteState == nil {
		if char == "/" && e.isAtStartOfMessageLocked() {
			action = 1
		} else if slices.Contains(e.autocompleteTriggerCharacters, char) {
			currentLine := e.currentLineLocked()
			textBeforeCursor := js.Slice(currentLine, 0, e.state.cursorCol)
			if e.autocompleteTriggerPattern.Test(textBeforeCursor) {
				action = 1
			}
		} else if ceWordCharTriggerRegex.Test(char) || CjkBreakRegex.Test(char) {
			currentLine := e.currentLineLocked()
			textBeforeCursor := js.Slice(currentLine, 0, e.state.cursorCol)
			if e.isInSlashCommandContextLocked(textBeforeCursor) || e.autocompleteTriggerPattern.Test(textBeforeCursor) {
				action = 1
			}
		}
	} else {
		action = 2
	}
	e.mu.Unlock()

	ceRunCallbacks(cb)
	switch action {
	case 1:
		e.tryTriggerAutocomplete(false)
	case 2:
		e.updateAutocomplete()
	}
}

func (e *Editor) handlePasteLocked(pastedText string, cb *[]func()) {
	e.cancelAutocompleteLocked()
	e.exitHistoryBrowsingLocked()
	e.lastAction = nil
	e.pushUndoSnapshotLocked()

	decodedText := ceCsiUCtrlRegex.ReplaceFunc(pastedText, func(m *jsre.Match) string {
		cp, err := strconv.Atoi(m.Group(1))
		if err != nil {
			return m.Group(0)
		}
		if cp >= 97 && cp <= 122 {
			return js.FromCharCode(cp - 96)
		}
		if cp >= 65 && cp <= 90 {
			return js.FromCharCode(cp - 64)
		}
		return m.Group(0)
	})

	cleanText := e.normalizeText(decodedText)

	units := js.ToUTF16(cleanText)
	kept := make([]uint16, 0, len(units))
	for _, u := range units {
		if u == '\n' || u >= 32 {
			kept = append(kept, u)
		}
	}
	filteredText := js.FromUTF16(kept)

	if strings.HasPrefix(filteredText, "/") || strings.HasPrefix(filteredText, "~") || strings.HasPrefix(filteredText, ".") {
		currentLine := e.currentLineLocked()
		charBeforeCursor := ""
		if e.state.cursorCol > 0 {
			charBeforeCursor = js.Slice(currentLine, e.state.cursorCol-1, e.state.cursorCol)
		}
		if charBeforeCursor != "" && ceAsciiWordCharRegex.Test(charBeforeCursor) {
			filteredText = " " + filteredText
		}
	}

	pastedLines := js.Split(filteredText, "\n")
	totalChars := js.Len(filteredText)
	if len(pastedLines) > 10 || totalChars > 1000 {
		e.pasteCounter++
		pasteId := e.pasteCounter
		if e.pastes == nil {
			e.pastes = omap.NewMap[int, string]()
		}
		e.pastes.Set(pasteId, filteredText)

		var marker string
		if len(pastedLines) > 10 {
			marker = fmt.Sprintf("[paste #%d +%d lines]", pasteId, len(pastedLines))
		} else {
			marker = fmt.Sprintf("[paste #%d %d chars]", pasteId, totalChars)
		}
		e.insertTextAtCursorInternalLocked(marker, cb)
		return
	}

	e.insertTextAtCursorInternalLocked(filteredText, cb)
}

func (e *Editor) handlePaste(pastedText string) {
	var cb []func()
	e.mu.Lock()
	e.handlePasteLocked(pastedText, &cb)
	e.mu.Unlock()
	ceRunCallbacks(cb)
}

func (e *Editor) addNewLineLocked(cb *[]func()) {
	e.cancelAutocompleteLocked()
	e.exitHistoryBrowsingLocked()
	e.lastAction = nil
	e.pushUndoSnapshotLocked()

	currentLine := e.currentLineLocked()
	before := js.Slice(currentLine, 0, e.state.cursorCol)
	after := js.SliceFrom(currentLine, e.state.cursorCol)

	e.state.lines[e.state.cursorLine] = before
	e.state.lines = slices.Insert(e.state.lines, e.state.cursorLine+1, after)

	e.state.cursorLine++
	e.setCursorColLocked(0)
	e.enqueueOnChangeLocked(cb)
}

func (e *Editor) addNewLine() {
	var cb []func()
	e.mu.Lock()
	e.addNewLineLocked(&cb)
	e.mu.Unlock()
	ceRunCallbacks(cb)
}

func (e *Editor) shouldSubmitOnBackslashEnterLocked(data string, kb *KeybindingsManager) bool {
	if e.DisableSubmit || !MatchesKey(data, "enter") || kb == nil {
		return false
	}
	submitKeys := kb.GetKeys(KeybindingTuiInputSubmit)
	hasShiftEnter := slices.Contains(submitKeys, KeyId("shift+enter")) || slices.Contains(submitKeys, KeyId("shift+return"))
	if !hasShiftEnter {
		return false
	}
	currentLine := e.currentLineLocked()
	return e.state.cursorCol > 0 && js.CharCodeAt(currentLine, e.state.cursorCol-1) == '\\'
}

func (e *Editor) shouldSubmitOnBackslashEnter(data string, kb *KeybindingsManager) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.shouldSubmitOnBackslashEnterLocked(data, kb)
}

func (e *Editor) submitValueLocked(cb *[]func()) {
	e.cancelAutocompleteLocked()
	result := js.Trim(e.expandPasteMarkersLocked(e.getTextLocked()))

	e.state = ceEditorState{lines: []string{""}, cursorLine: 0, cursorCol: 0}
	if e.pastes != nil {
		e.pastes.Clear()
	}
	e.pasteCounter = 0
	e.exitHistoryBrowsingLocked()
	e.scrollOffset = 0
	e.undoStack.Clear()
	e.lastAction = nil

	if e.OnChange != nil {
		fn := e.OnChange
		*cb = append(*cb, func() { fn("") })
	}
	if e.OnSubmit != nil {
		fn := e.OnSubmit
		*cb = append(*cb, func() { fn(result) })
	}
}

func (e *Editor) submitValue() {
	var cb []func()
	e.mu.Lock()
	e.submitValueLocked(&cb)
	e.mu.Unlock()
	ceRunCallbacks(cb)
}

func (e *Editor) handleBackspace() {
	var cb []func()
	e.mu.Lock()
	e.exitHistoryBrowsingLocked()
	e.lastAction = nil

	if e.state.cursorCol > 0 {
		e.pushUndoSnapshotLocked()

		line := e.currentLineLocked()
		beforeCursor := js.Slice(line, 0, e.state.cursorCol)
		graphemes := e.segmentLocked(beforeCursor, ceSegmentModeGrapheme)
		graphemeLength := 1
		var lastSeg string
		if len(graphemes) > 0 {
			lastSeg = graphemes[len(graphemes)-1].Segment
			graphemeLength = js.Len(lastSeg)
		}

		if m := cePasteMarkerSingle.Exec(lastSeg, 0); m != nil {
			targetId, err := strconv.Atoi(m.Group(1))
			if err == nil {
				if e.pastes != nil {
					e.pastes.Delete(targetId)
				}
				e.pasteCounter--

				var higherIds []int
				if e.pastes != nil {
					for _, id := range e.pastes.Keys() {
						if id > targetId {
							higherIds = append(higherIds, id)
						}
					}
					slices.Sort(higherIds)
					for _, id := range higherIds {
						if val, ok := e.pastes.Get(id); ok {
							e.pastes.Set(id-1, val)
						}
						e.pastes.Delete(id)
					}
				}

				for i, l := range e.state.lines {
					e.state.lines[i] = cePasteMarkerRegex.ReplaceFunc(l, func(pm *jsre.Match) string {
						x, convErr := strconv.Atoi(pm.Group(1))
						if convErr != nil || x <= targetId {
							return pm.Group(0)
						}
						return fmt.Sprintf("[paste #%d%s]", x-1, pm.Group(2))
					})
				}
			}
		}

		line = e.currentLineLocked()
		before := js.Slice(line, 0, e.state.cursorCol-graphemeLength)
		after := js.SliceFrom(line, e.state.cursorCol)
		e.state.lines[e.state.cursorLine] = before + after
		e.setCursorColLocked(e.state.cursorCol - graphemeLength)
	} else if e.state.cursorLine > 0 {
		e.pushUndoSnapshotLocked()

		currentLine := e.currentLineLocked()
		previousLine := e.state.lines[e.state.cursorLine-1]

		e.state.lines[e.state.cursorLine-1] = previousLine + currentLine
		e.state.lines = slices.Delete(e.state.lines, e.state.cursorLine, e.state.cursorLine+1)

		e.state.cursorLine--
		e.setCursorColLocked(js.Len(previousLine))
	}

	e.enqueueOnChangeLocked(&cb)

	action := 0 // 0=none, 1=update, 2=tryTrigger
	if e.autocompleteState != nil {
		action = 1
	} else {
		currentLine := e.currentLineLocked()
		textBeforeCursor := js.Slice(currentLine, 0, e.state.cursorCol)
		if e.isInSlashCommandContextLocked(textBeforeCursor) || e.autocompleteTriggerPattern.Test(textBeforeCursor) {
			action = 2
		}
	}
	e.mu.Unlock()

	ceRunCallbacks(cb)
	switch action {
	case 1:
		e.updateAutocomplete()
	case 2:
		e.tryTriggerAutocomplete(false)
	}
}

func (e *Editor) setCursorColLocked(col int) {
	e.state.cursorCol = col
	e.preferredVisualCol = nil
	e.snappedFromCursorCol = nil
}

func (e *Editor) setCursorCol(col int) {
	e.mu.Lock()
	e.setCursorColLocked(col)
	e.mu.Unlock()
}

func (e *Editor) moveToVisualLineLocked(visualLines []ceVisualLine, currentVisualLine int, targetVisualLine int) {
	if currentVisualLine < 0 || currentVisualLine >= len(visualLines) || targetVisualLine < 0 || targetVisualLine >= len(visualLines) {
		return
	}
	currentVL := visualLines[currentVisualLine]
	targetVL := visualLines[targetVisualLine]

	var currentVisualCol int
	if e.snappedFromCursorCol != nil {
		vlIndex := e.findVisualLineAtLocked(visualLines, currentVL.logicalLine, *e.snappedFromCursorCol)
		currentVisualCol = *e.snappedFromCursorCol - visualLines[vlIndex].startCol
	} else {
		currentVisualCol = e.state.cursorCol - currentVL.startCol
	}

	isLastSourceSegment := currentVisualLine == len(visualLines)-1 || visualLines[currentVisualLine+1].logicalLine != currentVL.logicalLine
	sourceMaxVisualCol := max(0, currentVL.length-1)
	if isLastSourceSegment {
		sourceMaxVisualCol = currentVL.length
	}

	isLastTargetSegment := targetVisualLine == len(visualLines)-1 || visualLines[targetVisualLine+1].logicalLine != targetVL.logicalLine
	targetMaxVisualCol := max(0, targetVL.length-1)
	if isLastTargetSegment {
		targetMaxVisualCol = targetVL.length
	}

	moveToVisualCol := e.computeVerticalMoveColumnLocked(currentVisualCol, sourceMaxVisualCol, targetMaxVisualCol)

	e.state.cursorLine = targetVL.logicalLine
	targetCol := targetVL.startCol + moveToVisualCol
	logicalLine := e.currentLineLocked()
	e.state.cursorCol = min(targetCol, js.Len(logicalLine))

	segments := e.segmentLocked(logicalLine, ceSegmentModeGrapheme)
	for _, seg := range segments {
		if seg.Index > e.state.cursorCol {
			break
		}
		segLen := js.Len(seg.Segment)
		if segLen <= 1 {
			continue
		}
		if e.state.cursorCol < seg.Index+segLen {
			isContinuation := seg.Index < targetVL.startCol
			isMovingDown := targetVisualLine > currentVisualLine

			if isContinuation && isMovingDown {
				segEnd := seg.Index + segLen
				next := targetVisualLine + 1
				for next < len(visualLines) &&
					visualLines[next].logicalLine == targetVL.logicalLine &&
					visualLines[next].startCol < segEnd {
					next++
				}
				if next < len(visualLines) {
					e.moveToVisualLineLocked(visualLines, currentVisualLine, next)
					return
				}
			}

			snapped := e.state.cursorCol
			e.snappedFromCursorCol = &snapped
			e.state.cursorCol = seg.Index
			return
		}
	}

	e.snappedFromCursorCol = nil
}

func (e *Editor) moveToVisualLine(visualLines []ceVisualLine, currentVisualLine int, targetVisualLine int) {
	e.mu.Lock()
	e.moveToVisualLineLocked(visualLines, currentVisualLine, targetVisualLine)
	e.mu.Unlock()
}

// computeVerticalMoveColumn applies the sticky-column decision table from editor.ts.
func (e *Editor) computeVerticalMoveColumnLocked(currentVisualCol int, sourceMaxVisualCol int, targetMaxVisualCol int) int {
	hasPreferred := e.preferredVisualCol != nil
	cursorInMiddle := currentVisualCol < sourceMaxVisualCol
	targetTooShort := targetMaxVisualCol < currentVisualCol

	if !hasPreferred || cursorInMiddle {
		if targetTooShort {
			pref := currentVisualCol
			e.preferredVisualCol = &pref
			return targetMaxVisualCol
		}
		e.preferredVisualCol = nil
		return currentVisualCol
	}

	targetCantFitPreferred := targetMaxVisualCol < *e.preferredVisualCol
	if targetTooShort || targetCantFitPreferred {
		return targetMaxVisualCol
	}

	result := *e.preferredVisualCol
	e.preferredVisualCol = nil
	return result
}

func (e *Editor) computeVerticalMoveColumn(currentVisualCol int, sourceMaxVisualCol int, targetMaxVisualCol int) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.computeVerticalMoveColumnLocked(currentVisualCol, sourceMaxVisualCol, targetMaxVisualCol)
}

func (e *Editor) moveToLineStartLocked() {
	e.lastAction = nil
	e.setCursorColLocked(0)
}

func (e *Editor) moveToLineStart() {
	e.mu.Lock()
	e.moveToLineStartLocked()
	e.mu.Unlock()
}

func (e *Editor) moveToLineEndLocked() {
	e.lastAction = nil
	e.setCursorColLocked(js.Len(e.currentLineLocked()))
}

func (e *Editor) moveToLineEnd() {
	e.mu.Lock()
	e.moveToLineEndLocked()
	e.mu.Unlock()
}

func (e *Editor) deleteToStartOfLineLocked(cb *[]func()) {
	e.exitHistoryBrowsingLocked()
	currentLine := e.currentLineLocked()

	if e.state.cursorCol > 0 {
		e.pushUndoSnapshotLocked()

		deletedText := js.Slice(currentLine, 0, e.state.cursorCol)
		accumulate := e.lastAction != nil && *e.lastAction == ceLastActionKill
		e.killRing.Push(deletedText, &KillRingPushOptions{Prepend: true, Accumulate: &accumulate})
		act := ceLastActionKill
		e.lastAction = &act

		e.state.lines[e.state.cursorLine] = js.SliceFrom(currentLine, e.state.cursorCol)
		e.setCursorColLocked(0)
	} else if e.state.cursorLine > 0 {
		e.pushUndoSnapshotLocked()

		accumulate := e.lastAction != nil && *e.lastAction == ceLastActionKill
		e.killRing.Push("\n", &KillRingPushOptions{Prepend: true, Accumulate: &accumulate})
		act := ceLastActionKill
		e.lastAction = &act

		previousLine := e.state.lines[e.state.cursorLine-1]
		e.state.lines[e.state.cursorLine-1] = previousLine + currentLine
		e.state.lines = slices.Delete(e.state.lines, e.state.cursorLine, e.state.cursorLine+1)
		e.state.cursorLine--
		e.setCursorColLocked(js.Len(previousLine))
	}

	e.enqueueOnChangeLocked(cb)
}

func (e *Editor) deleteToStartOfLine() {
	var cb []func()
	e.mu.Lock()
	e.deleteToStartOfLineLocked(&cb)
	e.mu.Unlock()
	ceRunCallbacks(cb)
}

func (e *Editor) deleteToEndOfLineLocked(cb *[]func()) {
	e.exitHistoryBrowsingLocked()
	currentLine := e.currentLineLocked()

	if e.state.cursorCol < js.Len(currentLine) {
		e.pushUndoSnapshotLocked()

		deletedText := js.SliceFrom(currentLine, e.state.cursorCol)
		accumulate := e.lastAction != nil && *e.lastAction == ceLastActionKill
		e.killRing.Push(deletedText, &KillRingPushOptions{Prepend: false, Accumulate: &accumulate})
		act := ceLastActionKill
		e.lastAction = &act

		e.state.lines[e.state.cursorLine] = js.Slice(currentLine, 0, e.state.cursorCol)
	} else if e.state.cursorLine < len(e.state.lines)-1 {
		e.pushUndoSnapshotLocked()

		accumulate := e.lastAction != nil && *e.lastAction == ceLastActionKill
		e.killRing.Push("\n", &KillRingPushOptions{Prepend: false, Accumulate: &accumulate})
		act := ceLastActionKill
		e.lastAction = &act

		nextLine := e.state.lines[e.state.cursorLine+1]
		e.state.lines[e.state.cursorLine] = currentLine + nextLine
		e.state.lines = slices.Delete(e.state.lines, e.state.cursorLine+1, e.state.cursorLine+2)
	}

	e.enqueueOnChangeLocked(cb)
}

func (e *Editor) deleteToEndOfLine() {
	var cb []func()
	e.mu.Lock()
	e.deleteToEndOfLineLocked(&cb)
	e.mu.Unlock()
	ceRunCallbacks(cb)
}

func (e *Editor) deleteWordBackwardsLocked(cb *[]func()) {
	e.exitHistoryBrowsingLocked()
	currentLine := e.currentLineLocked()

	if e.state.cursorCol == 0 {
		if e.state.cursorLine > 0 {
			e.pushUndoSnapshotLocked()

			accumulate := e.lastAction != nil && *e.lastAction == ceLastActionKill
			e.killRing.Push("\n", &KillRingPushOptions{Prepend: true, Accumulate: &accumulate})
			act := ceLastActionKill
			e.lastAction = &act

			previousLine := e.state.lines[e.state.cursorLine-1]
			e.state.lines[e.state.cursorLine-1] = previousLine + currentLine
			e.state.lines = slices.Delete(e.state.lines, e.state.cursorLine, e.state.cursorLine+1)
			e.state.cursorLine--
			e.setCursorColLocked(js.Len(previousLine))
		}
	} else {
		e.pushUndoSnapshotLocked()

		wasKill := e.lastAction != nil && *e.lastAction == ceLastActionKill
		oldCursorCol := e.state.cursorCol
		e.moveWordBackwardsLocked()
		deleteFrom := e.state.cursorCol
		e.setCursorColLocked(oldCursorCol)

		deletedText := js.Slice(currentLine, deleteFrom, e.state.cursorCol)
		e.killRing.Push(deletedText, &KillRingPushOptions{Prepend: true, Accumulate: &wasKill})
		act := ceLastActionKill
		e.lastAction = &act

		e.state.lines[e.state.cursorLine] = js.Slice(currentLine, 0, deleteFrom) + js.SliceFrom(currentLine, e.state.cursorCol)
		e.setCursorColLocked(deleteFrom)
	}

	e.enqueueOnChangeLocked(cb)
}

func (e *Editor) deleteWordBackwards() {
	var cb []func()
	e.mu.Lock()
	e.deleteWordBackwardsLocked(&cb)
	e.mu.Unlock()
	ceRunCallbacks(cb)
}

func (e *Editor) deleteWordForwardLocked(cb *[]func()) {
	e.exitHistoryBrowsingLocked()
	currentLine := e.currentLineLocked()

	if e.state.cursorCol >= js.Len(currentLine) {
		if e.state.cursorLine < len(e.state.lines)-1 {
			e.pushUndoSnapshotLocked()

			accumulate := e.lastAction != nil && *e.lastAction == ceLastActionKill
			e.killRing.Push("\n", &KillRingPushOptions{Prepend: false, Accumulate: &accumulate})
			act := ceLastActionKill
			e.lastAction = &act

			nextLine := e.state.lines[e.state.cursorLine+1]
			e.state.lines[e.state.cursorLine] = currentLine + nextLine
			e.state.lines = slices.Delete(e.state.lines, e.state.cursorLine+1, e.state.cursorLine+2)
		}
	} else {
		e.pushUndoSnapshotLocked()

		wasKill := e.lastAction != nil && *e.lastAction == ceLastActionKill
		oldCursorCol := e.state.cursorCol
		e.moveWordForwardsLocked()
		deleteTo := e.state.cursorCol
		e.setCursorColLocked(oldCursorCol)

		deletedText := js.Slice(currentLine, e.state.cursorCol, deleteTo)
		e.killRing.Push(deletedText, &KillRingPushOptions{Prepend: false, Accumulate: &wasKill})
		act := ceLastActionKill
		e.lastAction = &act

		e.state.lines[e.state.cursorLine] = js.Slice(currentLine, 0, e.state.cursorCol) + js.SliceFrom(currentLine, deleteTo)
	}

	e.enqueueOnChangeLocked(cb)
}

func (e *Editor) deleteWordForward() {
	var cb []func()
	e.mu.Lock()
	e.deleteWordForwardLocked(&cb)
	e.mu.Unlock()
	ceRunCallbacks(cb)
}

func (e *Editor) handleForwardDelete() {
	var cb []func()
	e.mu.Lock()
	e.exitHistoryBrowsingLocked()
	e.lastAction = nil

	currentLine := e.currentLineLocked()
	if e.state.cursorCol < js.Len(currentLine) {
		e.pushUndoSnapshotLocked()

		afterCursor := js.SliceFrom(currentLine, e.state.cursorCol)
		graphemes := e.segmentLocked(afterCursor, ceSegmentModeGrapheme)
		graphemeLength := 1
		if len(graphemes) > 0 {
			graphemeLength = js.Len(graphemes[0].Segment)
		}

		before := js.Slice(currentLine, 0, e.state.cursorCol)
		after := js.SliceFrom(currentLine, e.state.cursorCol+graphemeLength)
		e.state.lines[e.state.cursorLine] = before + after
	} else if e.state.cursorLine < len(e.state.lines)-1 {
		e.pushUndoSnapshotLocked()

		nextLine := e.state.lines[e.state.cursorLine+1]
		e.state.lines[e.state.cursorLine] = currentLine + nextLine
		e.state.lines = slices.Delete(e.state.lines, e.state.cursorLine+1, e.state.cursorLine+2)
	}

	e.enqueueOnChangeLocked(&cb)

	action := 0 // 0=none, 1=update, 2=tryTrigger
	if e.autocompleteState != nil {
		action = 1
	} else {
		lineAfter := e.currentLineLocked()
		textBeforeCursor := js.Slice(lineAfter, 0, e.state.cursorCol)
		if e.isInSlashCommandContextLocked(textBeforeCursor) || e.autocompleteTriggerPattern.Test(textBeforeCursor) {
			action = 2
		}
	}
	e.mu.Unlock()

	ceRunCallbacks(cb)
	switch action {
	case 1:
		e.updateAutocomplete()
	case 2:
		e.tryTriggerAutocomplete(false)
	}
}

func (e *Editor) buildVisualLineMapLocked(width int) []ceVisualLine {
	visualLines := []ceVisualLine{}

	for i, line := range e.state.lines {
		lineLen := js.Len(line)
		lineVisWidth := VisibleWidth(line)
		if lineLen == 0 {
			visualLines = append(visualLines, ceVisualLine{logicalLine: i, startCol: 0, length: 0})
		} else if lineVisWidth <= width {
			visualLines = append(visualLines, ceVisualLine{logicalLine: i, startCol: 0, length: lineLen})
		} else {
			chunks := WordWrapLine(line, width, e.segmentLocked(line, ceSegmentModeGrapheme))
			for _, chunk := range chunks {
				visualLines = append(visualLines, ceVisualLine{
					logicalLine: i,
					startCol:    chunk.StartIndex,
					length:      chunk.EndIndex - chunk.StartIndex,
				})
			}
		}
	}

	return visualLines
}

func (e *Editor) buildVisualLineMap(width int) []ceVisualLine {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.buildVisualLineMapLocked(width)
}

func (e *Editor) findVisualLineAtLocked(visualLines []ceVisualLine, line int, col int) int {
	for i, vl := range visualLines {
		if vl.logicalLine != line {
			continue
		}
		offset := col - vl.startCol
		isLastSegmentOfLine := i == len(visualLines)-1 || visualLines[i+1].logicalLine != vl.logicalLine
		if offset >= 0 && (offset < vl.length || (isLastSegmentOfLine && offset == vl.length)) {
			return i
		}
	}
	return len(visualLines) - 1
}

func (e *Editor) findVisualLineAt(visualLines []ceVisualLine, line int, col int) int {
	return e.findVisualLineAtLocked(visualLines, line, col)
}

func (e *Editor) findCurrentVisualLineLocked(visualLines []ceVisualLine) int {
	return e.findVisualLineAtLocked(visualLines, e.state.cursorLine, e.state.cursorCol)
}

func (e *Editor) findCurrentVisualLine(visualLines []ceVisualLine) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.findCurrentVisualLineLocked(visualLines)
}

func (e *Editor) moveCursorLocked(deltaLine int, deltaCol int) bool {
	e.lastAction = nil
	visualLines := e.buildVisualLineMapLocked(e.lastWidth)
	currentVisualLine := e.findCurrentVisualLineLocked(visualLines)

	if deltaLine != 0 {
		targetVisualLine := currentVisualLine + deltaLine
		if targetVisualLine >= 0 && targetVisualLine < len(visualLines) {
			e.moveToVisualLineLocked(visualLines, currentVisualLine, targetVisualLine)
		}
	}

	if deltaCol != 0 {
		currentLine := e.currentLineLocked()
		currentLineLen := js.Len(currentLine)

		if deltaCol > 0 {
			if e.state.cursorCol < currentLineLen {
				afterCursor := js.SliceFrom(currentLine, e.state.cursorCol)
				graphemes := e.segmentLocked(afterCursor, ceSegmentModeGrapheme)
				step := 1
				if len(graphemes) > 0 {
					step = js.Len(graphemes[0].Segment)
				}
				e.setCursorColLocked(e.state.cursorCol + step)
			} else if e.state.cursorLine < len(e.state.lines)-1 {
				e.state.cursorLine++
				e.setCursorColLocked(0)
			} else if currentVisualLine >= 0 && currentVisualLine < len(visualLines) {
				pref := e.state.cursorCol - visualLines[currentVisualLine].startCol
				e.preferredVisualCol = &pref
			}
		} else {
			if e.state.cursorCol > 0 {
				beforeCursor := js.Slice(currentLine, 0, e.state.cursorCol)
				graphemes := e.segmentLocked(beforeCursor, ceSegmentModeGrapheme)
				step := 1
				if len(graphemes) > 0 {
					step = js.Len(graphemes[len(graphemes)-1].Segment)
				}
				e.setCursorColLocked(e.state.cursorCol - step)
			} else if e.state.cursorLine > 0 {
				e.state.cursorLine--
				prevLine := e.state.lines[e.state.cursorLine]
				e.setCursorColLocked(js.Len(prevLine))
			}
		}
	}

	return e.autocompleteState != nil
}

func (e *Editor) moveCursor(deltaLine int, deltaCol int) {
	e.mu.Lock()
	needUpdateAC := e.moveCursorLocked(deltaLine, deltaCol)
	e.mu.Unlock()
	if needUpdateAC {
		e.updateAutocomplete()
	}
}

func (e *Editor) pageScroll(direction int) {
	e.mu.Lock()
	t := e.Tui
	e.mu.Unlock()

	terminalRows := 24
	if t != nil {
		if term := t.Terminal(); term != nil {
			terminalRows = term.Rows()
		}
	}
	pageSize := max(5, (terminalRows*3)/10)

	e.mu.Lock()
	e.lastAction = nil
	visualLines := e.buildVisualLineMapLocked(e.lastWidth)
	currentVisualLine := e.findCurrentVisualLineLocked(visualLines)
	targetVisualLine := max(0, min(len(visualLines)-1, currentVisualLine+direction*pageSize))
	e.moveToVisualLineLocked(visualLines, currentVisualLine, targetVisualLine)
	e.mu.Unlock()
}

func (e *Editor) moveWordBackwardsLocked() {
	e.lastAction = nil
	currentLine := e.currentLineLocked()

	if e.state.cursorCol == 0 {
		if e.state.cursorLine > 0 {
			e.state.cursorLine--
			prevLine := e.state.lines[e.state.cursorLine]
			e.setCursorColLocked(js.Len(prevLine))
		}
		return
	}

	e.setCursorColLocked(FindWordBackward(currentLine, e.state.cursorCol, &WordNavigationOptions{
		Segment: func(text string) []SegmentData {
			return e.segmentLocked(text, ceSegmentModeWord)
		},
		IsAtomicSegment: ceIsPasteMarker,
	}))
}

func (e *Editor) moveWordBackwards() {
	e.mu.Lock()
	e.moveWordBackwardsLocked()
	e.mu.Unlock()
}

func (e *Editor) yankLocked(cb *[]func()) {
	if e.killRing.Length() == 0 {
		return
	}
	e.pushUndoSnapshotLocked()
	text := e.killRing.Peek()
	if text != nil {
		e.insertYankedTextLocked(*text, cb)
	}
	act := ceLastActionYank
	e.lastAction = &act
}

func (e *Editor) yank() {
	var cb []func()
	e.mu.Lock()
	e.yankLocked(&cb)
	e.mu.Unlock()
	ceRunCallbacks(cb)
}

func (e *Editor) yankPopLocked(cb *[]func()) {
	if e.lastAction == nil || *e.lastAction != ceLastActionYank || e.killRing.Length() <= 1 {
		return
	}
	e.pushUndoSnapshotLocked()
	e.deleteYankedTextLocked(cb)
	e.killRing.Rotate()
	text := e.killRing.Peek()
	if text != nil {
		e.insertYankedTextLocked(*text, cb)
	}
	act := ceLastActionYank
	e.lastAction = &act
}

func (e *Editor) yankPop() {
	var cb []func()
	e.mu.Lock()
	e.yankPopLocked(&cb)
	e.mu.Unlock()
	ceRunCallbacks(cb)
}

func (e *Editor) insertYankedTextLocked(text string, cb *[]func()) {
	e.exitHistoryBrowsingLocked()
	lines := js.Split(text, "\n")

	if len(lines) == 1 {
		currentLine := e.currentLineLocked()
		before := js.Slice(currentLine, 0, e.state.cursorCol)
		after := js.SliceFrom(currentLine, e.state.cursorCol)
		e.state.lines[e.state.cursorLine] = before + text + after
		e.setCursorColLocked(e.state.cursorCol + js.Len(text))
	} else {
		currentLine := e.currentLineLocked()
		before := js.Slice(currentLine, 0, e.state.cursorCol)
		after := js.SliceFrom(currentLine, e.state.cursorCol)

		e.state.lines[e.state.cursorLine] = before + lines[0]
		for i := 1; i < len(lines)-1; i++ {
			e.state.lines = slices.Insert(e.state.lines, e.state.cursorLine+i, lines[i])
		}
		lastLineIndex := e.state.cursorLine + len(lines) - 1
		lastLineText := lines[len(lines)-1]
		e.state.lines = slices.Insert(e.state.lines, lastLineIndex, lastLineText+after)

		e.state.cursorLine = lastLineIndex
		e.setCursorColLocked(js.Len(lastLineText))
	}

	e.enqueueOnChangeLocked(cb)
}

func (e *Editor) insertYankedText(text string) {
	var cb []func()
	e.mu.Lock()
	e.insertYankedTextLocked(text, &cb)
	e.mu.Unlock()
	ceRunCallbacks(cb)
}

func (e *Editor) deleteYankedTextLocked(cb *[]func()) {
	yankedText := e.killRing.Peek()
	if yankedText == nil || *yankedText == "" {
		return
	}

	yankLines := js.Split(*yankedText, "\n")
	if len(yankLines) == 1 {
		currentLine := e.currentLineLocked()
		deleteLen := js.Len(*yankedText)
		before := js.Slice(currentLine, 0, e.state.cursorCol-deleteLen)
		after := js.SliceFrom(currentLine, e.state.cursorCol)
		e.state.lines[e.state.cursorLine] = before + after
		e.setCursorColLocked(e.state.cursorCol - deleteLen)
	} else {
		startLine := e.state.cursorLine - (len(yankLines) - 1)
		startCol := js.Len(e.state.lines[startLine]) - js.Len(yankLines[0])
		afterCursor := js.SliceFrom(e.currentLineLocked(), e.state.cursorCol)
		beforeYank := js.Slice(e.state.lines[startLine], 0, startCol)
		e.state.lines = slices.Replace(e.state.lines, startLine, startLine+len(yankLines), beforeYank+afterCursor)
		e.state.cursorLine = startLine
		e.setCursorColLocked(startCol)
	}

	e.enqueueOnChangeLocked(cb)
}

func (e *Editor) deleteYankedText() {
	var cb []func()
	e.mu.Lock()
	e.deleteYankedTextLocked(&cb)
	e.mu.Unlock()
	ceRunCallbacks(cb)
}

func (e *Editor) pushUndoSnapshotLocked() {
	e.undoStack.Push(ceCloneSnapshot(ceEditorSnapshot{
		state:        e.state,
		pastes:       e.pastes,
		pasteCounter: e.pasteCounter,
	}))
}

func (e *Editor) pushUndoSnapshot() {
	e.mu.Lock()
	e.pushUndoSnapshotLocked()
	e.mu.Unlock()
}

func (e *Editor) undoLocked(cb *[]func()) {
	e.exitHistoryBrowsingLocked()
	snapshot := e.undoStack.Pop()
	if snapshot == nil {
		return
	}
	cloned := ceCloneSnapshot(*snapshot)
	e.state = cloned.state
	e.pastes = cloned.pastes
	e.pasteCounter = cloned.pasteCounter
	e.lastAction = nil
	e.preferredVisualCol = nil
	e.enqueueOnChangeLocked(cb)
}

func (e *Editor) undo() {
	var cb []func()
	e.mu.Lock()
	e.undoLocked(&cb)
	e.mu.Unlock()
	ceRunCallbacks(cb)
}

func (e *Editor) jumpToCharLocked(char string, direction ceJumpMode) {
	e.lastAction = nil
	isForward := direction == ceJumpModeForward
	lines := e.state.lines

	end := -1
	step := -1
	if isForward {
		end = len(lines)
		step = 1
	}

	for lineIdx := e.state.cursorLine; lineIdx != end; lineIdx += step {
		line := lines[lineIdx]
		isCurrentLine := lineIdx == e.state.cursorLine

		idx := -1
		if isForward {
			from := 0
			if isCurrentLine {
				from = e.state.cursorCol + 1
			}
			idx = js.IndexOf(line, char, from)
		} else {
			if isCurrentLine {
				if e.state.cursorCol > 0 {
					idx = js.LastIndexOfFrom(line, char, e.state.cursorCol-1)
				}
			} else {
				idx = js.LastIndexOf(line, char)
			}
		}

		if idx != -1 {
			e.state.cursorLine = lineIdx
			e.setCursorColLocked(idx)
			return
		}
	}
}

func (e *Editor) jumpToChar(char string, direction ceJumpMode) {
	e.mu.Lock()
	e.jumpToCharLocked(char, direction)
	e.mu.Unlock()
}

func (e *Editor) moveWordForwardsLocked() {
	e.lastAction = nil
	currentLine := e.currentLineLocked()

	if e.state.cursorCol >= js.Len(currentLine) {
		if e.state.cursorLine < len(e.state.lines)-1 {
			e.state.cursorLine++
			e.setCursorColLocked(0)
		}
		return
	}

	e.setCursorColLocked(FindWordForward(currentLine, e.state.cursorCol, &WordNavigationOptions{
		Segment: func(text string) []SegmentData {
			return e.segmentLocked(text, ceSegmentModeWord)
		},
		IsAtomicSegment: ceIsPasteMarker,
	}))
}

func (e *Editor) moveWordForwards() {
	e.mu.Lock()
	e.moveWordForwardsLocked()
	e.mu.Unlock()
}

func (e *Editor) isSlashMenuAllowedLocked() bool {
	return e.state.cursorLine == 0
}

func (e *Editor) isSlashMenuAllowed() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.isSlashMenuAllowedLocked()
}

func (e *Editor) isAtStartOfMessageLocked() bool {
	if !e.isSlashMenuAllowedLocked() {
		return false
	}
	currentLine := e.currentLineLocked()
	beforeCursor := js.Slice(currentLine, 0, e.state.cursorCol)
	trimmed := js.Trim(beforeCursor)
	return trimmed == "" || trimmed == "/"
}

func (e *Editor) isAtStartOfMessage() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.isAtStartOfMessageLocked()
}

func (e *Editor) isInSlashCommandContextLocked(textBeforeCursor string) bool {
	return e.isSlashMenuAllowedLocked() && strings.HasPrefix(js.TrimStart(textBeforeCursor), "/")
}

func (e *Editor) isInSlashCommandContext(textBeforeCursor string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.isInSlashCommandContextLocked(textBeforeCursor)
}

func (e *Editor) getBestAutocompleteMatchIndex(items []AutocompleteItem, prefix string) int {
	if prefix == "" {
		return -1
	}
	firstPrefixIndex := -1
	for i, item := range items {
		if item.Value == prefix {
			return i
		}
		if firstPrefixIndex == -1 && strings.HasPrefix(item.Value, prefix) {
			firstPrefixIndex = i
		}
	}
	return firstPrefixIndex
}

func (e *Editor) createAutocompleteList(prefix string, items []AutocompleteItem) *SelectList {
	var layout *SelectListLayoutOptions
	if strings.HasPrefix(prefix, "/") {
		layout = &ceSlashCommandSelectListLayout
	}
	selectItems := make([]SelectItem, len(items))
	for i, it := range items {
		selectItems[i] = SelectItem{
			Value:       it.Value,
			Label:       it.Label,
			Description: it.Description,
		}
	}

	e.mu.Lock()
	maxVisible := e.autocompleteMaxVisible
	theme := e.theme.SelectList
	e.mu.Unlock()

	list := NewSelectList(selectItems, maxVisible, theme, layout)
	list.OnSelect = func(selected SelectItem) {
		e.mu.Lock()
		provider := e.autocompleteProvider
		if provider == nil {
			e.mu.Unlock()
			return
		}
		e.pushUndoSnapshotLocked()
		e.lastAction = nil
		linesCopy := slices.Clone(e.state.lines)
		curLine := e.state.cursorLine
		curCol := e.state.cursorCol
		acPrefix := e.autocompletePrefix
		e.mu.Unlock()

		var item AutocompleteItem
		for _, cand := range items {
			if cand.Value == selected.Value {
				item = cand
				break
			}
		}
		if item.Value == "" && item.Label == "" {
			item = AutocompleteItem{
				Value:       selected.Value,
				Label:       selected.Label,
				Description: selected.Description,
			}
		}

		result := provider.ApplyCompletion(linesCopy, curLine, curCol, item, acPrefix)

		var cb []func()
		e.mu.Lock()
		e.state.lines = result.Lines
		e.state.cursorLine = result.CursorLine
		e.setCursorColLocked(result.CursorCol)
		e.cancelAutocompleteLocked()
		e.enqueueOnChangeLocked(&cb)
		e.mu.Unlock()
		ceRunCallbacks(cb)
	}
	return list
}

func (e *Editor) tryTriggerAutocomplete(explicitTab bool) {
	e.requestAutocomplete(&ceAutocompleteRequestOptions{force: false, explicitTab: explicitTab})
}

func (e *Editor) handleTabCompletion() {
	e.mu.Lock()
	if e.autocompleteProvider == nil {
		e.mu.Unlock()
		return
	}
	currentLine := e.currentLineLocked()
	beforeCursor := js.Slice(currentLine, 0, e.state.cursorCol)
	slashCtx := e.isInSlashCommandContextLocked(beforeCursor) && !strings.Contains(js.TrimStart(beforeCursor), " ")
	e.mu.Unlock()

	if slashCtx {
		e.handleSlashCommandCompletion()
	} else {
		e.forceFileAutocomplete(true)
	}
}

func (e *Editor) handleSlashCommandCompletion() {
	e.requestAutocomplete(&ceAutocompleteRequestOptions{force: false, explicitTab: true})
}

func (e *Editor) forceFileAutocomplete(explicitTab bool) {
	e.requestAutocomplete(&ceAutocompleteRequestOptions{force: true, explicitTab: explicitTab})
}

func (e *Editor) requestAutocomplete(options *ceAutocompleteRequestOptions) {
	if options == nil {
		options = &ceAutocompleteRequestOptions{}
	}

	e.mu.Lock()
	provider := e.autocompleteProvider
	if provider == nil {
		e.mu.Unlock()
		return
	}
	if options.force {
		linesCopy := slices.Clone(e.state.lines)
		curLine := e.state.cursorLine
		curCol := e.state.cursorCol
		e.mu.Unlock()

		if !provider.ShouldTriggerFileCompletion(linesCopy, curLine, curCol) {
			return
		}

		e.mu.Lock()
		if e.autocompleteProvider == nil {
			e.mu.Unlock()
			return
		}
	}

	e.cancelAutocompleteRequestLocked()
	e.autocompleteStartToken++
	startToken := e.autocompleteStartToken
	debounceMs := e.getAutocompleteDebounceMsLocked(options)

	if debounceMs > 0 {
		optsCopy := *options
		e.autocompleteDebounceTimer = time.AfterFunc(time.Duration(debounceMs)*time.Millisecond, func() {
			e.mu.Lock()
			if e.autocompleteStartToken == startToken {
				e.autocompleteDebounceTimer = nil
			}
			e.mu.Unlock()
			_ = e.startAutocompleteRequest(startToken, &optsCopy)
		})
		e.mu.Unlock()
		return
	}
	e.mu.Unlock()

	optsCopy := *options
	go func() {
		_ = e.startAutocompleteRequest(startToken, &optsCopy)
	}()
}

func (e *Editor) startAutocompleteRequest(startToken int, options *ceAutocompleteRequestOptions) error {
	if options == nil {
		options = &ceAutocompleteRequestOptions{}
	}
	done := make(chan struct{})
	defer close(done)

	e.mu.Lock()
	prev := e.autocompleteRequestTask
	e.autocompleteRequestTask = func() error {
		<-done
		return nil
	}
	e.mu.Unlock()

	if prev != nil {
		_ = prev()
	}

	e.mu.Lock()
	if startToken != e.autocompleteStartToken || e.autocompleteProvider == nil {
		e.mu.Unlock()
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	e.autocompleteAbort = cancel
	e.autocompleteRequestId++
	requestId := e.autocompleteRequestId
	snapshotText := e.getTextLocked()
	snapshotLine := e.state.cursorLine
	snapshotCol := e.state.cursorCol
	e.mu.Unlock()

	err := e.runAutocompleteRequest(ctx, requestId, snapshotText, snapshotLine, snapshotCol, options)
	cancel()
	return err
}

func (e *Editor) setAutocompleteTriggerCharactersLocked(triggerCharacters []string) {
	next := slices.Clone(ceDefaultAutocompleteTriggerCharacters)
	for _, character := range triggerCharacters {
		if js.Len(character) != 1 || character == "/" || IsWhitespaceChar(character) || slices.Contains(next, character) {
			continue
		}
		next = append(next, character)
	}
	e.autocompleteTriggerCharacters = next
	e.autocompleteTriggerPattern = ceBuildTriggerPattern(next)
	e.autocompleteDebouncePattern = ceBuildDebouncePattern(next)
}

func (e *Editor) setAutocompleteTriggerCharacters(triggerCharacters []string) {
	e.mu.Lock()
	e.setAutocompleteTriggerCharactersLocked(triggerCharacters)
	e.mu.Unlock()
}

func (e *Editor) getAutocompleteDebounceMsLocked(options *ceAutocompleteRequestOptions) int64 {
	if options != nil && (options.explicitTab || options.force) {
		return 0
	}
	currentLine := e.currentLineLocked()
	textBeforeCursor := js.Slice(currentLine, 0, e.state.cursorCol)
	if e.autocompleteDebouncePattern.Test(textBeforeCursor) {
		return ceAttachmentAutocompleteDebounceMs
	}
	return 0
}

func (e *Editor) getAutocompleteDebounceMs(options *ceAutocompleteRequestOptions) int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.getAutocompleteDebounceMsLocked(options)
}

func (e *Editor) runAutocompleteRequest(ctx context.Context, requestId int, snapshotText string, snapshotLine int, snapshotCol int, options *ceAutocompleteRequestOptions) error {
	e.mu.Lock()
	provider := e.autocompleteProvider
	if provider == nil {
		e.mu.Unlock()
		return nil
	}
	linesCopy := slices.Clone(e.state.lines)
	curLine := e.state.cursorLine
	curCol := e.state.cursorCol
	e.mu.Unlock()

	force := options != nil && options.force
	suggestions, err := provider.GetSuggestions(ctx, linesCopy, curLine, curCol, &AutocompleteSuggestionsOptions{
		Force: &force,
	})
	if err != nil {
		return err
	}

	e.mu.Lock()
	if !e.isAutocompleteRequestCurrentLocked(ctx, requestId, snapshotText, snapshotLine, snapshotCol) {
		e.mu.Unlock()
		return nil
	}
	e.autocompleteAbort = nil
	t := e.Tui

	if suggestions == nil || len(suggestions.Items) == 0 {
		e.cancelAutocompleteLocked()
		e.mu.Unlock()
		if t != nil {
			t.RequestRender(nil)
		}
		return nil
	}

	if options != nil && options.force && options.explicitTab && len(suggestions.Items) == 1 {
		item := suggestions.Items[0]
		e.pushUndoSnapshotLocked()
		e.lastAction = nil
		applyLines := slices.Clone(e.state.lines)
		applyLine := e.state.cursorLine
		applyCol := e.state.cursorCol
		prov := e.autocompleteProvider
		e.mu.Unlock()

		if prov == nil {
			return nil
		}
		result := prov.ApplyCompletion(applyLines, applyLine, applyCol, item, suggestions.Prefix)

		var cb []func()
		e.mu.Lock()
		e.state.lines = result.Lines
		e.state.cursorLine = result.CursorLine
		e.setCursorColLocked(result.CursorCol)
		e.enqueueOnChangeLocked(&cb)
		t = e.Tui
		e.mu.Unlock()

		ceRunCallbacks(cb)
		if t != nil {
			t.RequestRender(nil)
		}
		return nil
	}

	state := ceAutocompleteStateRegular
	if options != nil && options.force {
		state = ceAutocompleteStateForce
	}
	e.mu.Unlock()

	e.applyAutocompleteSuggestions(suggestions, state)

	e.mu.Lock()
	t = e.Tui
	e.mu.Unlock()
	if t != nil {
		t.RequestRender(nil)
	}
	return nil
}

func (e *Editor) isAutocompleteRequestCurrentLocked(ctx context.Context, requestId int, snapshotText string, snapshotLine int, snapshotCol int) bool {
	aborted := ctx != nil && ctx.Err() != nil
	return !aborted &&
		requestId == e.autocompleteRequestId &&
		e.getTextLocked() == snapshotText &&
		e.state.cursorLine == snapshotLine &&
		e.state.cursorCol == snapshotCol
}

func (e *Editor) isAutocompleteRequestCurrent(ctx context.Context, requestId int, snapshotText string, snapshotLine int, snapshotCol int) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.isAutocompleteRequestCurrentLocked(ctx, requestId, snapshotText, snapshotLine, snapshotCol)
}

func (e *Editor) applyAutocompleteSuggestions(suggestions *AutocompleteSuggestions, state ceAutocompleteState) {
	list := e.createAutocompleteList(suggestions.Prefix, suggestions.Items)
	bestMatchIndex := e.getBestAutocompleteMatchIndex(suggestions.Items, suggestions.Prefix)
	if bestMatchIndex >= 0 {
		list.SetSelectedIndex(bestMatchIndex)
	}

	e.mu.Lock()
	e.autocompletePrefix = suggestions.Prefix
	e.autocompleteList = list
	st := state
	e.autocompleteState = &st
	e.mu.Unlock()
}

func (e *Editor) cancelAutocompleteRequestLocked() {
	e.autocompleteStartToken++
	if e.autocompleteDebounceTimer != nil {
		e.autocompleteDebounceTimer.Stop()
		e.autocompleteDebounceTimer = nil
	}
	if e.autocompleteAbort != nil {
		e.autocompleteAbort()
		e.autocompleteAbort = nil
	}
}

func (e *Editor) cancelAutocompleteRequest() {
	e.mu.Lock()
	e.cancelAutocompleteRequestLocked()
	e.mu.Unlock()
}

func (e *Editor) clearAutocompleteUiLocked() {
	e.autocompleteState = nil
	e.autocompleteList = nil
	e.autocompletePrefix = ""
}

func (e *Editor) clearAutocompleteUi() {
	e.mu.Lock()
	e.clearAutocompleteUiLocked()
	e.mu.Unlock()
}

func (e *Editor) cancelAutocompleteLocked() {
	e.cancelAutocompleteRequestLocked()
	e.clearAutocompleteUiLocked()
}

func (e *Editor) cancelAutocomplete() {
	e.mu.Lock()
	e.cancelAutocompleteLocked()
	e.mu.Unlock()
}

func (e *Editor) IsShowingAutocomplete() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.autocompleteState != nil
}

func (e *Editor) updateAutocomplete() {
	e.mu.Lock()
	if e.autocompleteState == nil || e.autocompleteProvider == nil {
		e.mu.Unlock()
		return
	}
	force := *e.autocompleteState == ceAutocompleteStateForce
	e.mu.Unlock()
	e.requestAutocomplete(&ceAutocompleteRequestOptions{force: force, explicitTab: false})
}

var (
	_ Component = (*Editor)(nil)
	_ Focusable = (*Editor)(nil)
)

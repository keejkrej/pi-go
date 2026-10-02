// Ported from packages/tui/src/components/editor.ts (pi v1.0.0).

package tui

import (
	"context"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/internal/jsre"
	"github.com/keejkrej/pi-go/internal/omap"
)

// cePasteMarkerRegex matches paste markers such as "[paste #1 +123 lines]" or "[paste #2 1234 chars]".
var cePasteMarkerRegex = jsre.MustCompile(`\[paste #(\d+)( (\+\d+ lines|\d+ chars))?\]`, "g")

// cePasteMarkerSingle is the non-global form, for testing one segment.
var cePasteMarkerSingle = jsre.MustCompile(`^\[paste #(\d+)( (\+\d+ lines|\d+ chars))?\]$`, "")

// ceSegmenter is the Intl.Segmenter surface editor.ts uses (segment → segments).
type ceSegmenter interface {
	Segment(text string) []SegmentData
}

// TS const graphemeSegmenter = getGraphemeSegmenter(). Nil so package init does not call an unported function.
var ceGraphemeSegmenter ceSegmenter

// TS const wordSegmenter = getWordSegmenter(). Nil so package init does not call an unported function.
var ceWordSegmenter ceSegmenter

func ceIsPasteMarker(segment string) bool {
	panic("unported: ceIsPasteMarker")
}

func ceSegmentWithMarkers(text string, base ceSegmenter, validIds *omap.Set[int]) []SegmentData {
	panic("unported: ceSegmentWithMarkers")
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
	panic("unported: WordWrapLine")
}

// ceEditorState is the text and cursor. Cursor indexes are TS string indexes.
type ceEditorState struct {
	lines      []string
	cursorLine int
	cursorCol  int
}

// ceEditorSnapshot is an undo snapshot: text state plus the paste registry.
type ceEditorSnapshot struct {
	state        ceEditorState
	pastes       *omap.Map[int, string]
	pasteCounter int
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
	panic("unported: ceEscapeCharacterClass")
}

func ceBuildTriggerPattern(triggerCharacters []string) *jsre.Regexp {
	panic("unported: ceBuildTriggerPattern")
}

func ceBuildDebouncePattern(triggerCharacters []string) *jsre.Regexp {
	panic("unported: ceBuildDebouncePattern")
}

func ceCreateScrollBorder(direction ceScrollDirection, hiddenLineCount int, width int) string {
	panic("unported: ceCreateScrollBorder")
}

// Editor is the multiline text input. It implements Component and Focusable.
// CustomEditor embeds *Editor and overrides HandleInput and RenderTopBorder.
// Editor.Render and the paste-tail path must call the outermost override
// (TS this.renderTopBorder / this.renderBottomBorder / this.handleInput).
type Editor struct {
	mu sync.Mutex

	state ceEditorState
	// Focused is set by TUI when focus changes. Render emits the cursor marker when it is set.
	Focused bool

	// Tui is the owning screen (TS protected).
	Tui      TUI
	theme    EditorTheme
	paddingX int

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
	panic("unported: NewEditor")
}

func (e *Editor) validPasteIds() *omap.Set[int] {
	panic("unported: Editor.validPasteIds")
}

func (e *Editor) segment(text string, mode ceSegmentMode) []SegmentData {
	panic("unported: Editor.segment")
}

func (e *Editor) GetPaddingX() int { return e.paddingX }

func (e *Editor) SetPaddingX(padding int) {
	panic("unported: Editor.SetPaddingX")
}

func (e *Editor) GetAutocompleteMaxVisible() int { return e.autocompleteMaxVisible }

func (e *Editor) SetAutocompleteMaxVisible(maxVisible int) {
	panic("unported: Editor.SetAutocompleteMaxVisible")
}

func (e *Editor) SetAutocompleteProvider(provider AutocompleteProvider) {
	panic("unported: Editor.SetAutocompleteProvider")
}

// AddToHistory records a submitted prompt for up/down navigation.
func (e *Editor) AddToHistory(text string) {
	panic("unported: Editor.AddToHistory")
}

func (e *Editor) isEditorEmpty() bool {
	panic("unported: Editor.isEditorEmpty")
}

func (e *Editor) isOnFirstVisualLine() bool {
	panic("unported: Editor.isOnFirstVisualLine")
}

func (e *Editor) isOnLastVisualLine() bool {
	panic("unported: Editor.isOnLastVisualLine")
}

func (e *Editor) navigateHistory(direction int) {
	panic("unported: Editor.navigateHistory")
}

func (e *Editor) exitHistoryBrowsing() {
	panic("unported: Editor.exitHistoryBrowsing")
}

func (e *Editor) setTextInternal(text string, cursorPlacement ceCursorPlacement) {
	panic("unported: Editor.setTextInternal")
}

// Invalidate drops cached render state. Editor currently has none.
func (e *Editor) Invalidate() {}

// RenderTopBorder draws the top border, with a scroll indicator when lines are hidden above.
// Editor.Render must call the outermost override.
func (e *Editor) RenderTopBorder(width int, hiddenLineCount int) string {
	panic("unported: Editor.RenderTopBorder")
}

// RenderBottomBorder draws the bottom border, with a scroll indicator when lines are hidden below.
// Editor.Render must call the outermost override.
func (e *Editor) RenderBottomBorder(width int, hiddenLineCount int) string {
	panic("unported: Editor.RenderBottomBorder")
}

func (e *Editor) Render(width int) []string {
	panic("unported: Editor.Render")
}

func (e *Editor) HandleMouse(event *TuiMouseEvent) *TuiMouseEventResult {
	panic("unported: Editor.HandleMouse")
}

// HandleInput applies one terminal input chunk.
// Bracketed-paste leftovers are fed back through the outermost HandleInput.
func (e *Editor) HandleInput(data string) {
	panic("unported: Editor.HandleInput")
}

func (e *Editor) layoutText(contentWidth int) []ceLayoutLine {
	panic("unported: Editor.layoutText")
}

func (e *Editor) GetText() string {
	panic("unported: Editor.GetText")
}

func (e *Editor) expandPasteMarkers(text string) string {
	panic("unported: Editor.expandPasteMarkers")
}

// GetExpandedText returns the text with paste markers replaced by their stored content.
func (e *Editor) GetExpandedText() string {
	panic("unported: Editor.GetExpandedText")
}

func (e *Editor) GetLines() []string {
	panic("unported: Editor.GetLines")
}

func (e *Editor) GetCursor() EditorCursor {
	panic("unported: Editor.GetCursor")
}

func (e *Editor) SetText(text string) {
	panic("unported: Editor.SetText")
}

// InsertTextAtCursor inserts text at the cursor as one undo step.
func (e *Editor) InsertTextAtCursor(text string) {
	panic("unported: Editor.InsertTextAtCursor")
}

func (e *Editor) normalizeText(text string) string {
	panic("unported: Editor.normalizeText")
}

func (e *Editor) insertTextAtCursorInternal(text string) {
	panic("unported: Editor.insertTextAtCursorInternal")
}

func (e *Editor) insertCharacter(char string, skipUndoCoalescing bool) {
	panic("unported: Editor.insertCharacter")
}

func (e *Editor) handlePaste(pastedText string) {
	panic("unported: Editor.handlePaste")
}

func (e *Editor) addNewLine() {
	panic("unported: Editor.addNewLine")
}

func (e *Editor) shouldSubmitOnBackslashEnter(data string, kb *KeybindingsManager) bool {
	panic("unported: Editor.shouldSubmitOnBackslashEnter")
}

func (e *Editor) submitValue() {
	panic("unported: Editor.submitValue")
}

func (e *Editor) handleBackspace() {
	panic("unported: Editor.handleBackspace")
}

func (e *Editor) setCursorCol(col int) {
	panic("unported: Editor.setCursorCol")
}

func (e *Editor) moveToVisualLine(visualLines []ceVisualLine, currentVisualLine int, targetVisualLine int) {
	panic("unported: Editor.moveToVisualLine")
}

// computeVerticalMoveColumn applies the sticky-column decision table from editor.ts.
func (e *Editor) computeVerticalMoveColumn(currentVisualCol int, sourceMaxVisualCol int, targetMaxVisualCol int) int {
	panic("unported: Editor.computeVerticalMoveColumn")
}

func (e *Editor) moveToLineStart() {
	panic("unported: Editor.moveToLineStart")
}

func (e *Editor) moveToLineEnd() {
	panic("unported: Editor.moveToLineEnd")
}

func (e *Editor) deleteToStartOfLine() {
	panic("unported: Editor.deleteToStartOfLine")
}

func (e *Editor) deleteToEndOfLine() {
	panic("unported: Editor.deleteToEndOfLine")
}

func (e *Editor) deleteWordBackwards() {
	panic("unported: Editor.deleteWordBackwards")
}

func (e *Editor) deleteWordForward() {
	panic("unported: Editor.deleteWordForward")
}

func (e *Editor) handleForwardDelete() {
	panic("unported: Editor.handleForwardDelete")
}

func (e *Editor) buildVisualLineMap(width int) []ceVisualLine {
	panic("unported: Editor.buildVisualLineMap")
}

func (e *Editor) findVisualLineAt(visualLines []ceVisualLine, line int, col int) int {
	panic("unported: Editor.findVisualLineAt")
}

func (e *Editor) findCurrentVisualLine(visualLines []ceVisualLine) int {
	panic("unported: Editor.findCurrentVisualLine")
}

func (e *Editor) moveCursor(deltaLine int, deltaCol int) {
	panic("unported: Editor.moveCursor")
}

func (e *Editor) pageScroll(direction int) {
	panic("unported: Editor.pageScroll")
}

func (e *Editor) moveWordBackwards() {
	panic("unported: Editor.moveWordBackwards")
}

func (e *Editor) yank() {
	panic("unported: Editor.yank")
}

func (e *Editor) yankPop() {
	panic("unported: Editor.yankPop")
}

func (e *Editor) insertYankedText(text string) {
	panic("unported: Editor.insertYankedText")
}

func (e *Editor) deleteYankedText() {
	panic("unported: Editor.deleteYankedText")
}

func (e *Editor) pushUndoSnapshot() {
	panic("unported: Editor.pushUndoSnapshot")
}

func (e *Editor) undo() {
	panic("unported: Editor.undo")
}

func (e *Editor) jumpToChar(char string, direction ceJumpMode) {
	panic("unported: Editor.jumpToChar")
}

func (e *Editor) moveWordForwards() {
	panic("unported: Editor.moveWordForwards")
}

func (e *Editor) isSlashMenuAllowed() bool {
	panic("unported: Editor.isSlashMenuAllowed")
}

func (e *Editor) isAtStartOfMessage() bool {
	panic("unported: Editor.isAtStartOfMessage")
}

func (e *Editor) isInSlashCommandContext(textBeforeCursor string) bool {
	panic("unported: Editor.isInSlashCommandContext")
}

func (e *Editor) getBestAutocompleteMatchIndex(items []AutocompleteItem, prefix string) int {
	panic("unported: Editor.getBestAutocompleteMatchIndex")
}

func (e *Editor) createAutocompleteList(prefix string, items []AutocompleteItem) *SelectList {
	panic("unported: Editor.createAutocompleteList")
}

func (e *Editor) tryTriggerAutocomplete(explicitTab bool) {
	panic("unported: Editor.tryTriggerAutocomplete")
}

func (e *Editor) handleTabCompletion() {
	panic("unported: Editor.handleTabCompletion")
}

func (e *Editor) handleSlashCommandCompletion() {
	panic("unported: Editor.handleSlashCommandCompletion")
}

func (e *Editor) forceFileAutocomplete(explicitTab bool) {
	panic("unported: Editor.forceFileAutocomplete")
}

func (e *Editor) requestAutocomplete(options *ceAutocompleteRequestOptions) {
	panic("unported: Editor.requestAutocomplete")
}

func (e *Editor) startAutocompleteRequest(startToken int, options *ceAutocompleteRequestOptions) error {
	panic("unported: Editor.startAutocompleteRequest")
}

func (e *Editor) setAutocompleteTriggerCharacters(triggerCharacters []string) {
	panic("unported: Editor.setAutocompleteTriggerCharacters")
}

func (e *Editor) getAutocompleteDebounceMs(options *ceAutocompleteRequestOptions) int64 {
	panic("unported: Editor.getAutocompleteDebounceMs")
}

func (e *Editor) runAutocompleteRequest(ctx context.Context, requestId int, snapshotText string, snapshotLine int, snapshotCol int, options *ceAutocompleteRequestOptions) error {
	panic("unported: Editor.runAutocompleteRequest")
}

func (e *Editor) isAutocompleteRequestCurrent(ctx context.Context, requestId int, snapshotText string, snapshotLine int, snapshotCol int) bool {
	panic("unported: Editor.isAutocompleteRequestCurrent")
}

func (e *Editor) applyAutocompleteSuggestions(suggestions *AutocompleteSuggestions, state ceAutocompleteState) {
	panic("unported: Editor.applyAutocompleteSuggestions")
}

func (e *Editor) cancelAutocompleteRequest() {
	panic("unported: Editor.cancelAutocompleteRequest")
}

func (e *Editor) clearAutocompleteUi() {
	panic("unported: Editor.clearAutocompleteUi")
}

func (e *Editor) cancelAutocomplete() {
	panic("unported: Editor.cancelAutocomplete")
}

func (e *Editor) IsShowingAutocomplete() bool {
	panic("unported: Editor.IsShowingAutocomplete")
}

func (e *Editor) updateAutocomplete() {
	panic("unported: Editor.updateAutocomplete")
}

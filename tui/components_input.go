// Ported from packages/tui/src/components/input.ts (pi v1.0.0).

package tui

import (
	"strings"
	"sync"

	"github.com/keejkrej/pi-go/internal/js"
)

const (
	cinBracketedPasteStart = "\x1b[200~"
	cinBracketedPasteEnd   = "\x1b[201~"
)

func cinBoolPtr(v bool) *bool {
	return &v
}

// cinLastAction is the previous editing action, used to coalesce undo and kill-ring entries.
// The zero value means no previous action.
type cinLastAction string

const (
	cinLastActionKill     cinLastAction = "kill"
	cinLastActionYank     cinLastAction = "yank"
	cinLastActionTypeWord cinLastAction = "type-word"
)

// cinInputState is one undo snapshot. Cursor is a UTF-16 index into Value.
type cinInputState struct {
	Value  string
	Cursor int
}

// InputOptions configures an Input. Nil options mean the defaults.
type InputOptions struct {
	// Prompt is the prefix drawn before the text. Nil means "> ".
	Prompt *string
	// Placeholder is shown when the value is empty. Nil means "".
	Placeholder *string
	// PlaceholderStyle styles the placeholder. Nil means the identity function.
	PlaceholderStyle func(text string) string
}

// Input is a single-line text input with horizontal scrolling.
type Input struct {
	mu                  sync.Mutex
	value               string
	cursor              int // UTF-16 index into value
	prompt              string
	placeholder         string
	placeholderStyle    func(text string) string
	renderedStartColumn int
	// OnSubmit is called with the current value on submit. Nil skips the callback.
	OnSubmit func(value string)
	// OnEscape is called on cancel. Nil skips the callback.
	OnEscape    func()
	focused     bool
	pasteBuffer string
	isInPaste   bool
	killRing    *KillRing
	lastAction  cinLastAction
	undoStack   *UndoStack[cinInputState]
}

// NewInput returns an input. Nil options use the defaults.
func NewInput(options *InputOptions) *Input {
	prompt := "> "
	placeholder := ""
	placeholderStyle := func(text string) string { return text }
	if options != nil {
		if options.Prompt != nil {
			prompt = *options.Prompt
		}
		if options.Placeholder != nil {
			placeholder = *options.Placeholder
		}
		if options.PlaceholderStyle != nil {
			placeholderStyle = options.PlaceholderStyle
		}
	}
	return &Input{
		prompt:           prompt,
		placeholder:      placeholder,
		placeholderStyle: placeholderStyle,
		killRing:         NewKillRing(),
		undoStack:        NewUndoStack[cinInputState](),
	}
}

// GetValue returns the current text.
func (i *Input) GetValue() string { return i.value }

// SetValue replaces the text and clamps the cursor to the new length.
func (i *Input) SetValue(value string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.value = value
	if maxLen := js.Len(value); i.cursor > maxLen {
		i.cursor = maxLen
	}
}

// Focused reports whether the TUI has given this input keyboard focus.
// It is the TypeScript focused field.
func (i *Input) Focused() bool { return i.focused }

// SetFocused sets the TypeScript focused field.
func (i *Input) SetFocused(focused bool) { i.focused = focused }

// WantsKeyRelease reports whether key-release events should be delivered.
// Input does not request them.
func (i *Input) WantsKeyRelease() bool { return false }

// HandleInput applies one chunk of raw terminal input.
func (i *Input) HandleInput(data string) {
	i.mu.Lock()
	if strings.Contains(data, cinBracketedPasteStart) {
		i.isInPaste = true
		i.pasteBuffer = ""
		data = strings.ReplaceAll(data, cinBracketedPasteStart, "")
	}

	if i.isInPaste {
		i.pasteBuffer += data
		endIndex := strings.Index(i.pasteBuffer, cinBracketedPasteEnd)
		if endIndex != -1 {
			pasteContent := i.pasteBuffer[:endIndex]
			remaining := i.pasteBuffer[endIndex+len(cinBracketedPasteEnd):]
			i.isInPaste = false
			i.pasteBuffer = ""
			i.handlePaste(pasteContent)
			i.mu.Unlock()
			if len(remaining) > 0 {
				i.HandleInput(remaining)
			}
			return
		}
		i.mu.Unlock()
		return
	}

	kb := GetKeybindings()

	if kb.Matches(data, KeybindingTuiSelectCancel) {
		onEscape := i.OnEscape
		i.mu.Unlock()
		if onEscape != nil {
			onEscape()
		}
		return
	}

	if kb.Matches(data, KeybindingTuiEditorUndo) {
		i.undo()
		i.mu.Unlock()
		return
	}

	if kb.Matches(data, KeybindingTuiInputSubmit) || data == "\n" {
		value := i.value
		onSubmit := i.OnSubmit
		i.mu.Unlock()
		if onSubmit != nil {
			onSubmit(value)
		}
		return
	}

	if kb.Matches(data, KeybindingTuiEditorDeleteCharBackward) {
		i.handleBackspace()
		i.mu.Unlock()
		return
	}
	if kb.Matches(data, KeybindingTuiEditorDeleteCharForward) {
		i.handleForwardDelete()
		i.mu.Unlock()
		return
	}
	if kb.Matches(data, KeybindingTuiEditorDeleteWordBackward) {
		i.deleteWordBackwards()
		i.mu.Unlock()
		return
	}
	if kb.Matches(data, KeybindingTuiEditorDeleteWordForward) {
		i.deleteWordForward()
		i.mu.Unlock()
		return
	}
	if kb.Matches(data, KeybindingTuiEditorDeleteToLineStart) {
		i.deleteToLineStart()
		i.mu.Unlock()
		return
	}
	if kb.Matches(data, KeybindingTuiEditorDeleteToLineEnd) {
		i.deleteToLineEnd()
		i.mu.Unlock()
		return
	}
	if kb.Matches(data, KeybindingTuiEditorYank) {
		i.yank()
		i.mu.Unlock()
		return
	}
	if kb.Matches(data, KeybindingTuiEditorYankPop) {
		i.yankPop()
		i.mu.Unlock()
		return
	}

	if kb.Matches(data, KeybindingTuiEditorCursorLeft) {
		i.lastAction = ""
		if i.cursor > 0 {
			beforeCursor := js.Slice(i.value, 0, i.cursor)
			graphemes := GetGraphemeSegmenter().Segment(beforeCursor)
			if len(graphemes) > 0 {
				i.cursor -= js.Len(graphemes[len(graphemes)-1].Segment)
			} else {
				i.cursor--
			}
		}
		i.mu.Unlock()
		return
	}
	if kb.Matches(data, KeybindingTuiEditorCursorRight) {
		i.lastAction = ""
		valLen := js.Len(i.value)
		if i.cursor < valLen {
			afterCursor := js.SliceFrom(i.value, i.cursor)
			graphemes := GetGraphemeSegmenter().Segment(afterCursor)
			if len(graphemes) > 0 {
				i.cursor += js.Len(graphemes[0].Segment)
			} else {
				i.cursor++
			}
		}
		i.mu.Unlock()
		return
	}
	if kb.Matches(data, KeybindingTuiEditorCursorLineStart) {
		i.lastAction = ""
		i.cursor = 0
		i.mu.Unlock()
		return
	}
	if kb.Matches(data, KeybindingTuiEditorCursorLineEnd) {
		i.lastAction = ""
		i.cursor = js.Len(i.value)
		i.mu.Unlock()
		return
	}
	if kb.Matches(data, KeybindingTuiEditorCursorWordLeft) {
		i.moveWordBackwards()
		i.mu.Unlock()
		return
	}
	if kb.Matches(data, KeybindingTuiEditorCursorWordRight) {
		i.moveWordForwards()
		i.mu.Unlock()
		return
	}

	if kittyChar := DecodeKittyPrintable(data); kittyChar != nil {
		i.insertCharacter(*kittyChar)
		i.mu.Unlock()
		return
	}

	hasControlChars := false
	for _, r := range data {
		if r < 32 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			hasControlChars = true
			break
		}
	}
	if !hasControlChars && data != "" {
		i.insertCharacter(data)
	}
	i.mu.Unlock()
}

// HandleMouse moves the cursor on a left-button press in the input row.
func (i *Input) HandleMouse(event TuiMouseEvent) *TuiMouseEventResult {
	if event.Type != TuiMouseEventTypePress || event.Button != TuiMouseButtonLeft || event.Y != 0 {
		return nil
	}
	i.mu.Lock()
	defer i.mu.Unlock()

	promptWidth := VisibleWidth(i.prompt)
	clickCol := event.X - promptWidth
	if clickCol < 0 {
		clickCol = 0
	}
	targetCol := i.renderedStartColumn + clickCol

	col := 0
	newCursor := 0
	for _, seg := range GetGraphemeSegmenter().Segment(i.value) {
		w := VisibleWidth(seg.Segment)
		if targetCol < col+(w+1)/2 {
			newCursor = seg.Index
			goto done
		}
		col += w
		newCursor = seg.Index + js.Len(seg.Segment)
	}
done:
	i.cursor = newCursor
	i.lastAction = ""
	return &TuiMouseEventResult{
		Handled: cinBoolPtr(true),
		Focus:   cinBoolPtr(true),
	}
}

// Invalidate drops cached render state. Input has none.
func (i *Input) Invalidate() {}

// Render draws the input as one line for the given viewport width.
func (i *Input) Render(width int) []string {
	i.mu.Lock()
	defer i.mu.Unlock()

	if width <= 0 {
		i.renderedStartColumn = 0
		return []string{""}
	}

	promptWidth := VisibleWidth(i.prompt)
	availableWidth := width - promptWidth
	if availableWidth <= 0 {
		i.renderedStartColumn = 0
		return []string{TruncateToWidth(i.prompt, width, "", false)}
	}

	padLine := func(line string) []string {
		if pad := width - VisibleWidth(line); pad > 0 {
			line += strings.Repeat(" ", pad)
		}
		return []string{line}
	}

	if i.value == "" {
		i.renderedStartColumn = 0
		if !i.focused {
			if i.placeholder == "" {
				return padLine(TruncateToWidth(i.prompt, width, "", false))
			}
			ph := TruncateToWidth(i.placeholder, availableWidth, "", false)
			styled := ""
			if ph != "" {
				styled = i.placeholderStyle(ph)
			}
			return padLine(TruncateToWidth(i.prompt+styled, width, "", false))
		}
		if i.placeholder == "" {
			return padLine(TruncateToWidth(i.prompt+CursorMarker+RenderFakeCursor(" "), width, "", false))
		}
		segs := GetGraphemeSegmenter().Segment(i.placeholder)
		first := " "
		rest := ""
		if len(segs) > 0 {
			first = segs[0].Segment
			rest = js.SliceFrom(i.placeholder, js.Len(first))
		}
		firstWidth := VisibleWidth(first)
		if firstWidth > availableWidth {
			return padLine(TruncateToWidth(i.prompt+CursorMarker+RenderFakeCursor(" "), width, "", false))
		}
		restTrunc := TruncateToWidth(rest, availableWidth-firstWidth, "", false)
		styledRest := ""
		if restTrunc != "" {
			styledRest = i.placeholderStyle(restTrunc)
		}
		return padLine(i.prompt + CursorMarker + RenderFakeCursor(i.placeholderStyle(first)) + styledRest)
	}

	beforeCursor := js.Slice(i.value, 0, i.cursor)
	afterCursor := js.SliceFrom(i.value, i.cursor)

	atCursor := " "
	afterCursorRest := ""
	cursorDisplayWidth := 1
	if afterCursor != "" {
		afterSegs := GetGraphemeSegmenter().Segment(afterCursor)
		if len(afterSegs) > 0 {
			atCursor = afterSegs[0].Segment
			afterCursorRest = js.SliceFrom(afterCursor, js.Len(atCursor))
			cursorDisplayWidth = VisibleWidth(atCursor)
			if cursorDisplayWidth < 1 {
				cursorDisplayWidth = 1
			}
		}
	}

	cursorCol := VisibleWidth(beforeCursor)
	totalTextWidth := VisibleWidth(i.value)
	totalContentWidth := totalTextWidth
	if i.focused && afterCursor == "" {
		totalContentWidth = cursorCol + 1
	}

	startCol := i.renderedStartColumn
	if maxStartCol := totalContentWidth - availableWidth; maxStartCol < 0 {
		startCol = 0
	} else if startCol > maxStartCol {
		startCol = maxStartCol
	}
	if startCol < 0 {
		startCol = 0
	}
	if cursorCol < startCol {
		startCol = cursorCol
	}
	if i.focused {
		if cursorCol+cursorDisplayWidth > startCol+availableWidth {
			startCol = cursorCol + cursorDisplayWidth - availableWidth
		}
	} else if cursorCol > startCol+availableWidth {
		startCol = cursorCol - availableWidth
		if startCol < 0 {
			startCol = 0
		}
	}

	if r := GetGraphemeCellRange(beforeCursor, startCol); r != nil && startCol > r.Start {
		if !i.focused || cursorCol+cursorDisplayWidth <= r.Start+availableWidth {
			startCol = r.Start
		} else {
			startCol = r.End
		}
	}
	for i.focused && startCol < cursorCol && cursorCol-startCol+cursorDisplayWidth > availableWidth {
		if r := GetGraphemeCellRange(beforeCursor, startCol); r != nil && r.End > startCol {
			startCol = r.End
		} else {
			startCol++
		}
	}
	i.renderedStartColumn = startCol

	if !i.focused {
		visible := SliceByColumn(i.value, startCol, availableWidth, true)
		return padLine(i.prompt + visible)
	}

	if cursorDisplayWidth > availableWidth {
		return padLine(TruncateToWidth(i.prompt+CursorMarker+RenderFakeCursor(" "), width, "", false))
	}

	beforeMaxWidth := availableWidth - cursorDisplayWidth
	if colDist := cursorCol - startCol; colDist < beforeMaxWidth {
		beforeMaxWidth = colDist
	}
	if beforeMaxWidth < 0 {
		beforeMaxWidth = 0
	}
	beforeSlice := SliceWithWidth(beforeCursor, startCol, beforeMaxWidth, true)

	afterWidth := availableWidth - beforeSlice.Width - cursorDisplayWidth
	afterSlice := ""
	if afterCursorRest != "" && afterWidth > 0 {
		afterSlice = TruncateToWidth(afterCursorRest, afterWidth, "", false)
	}
	return padLine(i.prompt + beforeSlice.Text + CursorMarker + RenderFakeCursor(atCursor) + afterSlice)
}

func (i *Input) insertCharacter(char string) {
	if IsWhitespaceChar(char) || i.lastAction != cinLastActionTypeWord {
		i.pushUndo()
	}
	i.lastAction = cinLastActionTypeWord
	i.value = js.Slice(i.value, 0, i.cursor) + char + js.SliceFrom(i.value, i.cursor)
	i.cursor += js.Len(char)
}

func (i *Input) handleBackspace() {
	i.lastAction = ""
	if i.cursor <= 0 {
		return
	}
	i.pushUndo()
	beforeCursor := js.Slice(i.value, 0, i.cursor)
	graphemes := GetGraphemeSegmenter().Segment(beforeCursor)
	graphemeLen := 1
	if len(graphemes) > 0 {
		graphemeLen = js.Len(graphemes[len(graphemes)-1].Segment)
	}
	if graphemeLen > i.cursor {
		graphemeLen = i.cursor
	}
	i.value = js.Slice(i.value, 0, i.cursor-graphemeLen) + js.SliceFrom(i.value, i.cursor)
	i.cursor -= graphemeLen
}

func (i *Input) handleForwardDelete() {
	i.lastAction = ""
	valLen := js.Len(i.value)
	if i.cursor >= valLen {
		return
	}
	i.pushUndo()
	afterCursor := js.SliceFrom(i.value, i.cursor)
	graphemes := GetGraphemeSegmenter().Segment(afterCursor)
	graphemeLen := 1
	if len(graphemes) > 0 {
		graphemeLen = js.Len(graphemes[0].Segment)
	}
	i.value = js.Slice(i.value, 0, i.cursor) + js.SliceFrom(i.value, i.cursor+graphemeLen)
}

func (i *Input) deleteToLineStart() {
	if i.cursor == 0 {
		return
	}
	i.pushUndo()
	deletedText := js.Slice(i.value, 0, i.cursor)
	acc := i.lastAction == cinLastActionKill
	i.killRing.Push(deletedText, &KillRingPushOptions{Prepend: true, Accumulate: &acc})
	i.lastAction = cinLastActionKill
	i.value = js.SliceFrom(i.value, i.cursor)
	i.cursor = 0
}

func (i *Input) deleteToLineEnd() {
	if i.cursor >= js.Len(i.value) {
		return
	}
	i.pushUndo()
	deletedText := js.SliceFrom(i.value, i.cursor)
	acc := i.lastAction == cinLastActionKill
	i.killRing.Push(deletedText, &KillRingPushOptions{Prepend: false, Accumulate: &acc})
	i.lastAction = cinLastActionKill
	i.value = js.Slice(i.value, 0, i.cursor)
}

func (i *Input) deleteWordBackwards() {
	if i.cursor == 0 {
		return
	}
	wasKill := i.lastAction == cinLastActionKill
	target := FindWordBackward(i.value, i.cursor, nil)
	if target >= i.cursor {
		return
	}
	i.pushUndo()
	deletedText := js.Slice(i.value, target, i.cursor)
	i.killRing.Push(deletedText, &KillRingPushOptions{Prepend: true, Accumulate: &wasKill})
	i.lastAction = cinLastActionKill
	i.value = js.Slice(i.value, 0, target) + js.SliceFrom(i.value, i.cursor)
	i.cursor = target
}

func (i *Input) deleteWordForward() {
	if i.cursor >= js.Len(i.value) {
		return
	}
	wasKill := i.lastAction == cinLastActionKill
	target := FindWordForward(i.value, i.cursor, nil)
	if target <= i.cursor {
		return
	}
	i.pushUndo()
	deletedText := js.Slice(i.value, i.cursor, target)
	i.killRing.Push(deletedText, &KillRingPushOptions{Prepend: false, Accumulate: &wasKill})
	i.lastAction = cinLastActionKill
	i.value = js.Slice(i.value, 0, i.cursor) + js.SliceFrom(i.value, target)
}

func (i *Input) yank() {
	text := i.killRing.Peek()
	if text == nil {
		return
	}
	i.pushUndo()
	i.value = js.Slice(i.value, 0, i.cursor) + *text + js.SliceFrom(i.value, i.cursor)
	i.cursor += js.Len(*text)
	i.lastAction = cinLastActionYank
}

func (i *Input) yankPop() {
	if i.lastAction != cinLastActionYank || i.killRing.Length() <= 1 {
		return
	}
	i.pushUndo()
	prevText := i.killRing.Peek()
	prevLen := 0
	if prevText != nil {
		prevLen = js.Len(*prevText)
	}
	i.killRing.Rotate()
	nextText := i.killRing.Peek()
	if nextText == nil {
		return
	}
	start := i.cursor - prevLen
	if start < 0 {
		start = 0
	}
	i.value = js.Slice(i.value, 0, start) + *nextText + js.SliceFrom(i.value, i.cursor)
	i.cursor = start + js.Len(*nextText)
	i.lastAction = cinLastActionYank
}

func (i *Input) pushUndo() {
	i.undoStack.Push(cinInputState{Value: i.value, Cursor: i.cursor})
}

func (i *Input) undo() {
	snapshot := i.undoStack.Pop()
	if snapshot == nil {
		return
	}
	i.value = snapshot.Value
	i.cursor = snapshot.Cursor
	i.lastAction = ""
}

func (i *Input) moveWordBackwards() {
	i.lastAction = ""
	if i.cursor == 0 {
		return
	}
	i.cursor = FindWordBackward(i.value, i.cursor, nil)
}

func (i *Input) moveWordForwards() {
	i.lastAction = ""
	if i.cursor >= js.Len(i.value) {
		return
	}
	i.cursor = FindWordForward(i.value, i.cursor, nil)
}

func (i *Input) handlePaste(pastedText string) {
	i.pushUndo()
	i.lastAction = ""
	cleanText := strings.ReplaceAll(pastedText, "\r\n", "")
	cleanText = strings.ReplaceAll(cleanText, "\r", "")
	cleanText = strings.ReplaceAll(cleanText, "\n", "")
	cleanText = strings.ReplaceAll(cleanText, "\t", "    ")
	i.value = js.Slice(i.value, 0, i.cursor) + cleanText + js.SliceFrom(i.value, i.cursor)
	i.cursor += js.Len(cleanText)
}

var (
	_ Component = (*Input)(nil)
	_ Focusable = (*Input)(nil)
)

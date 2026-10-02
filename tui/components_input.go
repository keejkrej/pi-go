// Ported from packages/tui/src/components/input.ts (pi v1.0.0).

package tui

import "sync"

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
	panic("unported: NewInput")
}

// GetValue returns the current text.
func (i *Input) GetValue() string { return i.value }

// SetValue replaces the text and clamps the cursor to the new length.
func (i *Input) SetValue(value string) {
	panic("unported: Input.SetValue")
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
	panic("unported: Input.HandleInput")
}

// HandleMouse moves the cursor on a left-button press in the input row.
func (i *Input) HandleMouse(event TuiMouseEvent) *TuiMouseEventResult {
	panic("unported: Input.HandleMouse")
}

// Invalidate drops cached render state. Input has none.
func (i *Input) Invalidate() {}

// Render draws the input as one line for the given viewport width.
func (i *Input) Render(width int) []string {
	panic("unported: Input.Render")
}

func (i *Input) insertCharacter(char string) {
	panic("unported: Input.insertCharacter")
}

func (i *Input) handleBackspace() {
	panic("unported: Input.handleBackspace")
}

func (i *Input) handleForwardDelete() {
	panic("unported: Input.handleForwardDelete")
}

func (i *Input) deleteToLineStart() {
	panic("unported: Input.deleteToLineStart")
}

func (i *Input) deleteToLineEnd() {
	panic("unported: Input.deleteToLineEnd")
}

func (i *Input) deleteWordBackwards() {
	panic("unported: Input.deleteWordBackwards")
}

func (i *Input) deleteWordForward() {
	panic("unported: Input.deleteWordForward")
}

func (i *Input) yank() {
	panic("unported: Input.yank")
}

func (i *Input) yankPop() {
	panic("unported: Input.yankPop")
}

func (i *Input) pushUndo() {
	panic("unported: Input.pushUndo")
}

func (i *Input) undo() {
	panic("unported: Input.undo")
}

func (i *Input) moveWordBackwards() {
	panic("unported: Input.moveWordBackwards")
}

func (i *Input) moveWordForwards() {
	panic("unported: Input.moveWordForwards")
}

func (i *Input) handlePaste(pastedText string) {
	panic("unported: Input.handlePaste")
}

var (
	_ Component = (*Input)(nil)
	_ Focusable = (*Input)(nil)
)

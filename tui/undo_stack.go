// Ported from packages/tui/src/undo-stack.ts (pi v1.0.0).

package tui

// UndoStack stores deep clones of state snapshots.
// Pop returns the stored clone directly, without cloning it again.
type UndoStack[S any] struct {
	stack []S
}

// NewUndoStack returns an empty stack.
func NewUndoStack[S any]() *UndoStack[S] {
	return &UndoStack[S]{stack: []S{}}
}

// Push stores a deep clone of state, matching structuredClone.
func (s *UndoStack[S]) Push(state S) {
	panic("unported: UndoStack.Push")
}

// Pop returns the most recent snapshot, or nil when the stack is empty.
func (s *UndoStack[S]) Pop() *S {
	panic("unported: UndoStack.Pop")
}

// Clear removes every snapshot.
func (s *UndoStack[S]) Clear() {
	panic("unported: UndoStack.Clear")
}

// Length returns the number of snapshots.
func (s *UndoStack[S]) Length() int {
	panic("unported: UndoStack.Length")
}

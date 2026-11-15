package tui_test

import (
	"testing"

	"github.com/keejkrej/pi-go/tui"
)

type undoTestSnapshot struct {
	lines  []string
	cursor struct {
		line int
		col  int
	}
	meta map[string]int
}

func TestUndoStack(t *testing.T) {
	t.Run("deep clones pushed snapshots including slices maps and unexported fields", func(t *testing.T) {
		s := tui.NewUndoStack[undoTestSnapshot]()
		snap := undoTestSnapshot{
			lines: []string{"hello", "world"},
			meta:  map[string]int{"a": 1},
		}
		snap.cursor.line = 1
		snap.cursor.col = 3

		s.Push(snap)
		snap.lines[0] = "mutated"
		snap.lines = append(snap.lines, "extra")
		snap.meta["a"] = 99
		snap.cursor.line = 0

		if s.Length() != 1 {
			t.Fatalf("Length() = %d, want 1", s.Length())
		}
		popped := s.Pop()
		if popped == nil {
			t.Fatal("Pop() = nil")
		}
		if len(popped.lines) != 2 || popped.lines[0] != "hello" || popped.lines[1] != "world" {
			t.Fatalf("popped.lines = %v", popped.lines)
		}
		if popped.cursor.line != 1 || popped.cursor.col != 3 {
			t.Fatalf("popped.cursor = %+v", popped.cursor)
		}
		if popped.meta["a"] != 1 {
			t.Fatalf("popped.meta = %v", popped.meta)
		}
		if s.Pop() != nil {
			t.Fatal("expected nil on empty Pop")
		}
	})

	t.Run("LIFO and Clear", func(t *testing.T) {
		s := tui.NewUndoStack[[]int]()
		s.Push([]int{1})
		s.Push([]int{2})
		s.Push([]int{3})
		if got := *s.Pop(); len(got) != 1 || got[0] != 3 {
			t.Fatalf("first Pop = %v, want [3]", got)
		}
		s.Clear()
		if s.Length() != 0 || s.Pop() != nil {
			t.Fatalf("Clear failed: len=%d", s.Length())
		}
	})

	t.Run("handles pointer cycles", func(t *testing.T) {
		type node struct {
			val  int
			next *node
		}
		n1 := &node{val: 1}
		n2 := &node{val: 2, next: n1}
		n1.next = n2

		s := tui.NewUndoStack[*node]()
		s.Push(n1)
		n1.val = 100
		n2.val = 200

		popped := *s.Pop()
		if popped == n1 || popped.val != 1 || popped.next == n2 || popped.next.val != 2 || popped.next.next != popped {
			t.Fatalf("cycle clone failed: %+v", popped)
		}
	})
}

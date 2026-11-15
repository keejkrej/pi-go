package tui_test

import (
	"testing"

	"github.com/keejkrej/pi-go/tui"
)

func krBoolPtr(v bool) *bool {
	return &v
}

func TestKillRing(t *testing.T) {
	t.Run("ignores empty pushes", func(t *testing.T) {
		kr := tui.NewKillRing()
		kr.Push("", &tui.KillRingPushOptions{Prepend: false})
		if kr.Length() != 0 {
			t.Fatalf("Length() = %d, want 0", kr.Length())
		}
		if kr.Peek() != nil {
			t.Fatalf("Peek() = %v, want nil", *kr.Peek())
		}
	})

	t.Run("pushes and peeks entries", func(t *testing.T) {
		kr := tui.NewKillRing()
		kr.Push("hello", &tui.KillRingPushOptions{Prepend: false})
		kr.Push("world", &tui.KillRingPushOptions{Prepend: false, Accumulate: krBoolPtr(false)})
		if kr.Length() != 2 {
			t.Fatalf("Length() = %d, want 2", kr.Length())
		}
		if got := kr.Peek(); got == nil || *got != "world" {
			t.Fatalf("Peek() = %v, want world", got)
		}
	})

	t.Run("accumulates prepend and append", func(t *testing.T) {
		kr := tui.NewKillRing()
		kr.Push("middle", &tui.KillRingPushOptions{Prepend: false})
		kr.Push(" ", &tui.KillRingPushOptions{Prepend: false, Accumulate: krBoolPtr(true)})
		kr.Push("end", &tui.KillRingPushOptions{Prepend: false, Accumulate: krBoolPtr(true)})
		if kr.Length() != 1 || *kr.Peek() != "middle end" {
			t.Fatalf("after append: len=%d peek=%v", kr.Length(), kr.Peek())
		}

		kr.Push("start ", &tui.KillRingPushOptions{Prepend: true, Accumulate: krBoolPtr(true)})
		if kr.Length() != 1 || *kr.Peek() != "start middle end" {
			t.Fatalf("after prepend: len=%d peek=%v", kr.Length(), kr.Peek())
		}
	})

	t.Run("rotates entries for yank-pop", func(t *testing.T) {
		kr := tui.NewKillRing()
		kr.Rotate()
		kr.Push("a", &tui.KillRingPushOptions{Prepend: false})
		kr.Rotate()
		if *kr.Peek() != "a" {
			t.Fatalf("single entry Rotate() changed Peek to %q", *kr.Peek())
		}
		kr.Push("b", &tui.KillRingPushOptions{Prepend: false})
		kr.Push("c", &tui.KillRingPushOptions{Prepend: false})

		if *kr.Peek() != "c" {
			t.Fatalf("Peek = %q, want c", *kr.Peek())
		}
		kr.Rotate()
		if *kr.Peek() != "b" {
			t.Fatalf("Peek after 1 rotate = %q, want b", *kr.Peek())
		}
		kr.Rotate()
		if *kr.Peek() != "a" {
			t.Fatalf("Peek after 2 rotates = %q, want a", *kr.Peek())
		}
		kr.Rotate()
		if *kr.Peek() != "c" {
			t.Fatalf("Peek after 3 rotates = %q, want c", *kr.Peek())
		}
	})
}

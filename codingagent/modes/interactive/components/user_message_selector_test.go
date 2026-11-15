package components

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/tui"
)

func tusrmsgReset(t *testing.T) {
	t.Helper()
	_, _ = theme.InitTheme("dark", false)
	orig := tui.GetKeybindings()
	tui.SetKeybindings(core.NewKeybindingsManager(nil).KeybindingsManager)
	t.Cleanup(func() {
		tui.SetKeybindings(orig)
	})
}

func TestUserMessageSelector(t *testing.T) {
	t.Run("defaults to latest message and wraps navigation", func(t *testing.T) {
		tusrmsgReset(t)
		items := []UserMessageItem{
			{Id: "id-1", Text: "first line\nsecond line"},
			{Id: "id-2", Text: "middle message"},
			{Id: "id-3", Text: "latest message"},
		}
		var selected string
		sel := NewUserMessageSelectorComponent(items, func(id string) { selected = id }, func() {})
		defer sel.Dispose()

		rendered := utils.StripAnsi(strings.Join(sel.Render(80), "\n"))
		if !strings.Contains(rendered, "› latest message") {
			t.Fatalf("expected latest message selected:\n%s", rendered)
		}
		if !strings.Contains(rendered, "first line second line") {
			t.Fatalf("expected normalized single-line text:\n%s", rendered)
		}

		list := sel.GetMessageList()
		if !list.CapturesViewportInput() {
			t.Fatal("CapturesViewportInput() = false, want true")
		}
		list.HandleInput("\x1b[B") // wrap to index 0
		list.HandleInput("\r")
		if selected != "id-1" {
			t.Fatalf("selected = %q, want %q", selected, "id-1")
		}
	})

	t.Run("preselects initialSelectedId and handles page/cancel", func(t *testing.T) {
		tusrmsgReset(t)
		items := make([]UserMessageItem, 15)
		for i := range items {
			items[i] = UserMessageItem{Id: fmt.Sprintf("u%d", i), Text: fmt.Sprintf("Message text %d", i)}
		}
		var selected string
		cancelled := false
		sel := NewUserMessageSelectorComponent(items, func(id string) { selected = id }, func() { cancelled = true }, "u5")
		defer sel.Dispose()

		list := sel.GetMessageList()
		rendered := utils.StripAnsi(strings.Join(list.Render(80), "\n"))
		if !strings.Contains(rendered, "› Message text 5") {
			t.Fatalf("expected u5 preselected:\n%s", rendered)
		}
		if !strings.Contains(rendered, "(6/15)") {
			t.Fatalf("expected scroll indicator (6/15):\n%s", rendered)
		}

		list.HandleInput("\x1b[5~") // PageUp
		list.HandleInput("\r")
		if selected != "u0" {
			t.Fatalf("after PageUp, selected = %q, want u0", selected)
		}

		list.HandleInput("\x1b")
		if !cancelled {
			t.Fatal("expected cancel on escape")
		}
	})

	t.Run("renders empty state and auto-cancels or stops cleanly on Dispose", func(t *testing.T) {
		tusrmsgReset(t)
		var cancelled atomic.Bool
		sel := NewUserMessageSelectorComponent(nil, func(string) {}, func() {
			cancelled.Store(true)
		})
		rendered := utils.StripAnsi(strings.Join(sel.Render(80), "\n"))
		if !strings.Contains(rendered, "No user messages found") {
			t.Fatalf("expected empty state text:\n%s", rendered)
		}
		sel.Dispose()
		time.Sleep(130 * time.Millisecond)
		if cancelled.Load() {
			t.Fatal("auto-cancel fired after Dispose()")
		}

		var autoCancelled atomic.Bool
		done := make(chan struct{})
		sel2 := NewUserMessageSelectorComponent(nil, func(string) {}, func() {
			if autoCancelled.CompareAndSwap(false, true) {
				close(done)
			}
		})
		defer sel2.Dispose()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for empty auto-cancel")
		}
	})
}

package tui

import (
	"strings"
	"testing"
)

func TestInputUnit_ConstructValueFocusAndMouse(t *testing.T) {
	in := NewInput(nil)
	if in.GetValue() != "" {
		t.Fatalf("initial value = %q, want empty", in.GetValue())
	}
	if in.Focused() {
		t.Fatal("initial Focused() = true, want false")
	}
	in.SetFocused(true)
	if !in.Focused() {
		t.Fatal("Focused() = false after SetFocused(true)")
	}
	if in.WantsKeyRelease() {
		t.Fatal("WantsKeyRelease() = true, want false")
	}
	in.Invalidate()

	// Non-left press returns nil without requiring terminal width measurement.
	if res := in.HandleMouse(TuiMouseEvent{
		Type:   TuiMouseEventTypePress,
		Button: TuiMouseButtonRight,
		X:      0,
		Y:      0,
	}); res != nil {
		t.Fatalf("HandleMouse right press = %#v, want nil", res)
	}
}

func TestInput_EditingCursorSubmitAndRender(t *testing.T) {
	t.Run("HandleMouse left press focuses and handles click", func(t *testing.T) {
		in := NewInput(nil)
		res := in.HandleMouse(TuiMouseEvent{
			Type:   TuiMouseEventTypePress,
			Button: TuiMouseButtonLeft,
			X:      0,
			Y:      0,
		})
		if res == nil || res.Handled == nil || !*res.Handled {
			t.Fatalf("HandleMouse left press = %#v, want handled", res)
		}
	})
	t.Run("setValue, typing, cursor movement, and backspace", func(t *testing.T) {
		in := NewInput(nil)
		in.SetValue("hello")
		if in.GetValue() != "hello" {
			t.Fatalf("GetValue = %q, want hello", in.GetValue())
		}
		in.HandleInput("\x05")
		in.HandleInput(" world")
		if in.GetValue() != "hello world" {
			t.Fatalf("GetValue after HandleInput = %q", in.GetValue())
		}
		in.HandleInput("\x7f")
		if in.GetValue() != "hello worl" {
			t.Fatalf("GetValue after backspace = %q", in.GetValue())
		}
	})

	t.Run("calls OnSubmit on Enter and OnEscape on Escape", func(t *testing.T) {
		in := NewInput(nil)
		var submitted string
		escaped := false
		in.OnSubmit = func(v string) { submitted = v }
		in.OnEscape = func() { escaped = true }

		in.SetValue("abc")
		in.HandleInput("\r")
		if submitted != "abc" {
			t.Fatalf("submitted = %q, want abc", submitted)
		}
		in.HandleInput("\x1b")
		if !escaped {
			t.Fatal("expected OnEscape to be called")
		}
	})

	t.Run("strips newlines on paste and supports undo", func(t *testing.T) {
		in := NewInput(nil)
		in.HandleInput("hello")
		in.HandleInput("\x1b[200~\nworld\x1b[201~")
		if strings.Contains(in.GetValue(), "\n") {
			t.Fatalf("newline leaked into single-line Input: %q", in.GetValue())
		}
		if in.GetValue() != "helloworld" {
			t.Fatalf("GetValue = %q, want helloworld", in.GetValue())
		}
		in.HandleInput("\x1b[45;5u") // Ctrl+- undo
		if in.GetValue() != "hello" {
			t.Fatalf("GetValue after undo = %q, want hello", in.GetValue())
		}
	})

	t.Run("Render respects custom prompt, placeholder, and width", func(t *testing.T) {
		prompt := "? "
		placeholder := "type here"
		in := NewInput(&InputOptions{
			Prompt:           &prompt,
			Placeholder:      &placeholder,
			PlaceholderStyle: func(s string) string { return "\x1b[2m" + s + "\x1b[22m" },
		})
		lines := in.Render(40)
		if len(lines) != 1 || VisibleWidth(lines[0]) != 40 {
			t.Fatalf("Render(40) = %#v (width=%d)", lines, VisibleWidth(lines[0]))
		}
		stripped := tmarkStripAnsi(lines[0])
		if !strings.Contains(stripped, "? ") || !strings.Contains(stripped, "type here") {
			t.Fatalf("unexpected rendered prompt/placeholder: %q", stripped)
		}
	})
}

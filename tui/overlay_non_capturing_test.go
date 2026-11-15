package tui_test

import (
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/keejkrej/pi-go/tui"
	"github.com/keejkrej/pi-go/tui/internal/testkit"
)

func oncBoolPtr(v bool) *bool { return &v }

type oncStaticOverlay struct {
	lines []string
}

func (o *oncStaticOverlay) Render(width int) []string { return o.lines }
func (o *oncStaticOverlay) Invalidate()               {}

type oncEmptyContent struct{}

func (e *oncEmptyContent) Render(width int) []string { return []string{} }
func (e *oncEmptyContent) Invalidate()               {}

type oncFocusableOverlay struct {
	focused bool
	inputs  []string
	lines   []string
	onInput func(data string)
}

func newOncFocusable(lines []string) *oncFocusableOverlay {
	return &oncFocusableOverlay{lines: lines}
}

func (f *oncFocusableOverlay) Focused() bool           { return f.focused }
func (f *oncFocusableOverlay) SetFocused(focused bool) { f.focused = focused }
func (f *oncFocusableOverlay) HandleInput(data string) {
	f.inputs = append(f.inputs, data)
	if f.onInput != nil {
		f.onInput(data)
	}
}
func (f *oncFocusableOverlay) Render(width int) []string { return f.lines }
func (f *oncFocusableOverlay) Invalidate()               {}

func oncRenderAndFlush(ui tui.TUI, term *testkit.VirtualTerminal) {
	ui.RenderNow(oncBoolPtr(true))
	term.Flush()
}

func TestOverlayNonCapturing(t *testing.T) {
	t.Run("focus management", func(t *testing.T) {
		t.Run("non-capturing overlay preserves focus on creation", func(t *testing.T) {
			term := testkit.NewVirtualTerminal(80, 24)
			ui := tui.NewTuiMainScreen(term, nil, nil)
			editor := newOncFocusable([]string{"EDITOR"})
			overlay := newOncFocusable([]string{"OVERLAY"})
			ui.AddChild(&oncEmptyContent{})
			ui.SetFocus(editor)
			ui.Start()
			defer ui.Stop(nil)

			ui.ShowOverlay(overlay, &tui.OverlayOptions{NonCapturing: oncBoolPtr(true)})
			oncRenderAndFlush(ui, term)
			if !editor.focused || overlay.focused {
				t.Fatalf("editor.focused=%v overlay.focused=%v, want true/false", editor.focused, overlay.focused)
			}
		})

		t.Run("focus() transfers focus and unfocus() restores previous focus", func(t *testing.T) {
			term := testkit.NewVirtualTerminal(80, 24)
			ui := tui.NewTuiMainScreen(term, nil, nil)
			editor := newOncFocusable([]string{"EDITOR"})
			overlay := newOncFocusable([]string{"OVERLAY"})
			ui.AddChild(&oncEmptyContent{})
			ui.SetFocus(editor)
			ui.Start()
			defer ui.Stop(nil)

			handle := ui.ShowOverlay(overlay, &tui.OverlayOptions{NonCapturing: oncBoolPtr(true)})
			handle.Focus()
			oncRenderAndFlush(ui, term)
			if editor.focused || !overlay.focused || !handle.IsFocused() {
				t.Fatalf("after Focus: editor=%v overlay=%v handle=%v", editor.focused, overlay.focused, handle.IsFocused())
			}

			handle.Unfocus(nil)
			oncRenderAndFlush(ui, term)
			if !editor.focused || overlay.focused || handle.IsFocused() {
				t.Fatalf("after Unfocus: editor=%v overlay=%v handle=%v", editor.focused, overlay.focused, handle.IsFocused())
			}
		})

		t.Run("setHidden(false) on non-capturing overlay does not auto-focus", func(t *testing.T) {
			term := testkit.NewVirtualTerminal(80, 24)
			ui := tui.NewTuiMainScreen(term, nil, nil)
			editor := newOncFocusable([]string{"EDITOR"})
			overlay := newOncFocusable([]string{"OVERLAY"})
			ui.AddChild(&oncEmptyContent{})
			ui.SetFocus(editor)
			ui.Start()
			defer ui.Stop(nil)

			handle := ui.ShowOverlay(overlay, &tui.OverlayOptions{NonCapturing: oncBoolPtr(true)})
			handle.SetHidden(true)
			handle.SetHidden(false)
			oncRenderAndFlush(ui, term)
			if !editor.focused || overlay.focused {
				t.Fatalf("editor=%v overlay=%v", editor.focused, overlay.focused)
			}
		})

		t.Run("hide() on unfocused vs focused non-capturing overlay", func(t *testing.T) {
			term := testkit.NewVirtualTerminal(80, 24)
			ui := tui.NewTuiMainScreen(term, nil, nil)
			editor := newOncFocusable([]string{"EDITOR"})
			o1 := newOncFocusable([]string{"O1"})
			o2 := newOncFocusable([]string{"O2"})
			ui.AddChild(&oncEmptyContent{})
			ui.SetFocus(editor)
			ui.Start()
			defer ui.Stop(nil)

			h1 := ui.ShowOverlay(o1, &tui.OverlayOptions{NonCapturing: oncBoolPtr(true)})
			h1.Hide()
			oncRenderAndFlush(ui, term)
			if !editor.focused {
				t.Fatal("editor should remain focused when hiding unfocused overlay")
			}

			h2 := ui.ShowOverlay(o2, &tui.OverlayOptions{NonCapturing: oncBoolPtr(true)})
			h2.Focus()
			h2.Hide()
			oncRenderAndFlush(ui, term)
			if !editor.focused || o2.focused {
				t.Fatalf("editor=%v o2=%v after focused hide", editor.focused, o2.focused)
			}
		})

		t.Run("capturing overlay removed with non-capturing below restores focus to editor", func(t *testing.T) {
			term := testkit.NewVirtualTerminal(80, 24)
			ui := tui.NewTuiMainScreen(term, nil, nil)
			editor := newOncFocusable([]string{"EDITOR"})
			nc := newOncFocusable([]string{"NC"})
			capOv := newOncFocusable([]string{"CAP"})
			ui.AddChild(&oncEmptyContent{})
			ui.SetFocus(editor)
			ui.Start()
			defer ui.Stop(nil)

			ui.ShowOverlay(nc, &tui.OverlayOptions{NonCapturing: oncBoolPtr(true)})
			handle := ui.ShowOverlay(capOv, nil)
			if !capOv.focused {
				t.Fatal("capturing overlay expected focused")
			}
			handle.Hide()
			oncRenderAndFlush(ui, term)
			if !editor.focused || nc.focused {
				t.Fatalf("editor=%v nc=%v", editor.focused, nc.focused)
			}
		})

		t.Run("sub-overlay cleanup then hideOverlay restores focus and input to editor", func(t *testing.T) {
			term := testkit.NewVirtualTerminal(80, 24)
			ui := tui.NewTuiMainScreen(term, nil, nil)
			editor := newOncFocusable([]string{"EDITOR"})
			timer := newOncFocusable([]string{"TIMER"})
			ctrl := newOncFocusable([]string{"CTRL"})
			ui.AddChild(&oncEmptyContent{})
			ui.SetFocus(editor)
			ui.Start()
			defer ui.Stop(nil)

			timerHandle := ui.ShowOverlay(timer, &tui.OverlayOptions{NonCapturing: oncBoolPtr(true)})
			ui.ShowOverlay(ctrl, nil)
			if !ctrl.focused || editor.focused {
				t.Fatal("ctrl expected focused")
			}
			timerHandle.Hide()
			ui.HideOverlay()
			oncRenderAndFlush(ui, term)
			if !editor.focused || ctrl.focused || timer.focused {
				t.Fatalf("editor=%v ctrl=%v timer=%v", editor.focused, ctrl.focused, timer.focused)
			}
			term.SendInput("x")
			oncRenderAndFlush(ui, term)
			if !reflect.DeepEqual(editor.inputs, []string{"x"}) || len(ctrl.inputs) != 0 || len(timer.inputs) != 0 {
				t.Fatalf("inputs editor=%v ctrl=%v timer=%v", editor.inputs, ctrl.inputs, timer.inputs)
			}
		})

		t.Run("removed focused child overlay does not become parent overlay fallback", func(t *testing.T) {
			term := testkit.NewVirtualTerminal(80, 24)
			ui := tui.NewTuiMainScreen(term, nil, nil)
			editor := newOncFocusable([]string{"EDITOR"})
			child := newOncFocusable([]string{"CHILD"})
			parent := newOncFocusable([]string{"PARENT"})
			ui.AddChild(&oncEmptyContent{})
			ui.SetFocus(editor)
			ui.Start()
			defer ui.Stop(nil)

			childHandle := ui.ShowOverlay(child, &tui.OverlayOptions{NonCapturing: oncBoolPtr(true)})
			childHandle.Focus()
			parentHandle := ui.ShowOverlay(parent, nil)
			if !parent.focused {
				t.Fatal("parent expected focused")
			}
			childHandle.Hide()
			parentHandle.Hide()
			term.SendInput("x")
			oncRenderAndFlush(ui, term)
			if !reflect.DeepEqual(editor.inputs, []string{"x"}) || len(child.inputs) != 0 || len(parent.inputs) != 0 || !editor.focused {
				t.Fatalf("editor=%v(%v) child=%v parent=%v", editor.focused, editor.inputs, child.inputs, parent.inputs)
			}
		})

		t.Run("handleInput redirection skips non-capturing overlays when focused overlay becomes invisible", func(t *testing.T) {
			term := testkit.NewVirtualTerminal(80, 24)
			ui := tui.NewTuiMainScreen(term, nil, nil)
			editor := newOncFocusable([]string{"EDITOR"})
			fallback := newOncFocusable([]string{"FALLBACK"})
			nc := newOncFocusable([]string{"NC"})
			primary := newOncFocusable([]string{"PRIMARY"})
			var isVisible atomic.Bool
			isVisible.Store(true)
			ui.AddChild(&oncEmptyContent{})
			ui.SetFocus(editor)
			ui.Start()
			defer ui.Stop(nil)

			ui.ShowOverlay(fallback, nil)
			ui.ShowOverlay(nc, &tui.OverlayOptions{NonCapturing: oncBoolPtr(true)})
			ui.ShowOverlay(primary, &tui.OverlayOptions{Visible: func(int, int) bool { return isVisible.Load() }})
			isVisible.Store(false)
			term.SendInput("x")
			oncRenderAndFlush(ui, term)
			if len(primary.inputs) != 0 || len(nc.inputs) != 0 || !reflect.DeepEqual(fallback.inputs, []string{"x"}) || !fallback.focused {
				t.Fatalf("primary=%v nc=%v fallback=%v(%v)", primary.inputs, nc.inputs, fallback.inputs, fallback.focused)
			}
		})

		t.Run("active base focus replacement receives close input before overlay restore", func(t *testing.T) {
			term := testkit.NewVirtualTerminal(80, 24)
			ui := tui.NewTuiMainScreen(term, nil, nil)
			editor := newOncFocusable([]string{"EDITOR"})
			replacement := newOncFocusable([]string{"REPLACEMENT"})
			overlay := newOncFocusable([]string{"OVERLAY"})
			overlay.onInput = func(data string) {
				if data == "b" {
					ui.SetFocus(replacement)
				}
			}
			replacement.onInput = func(data string) {
				if data == "\r" {
					ui.SetFocus(editor)
				}
			}
			ui.AddChild(&oncEmptyContent{})
			ui.SetFocus(editor)
			ui.Start()
			defer ui.Stop(nil)

			ui.ShowOverlay(overlay, nil)
			term.SendInput("b")
			oncRenderAndFlush(ui, term)
			if !replacement.focused {
				t.Fatal("replacement expected focused after 'b'")
			}
			term.SendInput("\r")
			oncRenderAndFlush(ui, term)
			if !reflect.DeepEqual(replacement.inputs, []string{"\r"}) || !reflect.DeepEqual(overlay.inputs, []string{"b"}) || !overlay.focused {
				t.Fatalf("replacement=%v overlay=%v(%v)", replacement.inputs, overlay.inputs, overlay.focused)
			}
			term.SendInput("x")
			oncRenderAndFlush(ui, term)
			if !reflect.DeepEqual(overlay.inputs, []string{"b", "x"}) {
				t.Fatalf("overlay.inputs = %v, want [b x]", overlay.inputs)
			}
		})

		t.Run("active replacement still receives input when it is another overlay preFocus", func(t *testing.T) {
			term := testkit.NewVirtualTerminal(80, 24)
			ui := tui.NewTuiMainScreen(term, nil, nil)
			editor := newOncFocusable([]string{"EDITOR"})
			replacement := newOncFocusable([]string{"REPLACEMENT"})
			passive := newOncFocusable([]string{"PASSIVE"})
			overlay := newOncFocusable([]string{"OVERLAY"})
			overlay.onInput = func(data string) {
				if data == "b" {
					ui.SetFocus(replacement)
				}
			}
			replacement.onInput = func(data string) {
				if data == "\r" {
					ui.SetFocus(editor)
				}
			}
			ui.AddChild(&oncEmptyContent{})
			ui.Start()
			defer ui.Stop(nil)

			ui.SetFocus(replacement)
			ui.ShowOverlay(passive, &tui.OverlayOptions{NonCapturing: oncBoolPtr(true)})
			ui.SetFocus(editor)
			ui.ShowOverlay(overlay, nil)
			term.SendInput("b")
			oncRenderAndFlush(ui, term)
			term.SendInput("1")
			term.SendInput("\r")
			oncRenderAndFlush(ui, term)
			if !reflect.DeepEqual(replacement.inputs, []string{"1", "\r"}) || !reflect.DeepEqual(overlay.inputs, []string{"b"}) || !overlay.focused {
				t.Fatalf("replacement=%v overlay=%v(%v)", replacement.inputs, overlay.inputs, overlay.focused)
			}
		})

		t.Run("blocked and removed replacements restore overlay cleanly", func(t *testing.T) {
			term := testkit.NewVirtualTerminal(80, 24)
			ui := tui.NewTuiMainScreen(term, nil, nil)
			base := tui.NewContainer()
			editor := newOncFocusable([]string{"EDITOR"})
			firstRep := newOncFocusable([]string{"FIRST"})
			secondRep := newOncFocusable([]string{"SECOND"})
			overlay := newOncFocusable([]string{"OVERLAY"})
			overlay.onInput = func(data string) {
				if data == "b" {
					ui.SetFocus(firstRep)
				}
			}
			firstRep.onInput = func(data string) {
				if data == "n" {
					ui.SetFocus(secondRep)
				}
			}
			secondRep.onInput = func(data string) {
				if data == "\r" {
					base.Clear()
					base.AddChild(editor)
					ui.SetFocus(editor)
				}
			}
			base.AddChild(editor)
			base.AddChild(firstRep)
			base.AddChild(secondRep)
			ui.AddChild(base)
			ui.SetFocus(editor)
			ui.Start()
			defer ui.Stop(nil)

			ui.ShowOverlay(overlay, nil)
			term.SendInput("b")
			term.SendInput("n")
			term.SendInput("2")
			term.SendInput("\r")
			oncRenderAndFlush(ui, term)
			if !reflect.DeepEqual(overlay.inputs, []string{"b"}) ||
				!reflect.DeepEqual(firstRep.inputs, []string{"n"}) ||
				!reflect.DeepEqual(secondRep.inputs, []string{"2", "\r"}) ||
				!overlay.focused {
				t.Fatalf("overlay=%v(%v) first=%v second=%v", overlay.inputs, overlay.focused, firstRep.inputs, secondRep.inputs)
			}
		})

		t.Run("unfocus target releases blocked overlay while replacement stays focused", func(t *testing.T) {
			term := testkit.NewVirtualTerminal(80, 24)
			ui := tui.NewTuiMainScreen(term, nil, nil)
			fallback := newOncFocusable([]string{"FALLBACK"})
			target := newOncFocusable([]string{"TARGET"})
			replacement := newOncFocusable([]string{"REPLACEMENT"})
			overlay := newOncFocusable([]string{"OVERLAY"})
			replacement.onInput = func(data string) {
				if data == "\r" {
					ui.SetFocus(fallback)
				}
			}
			ui.AddChild(&oncEmptyContent{})
			ui.Start()
			defer ui.Stop(nil)

			ovHandle := ui.ShowOverlay(overlay, nil)
			overlay.onInput = func(data string) {
				if data == "b" {
					ui.SetFocus(replacement)
					ovHandle.Unfocus(&tui.OverlayUnfocusOptions{Target: target})
				}
			}
			term.SendInput("b")
			oncRenderAndFlush(ui, term)
			if !replacement.focused {
				t.Fatal("replacement expected focused")
			}
			term.SendInput("\r")
			term.SendInput("x")
			oncRenderAndFlush(ui, term)
			if !reflect.DeepEqual(overlay.inputs, []string{"b"}) ||
				!reflect.DeepEqual(replacement.inputs, []string{"\r"}) ||
				len(fallback.inputs) != 0 ||
				!reflect.DeepEqual(target.inputs, []string{"x"}) {
				t.Fatalf("ov=%v rep=%v fb=%v target=%v", overlay.inputs, replacement.inputs, fallback.inputs, target.inputs)
			}
		})

		t.Run("base focus steal restores visible focused overlay or sub-overlay, not passive", func(t *testing.T) {
			term := testkit.NewVirtualTerminal(80, 24)
			ui := tui.NewTuiMainScreen(term, nil, nil)
			editor := newOncFocusable([]string{"EDITOR"})
			ctrl := newOncFocusable([]string{"CTRL"})
			sub := newOncFocusable([]string{"SUB"})
			passive := newOncFocusable([]string{"PASSIVE"})
			ui.AddChild(&oncEmptyContent{})
			ui.SetFocus(editor)
			ui.Start()
			defer ui.Stop(nil)

			ui.ShowOverlay(passive, &tui.OverlayOptions{NonCapturing: oncBoolPtr(true)})
			term.SendInput("p")
			if !reflect.DeepEqual(editor.inputs, []string{"p"}) || len(passive.inputs) != 0 {
				t.Fatalf("passive stole focus: editor=%v passive=%v", editor.inputs, passive.inputs)
			}

			ui.ShowOverlay(ctrl, nil)
			subHandle := ui.ShowOverlay(sub, &tui.OverlayOptions{NonCapturing: oncBoolPtr(true)})
			subHandle.Focus()
			ui.SetFocus(editor)
			term.SendInput("x")
			oncRenderAndFlush(ui, term)
			if !reflect.DeepEqual(sub.inputs, []string{"x"}) || len(ctrl.inputs) != 0 {
				t.Fatalf("sub=%v ctrl=%v", sub.inputs, ctrl.inputs)
			}
		})

		t.Run("temporarily invisible overlay restores when visible again", func(t *testing.T) {
			term := testkit.NewVirtualTerminal(80, 24)
			ui := tui.NewTuiMainScreen(term, nil, nil)
			editor := newOncFocusable([]string{"EDITOR"})
			overlay := newOncFocusable([]string{"OVERLAY"})
			var visible atomic.Bool
			visible.Store(true)
			ui.AddChild(&oncEmptyContent{})
			ui.SetFocus(editor)
			ui.Start()
			defer ui.Stop(nil)

			ui.ShowOverlay(overlay, &tui.OverlayOptions{Visible: func(int, int) bool { return visible.Load() }})
			ui.SetFocus(editor)
			visible.Store(false)
			term.SendInput("x")
			visible.Store(true)
			term.SendInput("y")
			oncRenderAndFlush(ui, term)
			if !reflect.DeepEqual(editor.inputs, []string{"x"}) || !reflect.DeepEqual(overlay.inputs, []string{"y"}) {
				t.Fatalf("editor=%v overlay=%v", editor.inputs, overlay.inputs)
			}
		})

		t.Run("setFocus(null) clears restore and blocked replacement setFocus(null) resumes", func(t *testing.T) {
			term := testkit.NewVirtualTerminal(80, 24)
			ui := tui.NewTuiMainScreen(term, nil, nil)
			overlay := newOncFocusable([]string{"OVERLAY"})
			ui.AddChild(&oncEmptyContent{})
			ui.Start()
			defer ui.Stop(nil)

			ui.ShowOverlay(overlay, nil)
			ui.SetFocus(nil)
			term.SendInput("x")
			if len(overlay.inputs) != 0 || overlay.focused {
				t.Fatalf("overlay unexpectedly retained focus: %v", overlay.inputs)
			}

			// Blocked replacement clearing focus resumes overlay.
			ov2 := newOncFocusable([]string{"OV2"})
			rep := newOncFocusable([]string{"REP"})
			rep.onInput = func(data string) {
				if data == "\r" {
					ui.SetFocus(nil)
				}
			}
			ov2.onInput = func(data string) {
				if data == "b" {
					ui.SetFocus(rep)
				}
			}
			ui.ShowOverlay(ov2, nil)
			term.SendInput("b")
			term.SendInput("\r")
			term.SendInput("x")
			if !reflect.DeepEqual(rep.inputs, []string{"\r"}) || !reflect.DeepEqual(ov2.inputs, []string{"b", "x"}) || !ov2.focused {
				t.Fatalf("rep=%v ov2=%v(%v)", rep.inputs, ov2.inputs, ov2.focused)
			}
		})

		t.Run("cyclic preFocus ancestry does not hang and focus-order top overlay restores", func(t *testing.T) {
			term := testkit.NewVirtualTerminal(80, 24)
			ui := tui.NewTuiMainScreen(term, nil, nil)
			editor := newOncFocusable([]string{"EDITOR"})
			lower := newOncFocusable([]string{"LOWER"})
			upper := newOncFocusable([]string{"UPPER"})
			ui.AddChild(&oncEmptyContent{})
			ui.SetFocus(lower)
			ui.Start()
			defer ui.Stop(nil)

			lh := ui.ShowOverlay(lower, &tui.OverlayOptions{NonCapturing: oncBoolPtr(true)})
			lh.Focus()
			ui.SetFocus(editor)
			term.SendInput("x")
			if !reflect.DeepEqual(editor.inputs, []string{"x"}) || len(lower.inputs) != 0 {
				t.Fatalf("editor=%v lower=%v", editor.inputs, lower.inputs)
			}

			lh.Hide()
			lh2 := ui.ShowOverlay(lower, nil)
			ui.ShowOverlay(upper, nil)
			lh2.Focus()
			ui.SetFocus(editor)
			term.SendInput("y")
			if !reflect.DeepEqual(lower.inputs, []string{"y"}) || len(upper.inputs) != 0 {
				t.Fatalf("lower=%v upper=%v", lower.inputs, upper.inputs)
			}
		})

		t.Run("multiple capturing and non-capturing overlays restore cleanly", func(t *testing.T) {
			term := testkit.NewVirtualTerminal(80, 24)
			ui := tui.NewTuiMainScreen(term, nil, nil)
			editor := newOncFocusable([]string{"EDITOR"})
			c1 := newOncFocusable([]string{"C1"})
			n1 := newOncFocusable([]string{"N1"})
			c2 := newOncFocusable([]string{"C2"})
			n2 := newOncFocusable([]string{"N2"})
			ui.AddChild(&oncEmptyContent{})
			ui.SetFocus(editor)
			ui.Start()
			defer ui.Stop(nil)

			c1h := ui.ShowOverlay(c1, nil)
			ui.ShowOverlay(n1, &tui.OverlayOptions{NonCapturing: oncBoolPtr(true)})
			c2h := ui.ShowOverlay(c2, nil)
			ui.ShowOverlay(n2, &tui.OverlayOptions{NonCapturing: oncBoolPtr(true)})
			if !c2.focused {
				t.Fatal("c2 expected focused")
			}
			// HideOverlay on topmost non-capturing does not disturb capturing focus.
			ui.HideOverlay()
			if !c2.focused {
				t.Fatal("c2 should remain focused after hiding non-capturing n2")
			}
			c2h.Hide()
			if !c1.focused {
				t.Fatal("c1 expected focused after c2h.Hide()")
			}
			c1h.Hide()
			if !editor.focused {
				t.Fatal("editor expected focused after c1h.Hide()")
			}
		})
	})

	t.Run("no-op guards and explicit null target", func(t *testing.T) {
		term := testkit.NewVirtualTerminal(80, 24)
		ui := tui.NewTuiMainScreen(term, nil, nil)
		editor := newOncFocusable([]string{"EDITOR"})
		overlay := newOncFocusable([]string{"OVERLAY"})
		ui.AddChild(&oncEmptyContent{})
		ui.SetFocus(editor)
		ui.Start()
		defer ui.Stop(nil)

		handle := ui.ShowOverlay(overlay, &tui.OverlayOptions{NonCapturing: oncBoolPtr(true)})
		handle.SetHidden(true)
		handle.Focus()
		if !editor.focused || handle.IsFocused() {
			t.Fatal("Focus on hidden overlay should be no-op")
		}
		handle.SetHidden(false)
		handle.Unfocus(nil) // no-op when not focused
		if !editor.focused || overlay.focused {
			t.Fatal("Unfocus on unfocused overlay should be no-op")
		}
		handle.Hide()
		handle.Focus() // no-op after hide
		if !editor.focused || handle.IsFocused() {
			t.Fatal("Focus after hide should be no-op")
		}

		// Explicit nil target clears focus without restoring overlays.
		ov2 := newOncFocusable([]string{"OV2"})
		h2 := ui.ShowOverlay(ov2, nil)
		h2.Unfocus(&tui.OverlayUnfocusOptions{Target: nil})
		term.SendInput("x")
		if len(ov2.inputs) != 0 || h2.IsFocused() {
			t.Fatalf("explicit nil target failed: inputs=%v focused=%v", ov2.inputs, h2.IsFocused())
		}
	})

	t.Run("focus cycle prevention and visual rendering order", func(t *testing.T) {
		term := testkit.NewVirtualTerminal(20, 6)
		ui := tui.NewTuiMainScreen(term, nil, nil)
		editor := newOncFocusable([]string{"EDITOR"})
		ui.AddChild(&oncEmptyContent{})
		ui.SetFocus(editor)
		ui.Start()
		defer ui.Stop(nil)

		opt := &tui.OverlayOptions{
			Row:          &tui.SizeValueCells{Value: 0},
			Col:          &tui.SizeValueCells{Value: 0},
			Width:        &tui.SizeValueCells{Value: 1},
			NonCapturing: oncBoolPtr(true),
		}
		a := ui.ShowOverlay(&oncStaticOverlay{lines: []string{"A"}}, opt)
		b := ui.ShowOverlay(&oncStaticOverlay{lines: []string{"B"}}, opt)
		c := ui.ShowOverlay(&oncStaticOverlay{lines: []string{"C"}}, opt)

		// Bumping middle overlay puts B on top, preserves A vs C relative order.
		oncRenderAndFlush(ui, term)
		if got := term.GetViewport()[0]; got != "C" {
			t.Fatalf("initial top = %q, want C", got)
		}
		b.Focus()
		oncRenderAndFlush(ui, term)
		if got := term.GetViewport()[0]; got != "B" {
			t.Fatalf("after b.Focus top = %q, want B", got)
		}
		b.Unfocus(nil)
		oncRenderAndFlush(ui, term)
		if got := term.GetViewport()[0]; got != "B" {
			t.Fatalf("Unfocus keeps visual order B, got %q", got)
		}
		b.Hide()
		oncRenderAndFlush(ui, term)
		if got := term.GetViewport()[0]; got != "C" {
			t.Fatalf("after b.Hide top = %q, want C", got)
		}
		c.Hide()
		oncRenderAndFlush(ui, term)
		if got := term.GetViewport()[0]; got != "A" {
			t.Fatalf("after c.Hide top = %q, want A", got)
		}
		a.Hide()

		// Cycling explicit unfocus target between 3 overlays and editor.
		oa := newOncFocusable([]string{"A"})
		ob := newOncFocusable([]string{"B"})
		oc := newOncFocusable([]string{"C"})
		ah := ui.ShowOverlay(oa, nil)
		bh := ui.ShowOverlay(ob, nil)
		ch := ui.ShowOverlay(oc, nil)

		ah.Focus()
		term.SendInput("a")
		bh.Focus()
		term.SendInput("b")
		ch.Focus()
		term.SendInput("c")
		ch.Unfocus(&tui.OverlayUnfocusOptions{Target: editor})
		term.SendInput("e")
		ah.Focus()
		term.SendInput("A")
		ah.Unfocus(&tui.OverlayUnfocusOptions{Target: editor})
		term.SendInput("E")
		if !reflect.DeepEqual(oa.inputs, []string{"a", "A"}) ||
			!reflect.DeepEqual(ob.inputs, []string{"b"}) ||
			!reflect.DeepEqual(oc.inputs, []string{"c"}) ||
			!reflect.DeepEqual(editor.inputs, []string{"e", "E"}) ||
			!editor.focused {
			t.Fatalf("cycle routing mismatch: a=%v b=%v c=%v ed=%v(%v)", oa.inputs, ob.inputs, oc.inputs, editor.inputs, editor.focused)
		}
	})
}

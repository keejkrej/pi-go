package tui_test

import (
	"testing"

	"github.com/keejkrej/pi-go/tui"
	"github.com/keejkrej/pi-go/tui/internal/testkit"
)

type shrinkDynamicComp struct {
	lines []string
}

func (c *shrinkDynamicComp) Render(width int) []string { return c.lines }
func (c *shrinkDynamicComp) Invalidate()               {}

func TestTuiShrinking(t *testing.T) {
	t.Run("clears stale trailing lines when content shrinks", func(t *testing.T) {
		term := testkit.NewVirtualTerminal(40, 10)
		ui := tui.NewTuiMainScreen(term, nil, nil)
		comp := &shrinkDynamicComp{
			lines: []string{"line 1", "line 2", "line 3", "line 4", "line 5", "line 6"},
		}
		ui.AddChild(comp)

		ui.Start()
		ui.RenderNow(nil)
		vp := term.FlushAndGetViewport()
		if vp[0] != "line 1" || vp[5] != "line 6" {
			ui.Stop(nil)
			t.Fatalf("initial viewport = %#v", vp)
		}

		comp.lines = []string{"short 1", "short 2"}
		ui.Invalidate()
		ui.RenderNow(nil)
		vp = term.FlushAndGetViewport()
		ui.Stop(nil)

		if vp[0] != "short 1" || vp[1] != "short 2" {
			t.Fatalf("shrunk top lines = %#v", vp[:4])
		}
		for i := 2; i < len(vp); i++ {
			if vp[i] != "" {
				t.Fatalf("stale content at row %d after shrink: %q (vp=%#v)", i, vp[i], vp)
			}
		}
	})

	t.Run("clearOnShrink clears screen and triggers redraw cleanly", func(t *testing.T) {
		term := testkit.NewVirtualTerminal(40, 10)
		ui := tui.NewTuiMainScreen(term, nil, nil)
		ui.SetClearOnShrink(true)
		if !ui.GetClearOnShrink() {
			t.Fatalf("GetClearOnShrink() = false, want true")
		}
		comp := &shrinkDynamicComp{
			lines: []string{"one", "two", "three", "four", "five"},
		}
		ui.AddChild(comp)

		ui.Start()
		ui.RenderNow(nil)
		_ = term.FlushAndGetViewport()
		redrawsBefore := ui.FullRedraws()

		comp.lines = []string{"only one"}
		ui.Invalidate()
		ui.RenderNow(nil)
		vp := term.FlushAndGetViewport()
		ui.Stop(nil)

		if ui.FullRedraws() <= redrawsBefore {
			t.Fatalf("expected full redraw on shrink with clearOnShrink enabled: before=%d after=%d", redrawsBefore, ui.FullRedraws())
		}
		if vp[0] != "only one" {
			t.Fatalf("vp[0] = %q, want %q", vp[0], "only one")
		}
		for i := 1; i < len(vp); i++ {
			if vp[i] != "" {
				t.Fatalf("stale row %d = %q, want empty", i, vp[i])
			}
		}
	})
}

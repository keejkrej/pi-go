package tui_test

import (
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/tui"
	"github.com/keejkrej/pi-go/tui/internal/testkit"
)

type shortContentStaticComp struct {
	lines []string
}

func (c *shortContentStaticComp) Render(width int) []string { return c.lines }
func (c *shortContentStaticComp) Invalidate()               {}

func TestOverlayShortContent(t *testing.T) {
	term := testkit.NewVirtualTerminal(40, 12)
	ui := tui.NewTuiMainScreen(term, nil, nil)
	ui.AddChild(&shortContentStaticComp{lines: []string{"short line 0", "short line 1"}})

	anchor := tui.OverlayAnchorCenter
	handle := ui.ShowOverlay(&shortContentStaticComp{lines: []string{"POPUP A", "POPUP B"}}, &tui.OverlayOptions{
		Anchor: &anchor,
		Width:  &tui.SizeValueCells{Value: 12},
	})

	ui.Start()
	ui.RenderNow(nil)
	vp := term.FlushAndGetViewport()

	found := false
	for _, row := range vp {
		if strings.Contains(row, "POPUP A") {
			found = true
			break
		}
	}
	if !found {
		ui.Stop(nil)
		t.Fatalf("overlay not rendered below short content: %#v", vp)
	}
	bounds := handle.GetBounds()
	if bounds == nil || bounds.Row < 2 {
		ui.Stop(nil)
		t.Fatalf("expected centered overlay below row 2, got bounds=%+v", bounds)
	}

	ui.HideOverlay()
	ui.RenderNow(nil)
	vp = term.FlushAndGetViewport()
	ui.Stop(nil)

	if vp[0] != "short line 0" || vp[1] != "short line 1" {
		t.Fatalf("unexpected top lines after HideOverlay: %#v", vp[:3])
	}
	for i := 2; i < len(vp); i++ {
		if vp[i] != "" {
			t.Fatalf("leftover overlay artifact at row %d: %q (vp=%#v)", i, vp[i], vp)
		}
	}
}

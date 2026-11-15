package tui_test

import (
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/keejkrej/pi-go/tui"
	"github.com/keejkrej/pi-go/tui/internal/testkit"
)

type styleLeakStaticComp struct {
	lines []string
}

func (c *styleLeakStaticComp) Render(width int) []string { return c.lines }
func (c *styleLeakStaticComp) Invalidate()               {}

func TestTuiOverlayStyleLeak(t *testing.T) {
	t.Run("base italic style does not leak into overlay text", func(t *testing.T) {
		term := testkit.NewVirtualTerminal(40, 6)
		ui := tui.NewTuiMainScreen(term, nil, nil)
		base := "\x1b[3m" + strings.Repeat("x", 30) + "\x1b[0m"
		ui.AddChild(&styleLeakStaticComp{lines: []string{base, base, base}})

		ui.ShowOverlay(&styleLeakStaticComp{lines: []string{"PLAIN"}}, &tui.OverlayOptions{
			Width: &tui.SizeValueCells{Value: 7},
			Row:   &tui.SizeValueCells{Value: 1},
			Col:   &tui.SizeValueCells{Value: 10},
		})

		ui.Start()
		ui.RenderNow(nil)
		viewport := term.FlushAndGetViewport()

		if !strings.Contains(viewport[1], "PLAIN") {
			ui.Stop(nil)
			t.Fatalf("viewport[1] = %q, expected PLAIN", viewport[1])
		}
		for x := 10; x < 17; x++ {
			cell := term.CellAt(x, 1)
			if cell != nil && cell.Style.Attrs&uv.AttrItalic != 0 {
				ui.Stop(nil)
				t.Fatalf("overlay cell (%d,1) %q leaked AttrItalic", x, cell.Content)
			}
		}
		if cell := term.CellAt(2, 1); cell == nil || cell.Style.Attrs&uv.AttrItalic == 0 {
			ui.Stop(nil)
			t.Fatalf("base cell (2,1) expected AttrItalic, got %#v", cell)
		}
		ui.Stop(nil)
	})

	t.Run("unclosed overlay SGR does not leak into trailing base text", func(t *testing.T) {
		term := testkit.NewVirtualTerminal(40, 6)
		ui := tui.NewTuiMainScreen(term, nil, nil)
		base := strings.Repeat("a", 30)
		ui.AddChild(&styleLeakStaticComp{lines: []string{base, base, base}})

		ui.ShowOverlay(&styleLeakStaticComp{lines: []string{"\x1b[3mITALIC"}}, &tui.OverlayOptions{
			Width: &tui.SizeValueCells{Value: 6},
			Row:   &tui.SizeValueCells{Value: 1},
			Col:   &tui.SizeValueCells{Value: 5},
		})

		ui.Start()
		ui.RenderNow(nil)
		_ = term.FlushAndGetViewport()

		if cell := term.CellAt(6, 1); cell == nil || cell.Style.Attrs&uv.AttrItalic == 0 {
			ui.Stop(nil)
			t.Fatalf("overlay cell (6,1) expected AttrItalic, got %#v", cell)
		}
		for x := 11; x < 20; x++ {
			cell := term.CellAt(x, 1)
			if cell != nil && cell.Style.Attrs&uv.AttrItalic != 0 {
				ui.Stop(nil)
				t.Fatalf("trailing base cell (%d,1) %q leaked AttrItalic from overlay", x, cell.Content)
			}
		}
		ui.Stop(nil)
	})
}

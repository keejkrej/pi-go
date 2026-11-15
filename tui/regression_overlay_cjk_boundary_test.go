package tui_test

import (
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/tui"
	"github.com/keejkrej/pi-go/tui/internal/testkit"
)

type cjkBoundaryStaticComp struct {
	lines []string
}

func (c *cjkBoundaryStaticComp) Render(width int) []string { return c.lines }
func (c *cjkBoundaryStaticComp) Invalidate()               {}

func TestRegressionOverlayCjkBoundary(t *testing.T) {
	term := testkit.NewVirtualTerminal(40, 6)
	ui := tui.NewTuiMainScreen(term, nil, nil)
	baseLine := strings.Repeat("あ", 20)
	ui.AddChild(&cjkBoundaryStaticComp{lines: []string{baseLine, baseLine, baseLine}})

	width := &tui.SizeValueCells{Value: 10}
	row := &tui.SizeValueCells{Value: 1}
	col := &tui.SizeValueCells{Value: 5} // Lands on second column of 3rd double-width 'あ'.
	ui.ShowOverlay(&cjkBoundaryStaticComp{lines: []string{"OVERLAY"}}, &tui.OverlayOptions{
		Width: width,
		Row:   row,
		Col:   col,
	})

	rendered := ui.Render(40)
	for i, line := range rendered {
		if w := tui.VisibleWidth(line); w != 40 {
			t.Fatalf("rendered line %d width = %d, want 40 (%q)", i, w, line)
		}
	}

	ui.Start()
	ui.RenderNow(nil)
	viewport := term.FlushAndGetViewport()
	ui.Stop(nil)

	if len(viewport) < 3 {
		t.Fatalf("viewport len = %d, want >= 3", len(viewport))
	}
	if !strings.Contains(viewport[1], "あ") || !strings.Contains(viewport[1], "OVERLAY") {
		t.Fatalf("viewport[1] = %q, expected preserved CJK prefix and OVERLAY", viewport[1])
	}
	idx := strings.Index(viewport[1], "OVERLAY")
	prefix := viewport[1][:idx]
	if w := tui.VisibleWidth(prefix); w != 5 {
		t.Fatalf("prefix %q visible width before OVERLAY = %d, want 5 (viewport[1]=%q)", prefix, w, viewport[1])
	}
}

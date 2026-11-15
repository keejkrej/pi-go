package components

import (
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/tui"
)

func TestBranchSummaryMessageComponentExpandAndOutputPad(t *testing.T) {
	msg := core.CreateBranchSummaryMessage("## Summary\nDetails on branch.", "node-1", "2026-01-01T00:00:00Z")
	comp := NewBranchSummaryMessageComponent(msg)

	collapsed := utils.StripAnsi(strings.Join(comp.Render(80), "\n"))
	if !strings.Contains(collapsed, "[branch]") || strings.Contains(collapsed, "Details on branch.") {
		t.Fatalf("expected collapsed header without body, got %q", collapsed)
	}

	comp.SetExpanded(true)
	expanded := utils.StripAnsi(strings.Join(comp.Render(80), "\n"))
	if !strings.Contains(expanded, "Details on branch.") {
		t.Fatalf("expected expanded summary body, got %q", expanded)
	}

	// Mouse click on header toggles back to collapsed.
	res := comp.HandleMouse(tui.TuiMouseEvent{
		Type:   tui.TuiMouseEventTypeClick,
		Button: tui.TuiMouseButtonLeft,
		X:      2,
		Y:      1,
		Width:  80,
		Height: len(comp.Render(80)),
	})
	if res == nil || res.Handled == nil || !*res.Handled {
		t.Fatalf("expected click on branch summary header to toggle")
	}
	afterClick := utils.StripAnsi(strings.Join(comp.Render(80), "\n"))
	if strings.Contains(afterClick, "Details on branch.") {
		t.Fatalf("expected click to collapse branch summary, got %q", afterClick)
	}

	comp.SetOutputPad(3)
	for i, l := range comp.Render(50) {
		if tui.VisibleWidth(l) > 50 {
			t.Fatalf("line %d exceeded width 50", i)
		}
	}
}

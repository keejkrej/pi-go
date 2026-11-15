package components

import (
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/tui"
)

func TestCompactionSummaryMessageComponentFormattingAndToggle(t *testing.T) {
	msg := core.CreateCompactionSummaryMessage("Preserved context summary.", 12345, "2026-01-01T00:00:00Z")
	comp := NewCompactionSummaryMessageComponent(msg)

	collapsed := utils.StripAnsi(strings.Join(comp.Render(80), "\n"))
	if !strings.Contains(collapsed, "[compaction]") {
		t.Fatalf("expected [compaction] label, got %q", collapsed)
	}
	if !strings.Contains(collapsed, "12,345") {
		t.Fatalf("expected formatted token count '12,345', got %q", collapsed)
	}
	if strings.Contains(collapsed, "Preserved context summary.") {
		t.Fatalf("unexpected expanded body before SetExpanded(true)")
	}

	comp.SetExpanded(true)
	expanded := utils.StripAnsi(strings.Join(comp.Render(80), "\n"))
	if !strings.Contains(expanded, "Preserved context summary.") {
		t.Fatalf("expected expanded summary body, got %q", expanded)
	}

	comp.SetOutputPad(2)
	for i, l := range comp.Render(60) {
		if tui.VisibleWidth(l) > 60 {
			t.Fatalf("line %d exceeded width 60", i)
		}
	}
}

package components

import (
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/tui"
)

func TestSkillInvocationMessageComponentExpandAndOutputPad(t *testing.T) {
	block := &core.ParsedSkillBlock{
		Name:     "deploy-helper",
		Location: "/skills/deploy/SKILL.md",
		Content:  "# Deploy\nRun checks before deploy.",
	}
	comp := NewSkillInvocationMessageComponent(block)

	collapsed := utils.StripAnsi(strings.Join(comp.Render(80), "\n"))
	if !strings.Contains(collapsed, "[skill]") || !strings.Contains(collapsed, "deploy-helper") {
		t.Fatalf("expected [skill] deploy-helper header, got %q", collapsed)
	}
	if strings.Contains(collapsed, "Run checks before deploy.") {
		t.Fatalf("unexpected skill body before expansion")
	}

	comp.SetExpanded(true)
	expanded := utils.StripAnsi(strings.Join(comp.Render(80), "\n"))
	if !strings.Contains(expanded, "Run checks before deploy.") {
		t.Fatalf("expected skill content when expanded, got %q", expanded)
	}

	res := comp.HandleMouse(tui.TuiMouseEvent{
		Type:   tui.TuiMouseEventTypeClick,
		Button: tui.TuiMouseButtonLeft,
		X:      2,
		Y:      1,
		Width:  80,
		Height: len(comp.Render(80)),
	})
	if res == nil || res.Handled == nil || !*res.Handled {
		t.Fatalf("expected left click on skill header to collapse")
	}

	comp.SetOutputPad(4)
	for i, l := range comp.Render(60) {
		if tui.VisibleWidth(l) > 60 {
			t.Fatalf("line %d exceeded width 60", i)
		}
	}
}

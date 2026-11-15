package components

import (
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/tui"
)

func TestCustomMessageComponentFallbackAndCustomRenderer(t *testing.T) {
	msg := core.CreateCustomMessage("notice", "Hello **custom** world", true, nil, "2026-01-01T00:00:00Z")
	comp := NewCustomMessageComponent(msg, nil)
	plain := utils.StripAnsi(strings.Join(comp.Render(60), "\n"))
	if !strings.Contains(plain, "[notice]") || !strings.Contains(plain, "Hello") {
		t.Fatalf("expected [notice] badge and content, got %q", plain)
	}

	comp.SetOutputPad(3)
	for i, l := range comp.Render(50) {
		if tui.VisibleWidth(l) > 50 {
			t.Fatalf("line %d exceeded width 50", i)
		}
	}

	var gotExpanded bool
	customComp := NewCustomMessageComponent(msg, func(m *core.CustomMessage, opts core.MessageRenderOptions, th *theme.Theme) tui.Component {
		gotExpanded = opts.Expanded
		label := "collapsed"
		if opts.Expanded {
			label = "expanded"
		}
		return tui.NewText("rendered-"+label, 0, 0, nil)
	})
	if p := utils.StripAnsi(strings.Join(customComp.Render(60), "\n")); !strings.Contains(p, "rendered-collapsed") || gotExpanded {
		t.Fatalf("expected custom collapsed render, got %q", p)
	}
	customComp.SetExpanded(true)
	if p := utils.StripAnsi(strings.Join(customComp.Render(60), "\n")); !strings.Contains(p, "rendered-expanded") || !gotExpanded {
		t.Fatalf("expected custom expanded render, got %q", p)
	}

	// Panicking renderer falls back safely without crashing.
	panicComp := NewCustomMessageComponent(msg, func(*core.CustomMessage, core.MessageRenderOptions, *theme.Theme) tui.Component {
		panic("bad extension renderer")
	})
	pFallback := utils.StripAnsi(strings.Join(panicComp.Render(60), "\n"))
	if !strings.Contains(pFallback, "[notice]") {
		t.Fatalf("expected fallback after renderer panic, got %q", pFallback)
	}
}

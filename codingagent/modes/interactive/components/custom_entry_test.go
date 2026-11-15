package components

import (
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/tui"
)

func TestCustomEntryComponentHasContentToggleAndPanicSafety(t *testing.T) {
	entry := &core.CustomEntry{CustomType: "plugin-note", Data: map[string]any{"ok": true}}

	noRenderer := NewCustomEntryComponent(entry, nil)
	if noRenderer.HasContent() {
		t.Fatalf("expected HasContent false with nil renderer")
	}
	if len(noRenderer.Render(60)) != 0 {
		t.Fatalf("expected empty lines when HasContent is false")
	}

	comp := NewCustomEntryComponent(entry, func(e *core.CustomEntry, opts core.EntryRenderOptions, th *theme.Theme) tui.Component {
		state := "closed"
		if opts.Expanded {
			state = "open"
		}
		return tui.NewText(e.CustomType+":"+state, 0, 0, nil)
	}, 2)
	if !comp.HasContent() {
		t.Fatalf("expected HasContent true")
	}
	if got := utils.StripAnsi(strings.Join(comp.Render(60), "\n")); !strings.Contains(got, "plugin-note:closed") {
		t.Fatalf("unexpected initial render %q", got)
	}
	comp.SetExpanded(true)
	if got := utils.StripAnsi(strings.Join(comp.Render(60), "\n")); !strings.Contains(got, "plugin-note:open") {
		t.Fatalf("expected expanded entry render, got %q", got)
	}

	comp.SetOutputPad(4)
	for i, l := range comp.Render(40) {
		if tui.VisibleWidth(l) > 40 {
			t.Fatalf("line %d exceeded width 40", i)
		}
	}

	panicky := NewCustomEntryComponent(entry, func(*core.CustomEntry, core.EntryRenderOptions, *theme.Theme) tui.Component {
		panic("boom")
	})
	if panicky.HasContent() || len(panicky.Render(40)) != 0 {
		t.Fatalf("expected panicked entry renderer to fail closed with HasContent=false")
	}
}

// Ported from packages/coding-agent/test/interactive-tui.test.ts (pi v1.5.2).

package interactive

import (
	"testing"

	"github.com/keejkrej/pi-go/tui"
)

func TestCreateInteractiveTuiRegularAndFullscreen(t *testing.T) {
	tithInitTestTheme(t)

	regTerm := tithNewVirtualTerminal(80, 24)
	t.Cleanup(regTerm.tithClose)
	reg := CreateInteractiveTui(InteractiveTuiOptions{
		TuiMode:            tui.TuiModeRegular,
		Terminal:           regTerm,
		ShowHardwareCursor: true,
	})
	if reg == nil {
		t.Fatalf("CreateInteractiveTui(regular) returned nil")
	}
	if reg.Mode() != tui.TuiModeRegular {
		t.Fatalf("expected regular mode, got %q", reg.Mode())
	}
	if !reg.GetShowHardwareCursor() {
		t.Fatalf("expected hardware cursor enabled")
	}
	if tui.IsViewportTui(reg) {
		t.Fatalf("regular TUI must not report ViewportTui")
	}

	copyOnSel := true
	wheel := tui.WheelScrollLines{Lines: 5}
	fullTerm := tithNewVirtualTerminal(80, 24)
	t.Cleanup(fullTerm.tithClose)
	full := CreateInteractiveTui(InteractiveTuiOptions{
		TuiMode:                    tui.TuiModeFullscreen,
		Terminal:                   fullTerm,
		FullscreenCopyOnSelect:     &copyOnSel,
		FullscreenWheelScrollLines: &wheel,
	})
	if full == nil {
		t.Fatalf("CreateInteractiveTui(fullscreen) returned nil")
	}
	if full.Mode() != tui.TuiModeFullscreen {
		t.Fatalf("expected fullscreen mode, got %q", full.Mode())
	}
	if !tui.IsViewportTui(full) {
		t.Fatalf("fullscreen TUI must report ViewportTui")
	}
}

func TestCreateInteractiveTuiReferenceTracksReboundTui(t *testing.T) {
	tithInitTestTheme(t)

	termA := tithNewVirtualTerminal(80, 24)
	t.Cleanup(termA.tithClose)
	termB := tithNewVirtualTerminal(80, 24)
	t.Cleanup(termB.tithClose)
	tuiA := CreateInteractiveTui(InteractiveTuiOptions{
		TuiMode:  tui.TuiModeRegular,
		Terminal: termA,
	})
	tuiB := CreateInteractiveTui(InteractiveTuiOptions{
		TuiMode:  tui.TuiModeFullscreen,
		Terminal: termB,
	})

	active := tuiA
	ref := CreateInteractiveTuiReference(func() tui.TUI { return active })

	if ref.Mode() != tui.TuiModeRegular {
		t.Fatalf("expected ref.Mode() == regular, got %q", ref.Mode())
	}
	if tui.IsViewportTui(ref) {
		t.Fatalf("ref.ViewportTui() should be false while pointing to regular")
	}

	stub := &tithStubComponent{lines: []string{"hello"}}
	ref.AddChild(stub)
	if len(tuiA.Children()) != 1 {
		t.Fatalf("expected child forwarded to tuiA, got %d", len(tuiA.Children()))
	}

	active = tuiB
	if ref.Mode() != tui.TuiModeFullscreen {
		t.Fatalf("expected ref.Mode() == fullscreen after rebind, got %q", ref.Mode())
	}
	if !tui.IsViewportTui(ref) {
		t.Fatalf("ref.ViewportTui() should be true after rebind to fullscreen")
	}
	if vtRef, ok := ref.(tui.ViewportTUI); ok {
		vtRef.SetLayoutRoot(stub)
	}
}

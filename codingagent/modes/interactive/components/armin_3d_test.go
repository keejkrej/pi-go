package components

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/tui"
)

type ta3MockTerm struct {
	tui.Terminal
	columns int
	rows    int
}

func (m ta3MockTerm) Columns() int { return m.columns }
func (m ta3MockTerm) Rows() int    { return m.rows }

type ta3OverlayHandle struct {
	hidden bool
	onHide func()
}

func (h *ta3OverlayHandle) Hide() {
	h.hidden = true
	if h.onHide != nil {
		h.onHide()
	}
}
func (h *ta3OverlayHandle) SetHidden(bool)                     {}
func (h *ta3OverlayHandle) IsHidden() bool                     { return h.hidden }
func (h *ta3OverlayHandle) Focus()                             {}
func (h *ta3OverlayHandle) Unfocus(*tui.OverlayUnfocusOptions) {}
func (h *ta3OverlayHandle) IsFocused() bool                    { return true }
func (h *ta3OverlayHandle) GetBounds() *tui.OverlayBounds      { return nil }

type ta3MockTUI struct {
	tui.TUI
	term        ta3MockTerm
	screenLines []string

	mu          sync.Mutex
	overlayComp tui.Component
	handle      *ta3OverlayHandle
	hasOverlay  bool
	renders     int
}

func (m *ta3MockTUI) Terminal() tui.Terminal   { return m.term }
func (m *ta3MockTUI) GetScreenLines() []string { return m.screenLines }
func (m *ta3MockTUI) Post(fn func()) {
	if fn != nil {
		fn()
	}
}
func (m *ta3MockTUI) RequestRender(*bool) {
	m.mu.Lock()
	m.renders++
	m.mu.Unlock()
}
func (m *ta3MockTUI) HasOverlay() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.hasOverlay
}
func (m *ta3MockTUI) QueryTerminalColors(tui.TuiQueryTerminalColorsOptions) (tui.TerminalColors, error) {
	return tui.TerminalColors{
		Background: &tui.RgbColor{R: 18, G: 20, B: 26},
		Foreground: &tui.RgbColor{R: 230, G: 235, B: 245},
	}, nil
}
func (m *ta3MockTUI) ShowOverlay(comp tui.Component, opts *tui.OverlayOptions) tui.OverlayHandle {
	m.mu.Lock()
	m.overlayComp = comp
	m.hasOverlay = true
	h := &ta3OverlayHandle{
		onHide: func() {
			m.mu.Lock()
			m.hasOverlay = false
			m.overlayComp = nil
			m.mu.Unlock()
		},
	}
	m.handle = h
	m.mu.Unlock()
	return h
}

type ta3NonAltTUI struct {
	tui.TUI
}

func TestEasterEgg3dOverlayLifecycleAndCancel(t *testing.T) {
	if PlayArmin3d(&ta3NonAltTUI{}) {
		t.Fatalf("expected non-alt TUI without GetScreenLines to return false")
	}

	ui := &ta3MockTUI{
		term:        ta3MockTerm{columns: 40, rows: 16},
		screenLines: []string{"hello world", "\x1b[38;2;255;100;50mstyled text\x1b[0m"},
	}

	if !PlayPiLogo3d(ui, 4, 2) {
		t.Fatalf("expected PlayPiLogo3d to launch")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ui.mu.Lock()
		comp := ui.overlayComp
		ui.mu.Unlock()
		if comp != nil {
			// Duplicate launch while overlay active is a no-op (returns true without replacing overlay).
			if !PlayArmin3d(ui) {
				t.Fatalf("expected duplicate PlayArmin3d while overlay active to return true (handled)")
			}
			ui.mu.Lock()
			same := ui.overlayComp == comp
			ui.mu.Unlock()
			if !same {
				t.Fatalf("expected duplicate PlayArmin3d not to replace active overlay")
			}
			lines := comp.Render(40)
			if len(lines) != 16 {
				t.Fatalf("expected 16 overlay lines, got %d", len(lines))
			}
			for _, line := range lines {
				if strings.TrimSpace(line) == "" {
					t.Fatalf("expected non-empty rasterized frame lines")
				}
			}
			// Press Escape to trigger exit & dispose.
			if ih, ok := comp.(interface{ HandleInput(string) }); ok {
				ih.HandleInput("\x1b")
			}
			break
		}
		time.Sleep(15 * time.Millisecond)
	}

	// Wait briefly for Hide() completion after exit animation.
	hideDeadline := time.Now().Add(2 * time.Second)
	for ui.HasOverlay() && time.Now().Before(hideDeadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if ui.HasOverlay() {
		t.Fatalf("expected 3d overlay to close after Escape")
	}

	if !PlayArmin3d(ui) {
		t.Fatalf("expected PlayArmin3d to launch cleanly after prior overlay closed")
	}
	time.Sleep(80 * time.Millisecond)
	ui.mu.Lock()
	h := ui.handle
	if disp, ok := ui.overlayComp.(interface{ Dispose() }); ok && disp != nil {
		disp.Dispose()
	}
	ui.mu.Unlock()
	if h != nil {
		h.Hide()
	}
}

func TestEasterEgg3dHelperFunctions(t *testing.T) {
	if PlayArmin3d(nil) || PlayPiLogo3d(nil, 0, 0) {
		t.Fatalf("expected nil TUI to return false cleanly")
	}
	cells := a3ParseScreen([]string{"\x1b[38;2;10;20;30mA\x1b[0mB"}, 4, a3Rgb{255, 255, 255}, a3Rgb{0, 0, 0})
	if len(cells) != 1 || len(cells[0]) != 2 {
		t.Fatalf("unexpected parsed grid dimensions: %dx%d", len(cells), len(cells[0]))
	}
	if cells[0][0].text != "A" || cells[0][0].fg != (a3Rgb{10, 20, 30}) {
		t.Fatalf("unexpected cell[0][0]: %+v", cells[0][0])
	}
	if cells[0][1].text != "B" || cells[0][1].fg != (a3Rgb{255, 255, 255}) {
		t.Fatalf("unexpected reset cell[0][1]: %+v", cells[0][1])
	}
}

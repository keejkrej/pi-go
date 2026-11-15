package components

import (
	"sync"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/tui"
)

type tarminMockTUI struct {
	tui.TUI
	mu      sync.Mutex
	renders int
}

func (m *tarminMockTUI) Post(fn func()) {
	if fn != nil {
		fn()
	}
}

func (m *tarminMockTUI) RequestRender(*bool) {
	m.mu.Lock()
	m.renders++
	m.mu.Unlock()
}

func TestArminComponentAndBitmap(t *testing.T) {
	hasSet := false
	for y := 0; y < ArminHeight; y++ {
		for x := 0; x < ArminWidth; x++ {
			if IsArminPixel(x, y) {
				hasSet = true
				break
			}
		}
	}
	if !hasSet {
		t.Fatalf("expected Armin bitmap to have set pixels")
	}
	if IsArminPixel(-1, 0) || IsArminPixel(ArminWidth, 0) || IsArminPixel(0, ArminHeight) {
		t.Fatalf("expected out-of-bounds pixels to return false")
	}

	ui := &tarminMockTUI{}
	for _, eff := range arminEffects {
		comp := arminNewComponentWithEffect(ui, eff)
		lines := comp.Render(80)
		if len(lines) != ArminHeight/2+1 {
			t.Fatalf("effect %s rendered %d lines, want %d", eff, len(lines), ArminHeight/2+1)
		}
		for _, line := range lines {
			if w := tui.VisibleWidth(line); w > 80 {
				t.Fatalf("effect %s exceeded width 80: %d", eff, w)
			}
		}
		comp.Invalidate()
		comp.Dispose()
	}

	// Verify animation ticker advances and Dispose stops cleanly.
	comp := NewArminComponent(ui)
	time.Sleep(120 * time.Millisecond)
	comp.Dispose()
	ui.mu.Lock()
	r := ui.renders
	ui.mu.Unlock()
	if r == 0 {
		t.Fatalf("expected Armin animation to request at least one render")
	}
}

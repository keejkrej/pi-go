package components

import (
	"sync"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/tui"
)

type tpbMockTUI struct {
	tui.TUI
	mu      sync.Mutex
	renders int
}

func (m *tpbMockTUI) Post(fn func()) {
	if fn != nil {
		fn()
	}
}

func (m *tpbMockTUI) RequestRender(*bool) {
	m.mu.Lock()
	m.renders++
	m.mu.Unlock()
}

func TestProviderBrandStylesAndAnimation(t *testing.T) {
	style := GetProviderBrandStyle("openrouter")
	if style == nil {
		t.Fatalf("expected non-nil ProviderBrandStyle for openrouter")
	}
	styled := style("OpenRouter", 250)
	if utils.StripAnsi(styled) != "OpenRouter" {
		t.Fatalf("expected visible text preserved, got %q", utils.StripAnsi(styled))
	}

	if none := GetProviderBrandStyle("nonexistent-provider"); none != nil {
		t.Fatalf("expected nil brand style for unknown provider")
	}

	shimmer := RadiusShimmer("radius-brand", 120)
	if utils.StripAnsi(shimmer) != "radius-brand" {
		t.Fatalf("expected RadiusShimmer to preserve characters, got %q", utils.StripAnsi(shimmer))
	}

	ui := &tpbMockTUI{}
	animating := true
	stop := StartBrandAnimation(ui, func() bool { return animating })
	deadline := time.Now().Add(800 * time.Millisecond)
	for time.Now().Before(deadline) {
		ui.mu.Lock()
		n := ui.renders
		ui.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	stop()
	animating = false

	ui.mu.Lock()
	renders := ui.renders
	ui.mu.Unlock()
	if renders == 0 {
		t.Fatalf("expected StartBrandAnimation to trigger RequestRender")
	}
}

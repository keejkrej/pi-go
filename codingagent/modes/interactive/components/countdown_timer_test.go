package components

import (
	"sync"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/tui"
)

type tctMockTUI struct {
	tui.TUI
	mu      sync.Mutex
	renders int
}

func (m *tctMockTUI) Post(fn func()) {
	if fn != nil {
		fn()
	}
}

func (m *tctMockTUI) RequestRender(*bool) {
	m.mu.Lock()
	m.renders++
	m.mu.Unlock()
}

func TestCountdownTimerTicksAndExpires(t *testing.T) {
	ui := &tctMockTUI{}
	var mu sync.Mutex
	var ticks []int
	expiredCh := make(chan struct{}, 2)

	timer := NewCountdownTimer(1100, ui, func(sec int) {
		mu.Lock()
		ticks = append(ticks, sec)
		mu.Unlock()
	}, func() {
		expiredCh <- struct{}{}
	})
	defer timer.Dispose()

	mu.Lock()
	initial := append([]int(nil), ticks...)
	mu.Unlock()
	if len(initial) != 1 || initial[0] != 2 {
		t.Fatalf("expected immediate initial tick ceil(1100ms)=2, got %v", initial)
	}

	select {
	case <-expiredCh:
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for countdown timer expiry")
	}

	mu.Lock()
	finalTicks := append([]int(nil), ticks...)
	mu.Unlock()
	if len(finalTicks) < 2 {
		t.Fatalf("expected subsequent countdown tick before expiry, got %v", finalTicks)
	}
}

func TestCountdownTimerDisposeStopsExpiry(t *testing.T) {
	ui := &tctMockTUI{}
	expired := make(chan struct{}, 1)

	timer := NewCountdownTimer(400, ui, nil, func() {
		expired <- struct{}{}
	})
	timer.Dispose()

	select {
	case <-expired:
		t.Fatalf("expected disposed timer not to call onExpire")
	case <-time.After(700 * time.Millisecond):
	}
}

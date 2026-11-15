// Ported from packages/coding-agent/src/modes/interactive/components/countdown-timer.ts (pi v1.5.2).

package components

import (
	"math"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/tui"
)

// CountdownTimer fires per-second countdown callbacks for dialog components and expires when remaining seconds reach 0.
type CountdownTimer struct {
	mu               sync.Mutex
	stopCh           chan struct{}
	stopOnce         sync.Once
	disposed         bool
	cancelled        bool
	remainingSeconds int
	ui               tui.TUI
	onTick           func(seconds int)
	onExpire         func()
}

// NewCountdownTimer immediately emits onTick(ceil(timeoutMs/1000)) and ticks every second until <= 0 or Dispose.
func NewCountdownTimer(timeoutMs int64, ui tui.TUI, onTick func(seconds int), onExpire func()) *CountdownTimer {
	initial := int(math.Ceil(float64(timeoutMs) / 1000.0))
	c := &CountdownTimer{
		stopCh:           make(chan struct{}),
		remainingSeconds: initial,
		ui:               ui,
		onTick:           onTick,
		onExpire:         onExpire,
	}
	if onTick != nil {
		onTick(c.remainingSeconds)
	}
	go c.run()
	return c
}

func (c *CountdownTimer) run() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopCh:
			return
		case <-ticker.C:
		}

		c.mu.Lock()
		if c.disposed {
			c.mu.Unlock()
			return
		}
		c.remainingSeconds--
		sec := c.remainingSeconds
		expired := sec <= 0
		if expired {
			c.disposed = true
			c.stopOnce.Do(func() { close(c.stopCh) })
		}
		ui := c.ui
		onTick := c.onTick
		onExpire := c.onExpire
		c.mu.Unlock()

		deliver := func() {
			c.mu.Lock()
			cancelled := c.cancelled
			c.mu.Unlock()
			if cancelled {
				return
			}
			if onTick != nil {
				onTick(sec)
			}
			if ui != nil {
				ui.RequestRender(nil)
			}
			if expired && onExpire != nil {
				onExpire()
			}
		}

		if ui != nil {
			ui.Post(deliver)
		} else {
			deliver()
		}

		if expired {
			return
		}
	}
}

// Dispose stops the countdown ticker without calling onExpire.
func (c *CountdownTimer) Dispose() {
	c.mu.Lock()
	c.disposed = true
	c.cancelled = true
	c.stopOnce.Do(func() { close(c.stopCh) })
	c.mu.Unlock()
}

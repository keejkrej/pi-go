// Ported from packages/coding-agent/test/experimental-server-lifecycle.test.ts (pi v1.0.0).

package experimental_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/codingagent/experimental"
)

type teslScheduledTimer struct {
	deadline time.Duration
	fn       func()
	stopped  bool
}

type teslFakeClock struct {
	mu     sync.Mutex
	now    time.Duration
	timers []*teslScheduledTimer
}

func (c *teslFakeClock) AfterFunc(d time.Duration, fn func()) func() bool {
	c.mu.Lock()
	timer := &teslScheduledTimer{
		deadline: c.now + d,
		fn:       fn,
	}
	c.timers = append(c.timers, timer)
	c.mu.Unlock()

	return func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		if timer.stopped {
			return false
		}
		timer.stopped = true
		return true
	}
}

func (c *teslFakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	target := c.now + d
	c.mu.Unlock()

	for {
		c.mu.Lock()
		var next *teslScheduledTimer
		for _, item := range c.timers {
			if item.stopped || item.deadline > target {
				continue
			}
			if next == nil || item.deadline < next.deadline {
				next = item
			}
		}
		if next == nil {
			c.now = target
			c.mu.Unlock()
			break
		}
		next.stopped = true
		c.now = next.deadline
		c.mu.Unlock()

		next.fn()
	}
}

func TestServerLifecycle(t *testing.T) {
	t.Run("holds a foreground server until explicit shutdown", func(t *testing.T) {
		clock := &teslFakeClock{}
		var retired atomic.Int32
		lifetime := experimental.NewServerLifetimeWithOptions(true, experimental.ServerLifetimeOptions{
			AfterFunc: clock.AfterFunc,
		})
		lifetime.Start(func() { retired.Add(1) })
		clock.Advance(60 * time.Second)
		if got := retired.Load(); got != 0 {
			t.Fatalf("expected foreground server not to retire, got %d", got)
		}
		lifetime.Stop()
	})

	t.Run("retires an automatic server if no first client arrives", func(t *testing.T) {
		clock := &teslFakeClock{}
		var retired atomic.Int32
		lifetime := experimental.NewServerLifetimeWithOptions(false, experimental.ServerLifetimeOptions{
			AfterFunc: clock.AfterFunc,
		})
		lifetime.Start(func() { retired.Add(1) })

		clock.Advance(10999 * time.Millisecond)
		if got := retired.Load(); got != 0 {
			t.Fatalf("expected no retirement before 11000ms, got %d", got)
		}
		clock.Advance(1 * time.Millisecond)
		if got := retired.Load(); got != 1 {
			t.Fatalf("expected single retirement at 11000ms, got %d", got)
		}
		lifetime.Stop()
	})

	t.Run("requires both client and worker demand to disappear", func(t *testing.T) {
		clock := &teslFakeClock{}
		var retired atomic.Int32
		lifetime := experimental.NewServerLifetimeWithOptions(false, experimental.ServerLifetimeOptions{
			AfterFunc: clock.AfterFunc,
		})
		lifetime.Start(func() { retired.Add(1) })
		lifetime.SetConnectionCount(1)
		lifetime.SetWorkerCount(1)
		lifetime.SetConnectionCount(0)

		clock.Advance(10 * time.Second)
		if got := retired.Load(); got != 0 {
			t.Fatalf("expected worker demand to prevent retirement, got %d", got)
		}

		lifetime.SetWorkerCount(0)
		clock.Advance(999 * time.Millisecond)
		if got := retired.Load(); got != 0 {
			t.Fatalf("expected idle grace to wait full 1000ms, got %d", got)
		}
		clock.Advance(1 * time.Millisecond)
		if got := retired.Load(); got != 1 {
			t.Fatalf("expected single retirement after idle grace elapsed, got %d", got)
		}
		lifetime.Stop()
	})
}

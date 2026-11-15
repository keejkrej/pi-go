// Ported from packages/coding-agent/test/experimental-session-worker-lifecycle.test.ts (pi v1.0.0).

package experimental_test

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/codingagent/experimental"
)

const teswlGeneration = "generation-1"

type teswlScheduledTimer struct {
	deadline time.Duration
	fn       func()
	stopped  bool
}

type teswlFakeClock struct {
	mu     sync.Mutex
	now    time.Duration
	timers []*teswlScheduledTimer
}

func (c *teswlFakeClock) AfterFunc(d time.Duration, fn func()) func() bool {
	c.mu.Lock()
	timer := &teswlScheduledTimer{
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

func (c *teswlFakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	target := c.now + d
	c.mu.Unlock()

	for {
		c.mu.Lock()
		var next *teswlScheduledTimer
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

func teswlCreateLifecycle(clock *teswlFakeClock) (*experimental.WorkerLifecycle, *atomic.Int32) {
	var retired atomic.Int32
	lc := experimental.NewWorkerLifecycle(experimental.WorkerLifecycleOptions{
		InitialServerConnectionId: teswlGeneration,
		InitialDemandGrace:        100 * time.Millisecond,
		OrphanDemandGrace:         200 * time.Millisecond,
		OnRetire:                  func() { retired.Add(1) },
		AfterFunc:                 clock.AfterFunc,
	})
	return lc, &retired
}

func TestSessionWorkerLifecycle(t *testing.T) {
	t.Run("retires only after client demand and Harness activity are both gone", func(t *testing.T) {
		clock := &teswlFakeClock{}
		lc, retired := teswlCreateLifecycle(clock)
		defer lc.Close()

		if err := lc.SetDemand(teswlGeneration, "attachment-1", true); err != nil {
			t.Fatalf("SetDemand(true) failed: %v", err)
		}
		lc.SetHarnessActive(true)
		if err := lc.SetDemand(teswlGeneration, "attachment-1", false); err != nil {
			t.Fatalf("SetDemand(false) failed: %v", err)
		}
		if retired.Load() != 0 {
			t.Fatalf("expected no retire while harness active, got %d", retired.Load())
		}

		lc.SetHarnessActive(false)
		if retired.Load() != 1 {
			t.Fatalf("expected retire once after harness idle, got %d", retired.Load())
		}
	})

	t.Run("retains the worker until every presentation attachment is released", func(t *testing.T) {
		clock := &teswlFakeClock{}
		lc, retired := teswlCreateLifecycle(clock)
		defer lc.Close()

		if err := lc.SetDemand(teswlGeneration, "attachment-1", true); err != nil {
			t.Fatal(err)
		}
		if err := lc.SetDemand(teswlGeneration, "attachment-2", true); err != nil {
			t.Fatal(err)
		}
		if err := lc.SetDemand(teswlGeneration, "attachment-1", false); err != nil {
			t.Fatal(err)
		}
		if retired.Load() != 0 {
			t.Fatalf("expected no retire while attachment-2 held, got %d", retired.Load())
		}
		if err := lc.SetDemand(teswlGeneration, "attachment-2", false); err != nil {
			t.Fatal(err)
		}
		if retired.Load() != 1 {
			t.Fatalf("expected single retire after all attachments released, got %d", retired.Load())
		}
	})

	t.Run("does not retire on repeated activity updates while the Harness stays active", func(t *testing.T) {
		clock := &teswlFakeClock{}
		lc, retired := teswlCreateLifecycle(clock)
		defer lc.Close()

		if err := lc.SetDemand(teswlGeneration, "attachment-1", true); err != nil {
			t.Fatal(err)
		}
		lc.SetHarnessActive(true)
		if err := lc.SetDemand(teswlGeneration, "attachment-1", false); err != nil {
			t.Fatal(err)
		}
		lc.SetHarnessActive(true)
		if retired.Load() != 0 {
			t.Fatalf("expected no retire while harness active, got %d", retired.Load())
		}
		lc.SetHarnessActive(false)
		if retired.Load() != 1 {
			t.Fatalf("expected single retire after harness idle, got %d", retired.Load())
		}
	})

	t.Run("an idle Harness update does not retire while demand remains", func(t *testing.T) {
		clock := &teswlFakeClock{}
		lc, retired := teswlCreateLifecycle(clock)
		defer lc.Close()

		if err := lc.SetDemand(teswlGeneration, "attachment-1", true); err != nil {
			t.Fatal(err)
		}
		lc.SetHarnessActive(false)
		if retired.Load() != 0 {
			t.Fatalf("expected no retire while demand held, got %d", retired.Load())
		}
	})

	t.Run("does not retire while a demand acknowledgement holds reconciliation", func(t *testing.T) {
		clock := &teswlFakeClock{}
		lc, retired := teswlCreateLifecycle(clock)
		defer lc.Close()

		if err := lc.SetDemand(teswlGeneration, "attachment-1", true); err != nil {
			t.Fatal(err)
		}
		release := lc.HoldRetirement()
		if err := lc.SetDemand(teswlGeneration, "attachment-1", false); err != nil {
			t.Fatal(err)
		}
		if retired.Load() != 0 {
			t.Fatalf("expected no retire while held, got %d", retired.Load())
		}
		release()
		if retired.Load() != 1 {
			t.Fatalf("expected single retire on release, got %d", retired.Load())
		}
	})

	t.Run("holds retirement only for requests from the active attachment", func(t *testing.T) {
		clock := &teswlFakeClock{}
		lc, retired := teswlCreateLifecycle(clock)
		defer lc.Close()

		if err := lc.SetDemand(teswlGeneration, "attachment-1", true); err != nil {
			t.Fatal(err)
		}
		release, err := lc.BeginRequest(teswlGeneration, "attachment-1")
		if err != nil {
			t.Fatal(err)
		}
		if err := lc.SetDemand(teswlGeneration, "attachment-1", false); err != nil {
			t.Fatal(err)
		}
		if retired.Load() != 0 {
			t.Fatalf("expected no retire while request held, got %d", retired.Load())
		}
		release()
		if retired.Load() != 1 {
			t.Fatalf("expected single retire on request release, got %d", retired.Load())
		}
		if _, err := lc.BeginRequest(teswlGeneration, "attachment-1"); err == nil || !strings.Contains(err.Error(), "retiring") {
			t.Fatalf("expected retiring error after retirement, got %v", err)
		}
	})

	t.Run("rejects requests from stale generations and attachments", func(t *testing.T) {
		clock := &teswlFakeClock{}
		lc, _ := teswlCreateLifecycle(clock)
		defer lc.Close()

		if err := lc.SetDemand(teswlGeneration, "attachment-1", true); err != nil {
			t.Fatal(err)
		}
		if _, err := lc.BeginRequest("stale", "attachment-1"); err == nil || !strings.Contains(err.Error(), "stale server generation") {
			t.Fatalf("expected stale server generation error, got %v", err)
		}
		if _, err := lc.BeginRequest(teswlGeneration, "wrong-attachment"); err == nil || !strings.Contains(err.Error(), "active attachment") {
			t.Fatalf("expected active attachment error, got %v", err)
		}
	})

	t.Run("retains disconnected-generation demand for the orphan grace", func(t *testing.T) {
		clock := &teswlFakeClock{}
		lc, retired := teswlCreateLifecycle(clock)
		defer lc.Close()

		if err := lc.SetDemand(teswlGeneration, "attachment-1", true); err != nil {
			t.Fatal(err)
		}
		lc.ServerDisconnected(teswlGeneration)

		clock.Advance(199 * time.Millisecond)
		if retired.Load() != 0 {
			t.Fatalf("expected no retire at 199ms, got %d", retired.Load())
		}
		clock.Advance(1 * time.Millisecond)
		if retired.Load() != 1 {
			t.Fatalf("expected retire once at 200ms orphan grace, got %d", retired.Load())
		}
	})

	t.Run("allows a replacement generation to retain the worker", func(t *testing.T) {
		clock := &teswlFakeClock{}
		lc, retired := teswlCreateLifecycle(clock)
		defer lc.Close()

		if err := lc.SetDemand(teswlGeneration, "attachment-1", true); err != nil {
			t.Fatal(err)
		}
		lc.ServerDisconnected(teswlGeneration)
		lc.ServerConnected("generation-2")
		if err := lc.SetDemand("generation-2", "attachment-2", true); err != nil {
			t.Fatal(err)
		}

		clock.Advance(200 * time.Millisecond)
		if retired.Load() != 0 {
			t.Fatalf("expected replacement attachment to retain worker, got %d", retired.Load())
		}
		if err := lc.SetDemand("generation-2", "attachment-2", false); err != nil {
			t.Fatal(err)
		}
		if retired.Load() != 1 {
			t.Fatalf("expected single retire once replacement released, got %d", retired.Load())
		}
	})

	t.Run("retires a launched worker that never receives initial demand", func(t *testing.T) {
		clock := &teswlFakeClock{}
		lc, retired := teswlCreateLifecycle(clock)
		defer lc.Close()

		clock.Advance(100 * time.Millisecond)
		if retired.Load() != 1 {
			t.Fatalf("expected single retire at initial grace 100ms, got %d", retired.Load())
		}
	})

	t.Run("rejects demand after retirement has won the race", func(t *testing.T) {
		clock := &teswlFakeClock{}
		lc, _ := teswlCreateLifecycle(clock)
		defer lc.Close()

		clock.Advance(100 * time.Millisecond)
		if err := lc.SetDemand(teswlGeneration, "attachment-1", true); err == nil || !strings.Contains(err.Error(), "retiring") {
			t.Fatalf("expected retiring error after retirement won, got %v", err)
		}
	})

	t.Run("rejects demand from a stale server generation", func(t *testing.T) {
		clock := &teswlFakeClock{}
		lc, _ := teswlCreateLifecycle(clock)
		defer lc.Close()

		if err := lc.SetDemand("stale", "attachment-1", true); err == nil || !strings.Contains(err.Error(), "stale server generation") {
			t.Fatalf("expected stale server generation error, got %v", err)
		}
	})
}

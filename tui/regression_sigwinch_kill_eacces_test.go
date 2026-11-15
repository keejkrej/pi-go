package tui

import (
	"syscall"
	"testing"
)

func TestRegressionSigwinchKillEacces(t *testing.T) {
	// Must ignore EACCES or permission errors without panicking.
	called := 0
	termRefreshTerminalDimensionsFor(12345, func(pid int) error {
		called++
		return syscall.EACCES
	})
	if called != 1 {
		t.Fatalf("expected kill callback once, got %d", called)
	}

	// Non-positive pids must be skipped without calling kill.
	called = 0
	for _, pid := range []int{0, -1, -999} {
		termRefreshTerminalDimensionsFor(pid, func(p int) error {
			called++
			return nil
		})
	}
	if called != 0 {
		t.Fatalf("expected non-positive pids to skip kill, called %d times", called)
	}

	// Real RefreshTerminalDimensions must not panic.
	RefreshTerminalDimensions()
}

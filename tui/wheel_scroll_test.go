// Ported from packages/tui/test/wheel-scroll.test.ts (pi v1.5.2).

package tui_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/keejkrej/pi-go/tui"
)

func wheelScroll(acc *tui.WheelScrollAccelerator, times []float64, dir int) []int {
	out := make([]int, len(times))
	for i, now := range times {
		out[i] = acc.Next(dir, now)
	}
	return out
}

func TestWheelScrollAccelerator(t *testing.T) {
	tTrue := true
	tFalse := false
	auto := tui.WheelScrollLines{Auto: true}

	t.Run("uses fixed line counts regardless of timing", func(t *testing.T) {
		lines := tui.WheelScrollLines{Lines: 3}
		acc := tui.NewWheelScrollAccelerator(&lines, &tTrue)
		if diff := cmp.Diff([]int{3, 3, 3, 3}, wheelScroll(acc, []float64{0, 10, 20, 1000}, 1)); diff != "" {
			t.Fatalf("mismatch (-want +got):\n%s", diff)
		}
		acc.SetLines(tui.WheelScrollLines{Lines: 0.5})
		if got := acc.Next(1, 2000); got != 1 {
			t.Fatalf("got %d, want 1", got)
		}
	})

	t.Run("keeps one line per event in auto mode when the terminal already accelerates", func(t *testing.T) {
		acc := tui.NewWheelScrollAccelerator(&auto, &tFalse)
		if diff := cmp.Diff([]int{1, 1, 1, 1}, wheelScroll(acc, []float64{0, 10, 20, 30}, 1)); diff != "" {
			t.Fatalf("mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("scales auto mode with wheel velocity", func(t *testing.T) {
		acc := tui.NewWheelScrollAccelerator(&auto, &tTrue)
		if diff := cmp.Diff([]int{1, 1, 1, 1}, wheelScroll(acc, []float64{0, 150, 300, 450}, 1)); diff != "" {
			t.Fatalf("150ms mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff([]int{1, 2, 2, 2}, wheelScroll(acc, []float64{1000, 1050, 1100, 1150}, 1)); diff != "" {
			t.Fatalf("50ms mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff([]int{1, 5, 5, 5}, wheelScroll(acc, []float64{2000, 2020, 2040, 2060}, 1)); diff != "" {
			t.Fatalf("20ms mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff([]int{1, 6, 6, 6}, wheelScroll(acc, []float64{3000, 3010, 3020, 3030}, 1)); diff != "" {
			t.Fatalf("10ms mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("does not accelerate bursts of events for a single notch", func(t *testing.T) {
		acc := tui.NewWheelScrollAccelerator(&auto, &tTrue)
		if diff := cmp.Diff([]int{1, 1, 1, 1}, wheelScroll(acc, []float64{0, 3, 6, 9}, 1)); diff != "" {
			t.Fatalf("mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("resets acceleration on direction changes and pauses", func(t *testing.T) {
		acc := tui.NewWheelScrollAccelerator(&auto, &tTrue)
		if diff := cmp.Diff([]int{1, 5, 5}, wheelScroll(acc, []float64{0, 20, 40}, 1)); diff != "" {
			t.Fatalf("mismatch (-want +got):\n%s", diff)
		}
		if got := acc.Next(-1, 60); got != 1 {
			t.Fatalf("direction flip got %d, want 1", got)
		}
		if diff := cmp.Diff([]int{1, 5}, wheelScroll(acc, []float64{500, 520}, -1)); diff != "" {
			t.Fatalf("after pause mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("carries fractional lines between events", func(t *testing.T) {
		acc := tui.NewWheelScrollAccelerator(&auto, &tTrue)
		if diff := cmp.Diff([]int{1, 2, 3, 2, 3}, wheelScroll(acc, []float64{0, 40, 80, 120, 160}, 1)); diff != "" {
			t.Fatalf("mismatch (-want +got):\n%s", diff)
		}
	})
}

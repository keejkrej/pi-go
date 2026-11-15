// Ported from packages/tui/src/wheel-scroll.ts (pi v1.0.0).

package tui

import (
	"fmt"
	"math"
	"os"
	"runtime"
	"strings"

	"github.com/keejkrej/pi-go/internal/jsonx"
)

// Several events closer than this belong to one physical notch (Ghostty emits them ~4 ms apart)
// or come from a high-resolution source. They move one line each and do not accelerate.
const (
	wsBurstGapMs     = 5
	wsGestureGapMs   = 200
	wsReferenceGapMs = 100
	wsMaxAutoLines   = 6
)

// WheelScrollLines is the line count for one mouse-wheel event, or "auto".
// Auto selects the "auto" variant. When Auto is false, Lines is the count and may be fractional
// (callers floor it). The zero value is not "auto".
// JSON form, used by settings.json, is a number or the string "auto".
type WheelScrollLines struct {
	Auto  bool
	Lines float64
}

// MarshalJSON emits a JSON number or the string "auto".
func (w WheelScrollLines) MarshalJSON() ([]byte, error) {
	if w.Auto {
		return []byte(`"auto"`), nil
	}
	return jsonx.Marshal(w.Lines)
}

// UnmarshalJSON accepts a JSON number or the string "auto".
func (w *WheelScrollLines) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == `"auto"` {
		*w = WheelScrollLines{Auto: true}
		return nil
	}
	var s string
	if err := jsonx.Unmarshal(data, &s); err == nil {
		if s == "auto" {
			*w = WheelScrollLines{Auto: true}
			return nil
		}
		return fmt.Errorf("invalid WheelScrollLines string %q", s)
	}
	var lines float64
	if err := jsonx.Unmarshal(data, &lines); err != nil {
		return fmt.Errorf("invalid WheelScrollLines: %w", err)
	}
	*w = WheelScrollLines{Lines: lines}
	return nil
}

// wsTerminalAcceleratesWheel reports whether the local terminal already accelerates wheel deltas.
// Local macOS terminals outside SSH emit one event per line. Other platforms, and SSH sessions
// where the client platform is unknown, usually send one event per wheel notch.
func wsTerminalAcceleratesWheel() bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	if _, ok := os.LookupEnv("SSH_CONNECTION"); ok {
		return false
	}
	if _, ok := os.LookupEnv("SSH_CLIENT"); ok {
		return false
	}
	if _, ok := os.LookupEnv("SSH_TTY"); ok {
		return false
	}
	return true
}

// WheelScrollAccelerator converts wheel events into line counts.
// In "auto" mode on terminals that do not accelerate wheel input, the count follows event
// velocity: an isolated notch moves one line, while a fast spin moves up to six lines per event.
type WheelScrollAccelerator struct {
	lines         WheelScrollLines
	accelerate    bool
	lastTime      float64 // TS initial value is Number.NEGATIVE_INFINITY
	lastDirection int
	averageGap    *float64
	carry         float64
}

// NewWheelScrollAccelerator converts wheel events into line counts.
// lines nil means "auto". accelerate nil means !wsTerminalAcceleratesWheel().
func NewWheelScrollAccelerator(lines *WheelScrollLines, accelerate *bool) *WheelScrollAccelerator {
	resolvedLines := WheelScrollLines{Auto: true}
	if lines != nil {
		resolvedLines = *lines
	}
	resolvedAccelerate := !wsTerminalAcceleratesWheel()
	if accelerate != nil {
		resolvedAccelerate = *accelerate
	}
	return &WheelScrollAccelerator{
		lines:      resolvedLines,
		accelerate: resolvedAccelerate,
		lastTime:   math.Inf(-1),
	}
}

// SetLines sets the line count and resets gesture state.
func (w *WheelScrollAccelerator) SetLines(lines WheelScrollLines) {
	w.lines = lines
	w.reset()
}

// Next returns the positive line count for a wheel event in direction at now.
// direction is -1 or 1. now is milliseconds from a monotonic clock (TS performance.now()).
func (w *WheelScrollAccelerator) Next(direction int, now float64) int {
	if !w.lines.Auto {
		if !math.IsNaN(w.lines.Lines) && !math.IsInf(w.lines.Lines, 0) {
			v := int(math.Floor(w.lines.Lines))
			if v < 1 {
				return 1
			}
			return v
		}
		return 1
	}
	if !w.accelerate {
		return 1
	}

	gap := now - w.lastTime
	sameGesture := direction == w.lastDirection && gap <= wsGestureGapMs
	w.lastTime = now
	w.lastDirection = direction
	if !sameGesture {
		w.averageGap = nil
		w.carry = 0
		return 1
	}
	if gap < wsBurstGapMs {
		return 1
	}

	var avg float64
	if w.averageGap == nil {
		avg = gap
	} else {
		avg = (*w.averageGap + gap) / 2
	}
	w.averageGap = &avg

	lines := math.Min(wsMaxAutoLines, math.Max(1, wsReferenceGapMs/avg)) + w.carry
	whole := math.Floor(lines)
	w.carry = lines - whole
	return int(whole)
}

func (w *WheelScrollAccelerator) reset() {
	w.lastTime = math.Inf(-1)
	w.lastDirection = 0
	w.averageGap = nil
	w.carry = 0
}

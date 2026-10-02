// Ported from packages/tui/src/wheel-scroll.ts (pi v1.0.0).

package tui

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
	panic("unported: WheelScrollLines.MarshalJSON")
}

// UnmarshalJSON accepts a JSON number or the string "auto".
func (w *WheelScrollLines) UnmarshalJSON(data []byte) error {
	panic("unported: WheelScrollLines.UnmarshalJSON")
}

// wsTerminalAcceleratesWheel reports whether the local terminal already accelerates wheel deltas.
// Local macOS terminals outside SSH emit one event per line. Other platforms, and SSH sessions
// where the client platform is unknown, usually send one event per wheel notch.
func wsTerminalAcceleratesWheel() bool {
	panic("unported: wsTerminalAcceleratesWheel")
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
	panic("unported: NewWheelScrollAccelerator")
}

// SetLines sets the line count and resets gesture state.
func (w *WheelScrollAccelerator) SetLines(lines WheelScrollLines) {
	panic("unported: WheelScrollAccelerator.SetLines")
}

// Next returns the positive line count for a wheel event in direction at now.
// direction is -1 or 1. now is milliseconds from a monotonic clock (TS performance.now()).
func (w *WheelScrollAccelerator) Next(direction int, now float64) int {
	panic("unported: WheelScrollAccelerator.Next")
}

func (w *WheelScrollAccelerator) reset() {
	panic("unported: WheelScrollAccelerator.reset")
}

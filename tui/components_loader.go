// Ported from packages/tui/src/components/loader.ts (pi v1.0.0).

package tui

import (
	"sync"
	"time"
)

// clDefaultFrames is the spinner cycle. Copy it before storing; do not mutate the package slice.
var clDefaultFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// clDefaultIntervalMs is the default frame interval.
// A missing, zero, or negative LoaderIndicatorOptions.IntervalMs uses this value.
const clDefaultIntervalMs int64 = 80

// LoaderIndicatorOptions configures the Loader spinner.
// Field order is frames, intervalMs.
type LoaderIndicatorOptions struct {
	// Frames is the animation cycle. Nil means clDefaultFrames.
	// An empty non-nil slice hides the indicator.
	Frames []string `json:"frames,omitzero"`
	// IntervalMs is the frame interval in milliseconds.
	// Nil, zero, and negative values use clDefaultIntervalMs.
	IntervalMs *int64 `json:"intervalMs,omitzero"`
}

// Loader is a Text that shows a message and an optional spinner.
// It implements Component. mu guards the frame index and the embedded Text
// because the animation ticker fires off the caller goroutine.
// NewLoader must initialize the embedded Text as text "", paddingX 1, paddingY 0
// (not Text's paddingY default of 1), message "Loading..." when the caller wants that default,
// and a copy of clDefaultFrames. Zero message is not "Loading...".
// interval nil means the animation is stopped.
type Loader struct {
	mu sync.Mutex
	Text

	frames                  []string
	intervalMs              int64
	currentFrame            int
	interval                *time.Ticker
	ui                      TUI
	renderIndicatorVerbatim bool
	spinnerColorFn          func(str string) string
	messageColorFn          func(str string) string
	message                 string
}

// NewLoader returns a loader. Nil indicator uses the default spinner.
// message is the text after the indicator. Pass "Loading..." for the TypeScript default.
func NewLoader(ui TUI, spinnerColorFn func(str string) string, messageColorFn func(str string) string, message string, indicator *LoaderIndicatorOptions) *Loader {
	panic("unported: NewLoader")
}

// Render returns a leading blank line plus the embedded Text render.
func (l *Loader) Render(width int) []string {
	panic("unported: Loader.Render")
}

// Start paints the current frame and starts the ticker when there is more than one frame.
func (l *Loader) Start() {
	panic("unported: Loader.Start")
}

// Stop stops the ticker. It does not change the displayed text.
func (l *Loader) Stop() {
	panic("unported: Loader.Stop")
}

// SetMessage replaces the message and repaints.
func (l *Loader) SetMessage(message string) {
	panic("unported: Loader.SetMessage")
}

// Invalidate drops the Text cache and repaints the current indicator.
func (l *Loader) Invalidate() {
	panic("unported: Loader.Invalidate")
}

// SetIndicator replaces the frames and restarts the animation.
// Nil indicator restores clDefaultFrames and colors the frame with spinnerColorFn.
// A non-nil indicator, including one with nil Frames, renders the frame verbatim.
func (l *Loader) SetIndicator(indicator *LoaderIndicatorOptions) {
	panic("unported: Loader.SetIndicator")
}

func (l *Loader) restartAnimation() {
	panic("unported: Loader.restartAnimation")
}

// getRenderedIndicator is the TypeScript protected method.
func (l *Loader) getRenderedIndicator() string {
	panic("unported: Loader.getRenderedIndicator")
}

func (l *Loader) updateDisplay() {
	panic("unported: Loader.updateDisplay")
}

var _ Component = (*Loader)(nil)

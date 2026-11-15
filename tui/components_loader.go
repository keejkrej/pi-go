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
	stopCh                  chan struct{}
	ui                      TUI
	renderIndicatorVerbatim bool
	spinnerColorFn          func(str string) string
	messageColorFn          func(str string) string
	message                 string
}

func clInitLoader(l *Loader, ui TUI, spinnerColorFn func(str string) string, messageColorFn func(str string) string, message string, indicator *LoaderIndicatorOptions) {
	if spinnerColorFn == nil {
		spinnerColorFn = func(str string) string { return str }
	}
	if messageColorFn == nil {
		messageColorFn = func(str string) string { return str }
	}
	l.Text = Text{
		text:     "",
		paddingX: 1,
		paddingY: 0,
	}
	l.frames = append([]string(nil), clDefaultFrames...)
	l.intervalMs = clDefaultIntervalMs
	l.ui = ui
	l.spinnerColorFn = spinnerColorFn
	l.messageColorFn = messageColorFn
	l.message = message
	l.SetIndicator(indicator)
}

// NewLoader returns a loader. Nil indicator uses the default spinner.
// message is the text after the indicator. Pass "Loading..." for the TypeScript default.
func NewLoader(ui TUI, spinnerColorFn func(str string) string, messageColorFn func(str string) string, message string, indicator *LoaderIndicatorOptions) *Loader {
	l := &Loader{}
	clInitLoader(l, ui, spinnerColorFn, messageColorFn, message, indicator)
	return l
}

// Render returns a leading blank line plus the embedded Text render.
func (l *Loader) Render(width int) []string {
	l.mu.Lock()
	textLines := l.Text.Render(width)
	l.mu.Unlock()
	result := make([]string, 0, len(textLines)+1)
	result = append(result, "")
	result = append(result, textLines...)
	return result
}

// Start paints the current frame and starts the ticker when there is more than one frame.
func (l *Loader) Start() {
	l.mu.Lock()
	ui := l.updateDisplayLocked()
	l.restartAnimationLocked()
	l.mu.Unlock()
	if ui != nil {
		ui.RequestRender(nil)
	}
}

// Stop stops the ticker. It does not change the displayed text.
func (l *Loader) Stop() {
	l.mu.Lock()
	l.stopLocked()
	l.mu.Unlock()
}

func (l *Loader) stopLocked() {
	if l.interval != nil {
		l.interval.Stop()
		l.interval = nil
	}
	if l.stopCh != nil {
		close(l.stopCh)
		l.stopCh = nil
	}
}

// SetMessage replaces the message and repaints.
func (l *Loader) SetMessage(message string) {
	l.mu.Lock()
	l.message = message
	ui := l.updateDisplayLocked()
	l.mu.Unlock()
	if ui != nil {
		ui.RequestRender(nil)
	}
}

// Invalidate drops the Text cache and repaints the current indicator.
func (l *Loader) Invalidate() {
	l.mu.Lock()
	l.Text.Invalidate()
	ui := l.updateDisplayLocked()
	l.mu.Unlock()
	if ui != nil {
		ui.RequestRender(nil)
	}
}

// SetIndicator replaces the frames and restarts the animation.
// Nil indicator restores clDefaultFrames and colors the frame with spinnerColorFn.
// A non-nil indicator, including one with nil Frames, renders the frame verbatim.
func (l *Loader) SetIndicator(indicator *LoaderIndicatorOptions) {
	l.mu.Lock()
	l.renderIndicatorVerbatim = indicator != nil
	if indicator != nil && indicator.Frames != nil {
		l.frames = append([]string(nil), indicator.Frames...)
	} else {
		l.frames = append([]string(nil), clDefaultFrames...)
	}
	if indicator != nil && indicator.IntervalMs != nil && *indicator.IntervalMs > 0 {
		l.intervalMs = *indicator.IntervalMs
	} else {
		l.intervalMs = clDefaultIntervalMs
	}
	l.currentFrame = 0
	ui := l.updateDisplayLocked()
	l.restartAnimationLocked()
	l.mu.Unlock()
	if ui != nil {
		ui.RequestRender(nil)
	}
}

func (l *Loader) restartAnimation() {
	l.mu.Lock()
	l.restartAnimationLocked()
	l.mu.Unlock()
}

func (l *Loader) restartAnimationLocked() {
	l.stopLocked()
	if len(l.frames) <= 1 {
		return
	}
	ticker := time.NewTicker(time.Duration(l.intervalMs) * time.Millisecond)
	stopCh := make(chan struct{})
	l.interval = ticker
	l.stopCh = stopCh
	go func() {
		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				l.mu.Lock()
				if l.interval != ticker {
					l.mu.Unlock()
					return
				}
				if len(l.frames) > 0 {
					l.currentFrame = (l.currentFrame + 1) % len(l.frames)
				}
				ui := l.updateDisplayLocked()
				l.mu.Unlock()
				if ui != nil {
					ui.RequestRender(nil)
				}
			}
		}
	}()
}

// getRenderedIndicator is the TypeScript protected method.
func (l *Loader) getRenderedIndicator() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.getRenderedIndicatorLocked()
}

func (l *Loader) getRenderedIndicatorLocked() string {
	frame := ""
	if l.currentFrame >= 0 && l.currentFrame < len(l.frames) {
		frame = l.frames[l.currentFrame]
	}
	if l.renderIndicatorVerbatim {
		return frame
	}
	return l.spinnerColorFn(frame)
}

func (l *Loader) updateDisplay() {
	l.mu.Lock()
	ui := l.updateDisplayLocked()
	l.mu.Unlock()
	if ui != nil {
		ui.RequestRender(nil)
	}
}

func (l *Loader) updateDisplayLocked() TUI {
	renderedFrame := l.getRenderedIndicatorLocked()
	indicator := ""
	if len(renderedFrame) > 0 {
		indicator = renderedFrame + " "
	}
	l.Text.SetText(indicator + l.messageColorFn(l.message))
	return l.ui
}

var _ Component = (*Loader)(nil)

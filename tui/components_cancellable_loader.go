// Ported from packages/tui/src/components/cancellable-loader.ts (pi v1.0.0).

package tui

import "context"

// CancellableLoader is a Loader that aborts when the cancel key is pressed.
// It implements Component through Loader. The TypeScript AbortSignal is ctx.
// NewCancellableLoader must install a non-nil ctx and cancel. A nil ctx is not a live signal.
// The loader starts un-aborted. Message and indicator defaults match NewLoader.
type CancellableLoader struct {
	Loader
	ctx    context.Context
	cancel context.CancelFunc
	// OnAbort is called after the signal is aborted when the user presses the cancel key.
	// Nil skips the callback.
	OnAbort func()
}

// NewCancellableLoader returns a loader that can be cancelled.
// Pass "Loading..." for the TypeScript default message. Nil indicator uses the default spinner.
func NewCancellableLoader(ui TUI, spinnerColorFn func(str string) string, messageColorFn func(str string) string, message string, indicator *LoaderIndicatorOptions) *CancellableLoader {
	ctx, cancel := context.WithCancel(context.Background())
	c := &CancellableLoader{
		ctx:    ctx,
		cancel: cancel,
	}
	clInitLoader(&c.Loader, ui, spinnerColorFn, messageColorFn, message, indicator)
	return c
}

// Signal returns the context aborted by the cancel key.
func (c *CancellableLoader) Signal() context.Context { return c.ctx }

// Aborted reports whether the cancel key has aborted the loader.
func (c *CancellableLoader) Aborted() bool {
	return c.ctx != nil && c.ctx.Err() != nil
}

// HandleInput aborts when data matches the tui.select.cancel binding.
func (c *CancellableLoader) HandleInput(data string) {
	kb := GetKeybindings()
	if kb.Matches(data, KeybindingTuiSelectCancel) {
		if c.cancel != nil {
			c.cancel()
		}
		if c.OnAbort != nil {
			c.OnAbort()
		}
	}
}

// Dispose stops the spinner.
func (c *CancellableLoader) Dispose() {
	c.Stop()
}

var _ Component = (*CancellableLoader)(nil)

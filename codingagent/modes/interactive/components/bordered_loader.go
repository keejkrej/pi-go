// Ported from packages/coding-agent/src/modes/interactive/components/bordered-loader.ts (pi v1.5.2).

package components

import (
	"context"
	"sync"

	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/tui"
)

// BorderedLoaderOptions configures a BorderedLoader.
type BorderedLoaderOptions struct {
	Cancellable *bool
}

// BorderedLoader wraps a Loader or CancellableLoader in dynamic borders for extension UI.
type BorderedLoader struct {
	*tui.Container

	mu                sync.Mutex
	cancellableLoader *tui.CancellableLoader
	loader            *tui.Loader
	cancellable       bool
	nonCancelCtx      context.Context
	nonCancelStop     context.CancelFunc

	// OnAbort is invoked when a cancellable loader is cancelled via input.
	OnAbort func()
}

// NewBorderedLoader creates a bordered loader component using th (or CurrentTheme if nil).
func NewBorderedLoader(ui tui.TUI, th *theme.Theme, message string, options ...*BorderedLoaderOptions) *BorderedLoader {
	if th == nil {
		th = theme.CurrentTheme()
	}
	cancellable := true
	if len(options) > 0 && options[0] != nil && options[0].Cancellable != nil {
		cancellable = *options[0].Cancellable
	}

	b := &BorderedLoader{
		Container:   tui.NewContainer(),
		cancellable: cancellable,
	}

	borderColor := func(s string) string { return th.Fg(theme.FgBorder, s) }
	spinnerColor := func(s string) string { return th.Fg(theme.FgAccent, s) }
	msgColor := func(s string) string { return th.Fg(theme.FgMuted, s) }

	b.AddChild(NewDynamicBorder(borderColor))
	if cancellable {
		cl := tui.NewCancellableLoader(ui, spinnerColor, msgColor, message, nil)
		cl.OnAbort = func() {
			b.mu.Lock()
			fn := b.OnAbort
			b.mu.Unlock()
			if fn != nil {
				fn()
			}
		}
		b.cancellableLoader = cl
		b.AddChild(cl)
		b.AddChild(tui.NewSpacer(1))
		b.AddChild(tui.NewText(theme.AppKeyHint(th, tui.KeybindingTuiSelectCancel, "cancel"), 1, 0, nil))
	} else {
		b.nonCancelCtx, b.nonCancelStop = context.WithCancel(context.Background())
		l := tui.NewLoader(ui, spinnerColor, msgColor, message, nil)
		b.loader = l
		b.AddChild(l)
	}
	b.AddChild(tui.NewSpacer(1))
	b.AddChild(NewDynamicBorder(borderColor))
	return b
}

// Signal returns the loader cancellation context.
func (b *BorderedLoader) Signal() context.Context {
	if b.cancellable && b.cancellableLoader != nil {
		return b.cancellableLoader.Signal()
	}
	if b.nonCancelCtx != nil {
		return b.nonCancelCtx
	}
	return context.Background()
}

// HandleInput forwards key input when the loader is cancellable.
func (b *BorderedLoader) HandleInput(data string) {
	if b.cancellable && b.cancellableLoader != nil {
		b.cancellableLoader.HandleInput(data)
	}
}

// Dispose stops the underlying loader and releases its timer.
func (b *BorderedLoader) Dispose() {
	if b.cancellableLoader != nil {
		b.cancellableLoader.Dispose()
	} else {
		if b.loader != nil {
			b.loader.Stop()
		}
		if b.nonCancelStop != nil {
			b.nonCancelStop()
		}
	}
}

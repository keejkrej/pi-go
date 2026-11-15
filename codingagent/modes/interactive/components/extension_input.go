// Ported from packages/coding-agent/src/modes/interactive/components/extension-input.ts (pi v1.5.2).

package components

import (
	"fmt"

	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/tui"
)

// ExtensionInputOptions configures optional timeout, initial value, and description for ExtensionInputComponent.
type ExtensionInputOptions struct {
	Tui          tui.TUI
	Timeout      *int64
	InitialValue *string
	Description  *string
}

// ExtensionInputComponent renders a single-line text input dialog for extension UI prompts.
type ExtensionInputComponent struct {
	*tui.Container
	input            *tui.Input
	onSubmitCallback func(value string)
	onCancelCallback func()
	titleText        *tui.Text
	baseTitle        string
	countdown        *CountdownTimer
	focused          bool
}

var _ tui.Focusable = (*ExtensionInputComponent)(nil)

// NewExtensionInputComponent constructs a text input dialog.
func NewExtensionInputComponent(
	title string,
	placeholder *string,
	onSubmit func(value string),
	onCancel func(),
	opts ...*ExtensionInputOptions,
) *ExtensionInputComponent {
	var cfg *ExtensionInputOptions
	if len(opts) > 0 {
		cfg = opts[0]
	}

	th := theme.CurrentTheme()
	c := &ExtensionInputComponent{
		Container:        tui.NewContainer(),
		onSubmitCallback: onSubmit,
		onCancelCallback: onCancel,
		baseTitle:        title,
	}

	c.AddChild(NewDynamicBorder())
	c.AddChild(tui.NewSpacer(1))

	c.titleText = tui.NewText(th.Fg(theme.FgAccent, title), 1, 0, nil)
	c.AddChild(c.titleText)
	if cfg != nil && cfg.Description != nil && *cfg.Description != "" {
		c.AddChild(tui.NewSpacer(1))
		c.AddChild(tui.NewText(th.Fg(theme.FgText, *cfg.Description), 1, 0, nil))
	}
	c.AddChild(tui.NewSpacer(1))

	if cfg != nil && cfg.Timeout != nil && *cfg.Timeout > 0 && cfg.Tui != nil {
		c.countdown = NewCountdownTimer(
			*cfg.Timeout,
			cfg.Tui,
			func(s int) {
				cur := theme.CurrentTheme()
				c.titleText.SetText(cur.Fg(theme.FgAccent, fmt.Sprintf("%s (%ds)", c.baseTitle, s)))
			},
			func() {
				if c.onCancelCallback != nil {
					c.onCancelCallback()
				}
			},
		)
	}

	var inputOpts *tui.InputOptions
	if placeholder != nil && *placeholder != "" {
		// Preserves placeholder acceptance signature; tui.Input handles inline text entry.
		_ = *placeholder
	}
	c.input = tui.NewInput(inputOpts)
	if cfg != nil && cfg.InitialValue != nil && *cfg.InitialValue != "" {
		c.input.SetValue(*cfg.InitialValue)
	}
	c.AddChild(c.input)
	c.AddChild(tui.NewSpacer(1))
	c.AddChild(tui.NewText(
		theme.AppKeyHint(th, tui.KeybindingTuiSelectConfirm, "submit")+
			"  "+
			theme.AppKeyHint(th, tui.KeybindingTuiSelectCancel, "cancel"),
		1,
		0,
		nil,
	))
	c.AddChild(tui.NewSpacer(1))
	c.AddChild(NewDynamicBorder())
	return c
}

// Focused reports whether the input currently holds focus.
func (e *ExtensionInputComponent) Focused() bool {
	return e.focused
}

// SetFocused propagates focus state to the embedded Input for cursor rendering.
func (e *ExtensionInputComponent) SetFocused(focused bool) {
	e.focused = focused
	e.input.SetFocused(focused)
}

// HandleInput processes submit, cancel, and input editing keys.
func (e *ExtensionInputComponent) HandleInput(keyData string) {
	kb := tui.GetKeybindings()
	switch {
	case kb.Matches(keyData, tui.KeybindingTuiSelectConfirm) || keyData == "\n":
		if e.onSubmitCallback != nil {
			e.onSubmitCallback(e.input.GetValue())
		}
	case kb.Matches(keyData, tui.KeybindingTuiSelectCancel):
		if e.onCancelCallback != nil {
			e.onCancelCallback()
		}
	default:
		e.input.HandleInput(keyData)
	}
}

// Dispose stops any active countdown timer.
func (e *ExtensionInputComponent) Dispose() {
	if e.countdown != nil {
		e.countdown.Dispose()
		e.countdown = nil
	}
}

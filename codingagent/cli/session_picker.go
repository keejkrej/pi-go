// Ported from packages/coding-agent/src/cli/session-picker.ts (pi v1.5.2).

package cli

import (
	"context"
	"errors"
	"sync"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/components"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/tui"
)

// ErrSessionPickerExit is returned when the user explicitly requests exiting from the startup session picker.
var ErrSessionPickerExit = errors.New("session picker exited")

// SessionPickerOptions configures SelectSession behavior and UI injection for tests.
type SessionPickerOptions struct {
	CurrentSessionFilePath *string
	AgentDir               string
	RenameSession          func(ctx context.Context, sessionPath string, nextName *string) error
	UI                     tui.TUI
	Terminal               tui.Terminal
	Keybindings            *core.KeybindingsManager
	DisableThemeInit       bool
}

// SelectSession displays the interactive terminal session selector and returns
// the selected session path, nil if cancelled, or ErrSessionPickerExit on exit key.
func SelectSession(
	ctx context.Context,
	currentSessionsLoader components.SessionsLoader,
	allSessionsLoader components.SessionsLoader,
	opts ...*SessionPickerOptions,
) (*string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var cfg SessionPickerOptions
	if len(opts) > 0 && opts[0] != nil {
		cfg = *opts[0]
	}

	if !cfg.DisableThemeInit {
		theme.SetThemeJsonValidator(theme.ValidateThemeJson)
		if _, err := theme.InitTheme(theme.SystemThemeName, false); err != nil {
			_, _ = theme.InitTheme("dark", false)
		}
	}

	kb := cfg.Keybindings
	if kb == nil {
		kb = core.KeybindingsManagerCreate(cfg.AgentDir)
	}
	if kb != nil && kb.KeybindingsManager != nil {
		tui.SetKeybindings(kb.KeybindingsManager)
	}

	ui := cfg.UI
	ownsUI := false
	if ui == nil {
		terminal := cfg.Terminal
		if terminal == nil {
			terminal = tui.NewProcessTerminal()
		}
		ui = tui.NewTuiMainScreen(terminal, nil, nil)
		ownsUI = true
	}

	var curPathArg []string
	if cfg.CurrentSessionFilePath != nil && *cfg.CurrentSessionFilePath != "" {
		curPathArg = []string{*cfg.CurrentSessionFilePath}
	}

	type pickerOutcome struct {
		selected *string
		exited   bool
	}
	doneCh := make(chan pickerOutcome, 1)
	var once sync.Once
	finish := func(out pickerOutcome) {
		once.Do(func() {
			doneCh <- out
		})
	}

	selector := components.NewSessionSelectorComponent(
		currentSessionsLoader,
		allSessionsLoader,
		func(sessionPath string) {
			p := sessionPath
			finish(pickerOutcome{selected: &p})
		},
		func() {
			finish(pickerOutcome{})
		},
		func() {
			finish(pickerOutcome{exited: true})
		},
		func() {
			ui.RequestRender(nil)
		},
		&components.SessionSelectorOptions{
			Keybindings:   kb,
			RenameSession: cfg.RenameSession,
		},
		curPathArg...,
	)

	if ownsUI {
		ui.Start()
	}
	ui.AddChild(selector)
	ui.SetFocus(selector)
	forceRender := true
	ui.RenderNow(&forceRender)

	var out pickerOutcome
	var waitErr error
	select {
	case out = <-doneCh:
	case <-ctx.Done():
		waitErr = ctx.Err()
	}

	selector.Dispose()
	ui.RemoveChild(selector)
	suShutdownUI(ui, ownsUI)

	if waitErr != nil {
		return nil, waitErr
	}
	if out.exited {
		return nil, ErrSessionPickerExit
	}
	return out.selected, nil
}

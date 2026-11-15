// Ported from packages/coding-agent/src/cli/config-selector.ts (pi v1.5.2).

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

// ErrConfigSelectorExit is returned when the user exits from the `pi config` selector.
var ErrConfigSelectorExit = errors.New("config selector exited")

// ConfigSelectorOptions configures SelectConfig and allows test UI injection.
type ConfigSelectorOptions struct {
	UI               tui.TUI
	Terminal         tui.Terminal
	Keybindings      *core.KeybindingsManager
	DisableThemeInit bool
}

// SelectConfig opens the interactive resource configuration selector for `pi config`.
func SelectConfig(
	ctx context.Context,
	resolvedPaths components.ScopedResolvedPaths,
	settingsManager *core.SettingsManager,
	cwd string,
	agentDir string,
	writeScope components.ConfigWriteScope,
	projectModeAvailable bool,
	options ...*ConfigSelectorOptions,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	var cfg ConfigSelectorOptions
	if len(options) > 0 && options[0] != nil {
		cfg = *options[0]
	}

	if !cfg.DisableThemeInit {
		theme.SetThemeJsonValidator(theme.ValidateThemeJson)
		if _, err := theme.InitTheme(theme.SystemThemeName, false); err != nil {
			_, _ = theme.InitTheme("dark", false)
		}
	}

	kb := cfg.Keybindings
	if kb == nil {
		kb = core.KeybindingsManagerCreate(agentDir)
	}
	if kb != nil && kb.KeybindingsManager != nil {
		tui.SetKeybindings(kb.KeybindingsManager)
	}

	ui := cfg.UI
	ownsUI := false
	terminal := cfg.Terminal
	if ui == nil {
		if terminal == nil {
			terminal = tui.NewProcessTerminal()
		}
		ui = tui.NewTuiMainScreen(terminal, nil, nil)
		ownsUI = true
	} else if terminal == nil {
		terminal = ui.Terminal()
	}

	terminalHeight := 24
	if terminal != nil && terminal.Rows() > 0 {
		terminalHeight = terminal.Rows()
	}

	type configOutcome struct {
		exited bool
	}
	doneCh := make(chan configOutcome, 1)
	var once sync.Once
	finish := func(out configOutcome) {
		once.Do(func() {
			doneCh <- out
		})
	}

	selector := components.NewConfigSelectorComponent(
		resolvedPaths,
		settingsManager,
		cwd,
		agentDir,
		func() {
			finish(configOutcome{})
		},
		func() {
			finish(configOutcome{exited: true})
		},
		func() {
			ui.RequestRender(nil)
		},
		terminalHeight,
		writeScope,
		projectModeAvailable,
	)

	if ownsUI {
		ui.Start()
	}
	ui.AddChild(selector)
	ui.SetFocus(selector)
	forceRender := true
	ui.RenderNow(&forceRender)

	var out configOutcome
	var waitErr error
	select {
	case out = <-doneCh:
	case <-ctx.Done():
		waitErr = ctx.Err()
	}

	ui.RemoveChild(selector)
	suShutdownUI(ui, ownsUI)

	if waitErr != nil {
		return waitErr
	}
	if out.exited {
		return ErrConfigSelectorExit
	}
	return nil
}

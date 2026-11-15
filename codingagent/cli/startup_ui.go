// Ported from packages/coding-agent/src/cli/startup-ui.ts (pi v1.5.2).

package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/components"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/tui"
	"golang.org/x/term"
)

// StartupUiOptions configures terminal/UI overrides for startup interactive prompts.
type StartupUiOptions struct {
	AgentDir                 string
	Terminal                 tui.Terminal
	UI                       tui.TUI
	Keybindings              *core.KeybindingsManager
	DisableLiveTerminalQuery bool
	DisableThemeController   bool
}

// FirstTimeSetupSummary describes whether first-time setup ran and its outcome.
type FirstTimeSetupSummary struct {
	Completed bool
	Result    *components.FirstTimeSetupResult
}

// IsInteractiveProcessTerminal reports whether both stdin and stdout are attached to a TTY.
func IsInteractiveProcessTerminal() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

// ShouldRunFirstTimeSetup reports whether the initial theme/analytics onboarding
// prompt should be presented before launching interactive or GUI mode.
func ShouldRunFirstTimeSetup(
	settingsManager *core.SettingsManager,
	agentDir string,
	cliTheme *string,
	interactive bool,
) bool {
	if !interactive || cliTheme != nil || settingsManager == nil {
		return false
	}
	if os.Getenv("CI") != "" {
		return false
	}
	if agentDir == "" {
		agentDir = utils.GetAgentDir()
	}
	settingsPath := filepath.Join(agentDir, "settings.json")
	if _, err := os.Stat(settingsPath); err == nil {
		global := settingsManager.GetGlobalSettings()
		if global.Theme != nil || global.EnableAnalytics != nil || global.LastChangelogVersion != nil {
			return false
		}
	}
	return true
}

// InitStartupThemes registers the schema validator, loads optional custom themes,
// and resolves initial terminal theme settings.
func InitStartupThemes(
	settingsManager *core.SettingsManager,
	customThemes []*theme.Theme,
	explicitTheme *string,
) (*core.InteractiveThemeController, error) {
	theme.SetThemeJsonValidator(theme.ValidateThemeJson)
	if explicitTheme != nil && *explicitTheme != "" && settingsManager != nil {
		settingsManager.ApplyOverrides(&core.Settings{Theme: explicitTheme})
	}
	ctrl := core.NewInteractiveThemeController(core.InteractiveThemeControllerOptions{
		SettingsManager: settingsManager,
		CustomThemes:    customThemes,
	})
	_, err := ctrl.ApplyFromSettings()
	return ctrl, err
}

func suResolveOptions(opts []*StartupUiOptions) StartupUiOptions {
	if len(opts) > 0 && opts[0] != nil {
		return *opts[0]
	}
	return StartupUiOptions{}
}

func suNewUI(settingsManager *core.SettingsManager, opt StartupUiOptions) (ui tui.TUI, ownsUI bool) {
	kb := opt.Keybindings
	if kb == nil {
		kb = core.KeybindingsManagerCreate(opt.AgentDir)
	}
	if kb != nil && kb.KeybindingsManager != nil {
		tui.SetKeybindings(kb.KeybindingsManager)
	}

	if opt.UI != nil {
		return opt.UI, false
	}
	terminal := opt.Terminal
	if terminal == nil {
		terminal = tui.NewProcessTerminal()
	}
	var showHwPtr *bool
	if settingsManager != nil {
		showHw := settingsManager.GetShowHardwareCursor()
		showHwPtr = &showHw
	}
	created := tui.NewTuiMainScreen(terminal, showHwPtr, nil)
	if settingsManager != nil {
		created.SetClearOnShrink(settingsManager.GetClearOnShrink())
	}
	return created, true
}

func suShutdownUI(ui tui.TUI, ownsUI bool) {
	if ui == nil {
		return
	}
	ui.Clear()
	if ownsUI {
		if term := ui.Terminal(); term != nil {
			_ = term.DrainInput(nil, nil)
		}
		preserve := false
		ui.Stop(&tui.TuiStopOptions{PreserveScreen: &preserve})
	}
}

// RunFirstTimeSetup runs the interactive first-time theme and analytics setup dialog
// and persists any accepted choices via settingsManager.
func RunFirstTimeSetup(
	ctx context.Context,
	settingsManager *core.SettingsManager,
	options ...*StartupUiOptions,
) (FirstTimeSetupSummary, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	opt := suResolveOptions(options)
	ui, ownsUI := suNewUI(settingsManager, opt)

	var themeCtrl *core.InteractiveThemeController
	if !opt.DisableThemeController {
		var setupComp *components.FirstTimeSetupComponent
		var compMu sync.Mutex
		theme.SetThemeJsonValidator(theme.ValidateThemeJson)
		themeCtrl = core.NewInteractiveThemeController(core.InteractiveThemeControllerOptions{
			SettingsManager: settingsManager,
			Ui:              ui,
			OnThemeApplied: func() {
				compMu.Lock()
				comp := setupComp
				compMu.Unlock()
				if comp != nil {
					ui.Post(func() {
						comp.Invalidate()
						ui.RequestRender(nil)
					})
				}
			},
		})
		_, _ = themeCtrl.ApplyFromSettings()
		defer func() {
			themeCtrl.Dispose()
			theme.StopThemeWatcher()
		}()

		return suRunFirstTimeSetupLoop(ctx, ui, ownsUI, settingsManager, themeCtrl, &compMu, &setupComp)
	}

	var setupComp *components.FirstTimeSetupComponent
	var compMu sync.Mutex
	return suRunFirstTimeSetupLoop(ctx, ui, ownsUI, settingsManager, nil, &compMu, &setupComp)
}

func suRunFirstTimeSetupLoop(
	ctx context.Context,
	ui tui.TUI,
	ownsUI bool,
	settingsManager *core.SettingsManager,
	themeCtrl *core.InteractiveThemeController,
	compMu *sync.Mutex,
	setupCompOut **components.FirstTimeSetupComponent,
) (FirstTimeSetupSummary, error) {
	type setupOutcome struct {
		completed bool
		result    components.FirstTimeSetupResult
	}
	doneCh := make(chan setupOutcome, 1)
	var once sync.Once
	finish := func(outcome setupOutcome) {
		once.Do(func() {
			doneCh <- outcome
		})
	}

	comp := components.NewFirstTimeSetupComponent(components.FirstTimeSetupOptions{
		OnThemePreview: func(themeName string) {
			if themeCtrl != nil {
				_ = themeCtrl.Preview(themeName)
			} else {
				_ = theme.SetTheme(themeName, false)
			}
			ui.RequestRender(nil)
		},
		OnSubmit: func(res components.FirstTimeSetupResult) {
			finish(setupOutcome{completed: true, result: res})
		},
		OnCancel: func() {
			finish(setupOutcome{completed: false})
		},
	})
	compMu.Lock()
	*setupCompOut = comp
	compMu.Unlock()

	if ownsUI {
		ui.Start()
	}
	ui.AddChild(comp)
	ui.SetFocus(comp)
	forceRender := true
	ui.RenderNow(&forceRender)

	var outcome setupOutcome
	var waitErr error
	select {
	case outcome = <-doneCh:
	case <-ctx.Done():
		waitErr = ctx.Err()
	}

	compMu.Lock()
	*setupCompOut = nil
	compMu.Unlock()
	ui.RemoveChild(comp)
	suShutdownUI(ui, ownsUI)

	if waitErr != nil {
		return FirstTimeSetupSummary{}, waitErr
	}
	if !outcome.completed {
		return FirstTimeSetupSummary{Completed: false}, nil
	}

	if settingsManager != nil {
		settingsManager.SetTheme(outcome.result.Theme)
		settingsManager.SetEnableAnalytics(outcome.result.ShareAnalytics)
		if outcome.result.ShareAnalytics {
			_ = settingsManager.GetOrCreateDeviceId()
		}
		if err := settingsManager.Flush(); err != nil {
			return FirstTimeSetupSummary{Completed: true, Result: &outcome.result}, err
		}
	}
	resCopy := outcome.result
	return FirstTimeSetupSummary{Completed: true, Result: &resCopy}, nil
}

// ShowStartupSelect renders an ExtensionSelectorComponent dialog on a transient
// TUI screen and resolves with the chosen option, or nil if cancelled.
func ShowStartupSelect(
	ctx context.Context,
	title string,
	options []string,
	description *string,
	uiOpts ...*StartupUiOptions,
) (*string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(options) == 0 {
		return nil, nil
	}
	opt := suResolveOptions(uiOpts)
	ui, ownsUI := suNewUI(nil, opt)

	if !opt.DisableThemeController {
		theme.SetThemeJsonValidator(theme.ValidateThemeJson)
		if _, err := theme.InitTheme(theme.SystemThemeName, false); err != nil {
			_, _ = theme.InitTheme("dark", false)
		}
	}

	type selectResult struct {
		value    string
		selected bool
	}
	doneCh := make(chan selectResult, 1)
	var once sync.Once
	finish := func(res selectResult) {
		once.Do(func() {
			doneCh <- res
		})
	}

	comp := components.NewExtensionSelectorComponent(
		title,
		options,
		func(selected string) {
			finish(selectResult{value: selected, selected: true})
		},
		func() {
			finish(selectResult{selected: false})
		},
		&components.ExtensionSelectorOptions{
			Tui:         ui,
			Description: description,
		},
	)

	if ownsUI {
		ui.Start()
	}
	ui.AddChild(comp)
	ui.SetFocus(comp)
	forceRender := true
	ui.RenderNow(&forceRender)

	var res selectResult
	var waitErr error
	select {
	case res = <-doneCh:
	case <-ctx.Done():
		waitErr = ctx.Err()
	}

	comp.Dispose()
	ui.RemoveChild(comp)
	suShutdownUI(ui, ownsUI)

	if waitErr != nil {
		return nil, waitErr
	}
	if !res.selected {
		return nil, nil
	}
	val := res.value
	return &val, nil
}

// ShowStartupConfirm renders a Yes/No confirmation prompt at startup.
func ShowStartupConfirm(
	ctx context.Context,
	title string,
	message string,
	uiOpts ...*StartupUiOptions,
) (bool, error) {
	var desc *string
	if message != "" {
		desc = &message
	}
	selected, err := ShowStartupSelect(ctx, title, []string{"Yes", "No"}, desc, uiOpts...)
	if err != nil || selected == nil {
		return false, err
	}
	return *selected == "Yes", nil
}

// ShowStartupInput renders an ExtensionInputComponent dialog at startup.
func ShowStartupInput(
	ctx context.Context,
	title string,
	placeholder *string,
	uiOpts ...*StartupUiOptions,
) (*string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	opt := suResolveOptions(uiOpts)
	ui, ownsUI := suNewUI(nil, opt)

	if !opt.DisableThemeController {
		theme.SetThemeJsonValidator(theme.ValidateThemeJson)
		if _, err := theme.InitTheme(theme.SystemThemeName, false); err != nil {
			_, _ = theme.InitTheme("dark", false)
		}
	}

	type inputResult struct {
		value     string
		submitted bool
	}
	doneCh := make(chan inputResult, 1)
	var once sync.Once
	finish := func(res inputResult) {
		once.Do(func() {
			doneCh <- res
		})
	}

	comp := components.NewExtensionInputComponent(
		title,
		placeholder,
		func(value string) {
			finish(inputResult{value: value, submitted: true})
		},
		func() {
			finish(inputResult{submitted: false})
		},
		&components.ExtensionInputOptions{
			Tui: ui,
		},
	)

	if ownsUI {
		ui.Start()
	}
	ui.AddChild(comp)
	ui.SetFocus(comp)
	forceRender := true
	ui.RenderNow(&forceRender)

	var res inputResult
	var waitErr error
	select {
	case res = <-doneCh:
	case <-ctx.Done():
		waitErr = ctx.Err()
	}

	comp.Dispose()
	ui.RemoveChild(comp)
	suShutdownUI(ui, ownsUI)

	if waitErr != nil {
		return nil, waitErr
	}
	if !res.submitted {
		return nil, nil
	}
	val := res.value
	return &val, nil
}

// WarnStderr prints a formatted warning line to stderr cleanly.
func WarnStderr(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", args...)
}

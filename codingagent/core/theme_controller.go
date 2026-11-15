// Ported from packages/coding-agent/src/modes/interactive/theme/theme-controller.ts (pi v1.5.2).

package core

import (
	"context"
	"strings"
	"sync"

	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/tui"
)

// TerminalQueryTimeoutMs matches the startup OSC 10/11/4 color query timeout in milliseconds.
const TerminalQueryTimeoutMs int64 = 100

func tcHasDetectedColors(colors tui.TerminalColors) bool {
	return colors.Foreground != nil || colors.Background != nil || len(colors.Palette) > 0
}

func tcUsesTerminalTheme(selection string) bool {
	trimmed := strings.TrimSpace(selection)
	if trimmed == "" || trimmed == theme.SystemThemeName {
		return true
	}
	return theme.ParseAutoThemeSetting(trimmed) != nil
}

// RequestTerminalColors queries terminal foreground/background/palette colors asynchronously
// and invokes onColors with the discovered palette (including late OSC replies until cancelled).
// It returns a cancel function that prevents further callbacks.
func RequestTerminalColors(
	ui tui.TUI,
	timeoutMs int64,
	onColors func(colors tui.TerminalColors, isLate bool),
) (cancel func(), done <-chan struct{}) {
	ch := make(chan struct{})
	if ui == nil {
		close(ch)
		return func() {}, ch
	}
	if timeoutMs <= 0 {
		timeoutMs = TerminalQueryTimeoutMs
	}

	var mu sync.Mutex
	cancelled := false
	isCancelled := func() bool {
		mu.Lock()
		defer mu.Unlock()
		return cancelled
	}

	go func() {
		defer close(ch)
		colors, err := ui.QueryTerminalColors(tui.TuiQueryTerminalColorsOptions{
			TimeoutMs: timeoutMs,
			OnLateReply: func(late tui.TerminalColors) {
				if isCancelled() || !tcHasDetectedColors(late) {
					return
				}
				if onColors != nil {
					onColors(late, true)
				}
			},
		})
		if err != nil || isCancelled() || !tcHasDetectedColors(colors) {
			return
		}
		if onColors != nil {
			onColors(colors, false)
		}
	}()

	return func() {
		mu.Lock()
		cancelled = true
		mu.Unlock()
	}, ch
}

// InteractiveThemeControllerOptions configures InteractiveThemeController.
type InteractiveThemeControllerOptions struct {
	SettingsManager        *SettingsManager
	Ui                     tui.TUI
	CustomThemes           []*theme.Theme
	OnThemeApplied         func()
	OnThemeError           func(err string)
	TerminalQueryTimeoutMs *int64
}

// InteractiveThemeController coordinates active theme selection, system color-scheme updates,
// and startup terminal color queries with SettingsManager and tui.TUI.
type InteractiveThemeController struct {
	mu              sync.Mutex
	settingsManager *SettingsManager
	ui              tui.TUI
	customThemes    []*theme.Theme
	onThemeApplied  func()
	onThemeError    func(err string)
	queryTimeoutMs  int64

	selection       string
	autoSyncEnabled bool
	disposed        bool
	unsubScheme     func()
	cancelQuery     func()
	queryDone       <-chan struct{}
}

// NewInteractiveThemeController creates an InteractiveThemeController for managing theme state.
func NewInteractiveThemeController(options InteractiveThemeControllerOptions) *InteractiveThemeController {
	timeout := TerminalQueryTimeoutMs
	if options.TerminalQueryTimeoutMs != nil && *options.TerminalQueryTimeoutMs > 0 {
		timeout = *options.TerminalQueryTimeoutMs
	}
	return &InteractiveThemeController{
		settingsManager: options.SettingsManager,
		ui:              options.Ui,
		customThemes:    append([]*theme.Theme(nil), options.CustomThemes...),
		onThemeApplied:  options.OnThemeApplied,
		onThemeError:    options.OnThemeError,
		queryTimeoutMs:  timeout,
		selection:       theme.SystemThemeName,
		autoSyncEnabled: true,
	}
}

// ApplyFromSettings loads registered custom themes, initializes the configured theme, and starts
// terminal color detection when connected to a TUI.
func (c *InteractiveThemeController) ApplyFromSettings() (*theme.Theme, error) {
	c.mu.Lock()
	if c.disposed {
		c.mu.Unlock()
		return theme.CurrentTheme(), nil
	}
	sm := c.settingsManager
	custom := append([]*theme.Theme(nil), c.customThemes...)
	selection := theme.SystemThemeName
	if sm != nil {
		if configured := sm.GetThemeSetting(); configured != nil && strings.TrimSpace(*configured) != "" {
			selection = strings.TrimSpace(*configured)
		}
	}
	c.selection = selection
	c.autoSyncEnabled = true
	ui := c.ui
	onErr := c.onThemeError
	c.mu.Unlock()

	theme.SetRegisteredThemes(custom)
	if ui != nil && tcUsesTerminalTheme(selection) {
		theme.MarkTerminalColorsPending()
	}

	applied, err := theme.InitTheme(selection, true)
	if err != nil && onErr != nil {
		onErr(err.Error())
	}
	c.bindUiListeners(ui)
	c.startTerminalColorQuery(ui)
	return applied, err
}

// RebindTui switches the attached TUI instance and restarts terminal color/scheme observation.
func (c *InteractiveThemeController) RebindTui(ui tui.TUI) {
	c.mu.Lock()
	if c.disposed {
		c.mu.Unlock()
		return
	}
	oldUnsub := c.unsubScheme
	oldCancel := c.cancelQuery
	c.unsubScheme = nil
	c.cancelQuery = nil
	c.queryDone = nil
	c.ui = ui
	c.mu.Unlock()

	if oldUnsub != nil {
		oldUnsub()
	}
	if oldCancel != nil {
		oldCancel()
	}

	c.bindUiListeners(ui)
	c.startTerminalColorQuery(ui)
}

func (c *InteractiveThemeController) bindUiListeners(ui tui.TUI) {
	c.mu.Lock()
	oldUnsub := c.unsubScheme
	c.unsubScheme = nil
	disposed := c.disposed
	c.mu.Unlock()

	if oldUnsub != nil {
		oldUnsub()
	}
	if disposed || ui == nil {
		return
	}

	ui.SetTerminalColorSchemeNotifications(true)
	unsub := ui.OnTerminalColorSchemeChange(func(scheme tui.TerminalColorScheme) {
		c.handleSchemeChange(scheme)
	})

	c.mu.Lock()
	if c.disposed || c.ui != ui {
		c.mu.Unlock()
		if unsub != nil {
			unsub()
		}
		return
	}
	c.unsubScheme = unsub
	c.mu.Unlock()
}

func (c *InteractiveThemeController) startTerminalColorQuery(ui tui.TUI) {
	c.mu.Lock()
	if c.disposed || ui == nil {
		c.mu.Unlock()
		return
	}
	if c.cancelQuery != nil {
		c.cancelQuery()
	}
	timeout := c.queryTimeoutMs
	c.mu.Unlock()

	cancel, done := RequestTerminalColors(ui, timeout, func(colors tui.TerminalColors, _ bool) {
		c.handleTerminalColors(colors)
	})

	c.mu.Lock()
	if c.disposed || c.ui != ui {
		c.mu.Unlock()
		cancel()
		return
	}
	c.cancelQuery = cancel
	c.queryDone = done
	c.mu.Unlock()
}

func (c *InteractiveThemeController) handleTerminalColors(colors tui.TerminalColors) {
	c.mu.Lock()
	if c.disposed || !c.autoSyncEnabled {
		c.mu.Unlock()
		return
	}
	selection := c.selection
	ui := c.ui
	onApplied := c.onThemeApplied
	c.mu.Unlock()

	theme.SetTerminalColors(colors)
	if tcUsesTerminalTheme(selection) {
		theme.SetTheme(selection, true)
	}
	if onApplied != nil {
		onApplied()
	}
	if ui != nil {
		ui.RequestRender(nil)
	}
}

func (c *InteractiveThemeController) handleSchemeChange(scheme tui.TerminalColorScheme) {
	c.mu.Lock()
	if c.disposed || !c.autoSyncEnabled {
		c.mu.Unlock()
		return
	}
	selection := c.selection
	ui := c.ui
	onApplied := c.onThemeApplied
	c.mu.Unlock()

	theme.SetTerminalColorScheme(&scheme)
	if tcUsesTerminalTheme(selection) {
		theme.SetTheme(selection, true)
	}
	if onApplied != nil {
		onApplied()
	}
	if ui != nil {
		ui.RequestRender(nil)
	}
}

// WaitForTerminalColors blocks until the most recent terminal color query finishes or ctx cancels.
func (c *InteractiveThemeController) WaitForTerminalColors(ctx ...context.Context) error {
	c.mu.Lock()
	done := c.queryDone
	c.mu.Unlock()
	if done == nil {
		return nil
	}
	var waitCtx context.Context
	if len(ctx) > 0 && ctx[0] != nil {
		waitCtx = ctx[0]
	}
	if waitCtx == nil {
		<-done
		return nil
	}
	select {
	case <-done:
		return nil
	case <-waitCtx.Done():
		return waitCtx.Err()
	}
}

// GetThemeSelection returns the active requested theme setting (e.g., "system", "dark", or "light/dark").
func (c *InteractiveThemeController) GetThemeSelection() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.selection
}

// GetTerminalTheme returns the effective dark/light scheme from the theme subsystem.
func (c *InteractiveThemeController) GetTerminalTheme() tui.TerminalColorScheme {
	return theme.GetTerminalTheme()
}

// Preview temporarily switches the active theme without updating persisted settings.
func (c *InteractiveThemeController) Preview(name string) theme.SetThemeResult {
	c.mu.Lock()
	c.selection = name
	c.autoSyncEnabled = true
	ui := c.ui
	onApplied := c.onThemeApplied
	c.mu.Unlock()

	res := theme.SetTheme(name, true)
	if res.Success && onApplied != nil {
		onApplied()
	}
	if res.Success && ui != nil {
		ui.RequestRender(nil)
	}
	return res
}

// SetThemeSetting validates, applies, and persists a theme selection in SettingsManager.
func (c *InteractiveThemeController) SetThemeSetting(setting string) theme.SetThemeResult {
	c.mu.Lock()
	c.selection = setting
	c.autoSyncEnabled = true
	sm := c.settingsManager
	ui := c.ui
	onApplied := c.onThemeApplied
	c.mu.Unlock()

	res := theme.SetTheme(setting, true)
	if res.Success {
		if sm != nil {
			sm.SetTheme(setting)
		}
		if onApplied != nil {
			onApplied()
		}
		if ui != nil {
			ui.RequestRender(nil)
		}
	}
	return res
}

// SetThemeName applies and persists a theme name or "light/dark" setting.
func (c *InteractiveThemeController) SetThemeName(name string) theme.SetThemeResult {
	return c.SetThemeSetting(name)
}

// SetThemeInstance directly activates an already-constructed Theme instance and stops auto scheme syncing.
func (c *InteractiveThemeController) SetThemeInstance(t *theme.Theme) {
	if t == nil {
		return
	}
	c.mu.Lock()
	c.autoSyncEnabled = false
	c.selection = t.Name
	ui := c.ui
	onApplied := c.onThemeApplied
	c.mu.Unlock()

	theme.SetThemeInstance(t)
	if onApplied != nil {
		onApplied()
	}
	if ui != nil {
		ui.RequestRender(nil)
	}
}

// SetCustomThemes updates the list of custom themes registered for lookup.
func (c *InteractiveThemeController) SetCustomThemes(themes []*theme.Theme) {
	c.mu.Lock()
	c.customThemes = append([]*theme.Theme(nil), themes...)
	c.mu.Unlock()
	theme.SetRegisteredThemes(themes)
}

// DisableAutoSync prevents future terminal color/scheme updates from overriding the active theme.
func (c *InteractiveThemeController) DisableAutoSync() {
	c.mu.Lock()
	c.autoSyncEnabled = false
	c.mu.Unlock()
}

// Dispose releases UI listeners, cancels pending color queries, and stops the theme watcher.
func (c *InteractiveThemeController) Dispose() {
	c.mu.Lock()
	if c.disposed {
		c.mu.Unlock()
		return
	}
	c.disposed = true
	unsub := c.unsubScheme
	cancel := c.cancelQuery
	c.unsubScheme = nil
	c.cancelQuery = nil
	c.mu.Unlock()

	if unsub != nil {
		unsub()
	}
	if cancel != nil {
		cancel()
	}
	theme.StopThemeWatcher()
}

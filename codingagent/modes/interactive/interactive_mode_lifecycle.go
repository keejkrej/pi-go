// Ported from packages/coding-agent/src/modes/interactive/interactive-mode.ts (pi v1.5.2).

package interactive

import (
	"context"
	"fmt"
	"strings"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/components"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/tui"
)

func (m *InteractiveMode) imlcUpdateEditorBorderColor() {
	if m.editor == nil && m.defaultEditor == nil {
		return
	}
	m.mu.Lock()
	isBash := m.isBashMode
	m.mu.Unlock()

	var borderFn func(string) string
	if isBash {
		borderFn = func(s string) string {
			return theme.CurrentTheme().Fg(theme.FgBashMode, s)
		}
	} else {
		level := ai.ModelThinkingLevelOff
		if session := m.imSession(); session != nil {
			level = session.ThinkingLevel()
		}
		borderFn = theme.CurrentTheme().GetThinkingBorderColor(level)
	}
	if m.editor != nil {
		imhSetEditorBorderColor(m.editor, borderFn)
	}
	if m.defaultEditor != nil && m.editor != imEditor(m.defaultEditor) {
		imhSetEditorBorderColor(m.defaultEditor, borderFn)
	}
}

func (m *InteractiveMode) imlcEnsureWorkingStatusIndicator() *components.StatusIndicator {
	m.mu.Lock()
	visible := m.workingVisible
	loading := m.loadingAnimation
	indicatorOpts := m.workingIndicator
	colorFn := m.defaultWorkingColorFn
	uiMode := m.uiMode
	msg := m.imlcDefaultWorkingLabelLocked()
	m.mu.Unlock()

	if !visible {
		m.imlcClearWorkingStatusIndicator()
		return nil
	}

	ui := m.imCurrentUI()
	if loading != nil {
		loading.SetMessage(msg)
		m.imlcAttachStatusIndicatorToUI(loading, uiMode)
		return loading
	}

	w := components.NewWorkingStatusIndicator(ui, msg, indicatorOpts, colorFn)
	m.mu.Lock()
	if !m.workingVisible || m.stopped {
		m.mu.Unlock()
		w.Dispose()
		return nil
	}
	if m.loadingAnimation != nil {
		existing := m.loadingAnimation
		m.mu.Unlock()
		w.Dispose()
		existing.SetMessage(msg)
		return existing
	}
	m.loadingAnimation = w.StatusIndicator
	m.mu.Unlock()

	m.imlcAttachStatusIndicatorToUI(w.StatusIndicator, uiMode)
	return w.StatusIndicator
}

func (m *InteractiveMode) imlcDefaultWorkingLabelLocked() string {
	if m.workingMessage != nil && strings.TrimSpace(*m.workingMessage) != "" {
		return *m.workingMessage
	}
	return m.defaultWorkingMessage
}

func (m *InteractiveMode) imlcAttachStatusIndicatorToUI(ind *components.StatusIndicator, uiMode core.TuiMode) {
	if ind == nil {
		return
	}
	if uiMode == tui.TuiModeFullscreen && m.defaultEditor != nil && m.editor == imEditor(m.defaultEditor) {
		m.statusContainer.Clear()
		m.defaultEditor.SetWorkingStatusIndicator(ind)
	} else {
		if m.defaultEditor != nil {
			m.defaultEditor.SetWorkingStatusIndicator(nil)
		}
		m.statusContainer.Clear()
		m.statusContainer.AddChild(ind)
	}
	m.imRequestRender()
}

func (m *InteractiveMode) imlcClearWorkingStatusIndicator() {
	m.mu.Lock()
	loading := m.loadingAnimation
	m.loadingAnimation = nil
	m.mu.Unlock()

	if m.defaultEditor != nil {
		m.defaultEditor.SetWorkingStatusIndicator(nil)
	}
	if loading != nil {
		m.statusContainer.RemoveChild(loading)
		loading.Dispose()
	}
	m.imRequestRender()
}

func (m *InteractiveMode) imlcUpdateWorkingMessage() {
	m.mu.Lock()
	loading := m.loadingAnimation
	visible := m.workingVisible
	msg := m.imlcDefaultWorkingLabelLocked()
	m.mu.Unlock()

	if !visible || loading == nil {
		return
	}
	loading.SetMessage(msg)
	m.imRequestRender()
}

func (m *InteractiveMode) imlcSetRetryStatus(attempt, maxAttempts int, delayMs int64) {
	m.imlcClearRetryStatus()
	ui := m.imCurrentUI()
	ind := components.NewRetryStatusIndicator(ui, attempt, maxAttempts, delayMs)

	m.mu.Lock()
	m.retryIndicator = ind
	m.mu.Unlock()

	m.statusContainer.AddChild(ind)
	m.imRequestRender()
}

func (m *InteractiveMode) imlcClearRetryStatus() {
	m.mu.Lock()
	ind := m.retryIndicator
	m.retryIndicator = nil
	m.mu.Unlock()

	if ind != nil {
		m.statusContainer.RemoveChild(ind)
		ind.Dispose()
		m.imRequestRender()
	}
}

func (m *InteractiveMode) imlcSetCompactionStatus(reason components.CompactionStatusReason) {
	m.imlcClearCompactionStatus()
	ui := m.imCurrentUI()
	ind := components.NewCompactionStatusIndicator(ui, reason)

	m.mu.Lock()
	m.compactionIndicator = ind
	m.mu.Unlock()

	m.statusContainer.AddChild(ind)
	m.imRequestRender()
}

func (m *InteractiveMode) imlcClearCompactionStatus() {
	m.mu.Lock()
	ind := m.compactionIndicator
	m.compactionIndicator = nil
	m.mu.Unlock()

	if ind != nil {
		m.statusContainer.RemoveChild(ind)
		ind.Dispose()
		m.imRequestRender()
	}
}

func (m *InteractiveMode) imlcStopAllStatusIndicators() {
	m.imlcClearWorkingStatusIndicator()
	m.imlcClearRetryStatus()
	m.imlcClearCompactionStatus()
	m.statusContainer.Clear()
}

func (m *InteractiveMode) imlcRenderExtensionWidgets() {
	m.aboveEditorWidgetContainer.Clear()
	m.belowEditorWidgetContainer.Clear()

	m.mu.Lock()
	order := append([]string(nil), m.extensionWidgetsOrder...)
	widgets := make([]*imWidgetEntry, 0, len(order))
	for _, key := range order {
		if entry, ok := m.extensionWidgets[key]; ok && entry != nil {
			widgets = append(widgets, entry)
		}
	}
	m.mu.Unlock()

	ui := m.imCurrentUI()
	th := theme.CurrentTheme()
	for _, w := range widgets {
		var comp tui.Component
		if w.Factory != nil {
			comp = w.Factory(ui, th)
		} else if len(w.Lines) > 0 {
			text := strings.Join(w.Lines, "\n")
			comp = tui.NewText(text, 1, 0, nil)
		}
		if comp == nil {
			continue
		}
		if w.Placement == core.WidgetPlacementBelowEditor {
			m.belowEditorWidgetContainer.AddChild(comp)
		} else {
			m.aboveEditorWidgetContainer.AddChild(comp)
		}
	}
	m.imRequestRender()
}

func (m *InteractiveMode) imlcRefreshFooter() {
	if m == nil {
		return
	}
	if m.customFooter != nil {
		m.customFooter.Invalidate()
		m.imRequestRender()
		return
	}
	m.mu.Lock()
	started := m.started
	stopped := m.stopped
	m.mu.Unlock()
	if stopped {
		return
	}
	if !started {
		m.imlcRefreshFooterSync(true)
		m.imRequestRender()
		return
	}
	m.imPostUI(func() {
		if m.imIsStopped() {
			return
		}
		m.imlcRefreshFooterSync(false)
		m.imRequestRender()
	})
}

func (m *InteractiveMode) imlcRefreshFooterSync(force bool) {
	m.imlcRefreshFooterWidth(0, force)
}

func (m *InteractiveMode) imlcRefreshFooterForWidth(width int) {
	m.imlcRefreshFooterWidth(width, false)
}

func (m *InteractiveMode) imlcRefreshFooterWidth(width int, force bool) {
	if m == nil {
		return
	}
	m.mu.Lock()
	ftr := m.footer
	proxy := m.footerProxy
	customFtr := m.customFooter
	stopped := m.stopped
	ui := m.ui
	m.mu.Unlock()
	if stopped {
		return
	}
	if customFtr != nil {
		customFtr.Invalidate()
		return
	}
	if ftr == nil || proxy == nil {
		return
	}
	if width <= 0 {
		width = 80
		if ui != nil {
			if term := ui.Terminal(); term != nil && term.Columns() > 0 {
				width = term.Columns()
			}
		}
	}
	if !m.footerRenderMu.TryLock() {
		return
	}
	defer m.footerRenderMu.Unlock()
	if !force && !m.imcIsFooterSafeToReadSession() {
		return
	}
	ftr.Invalidate()
	lines := ftr.Render(width)
	proxy.SetLines(width, lines)
}

func (m *InteractiveMode) imlcReloadRuntime(ctx context.Context) error {
	session := m.imSession()
	if session == nil {
		return nil
	}
	if ctx == nil {
		ctx = m.bgCtx
	}

	err := session.Reload(ctx, &core.ReloadOptions{
		BeforeSessionStart: func(reloadCtx context.Context) error {
			m.imCallUI(reloadCtx, func() {
				if m.keybindings != nil {
					m.keybindings.Reload()
					if m.keybindings.KeybindingsManager != nil {
						tui.SetKeybindings(m.keybindings.KeybindingsManager)
					}
				}
				m.imeuResetExtensionUI()
				m.imsbApplyRuntimeSettings()
			})
			return nil
		},
	})
	if err != nil {
		return err
	}

	m.imCallUI(ctx, func() {
		if m.themeController != nil {
			if rl := m.imResourceLoader(); rl != nil {
				m.themeController.SetCustomThemes(rl.GetThemes().Themes)
			}
			_, _ = m.themeController.ApplyFromSettings()
		}
		m.imsbBindCurrentSessionExtensions(ctx)
		m.imsbApplyRuntimeSettings()
		m.imcUpdateAvailableProviderCount()
		m.imRequestRender(true)
	})
	return nil
}

func (m *InteractiveMode) imlcShutdown(exitCode int) {
	if sm := m.imSettingsManager(); sm != nil {
		_ = sm.Flush()
	}
	if m.runtime != nil {
		_ = m.runtime.Dispose(context.Background())
	}
	m.Stop()
	utils.ConfigExit(exitCode)
}

func (m *InteractiveMode) imlcShowOverlay(component tui.Component, options *tui.OverlayOptions) tui.OverlayHandle {
	ui := m.imCurrentUI()
	if ui == nil || component == nil {
		return nil
	}
	handle := ui.ShowOverlay(component, options)
	m.mu.Lock()
	m.overlayHandle = handle
	m.mu.Unlock()
	ui.SetFocus(component)
	m.imRequestRender()
	return handle
}

func (m *InteractiveMode) imlcHideOverlay(restoreFocusTo ...tui.Component) {
	m.mu.Lock()
	handle := m.overlayHandle
	m.overlayHandle = nil
	m.mu.Unlock()

	if handle != nil {
		handle.Hide()
	} else if ui := m.imCurrentUI(); ui != nil {
		ui.HideOverlay()
	}

	ui := m.imCurrentUI()
	if ui != nil {
		if len(restoreFocusTo) > 0 && restoreFocusTo[0] != nil {
			ui.SetFocus(restoreFocusTo[0])
		} else if m.editor != nil {
			ui.SetFocus(m.editor)
		}
	}
	m.imRequestRender()
}

func (m *InteractiveMode) imlcShowTemporarySelector(selector tui.Component, onDispose ...func()) {
	m.editorContainer.Clear()
	m.editorContainer.AddChild(selector)
	ui := m.imCurrentUI()
	if ui != nil {
		ui.SetFocus(selector)
	}
	m.mu.Lock()
	m.pendingSelectorMode = true
	if len(onDispose) > 0 {
		m.customOverlayCancel = onDispose[0]
	} else {
		m.customOverlayCancel = nil
	}
	m.mu.Unlock()
	m.imRequestRender()
}

func (m *InteractiveMode) imlcRestoreEditor() {
	m.mu.Lock()
	cancelFn := m.customOverlayCancel
	m.customOverlayCancel = nil
	m.pendingSelectorMode = false
	m.mu.Unlock()

	if cancelFn != nil {
		cancelFn()
	}
	m.editorContainer.Clear()
	if m.editor != nil {
		m.editorContainer.AddChild(m.editor)
	}
	if ui := m.imCurrentUI(); ui != nil && m.editor != nil {
		ui.SetFocus(m.editor)
	}
	m.imRequestRender()
}

func (m *InteractiveMode) imlcRunSelectorDialog(ctx context.Context, build func(done func())) {
	if ctx == nil {
		ctx = m.bgCtx
	}
	doneCh := make(chan struct{})
	var closeOnce func()
	closed := false
	closeOnce = func() {
		m.mu.Lock()
		if !closed {
			closed = true
			close(doneCh)
		}
		m.mu.Unlock()
	}

	m.imPostUI(func() {
		build(func() {
			m.imlcRestoreEditor()
			closeOnce()
		})
	})

	select {
	case <-doneCh:
	case <-ctx.Done():
		m.imPostUI(func() {
			m.imlcRestoreEditor()
		})
		closeOnce()
	case <-m.stopCh:
		closeOnce()
	}
}

func (m *InteractiveMode) imlcConfirmModal(ctx context.Context, title, message string) bool {
	if ctx == nil {
		ctx = m.bgCtx
	}
	sel, err := m.imeuCreateExtensionUIContext().Select(ctx, title+": "+message, []string{"Yes", "No"})
	if err != nil || sel == nil {
		return false
	}
	return *sel == "Yes"
}

func (m *InteractiveMode) imlcInputModal(ctx context.Context, title string, placeholder *string) *string {
	if ctx == nil {
		ctx = m.bgCtx
	}
	val, err := m.imeuCreateExtensionUIContext().Input(ctx, title, placeholder)
	if err != nil {
		return nil
	}
	return val
}

func (m *InteractiveMode) imlcNotifyAsyncError(prefix string, err error) {
	if err == nil {
		return
	}
	m.ShowError(fmt.Sprintf("%s: %s", prefix, imhErrorMessage(err)))
}

// Ported from packages/coding-agent/src/modes/interactive/interactive-mode.ts (pi v1.5.2).

package interactive

import (
	"context"
	"fmt"
	"sync"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/components"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/tui"
)

type imeuExtensionUIContext struct {
	mode *InteractiveMode
}

var _ core.ExtensionUIContext = (*imeuExtensionUIContext)(nil)

func (m *InteractiveMode) imeuCreateExtensionUIContext() core.ExtensionUIContext {
	return &imeuExtensionUIContext{mode: m}
}

func (m *InteractiveMode) imeuResetExtensionUI() {
	m.mu.Lock()
	cancelSel := m.customOverlayCancel
	m.customOverlayCancel = nil
	m.extensionWidgets = make(map[string]*imWidgetEntry)
	m.extensionWidgetsOrder = nil
	m.customHeader = nil
	m.customFooter = nil
	m.customEditorFactory = nil
	m.customAutocompleteFactory = nil
	m.terminalInputListeners = nil
	m.workingMessage = nil
	m.workingVisible = true
	m.workingIndicator = nil
	m.hiddenThinkingLabel = nil
	m.mu.Unlock()

	if cancelSel != nil {
		cancelSel()
	}

	if m.footerDataProvider != nil {
		m.footerDataProvider.ClearExtensionStatuses()
	}
	m.aboveEditorWidgetContainer.Clear()
	m.belowEditorWidgetContainer.Clear()

	if m.defaultEditor != nil {
		m.editor = m.defaultEditor
		m.editorContainer.Clear()
		m.editorContainer.AddChild(m.editor)
	}
	if m.ui != nil {
		m.imcMountLayout(m.ui, m.imUiMode())
	}
	m.iminSetupAutocomplete()
	m.imlcUpdateEditorBorderColor()
}

func (e *imeuExtensionUIContext) Select(ctx context.Context, title string, options []string, opts ...*core.ExtensionUIDialogOptions) (*string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var dlgOpts core.ExtensionUIDialogOptions
	if len(opts) > 0 && opts[0] != nil {
		dlgOpts = *opts[0]
		if dlgOpts.Ctx != nil {
			ctx = dlgOpts.Ctx
		}
	}

	m := e.mode
	if m == nil || m.imIsStopped() {
		return nil, nil
	}

	resultCh := make(chan *string, 1)
	var once sync.Once
	finish := func(val *string) {
		once.Do(func() {
			resultCh <- val
		})
	}

	var selComp *components.ExtensionSelectorComponent
	m.imPostUI(func() {
		if m.imIsStopped() {
			finish(nil)
			return
		}
		if m.programStatusReporter != nil {
			m.programStatusReporter.SetBlocked("extension-select", &BlockedStatus{
				Kind:    tui.ProgramStatusKindPermission,
				Message: title,
			})
		}
		selOpts := &components.ExtensionSelectorOptions{
			Tui:     m.imCurrentUI(),
			Timeout: dlgOpts.Timeout,
			OnToggleToolsExpanded: func() {
				e.SetToolsExpanded(!e.GetToolsExpanded())
			},
		}
		selComp = components.NewExtensionSelectorComponent(
			title,
			options,
			func(selected string) {
				if m.programStatusReporter != nil {
					m.programStatusReporter.SetBlocked("extension-select", nil)
				}
				m.imlcRestoreEditor()
				finish(imhStrPtr(selected))
			},
			func() {
				if m.programStatusReporter != nil {
					m.programStatusReporter.SetBlocked("extension-select", nil)
				}
				m.imlcRestoreEditor()
				finish(nil)
			},
			selOpts,
		)
		m.imlcShowTemporarySelector(selComp, func() {
			selComp.Dispose()
		})
	})

	select {
	case val := <-resultCh:
		return val, nil
	case <-ctx.Done():
		m.imPostUI(func() {
			if m.programStatusReporter != nil {
				m.programStatusReporter.SetBlocked("extension-select", nil)
			}
			m.imlcRestoreEditor()
		})
		finish(nil)
		return nil, ctx.Err()
	case <-m.stopCh:
		finish(nil)
		return nil, nil
	}
}

func (e *imeuExtensionUIContext) Confirm(ctx context.Context, title, message string, opts ...*core.ExtensionUIDialogOptions) (bool, error) {
	heading := title
	if message != "" {
		if heading != "" {
			heading = title + "\n" + message
		} else {
			heading = message
		}
	}
	res, err := e.Select(ctx, heading, []string{"Yes", "No"}, opts...)
	if err != nil {
		return false, err
	}
	return res != nil && *res == "Yes", nil
}

func (e *imeuExtensionUIContext) Input(ctx context.Context, title string, placeholder *string, opts ...*core.ExtensionUIDialogOptions) (*string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var dlgOpts core.ExtensionUIDialogOptions
	if len(opts) > 0 && opts[0] != nil {
		dlgOpts = *opts[0]
		if dlgOpts.Ctx != nil {
			ctx = dlgOpts.Ctx
		}
	}

	m := e.mode
	if m == nil || m.imIsStopped() {
		return nil, nil
	}

	resultCh := make(chan *string, 1)
	var once sync.Once
	finish := func(val *string) {
		once.Do(func() {
			resultCh <- val
		})
	}

	m.imPostUI(func() {
		if m.imIsStopped() {
			finish(nil)
			return
		}
		if m.programStatusReporter != nil {
			m.programStatusReporter.SetBlocked("extension-input", &BlockedStatus{
				Kind:    tui.ProgramStatusKindQuestion,
				Message: title,
			})
		}
		inpOpts := &components.ExtensionInputOptions{
			Tui:     m.imCurrentUI(),
			Timeout: dlgOpts.Timeout,
		}
		inp := components.NewExtensionInputComponent(
			title,
			placeholder,
			func(value string) {
				if m.programStatusReporter != nil {
					m.programStatusReporter.SetBlocked("extension-input", nil)
				}
				m.imlcRestoreEditor()
				finish(imhStrPtr(value))
			},
			func() {
				if m.programStatusReporter != nil {
					m.programStatusReporter.SetBlocked("extension-input", nil)
				}
				m.imlcRestoreEditor()
				finish(nil)
			},
			inpOpts,
		)
		m.imlcShowTemporarySelector(inp, func() {
			inp.Dispose()
		})
	})

	select {
	case val := <-resultCh:
		return val, nil
	case <-ctx.Done():
		m.imPostUI(func() {
			if m.programStatusReporter != nil {
				m.programStatusReporter.SetBlocked("extension-input", nil)
			}
			m.imlcRestoreEditor()
		})
		finish(nil)
		return nil, ctx.Err()
	case <-m.stopCh:
		finish(nil)
		return nil, nil
	}
}

func (e *imeuExtensionUIContext) Notify(message string, notifyType ...string) {
	m := e.mode
	if m == nil {
		return
	}
	kind := "info"
	if len(notifyType) > 0 && notifyType[0] != "" {
		kind = notifyType[0]
	}
	switch kind {
	case "error":
		m.ShowError(message)
	case "warning":
		m.ShowWarning(message)
	default:
		m.ShowStatus(message)
	}
}

func (e *imeuExtensionUIContext) OnTerminalInput(handler core.TerminalInputHandler) func() {
	m := e.mode
	if m == nil || handler == nil {
		return func() {}
	}
	m.mu.Lock()
	m.nextTerminalListenerID++
	id := m.nextTerminalListenerID
	m.terminalInputListeners = append(m.terminalInputListeners, imKeyInputListenerEntry{
		ID: id,
		Fn: handler,
	})
	m.mu.Unlock()

	return func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		for i, entry := range m.terminalInputListeners {
			if entry.ID == id {
				m.terminalInputListeners = append(m.terminalInputListeners[:i], m.terminalInputListeners[i+1:]...)
				break
			}
		}
	}
}

func (e *imeuExtensionUIContext) SetStatus(key string, text *string) {
	m := e.mode
	if m == nil {
		return
	}
	m.imPostUI(func() {
		if m.footerDataProvider != nil {
			m.footerDataProvider.SetExtensionStatus(key, text)
		}
		m.imlcRefreshFooter()
	})
}

func (e *imeuExtensionUIContext) SetWorkingMessage(message ...string) {
	m := e.mode
	if m == nil {
		return
	}
	var msg *string
	if len(message) > 0 && message[0] != "" {
		v := message[0]
		msg = &v
	}
	m.mu.Lock()
	m.workingMessage = msg
	m.mu.Unlock()
	m.imPostUI(func() {
		m.imlcUpdateWorkingMessage()
	})
}

func (e *imeuExtensionUIContext) SetWorkingVisible(visible bool) {
	m := e.mode
	if m == nil {
		return
	}
	m.mu.Lock()
	m.workingVisible = visible
	m.mu.Unlock()
	m.imPostUI(func() {
		if visible {
			session := m.imSession()
			if session != nil && session.IsStreaming() {
				m.imlcEnsureWorkingStatusIndicator()
			}
		} else {
			m.imlcClearWorkingStatusIndicator()
		}
	})
}

func (e *imeuExtensionUIContext) SetWorkingIndicator(options *core.WorkingIndicatorOptions) {
	m := e.mode
	if m == nil {
		return
	}
	m.mu.Lock()
	m.workingIndicator = options
	loading := m.loadingAnimation
	m.mu.Unlock()

	if loading != nil {
		m.imPostUI(func() {
			m.imlcClearWorkingStatusIndicator()
			m.imlcEnsureWorkingStatusIndicator()
		})
	}
}

func (e *imeuExtensionUIContext) SetHiddenThinkingLabel(label ...string) {
	m := e.mode
	if m == nil {
		return
	}
	var lbl *string
	if len(label) > 0 && label[0] != "" {
		v := label[0]
		lbl = &v
	}
	m.mu.Lock()
	m.hiddenThinkingLabel = lbl
	m.mu.Unlock()

	m.imPostUI(func() {
		effective := imhDerefStr(lbl)
		for _, child := range m.chatContainer.Children() {
			if am, ok := child.(*components.AssistantMessageComponent); ok {
				am.SetHiddenThinkingLabel(effective)
			}
		}
		m.imRequestRender()
	})
}

func (e *imeuExtensionUIContext) SetWidgetLines(key string, content []string, options ...*core.ExtensionWidgetOptions) {
	m := e.mode
	if m == nil {
		return
	}
	placement := core.WidgetPlacementAboveEditor
	if len(options) > 0 && options[0] != nil && options[0].Placement != "" {
		placement = options[0].Placement
	}
	m.imeuSetWidget(key, &imWidgetEntry{
		Key:       key,
		Placement: placement,
		Lines:     append([]string(nil), content...),
	})
}

func (e *imeuExtensionUIContext) SetWidgetFactory(key string, factory func(t tui.TUI, th *theme.Theme) tui.Component, options ...*core.ExtensionWidgetOptions) {
	m := e.mode
	if m == nil {
		return
	}
	if factory == nil {
		m.imeuSetWidget(key, nil)
		return
	}
	placement := core.WidgetPlacementAboveEditor
	if len(options) > 0 && options[0] != nil && options[0].Placement != "" {
		placement = options[0].Placement
	}
	m.imeuSetWidget(key, &imWidgetEntry{
		Key:       key,
		Placement: placement,
		Factory:   factory,
	})
}

func (m *InteractiveMode) imeuSetWidget(key string, entry *imWidgetEntry) {
	m.mu.Lock()
	if entry == nil || (entry.Factory == nil && len(entry.Lines) == 0) {
		delete(m.extensionWidgets, key)
		filtered := m.extensionWidgetsOrder[:0]
		for _, k := range m.extensionWidgetsOrder {
			if k != key {
				filtered = append(filtered, k)
			}
		}
		m.extensionWidgetsOrder = filtered
	} else {
		if _, exists := m.extensionWidgets[key]; !exists {
			m.extensionWidgetsOrder = append(m.extensionWidgetsOrder, key)
		}
		m.extensionWidgets[key] = entry
	}
	m.mu.Unlock()

	m.imPostUI(func() {
		m.imlcRenderExtensionWidgets()
	})
}

func (e *imeuExtensionUIContext) SetFooter(factory func(t tui.TUI, th *theme.Theme, footerData core.ReadonlyFooterDataProvider) tui.Component) {
	m := e.mode
	if m == nil {
		return
	}
	m.imPostUI(func() {
		if m.customFooter != nil {
			imhDisposeComponent(m.customFooter)
			m.customFooter = nil
		}
		if factory != nil {
			m.customFooter = factory(m.imCurrentUI(), theme.CurrentTheme(), m.footerDataProvider)
		}
		if ui := m.imCurrentUI(); ui != nil {
			m.imcMountLayout(ui, m.imUiMode())
		}
		m.imRequestRender()
	})
}

func (e *imeuExtensionUIContext) SetHeader(factory func(t tui.TUI, th *theme.Theme) tui.Component) {
	m := e.mode
	if m == nil {
		return
	}
	m.imPostUI(func() {
		if m.customHeader != nil {
			imhDisposeComponent(m.customHeader)
			m.customHeader = nil
		}
		if factory != nil {
			m.customHeader = factory(m.imCurrentUI(), theme.CurrentTheme())
		}
		m.imcRenderHeaderAndChangelog()
		m.imRequestRender()
	})
}

func (e *imeuExtensionUIContext) SetTitle(title string) {
	m := e.mode
	if m == nil {
		return
	}
	m.imPostUI(func() {
		ui := m.imCurrentUI()
		if ui != nil && ui.Terminal() != nil {
			ui.Terminal().SetTitle(title)
		}
	})
}

func (e *imeuExtensionUIContext) Custom(
	ctx context.Context,
	factory func(t tui.TUI, th *theme.Theme, keybindings *tui.KeybindingsManager, done func(any)) (tui.Component, error),
	options ...*core.ExtensionCustomOptions,
) (any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	m := e.mode
	if m == nil || m.imIsStopped() || factory == nil {
		return nil, nil
	}
	var opts core.ExtensionCustomOptions
	if len(options) > 0 && options[0] != nil {
		opts = *options[0]
	}

	type customRes struct {
		val any
		err error
	}
	resCh := make(chan customRes, 1)
	var once sync.Once
	finish := func(val any, err error) {
		once.Do(func() {
			resCh <- customRes{val: val, err: err}
		})
	}

	m.imPostUI(func() {
		ui := m.imCurrentUI()
		if m.imIsStopped() || ui == nil {
			finish(nil, nil)
			return
		}
		if opts.Blocked != nil && m.programStatusReporter != nil {
			m.programStatusReporter.SetBlocked("extension-custom", &BlockedStatus{
				Kind:    opts.Blocked.Kind,
				Message: opts.Blocked.Message,
			})
		}

		var comp tui.Component
		doneFn := func(result any) {
			if opts.Blocked != nil && m.programStatusReporter != nil {
				m.programStatusReporter.SetBlocked("extension-custom", nil)
			}
			if opts.Overlay {
				m.imlcHideOverlay()
			} else {
				m.imlcRestoreEditor()
			}
			if comp != nil {
				imhDisposeComponent(comp)
			}
			finish(result, nil)
		}

		var kbMgr *tui.KeybindingsManager
		if m.keybindings != nil {
			kbMgr = m.keybindings.KeybindingsManager
		} else {
			kbMgr = tui.GetKeybindings()
		}

		var err error
		comp, err = factory(ui, theme.CurrentTheme(), kbMgr, doneFn)
		if err != nil {
			if opts.Blocked != nil && m.programStatusReporter != nil {
				m.programStatusReporter.SetBlocked("extension-custom", nil)
			}
			finish(nil, err)
			return
		}
		if comp == nil {
			if opts.Blocked != nil && m.programStatusReporter != nil {
				m.programStatusReporter.SetBlocked("extension-custom", nil)
			}
			finish(nil, nil)
			return
		}

		if opts.Overlay {
			ovOpts := opts.OverlayOptions
			if ovOpts == nil && opts.OverlayFn != nil {
				resolved := opts.OverlayFn()
				ovOpts = &resolved
			}
			handle := m.imlcShowOverlay(comp, ovOpts)
			if opts.OnHandle != nil && handle != nil {
				opts.OnHandle(handle)
			}
		} else {
			m.imlcShowTemporarySelector(comp, func() {
				imhDisposeComponent(comp)
			})
		}
	})

	select {
	case out := <-resCh:
		return out.val, out.err
	case <-ctx.Done():
		m.imPostUI(func() {
			if opts.Blocked != nil && m.programStatusReporter != nil {
				m.programStatusReporter.SetBlocked("extension-custom", nil)
			}
			if opts.Overlay {
				m.imlcHideOverlay()
			} else {
				m.imlcRestoreEditor()
			}
		})
		return nil, ctx.Err()
	case <-m.stopCh:
		return nil, nil
	}
}

func (e *imeuExtensionUIContext) PasteToEditor(text string) {
	m := e.mode
	if m == nil {
		return
	}
	m.imPostUI(func() {
		if m.editor != nil {
			m.editor.InsertTextAtCursor(text)
			m.imRequestRender()
		}
	})
}

func (e *imeuExtensionUIContext) SetEditorText(text string) {
	m := e.mode
	if m == nil {
		return
	}
	m.imPostUI(func() {
		if m.editor != nil {
			m.editor.SetText(text)
			m.imRequestRender()
		}
	})
}

func (e *imeuExtensionUIContext) GetEditorText() string {
	m := e.mode
	if m == nil || m.editor == nil {
		return ""
	}
	return m.editor.GetText()
}

func (e *imeuExtensionUIContext) Editor(ctx context.Context, title string, prefill ...string) (*string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	m := e.mode
	if m == nil || m.imIsStopped() {
		return nil, nil
	}
	var prefillPtr *string
	if len(prefill) > 0 && prefill[0] != "" {
		v := prefill[0]
		prefillPtr = &v
	}

	resultCh := make(chan *string, 1)
	var once sync.Once
	finish := func(val *string) {
		once.Do(func() {
			resultCh <- val
		})
	}

	m.imPostUI(func() {
		if m.imIsStopped() {
			finish(nil)
			return
		}
		var extCmd *string
		if sm := m.imSettingsManager(); sm != nil {
			if cmd := sm.GetExternalEditorCommand(); cmd != "" {
				extCmd = &cmd
			}
		}
		edComp := components.NewExtensionEditorComponent(
			m.imCurrentUI(),
			m.keybindings,
			title,
			prefillPtr,
			func(text string) {
				m.imlcRestoreEditor()
				finish(imhStrPtr(text))
			},
			func() {
				m.imlcRestoreEditor()
				finish(nil)
			},
			nil,
			extCmd,
		)
		m.imlcShowTemporarySelector(edComp)
	})

	select {
	case val := <-resultCh:
		return val, nil
	case <-ctx.Done():
		m.imPostUI(func() {
			m.imlcRestoreEditor()
		})
		finish(nil)
		return nil, ctx.Err()
	case <-m.stopCh:
		finish(nil)
		return nil, nil
	}
}

func (e *imeuExtensionUIContext) AddAutocompleteProvider(factory core.AutocompleteProviderFactory) {
	m := e.mode
	if m == nil {
		return
	}
	m.mu.Lock()
	m.customAutocompleteFactory = factory
	m.mu.Unlock()
	m.imPostUI(func() {
		m.iminSetupAutocomplete()
	})
}

func (e *imeuExtensionUIContext) SetEditorComponent(factory core.EditorFactory) {
	m := e.mode
	if m == nil {
		return
	}
	m.mu.Lock()
	m.customEditorFactory = factory
	m.mu.Unlock()

	m.imPostUI(func() {
		prevText := ""
		if m.editor != nil {
			prevText = m.editor.GetText()
		}
		if factory != nil {
			var kbMgr *tui.KeybindingsManager
			if m.keybindings != nil {
				kbMgr = m.keybindings.KeybindingsManager
			} else {
				kbMgr = tui.GetKeybindings()
			}
			customEd := factory(m.imCurrentUI(), theme.GetEditorTheme(), kbMgr)
			if customEd != nil {
				m.editor = customEd
			} else {
				m.editor = m.defaultEditor
			}
		} else {
			m.editor = m.defaultEditor
		}
		if m.editor != nil {
			m.editor.SetText(prevText)
		}
		m.editorContainer.Clear()
		if m.editor != nil {
			m.editorContainer.AddChild(m.editor)
		}
		m.iminSetupAutocomplete()
		m.iminSetupKeyHandlers()
		m.iminSetupEditorSubmitHandler()
		if ui := m.imCurrentUI(); ui != nil && m.editor != nil {
			ui.SetFocus(m.editor)
		}
		m.imRequestRender()
	})
}

func (e *imeuExtensionUIContext) GetEditorComponent() core.EditorFactory {
	m := e.mode
	if m == nil {
		return nil
	}
	m.mu.Lock()
	f := m.customEditorFactory
	m.mu.Unlock()
	return f
}

func (e *imeuExtensionUIContext) Theme() *theme.Theme {
	return theme.CurrentTheme()
}

func (e *imeuExtensionUIContext) GetAllThemes() []core.ThemeInfo {
	withPaths := theme.GetAvailableThemesWithPaths()
	out := make([]core.ThemeInfo, len(withPaths))
	for i, wp := range withPaths {
		var pathPtr *string
		if wp.Path != "" {
			p := wp.Path
			pathPtr = &p
		}
		out[i] = core.ThemeInfo{
			Name: wp.Name,
			Path: pathPtr,
		}
	}
	return out
}

func (e *imeuExtensionUIContext) GetTheme(name string) *theme.Theme {
	th, err := theme.GetThemeByName(name)
	if err != nil {
		return nil
	}
	return th
}

func (e *imeuExtensionUIContext) SetThemeByName(name string) core.SetThemeResult {
	m := e.mode
	if m != nil && m.themeController != nil {
		res := m.themeController.SetThemeName(name)
		if res.Success {
			m.imPostUI(func() {
				if ui := m.imCurrentUI(); ui != nil {
					ui.Invalidate()
				}
				m.imlcUpdateEditorBorderColor()
				m.imRequestRender()
			})
		}
		var errPtr *string
		if res.Error != "" {
			errMsg := res.Error
			errPtr = &errMsg
		}
		return core.SetThemeResult{Success: res.Success, Error: errPtr}
	}
	res := theme.SetTheme(name, true)
	var errPtr *string
	if res.Error != "" {
		errMsg := res.Error
		errPtr = &errMsg
	}
	return core.SetThemeResult{Success: res.Success, Error: errPtr}
}

func (e *imeuExtensionUIContext) SetTheme(th *theme.Theme) core.SetThemeResult {
	if th == nil {
		msg := "theme is nil"
		return core.SetThemeResult{Success: false, Error: &msg}
	}
	m := e.mode
	if m != nil && m.themeController != nil {
		m.themeController.SetThemeInstance(th)
		m.imPostUI(func() {
			if ui := m.imCurrentUI(); ui != nil {
				ui.Invalidate()
			}
			m.imlcUpdateEditorBorderColor()
			m.imRequestRender()
		})
		return core.SetThemeResult{Success: true}
	}
	theme.SetThemeInstance(th)
	return core.SetThemeResult{Success: true}
}

func (e *imeuExtensionUIContext) GetToolsExpanded() bool {
	m := e.mode
	if m == nil {
		return false
	}
	m.mu.Lock()
	exp := m.toolExpanded
	m.mu.Unlock()
	return exp
}

func (e *imeuExtensionUIContext) SetToolsExpanded(expanded bool) {
	m := e.mode
	if m == nil {
		return
	}
	m.mu.Lock()
	m.toolExpanded = expanded
	m.mu.Unlock()

	m.imPostUI(func() {
		for _, child := range m.chatContainer.Children() {
			if exp, ok := imhIsExpandable(child); ok {
				exp.SetExpanded(expanded)
			}
		}
		m.imRequestRender()
	})
}

func (e *imeuExtensionUIContext) String() string {
	return fmt.Sprintf("ExtensionUIContext(mode=tui)")
}

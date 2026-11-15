// Ported from packages/coding-agent/src/modes/interactive/interactive-mode.ts (pi v1.5.2).

package interactive

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/components"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/tui"
)

// Ui returns the active tui.TUI instance, if initialized.
func (m *InteractiveMode) Ui() tui.TUI {
	return m.imCurrentUI()
}

// Session returns the currently bound core.AgentSession.
func (m *InteractiveMode) Session() *core.AgentSession {
	return m.imSession()
}

// Init initializes the theme controller, TUI, editor, footer, session bindings,
// key handlers, and initial chat transcript. Safe to call multiple times (runs once).
func (m *InteractiveMode) Init(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	m.initOnce.Do(func() {
		m.initErr = m.imcInit(ctx)
	})
	return m.initErr
}

func (m *InteractiveMode) imcInit(ctx context.Context) error {
	sm := m.imSettingsManager()
	session := m.imSession()
	if session == nil {
		return fmt.Errorf("interactive mode requires an initialized AgentSession")
	}
	if m.keybindings != nil && m.keybindings.KeybindingsManager != nil {
		tui.SetKeybindings(m.keybindings.KeybindingsManager)
	}

	var customThemes []*theme.Theme
	if rl := m.imResourceLoader(); rl != nil {
		customThemes = rl.GetThemes().Themes
	}

	if m.options.InitialThemeSetting != nil && sm != nil && strings.TrimSpace(*m.options.InitialThemeSetting) != "" {
		theme.SetTheme(*m.options.InitialThemeSetting, true)
	}

	m.themeController = core.NewInteractiveThemeController(core.InteractiveThemeControllerOptions{
		SettingsManager: sm,
		CustomThemes:    customThemes,
		OnThemeApplied: func() {
			m.imPostUI(func() {
				if ui := m.imCurrentUI(); ui != nil {
					ui.Invalidate()
				}
				m.imlcUpdateEditorBorderColor()
				m.imRequestRender()
			})
		},
		OnThemeError: func(errMsg string) {
			m.imPostUI(func() {
				m.ShowWarning(fmt.Sprintf("Theme error: %s", errMsg))
			})
		},
	})
	if _, err := m.themeController.ApplyFromSettings(); err != nil {
		m.imcQueueOrShowWarning(fmt.Sprintf("Failed to load theme: %s", err.Error()))
	}

	mode := m.imUiMode()
	ui := m.imcCreateUI(mode)
	m.mu.Lock()
	m.ui = ui
	m.uiMode = mode
	m.mu.Unlock()

	m.themeController.RebindTui(ui)

	m.imcCreateEditor()
	m.imcSyncCachedSessionNameFrom(session)
	m.footer = components.NewFooterComponent(session, m.footerDataProvider)
	m.footer.SetAutoCompactEnabled(session.AutoCompactionEnabled())
	m.footerProxy = imcNewFooterProxy(func(width int) {
		m.imPostUI(func() {
			m.imlcRefreshFooterForWidth(width)
		})
	})

	m.imcConfigureUiCallbacks(ui)
	m.imcMountLayout(ui, mode)

	if sm != nil {
		ui.SetClearOnShrink(sm.GetClearOnShrink())
	}

	// Render welcome header and changelog notices
	m.imcRenderHeaderAndChangelog()
	m.mu.Lock()
	m.headerRendered = true
	m.mu.Unlock()

	// Setup autocomplete, key handlers, and editor submit handler
	m.iminSetupAutocomplete()
	m.iminSetupKeyHandlers()
	m.iminSetupEditorSubmitHandler()

	// Update terminal title and bind extension UI / session listeners
	m.imcUpdateTerminalTitle()
	m.imsbBindCurrentSessionExtensions(ctx)
	m.imevSubscribeToAgent()

	// Render initial messages from loaded session transcript
	m.imtrRenderInitialMessages()

	// Drain any startup warnings/notices collected before header rendered
	m.imcFlushQueuedStartupNotices()

	// Watch theme file changes and git branch updates cleanly on the UI thread
	theme.OnThemeChange(func() {
		if m.imIsStopped() {
			return
		}
		m.imPostUI(func() {
			if cur := m.imCurrentUI(); cur != nil {
				cur.Invalidate()
			}
			m.imlcUpdateEditorBorderColor()
			m.imlcRefreshFooterSync(false)
			m.imRequestRender()
		})
	})

	if m.footerDataProvider != nil {
		unsubBranch := m.footerDataProvider.OnBranchChange(func() {
			if m.imIsStopped() {
				return
			}
			m.imPostUI(func() {
				m.imlcRefreshFooterSync(false)
				m.imRequestRender()
			})
		})
		m.mu.Lock()
		m.unsubscribeFooterBranch = unsubBranch
		m.mu.Unlock()
	}

	// Render initial footer snapshot and focus editor before starting UI render timers.
	m.imlcRefreshFooterSync(true)
	ui.SetFocus(m.editor)

	// Start UI
	ui.Start()
	m.mu.Lock()
	m.started = true
	m.mu.Unlock()

	// Wait briefly for startup terminal palette response (timeout is bounded inside controller)
	_ = m.themeController.WaitForTerminalColors(ctx)

	m.imCallUI(nil, func() {
		m.imcUpdateAvailableProviderCount()
		m.imcReportProgramStatus()
		force := false
		ui.RenderNow(&force)
	})

	m.mu.Lock()
	m.isInitialized = true
	m.mu.Unlock()
	return nil
}

func (m *InteractiveMode) imcCreateTerminal() tui.Terminal {
	if m.options.Terminal != nil {
		return m.options.Terminal
	}
	return tui.NewProcessTerminal()
}

func (m *InteractiveMode) imcShowHardwareCursor() bool {
	if sm := m.imSettingsManager(); sm != nil {
		return sm.GetShowHardwareCursor()
	}
	return false
}

func (m *InteractiveMode) imcCreateUI(mode core.TuiMode) tui.TUI {
	term := m.imcCreateTerminal()
	showHwCursor := m.imcShowHardwareCursor()
	sm := m.imSettingsManager()

	if mode == tui.TuiModeFullscreen {
		var wheel *tui.WheelScrollLines
		copyOnSelect := true
		if sm != nil {
			w := sm.GetFullscreenWheelScrollLines()
			wheel = &w
			copyOnSelect = sm.GetFullscreenCopyOnSelect()
		}
		mouse := true
		altOpts := &tui.TuiAltScreenOptions{
			WheelScrollLines: wheel,
			Mouse:            &mouse,
			OpenUrl: func(url string) {
				if strings.TrimSpace(url) != "" {
					utils.OpenBrowser(url)
				}
			},
			OnRightClickPaste: func() {
				if runtime.GOOS == "windows" {
					m.iminPasteFromClipboard()
				}
			},
			CopyOnSelect: &copyOnSelect,
			CopySelection: func(text string) (any, error) {
				if text == "" {
					return false, nil
				}
				if err := utils.CopyToClipboard(text); err != nil {
					return err.Error(), nil
				}
				return true, nil
			},
			SearchMatchStyle: func(text string) string {
				return theme.CurrentTheme().Bg(theme.BgSelected, text)
			},
			SearchCurrentMatchStyle: func(text string) string {
				return theme.CurrentTheme().Bg(theme.BgUserMessage, theme.CurrentTheme().Fg(theme.FgAccent, text))
			},
			SearchNavigationButtonStyle: func(text string, hovered bool) string {
				if hovered {
					return theme.CurrentTheme().Inverse(theme.CurrentTheme().Fg(theme.FgAccent, text))
				}
				return theme.CurrentTheme().Fg(theme.FgMuted, text)
			},
			ScrollToEndIndicator: func() string {
				return theme.CurrentTheme().Bg(theme.BgCustomMessage, theme.CurrentTheme().Fg(theme.FgAccent, " ↓ Jump to bottom "))
			},
		}
		ui := tui.NewTuiAltScreen(term, &showHwCursor, nil, altOpts)
		if sm != nil {
			ui.SetClearOnShrink(sm.GetClearOnShrink())
		}
		return ui
	}

	ui := tui.NewTuiMainScreen(term, &showHwCursor, nil)
	if sm != nil {
		ui.SetClearOnShrink(sm.GetClearOnShrink())
	}
	return ui
}

func (m *InteractiveMode) imcConfigureUiCallbacks(ui tui.TUI) {
	if ui == nil {
		return
	}
	ui.SetOnDebug(func() {
		m.imPostUI(func() {
			m.imcmdHandleDebugCommand()
		})
	})
	m.imcAttachInputListener(ui)
}

func (m *InteractiveMode) imcAttachInputListener(ui tui.TUI) {
	if ui == nil {
		return
	}
	m.mu.Lock()
	oldUnsub := m.unsubscribeUIInput
	m.unsubscribeUIInput = nil
	m.mu.Unlock()
	if oldUnsub != nil {
		oldUnsub()
	}

	unsub := ui.AddInputListener(func(data string) *tui.TuiInputListenerResult {
		m.mu.Lock()
		listeners := append([]imKeyInputListenerEntry(nil), m.terminalInputListeners...)
		m.mu.Unlock()

		current := data
		modified := false
		for _, entry := range listeners {
			if entry.Fn == nil {
				continue
			}
			res := entry.Fn(current)
			if res == nil {
				continue
			}
			if res.Consume {
				return &tui.TuiInputListenerResult{Consume: imhBoolPtr(true)}
			}
			if res.Data != nil {
				current = *res.Data
				modified = true
			}
		}
		if modified {
			return &tui.TuiInputListenerResult{Data: imhStrPtr(current)}
		}
		return nil
	})

	m.mu.Lock()
	m.unsubscribeUIInput = unsub
	m.mu.Unlock()
}

func (m *InteractiveMode) imcCreateEditor() {
	ui := m.imCurrentUI()
	sm := m.imSettingsManager()
	paddingX := 1
	maxVis := 10
	if sm != nil {
		paddingX = sm.GetEditorPaddingX()
		maxVis = sm.GetAutocompleteMaxVisible()
	}
	embedWorkingStatus := m.imUiMode() == tui.TuiModeFullscreen
	m.defaultEditor = components.NewCustomEditor(
		ui,
		theme.GetEditorTheme(),
		m.keybindings,
		&components.CustomEditorOptions{
			PaddingX:               &paddingX,
			AutocompleteMaxVisible: &maxVis,
			EmbedWorkingStatus:     &embedWorkingStatus,
		},
	)
	m.editor = m.defaultEditor
	m.editorContainer.Clear()
	m.editorContainer.AddChild(m.editor)
	m.imlcUpdateEditorBorderColor()
}

func (m *InteractiveMode) imcMountLayout(ui tui.TUI, mode core.TuiMode) {
	if ui == nil {
		return
	}
	if mode == tui.TuiModeFullscreen && tui.IsViewportTui(ui) {
		doc := tui.NewContainer()
		doc.AddChild(m.headerContainer)
		doc.AddChild(m.chatContainer)

		var scrollbar *tui.ScrollViewScrollbar
		if sm := m.imSettingsManager(); sm != nil {
			sb := sm.GetFullscreenScrollbar()
			scrollbar = &sb
		}

		var footerComp tui.Component = m.footer
		if m.footerProxy != nil {
			footerComp = m.footerProxy
		}
		if m.customFooter != nil {
			footerComp = m.customFooter
		}
		m.chatViewport = CreateChatViewport(ChatViewportOptions{
			Document:        doc,
			PendingMessages: m.pendingMessagesContainer,
			Status:          m.statusContainer,
			WidgetsAbove:    m.aboveEditorWidgetContainer,
			Editor:          m.editorContainer,
			WidgetsBelow:    m.belowEditorWidgetContainer,
			Footer:          footerComp,
			Scrollbar:       scrollbar,
			ScrollbarTrackStyle: func(text string) string {
				return theme.CurrentTheme().Fg(theme.FgDim, text)
			},
			ScrollbarThumbStyle: func(text string) string {
				return theme.CurrentTheme().Fg(theme.FgAccent, text)
			},
		})
		ui.SetChildren([]tui.Component{m.chatViewport.Root})
		if vtui, ok := ui.(tui.ViewportTUI); ok {
			vtui.SetLayoutRoot(m.chatViewport.Root)
		}
		return
	}

	m.chatViewport = nil
	children := []tui.Component{
		m.headerContainer,
		m.chatContainer,
		m.pendingMessagesContainer,
		m.statusContainer,
		m.aboveEditorWidgetContainer,
		m.editorContainer,
		m.belowEditorWidgetContainer,
	}
	if m.customFooter != nil {
		children = append(children, m.customFooter)
	} else if m.footerProxy != nil {
		children = append(children, m.footerProxy)
	} else if m.footer != nil {
		children = append(children, m.footer)
	}
	ui.SetChildren(children)
}

// SwitchTuiMode switches between regular scrollback mode and fullscreen alt-screen mode.
func (m *InteractiveMode) SwitchTuiMode(mode core.TuiMode) {
	m.mu.Lock()
	currentMode := m.uiMode
	oldUI := m.ui
	m.mu.Unlock()
	if currentMode == mode && oldUI != nil {
		return
	}

	if oldUI != nil {
		oldUI.HideOverlay()
		preserve := mode == tui.TuiModeRegular
		if preserve {
			if sm := m.imSettingsManager(); sm != nil && sm.GetFullscreenExitOutput() == core.FullscreenExitOutputResumeHint {
				preserve = false
			}
		}
		oldUI.Stop(&tui.TuiStopOptions{PreserveScreen: &preserve})
	}

	newUI := m.imcCreateUI(mode)

	// Re-create defaultEditor preserving current editor text cleanly when switching embedded indicator setting.
	prevText := ""
	if m.editor != nil {
		prevText = m.editor.GetText()
	}

	m.mu.Lock()
	m.ui = newUI
	m.uiMode = mode
	customFactory := m.customEditorFactory
	m.mu.Unlock()

	if m.themeController != nil {
		m.themeController.RebindTui(newUI)
	}

	m.imcCreateEditor()
	if customFactory != nil {
		if customEd := customFactory(newUI, theme.GetEditorTheme(), m.keybindings.KeybindingsManager); customEd != nil {
			m.editor = customEd
			m.editorContainer.Clear()
			m.editorContainer.AddChild(m.editor)
		}
	}
	if prevText != "" && m.editor != nil {
		m.editor.SetText(prevText)
	}

	m.iminSetupAutocomplete()
	m.iminSetupKeyHandlers()
	m.iminSetupEditorSubmitHandler()

	// Rebind active working indicator to the new UI / editor
	m.mu.Lock()
	loading := m.loadingAnimation
	m.mu.Unlock()
	if loading != nil && m.defaultEditor != nil && mode == tui.TuiModeFullscreen {
		m.defaultEditor.SetWorkingStatusIndicator(loading)
	} else if m.defaultEditor != nil {
		m.defaultEditor.SetWorkingStatusIndicator(nil)
	}

	m.imcConfigureUiCallbacks(newUI)
	m.imcMountLayout(newUI, mode)
	newUI.Start()
	newUI.SetFocus(m.editor)
	m.imRequestRender(true)
}

func (m *InteractiveMode) imcRenderHeaderAndChangelog() {
	m.headerContainer.Clear()
	if m.customHeader != nil {
		m.headerContainer.AddChild(m.customHeader)
		return
	}

	sm := m.imSettingsManager()
	var silent, hideHeader bool
	if sm != nil {
		qs := sm.GetQuietStartup()
		silent = qs.Silent
		hideHeader = qs.Header
	}
	if silent {
		// Record changelog version quietly if needed and show no startup chrome.
		if sm != nil {
			last := sm.GetLastChangelogVersion()
			if last == nil || *last != utils.VERSION {
				sm.SetLastChangelogVersion(utils.VERSION)
			}
		}
		return
	}

	if !hideHeader {
		logoText := m.imcBuildWelcomeHeaderText()
		m.builtInHeader = components.NewThemedText(func() string {
			return logoText()
		}, 1, 1)
		m.headerContainer.AddChild(m.builtInHeader)
	}

	// Show changelog entries when version upgraded (unless collapsed/silent).
	if sm != nil {
		lastVer := imhDerefStr(sm.GetLastChangelogVersion())
		collapseChangelog := sm.GetCollapseChangelog()
		changelogPath := utils.GetChangelogPath()
		if changelogPath != "" {
			if entries, err := utils.ParseChangelog(changelogPath); err == nil && len(entries) > 0 {
				newEntries, _ := utils.GetNewEntries(entries, lastVer)
				if len(newEntries) > 0 && lastVer != utils.VERSION {
					if collapseChangelog {
						hint := fmt.Sprintf("Updated to v%s. Use /changelog to view release notes.", utils.VERSION)
						m.headerContainer.AddChild(components.NewThemedText(func() string {
							return theme.CurrentTheme().Fg(theme.FgMuted, hint)
						}, 1, 0))
					} else {
						formatted := m.imcFormatChangelogEntries(newEntries)
						if formatted != "" {
							m.headerContainer.AddChild(components.NewDynamicBorder())
							m.headerContainer.AddChild(tui.NewMarkdown(formatted, 1, 1, theme.GetMarkdownTheme(), nil, nil))
							m.headerContainer.AddChild(components.NewDynamicBorder())
						}
					}
				}
			}
		}
		if lastVer != utils.VERSION {
			sm.SetLastChangelogVersion(utils.VERSION)
		}
	}
}

func (m *InteractiveMode) imcBuildWelcomeHeaderText() func() string {
	return func() string {
		th := theme.CurrentTheme()
		title := fmt.Sprintf("%s v%s", th.Bold(th.Fg(theme.FgAccent, utils.AppName)), utils.VERSION)
		hints := []string{
			theme.AppKeyHint(th, core.KeybindingAppInterrupt, "to interrupt"),
			theme.AppKeyHint(th, core.KeybindingAppClear, "to clear"),
			theme.RawKeyHint(th, imhAppKeyDisplay(m.keybindings, core.KeybindingAppClear)+" twice", "to exit"),
			theme.AppKeyHint(th, core.KeybindingAppModelSelect, "for models"),
			theme.AppKeyHint(th, core.KeybindingAppThinkingCycle, "to cycle thinking"),
			theme.AppKeyHint(th, core.KeybindingAppClipboardPasteImage, "to paste files, images, or text"),
			theme.RawKeyHint(th, "/", "for commands"),
			theme.RawKeyHint(th, "!", "to run bash"),
		}
		var b strings.Builder
		if components.SupportsPiLogo() {
			for _, line := range components.PiLogoLines() {
				b.WriteString(line)
				b.WriteByte('\n')
			}
		} else {
			b.WriteString(components.PiWordmark())
			b.WriteByte(' ')
		}
		b.WriteString(title)
		b.WriteString("\n\n")
		for i, h := range hints {
			if i > 0 {
				b.WriteByte('\n')
			}
			b.WriteString("  ")
			b.WriteString(h)
		}
		return b.String()
	}
}

func (m *InteractiveMode) imcFormatChangelogEntries(entries []utils.ChangelogEntry) string {
	var sections []string
	for _, entry := range entries {
		header := fmt.Sprintf("## v%d.%d.%d", entry.Major, entry.Minor, entry.Patch)
		content := utils.NormalizeChangelogLinksForEntry(strings.TrimSpace(entry.Content), entry)
		if content != "" {
			sections = append(sections, header+"\n\n"+content)
		} else {
			sections = append(sections, header)
		}
	}
	return strings.Join(sections, "\n\n")
}

// Run starts interactive mode, runs startup checks, processes initial input, and waits until exit.
func (m *InteractiveMode) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()

	watchDone := make(chan struct{})
	defer close(watchDone)
	go func() {
		select {
		case <-m.stopCh:
			runCancel()
		case <-runCtx.Done():
		case <-watchDone:
		}
	}()

	if err := m.Init(runCtx); err != nil {
		m.Stop()
		return err
	}

	sm := m.imSettingsManager()

	// Refresh model catalogs asynchronously when not offline
	if !core.IsOfflineModeEnabled() {
		if mr := m.imModelRuntime(); mr != nil {
			m.imSpawn(func() {
				refreshCtx, cancel := context.WithTimeout(m.bgCtx, 15*time.Second)
				defer cancel()
				_, _ = components.RefreshModelCatalogs(refreshCtx, mr)
				if m.imIsStopped() {
					return
				}
				m.imPostUI(func() {
					m.imcUpdateAvailableProviderCount()
				})
			})
		}
	}

	// Check for new Pi release asynchronously
	if !m.options.DisableVersionCheck {
		m.imSpawn(func() {
			verCtx, cancel := context.WithTimeout(m.bgCtx, 5*time.Second)
			defer cancel()
			rel := utils.CheckForNewPiVersion(verCtx, utils.VERSION)
			if rel == nil || m.imIsStopped() {
				return
			}
			m.imPostUI(func() {
				m.imcShowNewVersionNotification(rel)
			})
		})
	}

	// Check tmux keyboard setup asynchronously
	if !m.options.DisableTmuxCheck {
		m.imSpawn(func() {
			warning := m.imcCheckTmuxKeyboardSetup(m.bgCtx)
			if warning == "" || m.imIsStopped() {
				return
			}
			m.imPostUI(func() {
				m.ShowWarning(warning)
			})
		})
	}

	// Surface startup warnings/notices (crash recovery, install changes, diagnostics, fallback)
	m.imPostUI(func() {
		m.imcSurfaceStartupWarnings(sm)
	})

	// Process initial prompt / messages from CLI flags sequentially unless shutdown already requested.
	select {
	case <-m.stopCh:
	default:
		if err := m.imcProcessInitialInputs(runCtx); err != nil {
			select {
			case <-m.stopCh:
			default:
				m.imPostUI(func() {
					m.ShowError(imhErrorMessage(err))
				})
			}
		}
	}

	// Wait until context cancellation or interactive shutdown request.
	select {
	case <-ctx.Done():
	case <-m.stopCh:
	}

	interactiveQuit := false
	select {
	case <-m.stopCh:
		interactiveQuit = ctx.Err() == nil
	default:
	}

	m.Stop()

	if interactiveQuit {
		out := m.options.Stdout
		if out == nil && m.options.Terminal == nil {
			out = os.Stdout
		}
		if out != nil {
			if cmd := FormatResumeCommand(m.imSessionManager()); cmd != nil && *cmd != "" {
				_, _ = fmt.Fprintf(out, "%s %s\n", theme.CurrentTheme().Fg(theme.FgDim, "To resume this session:"), *cmd)
			}
		}
	}
	return nil
}

func (m *InteractiveMode) imcProcessInitialInputs(ctx context.Context) error {
	session := m.imSession()
	if session == nil {
		return nil
	}

	if m.options.InitialMessage != nil {
		msg := *m.options.InitialMessage
		var imgs []*ai.ImageContent
		for i := range m.options.InitialImages {
			img := m.options.InitialImages[i]
			imgs = append(imgs, &img)
		}
		if err := m.imcSubmitInitialInput(ctx, msg, imgs); err != nil {
			return err
		}
	}

	for _, msg := range m.options.InitialMessages {
		if m.imIsStopped() || ctx.Err() != nil {
			return ctx.Err()
		}
		if err := m.imcSubmitInitialInput(ctx, msg, nil); err != nil {
			return err
		}
	}
	return nil
}

func (m *InteractiveMode) imcSubmitInitialInput(ctx context.Context, text string, images []*ai.ImageContent) error {
	session := m.imSession()
	if session == nil {
		return nil
	}
	handled, err := m.imcmdHandleSlashCommand(ctx, text)
	if err != nil {
		return err
	}
	if handled {
		return nil
	}
	// Check prompt options with images
	var opts *core.PromptOptions
	if len(images) > 0 {
		opts = &core.PromptOptions{Images: images}
	}
	m.imcBeginSessionMutation()
	defer m.imcEndSessionMutation()
	if opts != nil {
		return session.Prompt(ctx, text, opts)
	}
	return session.Prompt(ctx, text)
}

func (m *InteractiveMode) imcSurfaceStartupWarnings(sm *core.SettingsManager) {
	// 1. Model fallback notice
	if m.runtime != nil && m.runtime.ModelFallbackMessage != nil && *m.runtime.ModelFallbackMessage != "" {
		m.ShowWarning(*m.runtime.ModelFallbackMessage)
	} else if m.options.ModelFallbackMessage != nil && *m.options.ModelFallbackMessage != "" {
		m.ShowWarning(*m.options.ModelFallbackMessage)
	}

	// 2. Migrated providers notice
	if len(m.options.MigratedProviders) > 0 {
		m.ShowStatus(fmt.Sprintf("Migrated credentials to %s for: %s", utils.GetAuthPath(), strings.Join(m.options.MigratedProviders, ", ")))
	}

	// 3. Startup diagnostics
	for _, diag := range m.options.StartupDiagnostics {
		switch diag.Type {
		case "error":
			m.ShowError(diag.Message)
		case "warning":
			m.ShowWarning(diag.Message)
		default:
			if diag.Message != "" {
				m.ShowStatus(diag.Message)
			}
		}
	}

	// 4. Settings drain errors
	if sm != nil {
		for _, sErr := range sm.DrainErrors() {
			m.ShowWarning(fmt.Sprintf("Settings (%s): %s", sErr.Scope, sErr.Error))
		}
	}

	// 5. Auto-trust reload confirmation hint
	if m.options.AutoTrustOnReloadCwd != nil && *m.options.AutoTrustOnReloadCwd != "" {
		m.ShowStatus(fmt.Sprintf("Trusted project: %s", imhNormalizePathForDisplay(*m.options.AutoTrustOnReloadCwd)))
	}

	// 6. Unnotified crash record from previous session
	m.imcShowUnnotifiedCrashWarning()

	// 7. Install method change check
	m.imcCheckInstallChangeWarning()

	// 8. Resource / extension / skill / prompt diagnostics
	m.imrShowStartupResourceDiagnostics()
}

func (m *InteractiveMode) imcShowUnnotifiedCrashWarning() {
	agentDir := m.imAgentDir()
	var crash *core.CrashRecord
	if agentDir != "" {
		crash = core.TakeUnnotifiedCrashAt(time.Now().UnixMilli(), core.CrashLogPath(agentDir))
	} else {
		crash = core.TakeUnnotifiedCrashAt(time.Now().UnixMilli())
	}
	if crash == nil {
		return
	}
	msg := fmt.Sprintf("Previous session exited unexpectedly (%s): %s", crash.Kind, crash.Message)
	if runner := m.imExtensionRunner(); runner != nil {
		var loadedExts []core.Extension
		if rl := m.imResourceLoader(); rl != nil {
			loadedExts = rl.GetExtensions().Extensions
		}
		matches := core.FindExtensionStackMatches(crash.Stack, loadedExts)
		if hint := FormatCrashExtensionHint(matches); hint != nil {
			msg += ". " + *hint
		}
	}
	msg += ". Run /bug to report this crash."
	m.ShowWarning(msg)
}

func (m *InteractiveMode) imcCheckInstallChangeWarning() {
	m.mu.Lock()
	if m.installChangeWarningShown {
		m.mu.Unlock()
		return
	}
	m.installChangeWarningShown = true
	m.mu.Unlock()

	pkgPath := utils.GetPackageJsonPath()
	change := utils.DetectInstallChange(pkgPath)
	if change == nil {
		return
	}
	cause := fmt.Sprintf("The %s installation this session runs from was removed or replaced", utils.AppName)
	if change.Kind == "updated" {
		cause = fmt.Sprintf("%s was updated to %s while this session was running (%s)", utils.AppName, change.Version, utils.VERSION)
	}
	var sm *core.SessionManager
	if s := m.imSession(); s != nil {
		sm = s.SessionManager
	}
	restart := fmt.Sprintf("Restart %s.", utils.AppName)
	if cmd := FormatResumeCommand(sm); cmd != nil && *cmd != "" {
		restart = fmt.Sprintf("Restart with `%s` to continue this session.", *cmd)
	}
	m.ShowWarning(fmt.Sprintf("%s. Features that load code on demand can fail until restart. %s", cause, restart))
}

func (m *InteractiveMode) imcCheckTmuxKeyboardSetup(ctx context.Context) string {
	if os.Getenv("TMUX") == "" {
		return ""
	}
	query := func(option string) (string, bool) {
		qCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
		defer cancel()
		cmd := exec.CommandContext(qCtx, "tmux", "show", "-gv", option)
		out, err := cmd.Output()
		if err != nil {
			return "", false
		}
		return strings.TrimSpace(string(out)), true
	}

	extKeys, ok := query("extended-keys")
	if !ok {
		return ""
	}
	if extKeys == "off" {
		return "tmux extended-keys is off. Modified Enter keys may not work. Add `set -g extended-keys on` to ~/.tmux.conf and restart tmux."
	}
	extFmt, ok := query("extended-keys-format")
	if ok && extFmt == "xterm" {
		return "tmux extended-keys-format is xterm. Pi works best with csi-u. Add `set -g extended-keys-format csi-u` to ~/.tmux.conf and restart tmux."
	}
	return ""
}

func (m *InteractiveMode) imcShowNewVersionNotification(release *utils.PiReleaseInfo) {
	if release == nil {
		return
	}
	m.mu.Lock()
	if m.startupVersionNoticeHandled {
		m.mu.Unlock()
		return
	}
	m.startupVersionNoticeHandled = true
	m.mu.Unlock()

	changelogURL := "https://pi.dev/changelog"
	m.chatContainer.AddChild(tui.NewSpacer(1))
	m.chatContainer.AddChild(components.NewDynamicBorder())
	m.chatContainer.AddChild(components.NewThemedText(func() string {
		th := theme.CurrentTheme()
		line1 := th.Fg(theme.FgMuted, fmt.Sprintf("New version %s is available. Run ", release.Version)) +
			th.Fg(theme.FgAccent, fmt.Sprintf("%s update", utils.AppName))
		linkText := th.Fg(theme.FgAccent, changelogURL)
		if tui.GetCapabilities().Hyperlinks {
			linkText = tui.Hyperlink(linkText, changelogURL)
		}
		line2 := th.Fg(theme.FgDim, "Changelog: ") + linkText
		return line1 + "\n" + line2
	}, 1, 1))
	m.chatContainer.AddChild(components.NewDynamicBorder())
	m.imRequestRender()
}

func (m *InteractiveMode) imcUpdateAvailableProviderCount() {
	if m.footerDataProvider == nil {
		return
	}
	mr := m.imModelRuntime()
	if mr == nil {
		return
	}
	models := mr.GetAvailableSnapshot()
	providers := make(map[string]struct{})
	for _, mdl := range models {
		if mdl != nil && mdl.Provider != "" {
			providers[string(mdl.Provider)] = struct{}{}
		}
	}
	m.footerDataProvider.SetAvailableProviderCount(len(providers))
	m.imRequestRender()
}

func (m *InteractiveMode) imcUpdateTerminalTitle() {
	ui := m.imCurrentUI()
	if ui == nil {
		return
	}
	term := ui.Terminal()
	if term == nil {
		return
	}
	cwd := m.imCwd()
	base := filepath.Base(cwd)
	if base == "" || base == "." || base == "/" {
		base = cwd
	}
	title := utils.AppTitle
	if name := m.imcCachedSessionName(); name != nil && strings.TrimSpace(*name) != "" {
		title = fmt.Sprintf("%s - %s (%s)", utils.AppTitle, strings.TrimSpace(*name), base)
		term.SetTitle(title)
		return
	}
	if base != "" {
		title = fmt.Sprintf("%s - %s", utils.AppTitle, base)
	}
	term.SetTitle(title)
}

func (m *InteractiveMode) imcReportProgramStatus() {
	if m.programStatusReporter != nil {
		m.programStatusReporter.Report()
	}
}

func (m *InteractiveMode) imcQueueOrShowWarning(msg string) {
	m.mu.Lock()
	ready := m.headerRendered
	if !ready {
		m.queuedStartupNotices = append(m.queuedStartupNotices, imStartupNotice{Kind: "warning", Message: msg})
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()
	m.ShowWarning(msg)
}

func (m *InteractiveMode) imcFlushQueuedStartupNotices() {
	m.mu.Lock()
	notices := m.queuedStartupNotices
	m.queuedStartupNotices = nil
	m.mu.Unlock()
	for _, n := range notices {
		switch n.Kind {
		case "error":
			m.ShowError(n.Message)
		case "warning":
			m.ShowWarning(n.Message)
		default:
			m.ShowStatus(n.Message)
		}
	}
}

// ShowError renders an error message into the chat transcript.
func (m *InteractiveMode) ShowError(errorMessage string) {
	m.imPostUI(func() {
		m.mu.Lock()
		if m.stopped {
			m.mu.Unlock()
			return
		}
		ready := m.headerRendered
		if !ready {
			m.queuedStartupNotices = append(m.queuedStartupNotices, imStartupNotice{Kind: "error", Message: errorMessage})
			m.mu.Unlock()
			return
		}
		m.mu.Unlock()

		m.chatContainer.AddChild(tui.NewSpacer(1))
		m.chatContainer.AddChild(components.NewThemedText(func() string {
			return theme.CurrentTheme().Fg(theme.FgError, "Error: "+errorMessage)
		}, 1, 0))
		m.imRequestRender()
	})
}

// ShowWarning renders a warning message into the chat transcript.
func (m *InteractiveMode) ShowWarning(warningMessage string) {
	m.imPostUI(func() {
		m.mu.Lock()
		if m.stopped {
			m.mu.Unlock()
			return
		}
		ready := m.headerRendered
		if !ready {
			m.queuedStartupNotices = append(m.queuedStartupNotices, imStartupNotice{Kind: "warning", Message: warningMessage})
			m.mu.Unlock()
			return
		}
		m.mu.Unlock()

		m.chatContainer.AddChild(tui.NewSpacer(1))
		m.chatContainer.AddChild(components.NewThemedText(func() string {
			return theme.CurrentTheme().Fg(theme.FgWarning, "Warning: "+warningMessage)
		}, 1, 0))
		m.imRequestRender()
	})
}

// ShowStatus renders a muted status line into the chat transcript.
func (m *InteractiveMode) ShowStatus(statusMessage string) {
	m.imPostUI(func() {
		m.mu.Lock()
		if m.stopped {
			m.mu.Unlock()
			return
		}
		ready := m.headerRendered
		if !ready {
			m.queuedStartupNotices = append(m.queuedStartupNotices, imStartupNotice{Kind: "status", Message: statusMessage})
			m.mu.Unlock()
			return
		}
		m.mu.Unlock()

		m.chatContainer.AddChild(tui.NewSpacer(1))
		m.chatContainer.AddChild(components.NewThemedText(func() string {
			return theme.CurrentTheme().Fg(theme.FgMuted, statusMessage)
		}, 1, 0))
		m.imRequestRender()
	})
}

// imcRequestShutdown closes stopCh so Run exits cleanly.
func (m *InteractiveMode) imcRequestShutdown() {
	m.mu.Lock()
	if !m.stopped {
		select {
		case <-m.stopCh:
		default:
			close(m.stopCh)
		}
	}
	m.mu.Unlock()
}

// Stop tears down subscriptions, background checks, theme watchers, footer provider, and the TUI.
func (m *InteractiveMode) Stop() {
	if m == nil {
		return
	}
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return
	}
	m.stopped = true
	unsubSession := m.unsubscribeSession
	m.unsubscribeSession = nil
	unsubBranch := m.unsubscribeFooterBranch
	m.unsubscribeFooterBranch = nil
	unsubInput := m.unsubscribeUIInput
	m.unsubscribeUIInput = nil
	unsubExtErr := m.extensionErrUnsub
	m.extensionErrUnsub = nil
	loading := m.loadingAnimation
	m.loadingAnimation = nil
	retryInd := m.retryIndicator
	m.retryIndicator = nil
	compInd := m.compactionIndicator
	m.compactionIndicator = nil
	if m.doubleEscapeConfirmTimer != nil {
		m.doubleEscapeConfirmTimer.Stop()
		m.doubleEscapeConfirmTimer = nil
	}
	if m.compactionCancel != nil {
		m.compactionCancel()
		m.compactionCancel = nil
	}
	if m.bashCancel != nil {
		m.bashCancel()
		m.bashCancel = nil
	}
	ui := m.ui
	started := m.started
	tc := m.themeController
	fdp := m.footerDataProvider
	ftr := m.footer
	bgCancel := m.bgCancel
	verCancel := m.versionCheckCancel
	select {
	case <-m.stopCh:
	default:
		close(m.stopCh)
	}
	m.mu.Unlock()

	if verCancel != nil {
		verCancel()
	}
	if bgCancel != nil {
		bgCancel()
	}
	if sm := m.imSettingsManager(); sm != nil {
		_ = sm.Flush()
	}
	if s := m.imSession(); s != nil && s.IsStreaming() {
		abortCtx, abortCancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
		_ = s.Abort(abortCtx)
		abortCancel()
	}
	m.imcWaitBackground(time.Second)

	if unsubSession != nil {
		unsubSession()
	}
	if unsubBranch != nil {
		unsubBranch()
	}
	if unsubInput != nil {
		unsubInput()
	}
	if unsubExtErr != nil {
		unsubExtErr()
	}
	if loading != nil {
		loading.Dispose()
	}
	if retryInd != nil {
		retryInd.Dispose()
	}
	if compInd != nil {
		compInd.Dispose()
	}
	if tc != nil {
		tc.Dispose()
	}
	if ftr != nil {
		ftr.Dispose()
	}
	if fdp != nil {
		fdp.Dispose()
	}
	theme.StopThemeWatcher()

	if m.programStatusReporter != nil {
		m.programStatusReporter.Reset()
	}

	if ui != nil && started {
		m.imcStopUI(ui, nil)
	}
}

type imcFooterProxy struct {
	mu           sync.Mutex
	cachedWidth  int
	pendingWidth int
	cachedLines  []string
	onNeedWidth  func(width int)
}

func imcNewFooterProxy(onNeedWidth func(width int)) *imcFooterProxy {
	return &imcFooterProxy{
		onNeedWidth: onNeedWidth,
	}
}

func (p *imcFooterProxy) Invalidate() {}

func (p *imcFooterProxy) SetLines(width int, lines []string) {
	if p == nil {
		return
	}
	cp := make([]string, len(lines))
	copy(cp, lines)
	p.mu.Lock()
	p.cachedWidth = width
	p.pendingWidth = width
	p.cachedLines = cp
	p.mu.Unlock()
}

func (p *imcFooterProxy) Render(width int) []string {
	if p == nil || width <= 0 {
		return []string{}
	}
	p.mu.Lock()
	cachedW := p.cachedWidth
	pendingW := p.pendingWidth
	needNotify := cachedW != width && pendingW != width
	if needNotify {
		p.pendingWidth = width
	}
	cached := make([]string, len(p.cachedLines))
	copy(cached, p.cachedLines)
	cb := p.onNeedWidth
	p.mu.Unlock()

	if needNotify && cb != nil {
		cb(width)
	}
	if len(cached) == 0 {
		return []string{}
	}
	if cachedW != width {
		for i, line := range cached {
			if tui.VisibleWidth(line) > width {
				cached[i] = tui.TruncateToWidth(line, width, "...", false)
			}
		}
	}
	return cached
}

func (m *InteractiveMode) imcCachedSessionName() *string {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	name := strings.TrimSpace(m.cachedSessionName)
	m.mu.Unlock()
	if name == "" {
		return nil
	}
	cp := name
	return &cp
}

func (m *InteractiveMode) imcSetCachedSessionName(name string) {
	if m == nil {
		return
	}
	trimmed := strings.TrimSpace(name)
	m.mu.Lock()
	m.cachedSessionName = trimmed
	m.mu.Unlock()
}

func (m *InteractiveMode) imcSyncCachedSessionNameFrom(session *core.AgentSession) {
	if m == nil {
		return
	}
	name := ""
	if session != nil {
		if ptr := session.SessionName(); ptr != nil {
			name = strings.TrimSpace(*ptr)
		}
	}
	m.imcSetCachedSessionName(name)
}

func (m *InteractiveMode) imcBeginSessionMutation() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.sessionWriteDepth++
	m.mu.Unlock()
	// Wait for any in-flight FooterComponent.Render call inside imlcRefreshFooterSync.
	m.footerRenderMu.Lock()
	m.footerRenderMu.Unlock()
}

func (m *InteractiveMode) imcEndSessionMutation() {
	if m == nil {
		return
	}
	m.mu.Lock()
	if m.sessionWriteDepth > 0 {
		m.sessionWriteDepth--
	}
	depth := m.sessionWriteDepth
	m.mu.Unlock()
	if depth == 0 {
		m.imlcRefreshFooter()
	}
}

func (m *InteractiveMode) imcIsFooterSafeToReadSession() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	depth := m.sessionWriteDepth
	session := m.imSession()
	m.mu.Unlock()
	if depth > 0 {
		return false
	}
	if session == nil {
		return false
	}
	return session.IsIdle() && !session.IsCompacting()
}

func (m *InteractiveMode) imcWaitBackground(timeout time.Duration) {
	if m == nil {
		return
	}
	done := make(chan struct{})
	go func() {
		m.bgWg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
	}
}

func (m *InteractiveMode) imcStopUI(ui tui.TUI, options *tui.TuiStopOptions) {
	if ui == nil {
		return
	}
	// Two sequential Post roundtrips drain both in-flight closures and any nested
	// imPostUI callbacks they scheduled before ui.Stop invokes afterTerminalStop.
	done1 := make(chan struct{})
	ui.Post(func() {
		ui.HideOverlay()
		ui.RenderNow(nil)
		close(done1)
	})
	select {
	case <-done1:
	case <-time.After(time.Second):
		ui.HideOverlay()
	}
	done2 := make(chan struct{})
	ui.Post(func() {
		close(done2)
	})
	select {
	case <-done2:
	case <-time.After(time.Second):
	}
	if term := ui.Terminal(); term != nil {
		_ = term.DrainInput(nil, nil)
	}
	ui.Stop(options)
}

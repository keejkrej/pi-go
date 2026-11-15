// Ported from packages/coding-agent/src/modes/interactive/interactive-mode.ts (pi v1.5.2).

package interactive

import (
	"context"
	"fmt"
	"strings"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/components"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/tui"
)

type imsbRebindOptions struct {
	ShowInitialMessages bool
	RestoreInputText    *string
}

func (m *InteractiveMode) imsbBindCurrentSessionExtensions(ctx context.Context) {
	session := m.imSession()
	if session == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}

	uiCtx := m.imeuCreateExtensionUIContext()
	cmdActions := m.imsbCreateCommandContextActions()
	bindings := core.ExtensionBindings{
		UIContext:             uiCtx,
		Mode:                  core.ExtensionModeTui,
		CommandContextActions: cmdActions,
		AbortHandler: func() {
			if s := m.imSession(); s != nil {
				_ = s.Abort(m.bgCtx)
			}
		},
		ShutdownHandler: func() {
			m.imcRequestShutdown()
		},
		OnError: func(err core.ExtensionError) {
			m.imPostUI(func() {
				m.imsbShowExtensionError(err)
			})
		},
	}

	if err := session.BindExtensions(ctx, bindings); err != nil {
		m.ShowError(fmt.Sprintf("Failed to bind session extensions: %s", imhErrorMessage(err)))
	}

	m.imsbSetupExtensionShortcuts()
	m.iminSetupAutocomplete()
}

func (m *InteractiveMode) imsbCreateCommandContextActions() *core.ExtensionCommandContextActions {
	return &core.ExtensionCommandContextActions{
		WaitForIdle: func(ctx context.Context) error {
			s := m.imSession()
			if s == nil {
				return nil
			}
			return s.WaitForIdle(ctx)
		},
		NewSession: func(ctx context.Context, options ...*core.ExtensionNewSessionOptions) (core.CancelledResult, error) {
			if m.runtime == nil {
				return core.CancelledResult{Cancelled: false}, nil
			}
			var rtOpts *core.NewSessionRuntimeOptions
			var withSession func(context.Context, core.ReplacedSessionContext) error
			if len(options) > 0 && options[0] != nil {
				opt := options[0]
				withSession = opt.WithSession
				if opt.ParentSession != nil || opt.Setup != nil {
					rtOpts = &core.NewSessionRuntimeOptions{
						ParentSession: opt.ParentSession,
					}
					if opt.Setup != nil {
						setupFn := opt.Setup
						rtOpts.Setup = func(sm *core.SessionManager) error {
							return setupFn(ctx, sm)
						}
					}
				}
			}
			m.imcBeginSessionMutation()
			res, err := m.runtime.NewSession(ctx, rtOpts)
			m.imcEndSessionMutation()
			if err != nil {
				return core.CancelledResult{Cancelled: false}, err
			}
			if res != nil && res.Cancelled {
				return core.CancelledResult{Cancelled: true}, nil
			}
			if withSession != nil {
				if s := m.imSession(); s != nil {
					_ = withSession(ctx, s.CreateReplacedSessionContext())
				}
			}
			m.imCallUI(ctx, func() {
				m.imsbRebindCurrentSession(ctx)
			})
			return core.CancelledResult{Cancelled: false}, nil
		},
		Fork: func(ctx context.Context, entryId string, options ...*core.ExtensionForkOptions) (core.CancelledResult, error) {
			if m.runtime == nil {
				return core.CancelledResult{Cancelled: false}, nil
			}
			var rtOpts *core.ForkSessionOptions
			var withSession func(context.Context, core.ReplacedSessionContext) error
			if len(options) > 0 && options[0] != nil {
				opt := options[0]
				withSession = opt.WithSession
				if opt.Position != "" {
					rtOpts = &core.ForkSessionOptions{Position: opt.Position}
				}
			}
			m.imcBeginSessionMutation()
			res, err := m.runtime.Fork(ctx, entryId, rtOpts)
			m.imcEndSessionMutation()
			if err != nil {
				return core.CancelledResult{Cancelled: false}, err
			}
			if res != nil && res.Cancelled {
				return core.CancelledResult{Cancelled: true}, nil
			}
			if withSession != nil {
				if s := m.imSession(); s != nil {
					_ = withSession(ctx, s.CreateReplacedSessionContext())
				}
			}
			var selectedText *string
			if res != nil {
				selectedText = res.SelectedText
			}
			m.imCallUI(ctx, func() {
				m.imsbRebindCurrentSession(ctx, &imsbRebindOptions{
					ShowInitialMessages: true,
					RestoreInputText:    selectedText,
				})
			})
			return core.CancelledResult{Cancelled: false}, nil
		},
		NavigateTree: func(ctx context.Context, targetId string, options ...*core.ExtensionNavigateTreeOptions) (core.CancelledResult, error) {
			s := m.imSession()
			if s == nil {
				return core.CancelledResult{Cancelled: false}, nil
			}
			var navOpts *core.NavigateTreeOptions
			if len(options) > 0 && options[0] != nil {
				o := options[0]
				navOpts = &core.NavigateTreeOptions{
					Summarize:           o.Summarize,
					CustomInstructions:  o.CustomInstructions,
					ReplaceInstructions: o.ReplaceInstructions,
					Label:               o.Label,
				}
			}
			m.imcBeginSessionMutation()
			res, err := s.NavigateTree(ctx, targetId, navOpts)
			m.imcEndSessionMutation()
			if err != nil {
				return core.CancelledResult{Cancelled: false}, err
			}
			if res == nil || res.Cancelled {
				return core.CancelledResult{Cancelled: true}, nil
			}
			m.imCallUI(ctx, func() {
				m.imtrRebuildTranscript()
				if res.EditorText != nil && m.editor != nil {
					m.editor.SetText(*res.EditorText)
				}
				m.imlcRefreshFooterSync(true)
				m.imRequestRender()
			})
			return core.CancelledResult{Cancelled: false}, nil
		},
		SwitchSession: func(ctx context.Context, sessionPath string, options ...*core.ExtensionSwitchSessionOptions) (core.CancelledResult, error) {
			if m.runtime == nil {
				return core.CancelledResult{Cancelled: false}, nil
			}
			var withSession func(context.Context, core.ReplacedSessionContext) error
			if len(options) > 0 && options[0] != nil {
				withSession = options[0].WithSession
			}
			m.imcBeginSessionMutation()
			res, err := m.runtime.SwitchSession(ctx, sessionPath)
			m.imcEndSessionMutation()
			if err != nil {
				return core.CancelledResult{Cancelled: false}, err
			}
			if res != nil && res.Cancelled {
				return core.CancelledResult{Cancelled: true}, nil
			}
			if withSession != nil {
				if s := m.imSession(); s != nil {
					_ = withSession(ctx, s.CreateReplacedSessionContext())
				}
			}
			m.imCallUI(ctx, func() {
				m.imsbRebindCurrentSession(ctx, &imsbRebindOptions{
					ShowInitialMessages: true,
				})
			})
			return core.CancelledResult{Cancelled: false}, nil
		},
		Reload: func(ctx context.Context) error {
			return m.imlcReloadRuntime(ctx)
		},
	}
}

func (m *InteractiveMode) imsbRebindCurrentSession(ctx context.Context, options ...*imsbRebindOptions) {
	if ctx == nil {
		ctx = context.Background()
	}
	var opts imsbRebindOptions
	if len(options) > 0 && options[0] != nil {
		opts = *options[0]
	}

	// Detach previous session subscription
	m.mu.Lock()
	unsub := m.unsubscribeSession
	m.unsubscribeSession = nil
	unsubExtErr := m.extensionErrUnsub
	m.extensionErrUnsub = nil
	psr := m.programStatusReporter
	m.streamingComponent = nil
	m.streamingMessage = nil
	m.pendingTools = make(map[string]*components.ToolExecutionComponent)
	m.pendingToolsOrder = nil
	m.queuedSteering = nil
	m.queuedFollowUps = nil
	m.compactionQueuedMessages = nil
	m.hasShownAnthropicWarning = false
	m.mu.Unlock()

	if unsub != nil {
		unsub()
	}
	if unsubExtErr != nil {
		unsubExtErr()
	}
	if psr != nil {
		psr.Reset()
	}

	// Clear chat, pending messages, status indicators, and extension UI state
	m.imlcStopAllStatusIndicators()
	m.imeuResetExtensionUI()
	m.chatContainer.Clear()
	m.pendingMessagesContainer.Clear()
	m.statusContainer.Clear()

	session := m.imSession()
	m.imcSyncCachedSessionNameFrom(session)
	if m.footerDataProvider != nil {
		m.footerDataProvider.SetCwd(m.imCwd())
	}
	if m.footer != nil {
		m.footer.SetSession(session)
		if session != nil {
			m.footer.SetAutoCompactEnabled(session.AutoCompactionEnabled())
		}
	}
	m.imlcRefreshFooterSync(true)

	m.imsbBindCurrentSessionExtensions(ctx)
	m.imevSubscribeToAgent()

	if opts.ShowInitialMessages {
		m.imtrRenderInitialMessages()
	} else if session != nil {
		m.imtrRenderInitialMessages()
	}

	if opts.RestoreInputText != nil && m.editor != nil {
		m.editor.SetText(*opts.RestoreInputText)
	}

	m.imcUpdateTerminalTitle()
	m.imlcUpdateEditorBorderColor()
	m.imcUpdateAvailableProviderCount()
	m.imcReportProgramStatus()
	m.imRequestRender(true)
}

func (m *InteractiveMode) imsbApplyRuntimeSettings() {
	sm := m.imSettingsManager()
	session := m.imSession()
	ui := m.imCurrentUI()

	if sm != nil {
		hideThinking := sm.GetHideThinkingBlock()
		outputPad := sm.GetOutputPad()
		showImages := sm.GetShowImages()
		imgWidth := sm.GetImageWidthCells()
		clearOnShrink := sm.GetClearOnShrink()

		m.mu.Lock()
		m.hideThinkingBlock = hideThinking
		m.mu.Unlock()

		if ui != nil {
			ui.SetClearOnShrink(clearOnShrink)
			ui.SetShowHardwareCursor(sm.GetShowHardwareCursor())
		}
		if m.defaultEditor != nil {
			m.defaultEditor.SetPaddingX(sm.GetEditorPaddingX())
			m.defaultEditor.SetAutocompleteMaxVisible(sm.GetAutocompleteMaxVisible())
		}

		// Update rendered transcript children cleanly
		for _, child := range m.chatContainer.Children() {
			switch c := child.(type) {
			case *components.AssistantMessageComponent:
				c.SetHideThinkingBlock(hideThinking)
				c.SetOutputPad(outputPad)
			case *components.ToolExecutionComponent:
				c.SetShowImages(showImages)
				c.SetImageWidthCells(imgWidth)
				c.SetOutputPad(outputPad)
			case *components.BashExecutionComponent:
				c.SetOutputPad(outputPad)
			case *components.UserMessageComponent:
				c.SetOutputPad(outputPad)
			case *components.BranchSummaryMessageComponent:
				c.SetOutputPad(outputPad)
			case *components.CompactionSummaryMessageComponent:
				c.SetOutputPad(outputPad)
			case *components.CustomMessageComponent:
				c.SetOutputPad(outputPad)
			case *components.CustomEntryComponent:
				c.SetOutputPad(outputPad)
			case *components.SkillInvocationMessageComponent:
				c.SetOutputPad(outputPad)
			}
		}

		// Update target TUI mode if setting changed cleanly
		targetMode := sm.GetTuiMode()
		m.mu.Lock()
		currentMode := m.uiMode
		m.mu.Unlock()
		if m.options.TuiMode == nil && targetMode != "" && targetMode != currentMode {
			m.SwitchTuiMode(targetMode)
		}
	}

	if session != nil && m.footer != nil {
		m.footer.SetAutoCompactEnabled(session.AutoCompactionEnabled())
	}

	m.imlcUpdateEditorBorderColor()
	m.imRequestRender()
}

func (m *InteractiveMode) imsbSetupExtensionShortcuts() {
	if m.defaultEditor == nil {
		return
	}
	runner := m.imExtensionRunner()
	if runner == nil {
		m.defaultEditor.OnExtensionShortcut = nil
		return
	}

	var kbCfg tui.KeybindingsConfig
	if m.keybindings != nil {
		kbCfg = m.keybindings.GetEffectiveConfig()
	}
	shortcuts := runner.GetShortcuts(kbCfg)
	if len(shortcuts) == 0 {
		m.defaultEditor.OnExtensionShortcut = nil
		return
	}

	m.defaultEditor.OnExtensionShortcut = func(data string) bool {
		for keyID, sc := range shortcuts {
			if tui.MatchesKey(data, keyID) {
				if sc.Handler == nil {
					return true
				}
				handler := sc.Handler
				m.imSpawn(func() {
					ectx := runner.CreateContext()
					if err := handler(m.bgCtx, ectx); err != nil {
						m.ShowError(fmt.Sprintf("Shortcut %s error: %s", keyID, imhErrorMessage(err)))
					}
				})
				return true
			}
		}
		return false
	}
}

func (m *InteractiveMode) imsbShowExtensionError(err core.ExtensionError) {
	loc := ""
	if err.ExtensionPath != "" {
		loc = " (" + m.imrFormatDisplayPath(err.ExtensionPath) + ")"
	}
	evt := ""
	if err.Event != "" {
		evt = " during " + err.Event
	}
	m.ShowError(fmt.Sprintf("Extension error%s%s: %s", evt, loc, err.Error))
	if err.Stack != nil && strings.TrimSpace(*err.Stack) != "" && m.options.Verbose {
		m.chatContainer.AddChild(components.NewThemedText(func() string {
			return theme.CurrentTheme().Fg(theme.FgDim, *err.Stack)
		}, 1, 0))
		m.imRequestRender()
	}
}

// Ported from packages/coding-agent/src/modes/interactive/interactive-mode.ts (pi v1.5.2).

package interactive

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/components"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/tui"
)

func (m *InteractiveMode) imselShowDialog(setup func(done func())) {
	if m == nil || m.imIsStopped() {
		return
	}
	var once sync.Once
	done := func() {
		once.Do(func() {
			m.imlcRestoreEditor()
		})
	}
	m.imPostUI(func() {
		if m.imIsStopped() {
			return
		}
		setup(done)
	})
}

func (m *InteractiveMode) imselShowSettingsSelector() {
	session := m.imSession()
	sm := m.imSettingsManager()
	if session == nil || sm == nil {
		return
	}

	m.imselShowDialog(func(done func()) {
		var defaultModel string
		defProv := sm.GetDefaultProvider()
		defMod := sm.GetDefaultModel()
		if defProv != nil && *defProv != "" && defMod != nil && *defMod != "" {
			defaultModel = *defProv + "/" + *defMod
		} else {
			defaultModel = "not set"
		}

		var availableDefaultModels []*ai.Model
		if mr := m.imModelRuntime(); mr != nil {
			availableDefaultModels = mr.GetAvailableSnapshot()
		}

		thinkingLevel := agent.ThinkingLevel(core.DefaultThinkingLevel)
		if defThinking := sm.GetDefaultThinkingLevel(); defThinking != nil {
			thinkingLevel = *defThinking
		}

		availableThinking := []agent.ThinkingLevel{
			ai.ModelThinkingLevelOff,
			ai.ModelThinkingLevelMinimal,
			ai.ModelThinkingLevelLow,
			ai.ModelThinkingLevelMedium,
			ai.ModelThinkingLevelHigh,
			ai.ModelThinkingLevelXhigh,
		}

		currentTheme := theme.SystemThemeName
		var terminalTheme tui.TerminalColorScheme
		if m.themeController != nil {
			if sel := m.themeController.GetThemeSelection(); sel != "" {
				currentTheme = sel
			}
			terminalTheme = m.themeController.GetTerminalTheme()
		}

		m.mu.Lock()
		hideThinking := m.hideThinkingBlock
		outputPad := m.imOutputPad()
		uiMode := m.uiMode
		m.mu.Unlock()
		if ui := m.imCurrentUI(); ui != nil {
			uiMode = ui.Mode()
		}

		idleTimeout, _ := sm.GetHttpIdleTimeoutMs()

		cfg := components.SettingsConfig{
			AutoCompact:                session.AutoCompactionEnabled(),
			DefaultModel:               defaultModel,
			CurrentModel:               session.Model(),
			AvailableDefaultModels:     availableDefaultModels,
			ShowImages:                 sm.GetShowImages(),
			ImageWidthCells:            sm.GetImageWidthCells(),
			AutoResizeImages:           sm.GetImageAutoResize(),
			BlockImages:                sm.GetBlockImages(),
			EnableSkillCommands:        sm.GetEnableSkillCommands(),
			SteeringMode:               session.SteeringMode(),
			FollowUpMode:               session.FollowUpMode(),
			Transport:                  sm.GetTransport(),
			SessionShareDestination:    sm.GetSessionShareDestination(),
			HttpIdleTimeoutMs:          idleTimeout,
			CacheWarmingMode:           sm.GetCacheWarmingMode(),
			ThinkingLevel:              thinkingLevel,
			AvailableThinkingLevels:    availableThinking,
			ModelThinkingLevels:        sm.GetAllModelThinkingLevels(),
			CurrentTheme:               currentTheme,
			TerminalTheme:              terminalTheme,
			AvailableThemes:            theme.GetAvailableThemes(),
			HideThinkingBlock:          hideThinking,
			MermaidRenderingMode:       sm.GetMermaidRenderingMode(),
			ShowCacheMissNotices:       sm.GetShowCacheMissNotices(),
			CollapseChangelog:          sm.GetCollapseChangelog(),
			EnableInstallTelemetry:     sm.GetEnableInstallTelemetry(),
			DoubleEscapeAction:         sm.GetDoubleEscapeAction(),
			TreeFilterMode:             sm.GetTreeFilterMode(),
			ShowHardwareCursor:         sm.GetShowHardwareCursor(),
			EditorPaddingX:             sm.GetEditorPaddingX(),
			OutputPad:                  outputPad,
			AutocompleteMaxVisible:     sm.GetAutocompleteMaxVisible(),
			QuietStartup:               sm.GetQuietStartup(),
			DefaultProjectTrust:        sm.GetDefaultProjectTrust(),
			ClearOnShrink:              sm.GetClearOnShrink(),
			ShowTerminalProgress:       sm.GetShowTerminalProgress(),
			TuiMode:                    uiMode,
			FullscreenExitOutput:       sm.GetFullscreenExitOutput(),
			FullscreenScrollbar:        sm.GetFullscreenScrollbar(),
			FullscreenCopyOnSelect:     sm.GetFullscreenCopyOnSelect(),
			FullscreenWheelScrollLines: sm.GetFullscreenWheelScrollLines(),
			Warnings:                   sm.GetWarnings(),
		}

		var selector *components.SettingsSelectorComponent
		callbacks := components.SettingsCallbacks{
			OnAutoCompactChange: func(enabled bool) {
				session.SetAutoCompactionEnabled(enabled)
				if m.footer != nil {
					m.footer.SetAutoCompactEnabled(enabled)
				}
			},
			OnShowImagesChange: func(enabled bool) {
				sm.SetShowImages(enabled)
				for _, child := range m.chatContainer.Children() {
					if tc, ok := child.(*components.ToolExecutionComponent); ok {
						tc.SetShowImages(enabled)
					}
				}
			},
			OnImageWidthCellsChange: func(width int) {
				sm.SetImageWidthCells(float64(width))
				for _, child := range m.chatContainer.Children() {
					if tc, ok := child.(*components.ToolExecutionComponent); ok {
						tc.SetImageWidthCells(width)
					}
				}
			},
			OnAutoResizeImagesChange: func(enabled bool) {
				sm.SetImageAutoResize(enabled)
			},
			OnBlockImagesChange: func(blocked bool) {
				sm.SetBlockImages(blocked)
			},
			OnEnableSkillCommandsChange: func(enabled bool) {
				sm.SetEnableSkillCommands(enabled)
				m.iminSetupAutocomplete()
			},
			OnSteeringModeChange: func(mode agent.QueueMode) {
				session.SetSteeringMode(mode)
			},
			OnFollowUpModeChange: func(mode agent.QueueMode) {
				session.SetFollowUpMode(mode)
			},
			OnTransportChange: func(transport core.TransportSetting) {
				sm.SetTransport(transport)
				if session.Agent != nil {
					session.Agent.SetTransport(ai.Transport(transport))
				}
			},
			OnSessionShareDestinationChange: func(destination core.SessionShareDestination) {
				sm.SetSessionShareDestination(destination)
			},
			OnHttpIdleTimeoutMsChange: func(timeoutMs int64) {
				_ = sm.SetHttpIdleTimeoutMs(float64(timeoutMs))
				_ = core.ConfigureHttpDispatcher(timeoutMs)
				m.ShowStatus(fmt.Sprintf("HTTP idle timeout: %s", core.FormatHttpIdleTimeoutMs(timeoutMs)))
			},
			OnCacheWarmingModeChange: func(mode core.CacheWarmingMode) {
				_ = session.SetCacheWarmingMode(mode)
				m.ShowStatus(fmt.Sprintf("Cache warming: %s", mode))
			},
			OnModelThinkingLevelChange: func(provider, modelId string, level agent.ThinkingLevel) {
				sm.SetModelThinkingLevel(provider, modelId, level)
				cur := session.Model()
				if cur != nil && string(cur.Provider) == provider && cur.Id == modelId {
					m.imcBeginSessionMutation()
					session.SetThinkingLevel(level)
					m.imcEndSessionMutation()
					m.imlcRefreshFooterSync(false)
					m.imlcUpdateEditorBorderColor()
				}
			},
			OnModelThinkingLevelRemove: func(provider, modelId string) {
				sm.RemoveModelThinkingLevel(provider, modelId)
				cur := session.Model()
				if cur != nil && string(cur.Provider) == provider && cur.Id == modelId {
					globalDefault := agent.ThinkingLevel(core.DefaultThinkingLevel)
					if def := sm.GetDefaultThinkingLevel(); def != nil {
						globalDefault = *def
					}
					m.imcBeginSessionMutation()
					session.SetThinkingLevel(globalDefault)
					m.imcEndSessionMutation()
					m.imlcRefreshFooterSync(false)
					m.imlcUpdateEditorBorderColor()
				}
			},
			OnThemeChange: func(themeSetting string) {
				sm.SetTheme(themeSetting)
				if m.themeController != nil {
					m.themeController.SetThemeSetting(themeSetting)
				}
			},
			OnThemePreview: func(themeName string) {
				if m.themeController != nil {
					m.themeController.Preview(themeName)
				}
			},
			OnHideThinkingBlockChange: func(hidden bool) {
				sm.SetHideThinkingBlock(hidden)
				m.mu.Lock()
				m.hideThinkingBlock = hidden
				m.mu.Unlock()
				for _, child := range m.chatContainer.Children() {
					if ac, ok := child.(*components.AssistantMessageComponent); ok {
						ac.SetHideThinkingBlock(hidden)
					}
				}
				m.imRequestRender()
			},
			OnMermaidRenderingModeChange: func(mode core.MermaidRenderingMode) {
				sm.SetMermaidRenderingMode(mode)
				m.chatContainer.Invalidate()
				m.imRequestRender()
			},
			OnShowCacheMissNoticesChange: func(shown bool) {
				sm.SetShowCacheMissNotices(shown)
				m.imtrRebuildTranscript()
			},
			OnCollapseChangelogChange: func(collapsed bool) {
				sm.SetCollapseChangelog(collapsed)
			},
			OnEnableInstallTelemetryChange: func(enabled bool) {
				sm.SetEnableInstallTelemetry(enabled)
			},
			OnDoubleEscapeActionChange: func(action core.DoubleEscapeAction) {
				sm.SetDoubleEscapeAction(action)
			},
			OnTreeFilterModeChange: func(mode core.TreeFilterMode) {
				sm.SetTreeFilterMode(mode)
			},
			OnShowHardwareCursorChange: func(enabled bool) {
				sm.SetShowHardwareCursor(enabled)
				if ui := m.imCurrentUI(); ui != nil {
					ui.SetShowHardwareCursor(enabled)
				}
			},
			OnEditorPaddingXChange: func(padding int) {
				sm.SetEditorPaddingX(float64(padding))
				if m.defaultEditor != nil {
					m.defaultEditor.SetPaddingX(padding)
				}
				if setter, ok := m.editor.(interface{ SetPaddingX(int) }); ok && m.editor != imEditor(m.defaultEditor) {
					setter.SetPaddingX(padding)
				}
			},
			OnOutputPadChange: func(padding int) {
				sm.SetOutputPad(padding)
				for _, container := range []*tui.Container{m.chatContainer, m.pendingMessagesContainer} {
					for _, child := range container.Children() {
						if setter, ok := child.(interface{ SetOutputPad(int) }); ok {
							setter.SetOutputPad(padding)
						}
					}
				}
				m.imRequestRender()
			},
			OnAutocompleteMaxVisibleChange: func(maxVisible int) {
				sm.SetAutocompleteMaxVisible(float64(maxVisible))
				if m.defaultEditor != nil {
					m.defaultEditor.SetAutocompleteMaxVisible(maxVisible)
				}
				if setter, ok := m.editor.(interface{ SetAutocompleteMaxVisible(int) }); ok && m.editor != imEditor(m.defaultEditor) {
					setter.SetAutocompleteMaxVisible(maxVisible)
				}
			},
			OnQuietStartupChange: func(quiet core.QuietStartup) {
				sm.SetQuietStartup(quiet)
			},
			OnDefaultProjectTrustChange: func(defaultTrust core.DefaultProjectTrust) {
				sm.SetDefaultProjectTrust(defaultTrust)
			},
			OnClearOnShrinkChange: func(enabled bool) {
				sm.SetClearOnShrink(enabled)
				if ui := m.imCurrentUI(); ui != nil {
					ui.SetClearOnShrink(enabled)
				}
				m.mu.Lock()
				hasLoading := m.loadingAnimation != nil
				m.mu.Unlock()
				if !enabled && !hasLoading {
					m.statusContainer.Clear()
				}
			},
			OnShowTerminalProgressChange: func(enabled bool) {
				sm.SetShowTerminalProgress(enabled)
			},
			OnTuiModeChange: func(mode tui.TuiMode) {
				ui := m.imCurrentUI()
				if ui != nil && ui.HasOverlay() {
					if selector != nil {
						selector.GetSettingsList().UpdateValue("tui-mode", string(ui.Mode()))
					}
					m.ShowStatus("Close active overlays before changing TUI mode")
					return
				}
				m.SwitchTuiMode(core.TuiMode(mode))
				sm.SetTuiMode(core.TuiMode(mode))
				m.mu.Lock()
				hasLoading := m.loadingAnimation != nil
				m.mu.Unlock()
				if !hasLoading {
					m.statusContainer.Clear()
				}
				m.ShowStatus(fmt.Sprintf("TUI mode: %s", mode))
			},
			OnFullscreenExitOutputChange: func(output core.FullscreenExitOutput) {
				sm.SetFullscreenExitOutput(output)
			},
			OnFullscreenScrollbarChange: func(mode tui.ScrollViewScrollbar) {
				sm.SetFullscreenScrollbar(mode)
				if m.chatViewport != nil && m.chatViewport.Transcript != nil {
					m.chatViewport.Transcript.SetScrollbar(mode)
					m.imRequestRender()
				}
			},
			OnFullscreenCopyOnSelectChange: func(enabled bool) {
				sm.SetFullscreenCopyOnSelect(enabled)
				if alt, ok := m.imCurrentUI().(interface{ SetCopyOnSelect(bool) }); ok {
					alt.SetCopyOnSelect(enabled)
				}
			},
			OnFullscreenWheelScrollLinesChange: func(lines tui.WheelScrollLines) {
				sm.SetFullscreenWheelScrollLines(lines)
				if alt, ok := m.imCurrentUI().(interface{ SetWheelScrollLines(tui.WheelScrollLines) }); ok {
					alt.SetWheelScrollLines(lines)
				}
			},
			OnWarningsChange: func(warnings core.WarningSettings) {
				sm.SetWarnings(warnings)
			},
			OnCancel: func() {
				done()
				m.imRequestRender()
			},
		}

		selector = components.NewSettingsSelectorComponent(cfg, callbacks)
		m.imlcShowTemporarySelector(selector)
		if ui := m.imCurrentUI(); ui != nil {
			ui.SetFocus(selector.GetSettingsList())
		}
	})
}

func (m *InteractiveMode) imselShowTrustSelector() {
	session := m.imSession()
	if session == nil || session.SessionManager == nil {
		return
	}
	cwd := session.SessionManager.GetCwd()
	trustStore := core.NewProjectTrustStore(m.imAgentDir())
	savedDecision, _ := trustStore.GetEntry(cwd)
	sm := m.imSettingsManager()
	projectTrusted := sm != nil && sm.IsProjectTrusted()

	m.imselShowDialog(func(done func()) {
		selector := components.NewTrustSelectorComponent(components.TrustSelectorOptions{
			Cwd:            cwd,
			SavedDecision:  savedDecision,
			ProjectTrusted: projectTrusted,
			OnSelect: func(sel components.TrustSelection) {
				_ = trustStore.SetMany(sel.Updates)
				done()
				stateLabel := "untrusted"
				if sel.Trusted {
					stateLabel = "trusted"
				}
				m.ShowStatus(fmt.Sprintf("Saved trust decision: %s. Restart %s for this to take effect.", stateLabel, utils.AppName))
			},
			OnCancel: func() {
				done()
				m.imRequestRender()
			},
		})
		m.imlcShowTemporarySelector(selector)
	})
}

func (m *InteractiveMode) imselShowModelSelector(initialSearch string) {
	session := m.imSession()
	if session == nil {
		return
	}

	m.imselShowDialog(func(done func()) {
		selectModel := func(model *ai.Model, persist bool) {
			done()
			if model == nil {
				m.imRequestRender()
				return
			}
			m.imSpawn(func() {
				m.imcBeginSessionMutation()
				err := session.SetModel(m.bgCtx, model, &core.ModelMutationOptions{Persist: persist})
				m.imcEndSessionMutation()
				if err != nil {
					m.imPostUI(func() {
						m.ShowError(imhErrorMessage(err))
					})
					return
				}
				m.imPostUI(func() {
					m.imcUpdateAvailableProviderCount()
					m.imlcRefreshFooterSync(false)
					m.imlcUpdateEditorBorderColor()
					if persist {
						m.ShowStatus(fmt.Sprintf("Default model: %s/%s", model.Provider, model.Id))
					} else {
						m.ShowStatus(fmt.Sprintf("Model: %s", model.Id))
					}
				})
				m.imselMaybeWarnAnthropicSubscriptionAuth(m.bgCtx, model)
			})
		}

		var defRef *components.DefaultModelRef
		if sm := m.imSettingsManager(); sm != nil {
			defProv := sm.GetDefaultProvider()
			defMod := sm.GetDefaultModel()
			if defProv != nil && *defProv != "" && defMod != nil && *defMod != "" {
				defRef = &components.DefaultModelRef{
					Provider: *defProv,
					Id:       *defMod,
				}
			}
		}

		var initialPtr *string
		if initialSearch != "" {
			s := initialSearch
			initialPtr = &s
		}

		selector := components.NewModelSelectorComponent(
			m.imCurrentUI(),
			session.Model(),
			session.ModelRuntime(),
			session.ScopedModels(),
			func(model *ai.Model) { selectModel(model, false) },
			func() {
				done()
				m.imRequestRender()
			},
			initialPtr,
			func(model *ai.Model) { selectModel(model, true) },
			defRef,
		)
		m.imlcShowTemporarySelector(selector, selector.Dispose)
	})
}

func (m *InteractiveMode) imselShowThinkingSelector() {
	session := m.imSession()
	if session == nil {
		return
	}
	available := session.GetAvailableThinkingLevels()
	if len(available) == 0 {
		m.ShowStatus("Current model does not support thinking level selection")
		return
	}

	m.imselShowDialog(func(done func()) {
		var defaults []ai.ModelThinkingLevel
		if sm := m.imSettingsManager(); sm != nil {
			if def := sm.GetDefaultThinkingLevel(); def != nil {
				defaults = append(defaults, *def)
			}
		}

		selector := components.NewThinkingSelectorComponent(
			session.ThinkingLevel(),
			available,
			func(level ai.ModelThinkingLevel) {
				done()
				m.imselSelectThinkingLevel(level, false)
			},
			func() {
				done()
				m.imRequestRender()
			},
			func(level ai.ModelThinkingLevel) {
				done()
				m.imselSelectThinkingLevel(level, true)
			},
			defaults...,
		)
		m.imlcShowTemporarySelector(selector)
	})
}

func (m *InteractiveMode) imselSelectThinkingLevel(level ai.ModelThinkingLevel, persist bool) {
	session := m.imSession()
	if session == nil {
		return
	}
	m.imcBeginSessionMutation()
	session.SetThinkingLevel(agent.ThinkingLevel(level), &core.ModelMutationOptions{Persist: persist})
	m.imcEndSessionMutation()
	m.imlcRefreshFooterSync(false)
	m.imlcUpdateEditorBorderColor()
	if persist {
		m.ShowStatus(fmt.Sprintf("Default thinking level: %s", level))
	} else {
		m.ShowStatus(fmt.Sprintf("Thinking level: %s", level))
	}
}

func (m *InteractiveMode) imselShowThemeSelector() {
	currentTheme := theme.SystemThemeName
	if m.themeController != nil {
		if sel := m.themeController.GetThemeSelection(); sel != "" {
			currentTheme = sel
		}
	}

	m.imselShowDialog(func(done func()) {
		selector := components.NewThemeSelectorComponent(
			currentTheme,
			func(themeName string) {
				done()
				if m.themeController != nil {
					res := m.themeController.SetThemeSetting(themeName)
					if res.Success {
						m.ShowStatus(fmt.Sprintf("Theme: %s", themeName))
					} else if res.Error != "" {
						m.ShowError(res.Error)
					}
				} else {
					res := theme.SetTheme(themeName, true)
					if res.Success {
						if sm := m.imSettingsManager(); sm != nil {
							sm.SetTheme(themeName)
						}
						m.ShowStatus(fmt.Sprintf("Theme: %s", themeName))
					}
				}
				if ui := m.imCurrentUI(); ui != nil {
					ui.Invalidate()
				}
				m.imRequestRender()
			},
			func() {
				done()
				if m.themeController != nil {
					m.themeController.Preview(currentTheme)
				}
				m.imRequestRender()
			},
			func(themeName string) {
				if m.themeController != nil {
					m.themeController.Preview(themeName)
				}
			},
		)
		m.imlcShowTemporarySelector(selector)
		if ui := m.imCurrentUI(); ui != nil {
			ui.SetFocus(selector.GetSelectList())
		}
	})
}

func (m *InteractiveMode) imselShowScopedModelsSelector() {
	session := m.imSession()
	sm := m.imSettingsManager()
	mr := m.imModelRuntime()
	if session == nil || mr == nil {
		return
	}

	availableModels := append([]*ai.Model(nil), mr.GetAvailableSnapshot()...)
	buildModelIDSet := func(models []*ai.Model) map[string]struct{} {
		set := make(map[string]struct{}, len(models))
		for _, mod := range models {
			if mod != nil {
				set[string(mod.Provider)+"/"+mod.Id] = struct{}{}
			}
		}
		return set
	}
	availableModelIds := buildModelIDSet(availableModels)

	var configuredPatterns []string
	if sm != nil {
		configuredPatterns = sm.GetEnabledModels()
	}
	configuredEnabledIds := func(models []*ai.Model) []string {
		if len(configuredPatterns) == 0 {
			return nil
		}
		resolved := core.ResolveModelScopeFromModels(configuredPatterns, models)
		ids := make([]string, 0, len(resolved.ScopedModels)+len(resolved.Diagnostics))
		for _, scoped := range resolved.ScopedModels {
			if scoped.Model != nil {
				ids = append(ids, string(scoped.Model.Provider)+"/"+scoped.Model.Id)
			}
		}
		for _, diag := range resolved.Diagnostics {
			if diag.Code == core.ModelScopeNoMatch && !slices.Contains(ids, diag.Pattern) {
				ids = append(ids, diag.Pattern)
			}
		}
		return ids
	}

	sessionScoped := session.ScopedModels()
	var currentEnabledIds []string
	if len(sessionScoped) > 0 {
		currentEnabledIds = make([]string, 0, len(sessionScoped))
		for _, sc := range sessionScoped {
			if sc.Model != nil {
				currentEnabledIds = append(currentEnabledIds, string(sc.Model.Provider)+"/"+sc.Model.Id)
			}
		}
	} else {
		currentEnabledIds = configuredEnabledIds(availableModels)
	}

	selectionChanged := false

	updateSessionModels := func(enabledIds []string) {
		if enabledIds == nil {
			currentEnabledIds = nil
		} else {
			currentEnabledIds = append([]string(nil), enabledIds...)
		}

		hasEnabledAvailable := false
		for _, id := range enabledIds {
			if _, ok := availableModelIds[id]; ok {
				hasEnabledAvailable = true
				break
			}
		}
		allEnabled := enabledIds != nil && len(availableModelIds) > 0
		if allEnabled {
			for id := range availableModelIds {
				if !slices.Contains(enabledIds, id) {
					allEnabled = false
					break
				}
			}
		}

		if enabledIds != nil && hasEnabledAvailable && !allEnabled {
			resolved := core.ResolveModelScopeFromModels(enabledIds, availableModels)
			nextScoped := make([]core.ScopedModel, 0, len(resolved.ScopedModels))
			for _, sc := range resolved.ScopedModels {
				nextScoped = append(nextScoped, core.ScopedModel{
					Model:         sc.Model,
					ThinkingLevel: sc.ThinkingLevel,
				})
			}
			session.SetScopedModels(nextScoped)
		} else {
			session.SetScopedModels(nil)
		}
		m.imcUpdateAvailableProviderCount()
		m.imRequestRender()
	}

	m.imselShowDialog(func(done func()) {
		var mu sync.Mutex
		disposed := false
		refreshCtx, cancelRefresh := context.WithTimeout(m.bgCtx, 15*time.Second)

		selector := components.NewScopedModelsSelectorComponent(
			components.ModelsConfig{
				AllModels:       availableModels,
				EnabledModelIds: currentEnabledIds,
				RefreshStatus:   imhStrPtr("Refreshing model catalogs…"),
			},
			components.ModelsCallbacks{
				OnChange: func(enabledIds []string) {
					selectionChanged = true
					updateSessionModels(enabledIds)
				},
				OnPersist: func(enabledIds []string) {
					allEnabled := enabledIds != nil && len(enabledIds) == len(availableModels)
					if allEnabled {
						for _, id := range enabledIds {
							if _, ok := availableModelIds[id]; !ok {
								allEnabled = false
								break
							}
						}
					}
					var patterns []string
					if enabledIds != nil && !allEnabled {
						patterns = append([]string(nil), enabledIds...)
					}
					if sm != nil {
						sm.SetEnabledModels(patterns)
					}
					m.ShowStatus("Model selection saved to settings")
				},
				OnCancel: func() {
					done()
					m.imRequestRender()
				},
			},
		)

		m.imlcShowTemporarySelector(selector, func() {
			mu.Lock()
			disposed = true
			mu.Unlock()
			cancelRefresh()
		})

		m.imSpawn(func() {
			defer cancelRefresh()
			res, err := components.RefreshModelCatalogs(refreshCtx, mr)
			m.imPostUI(func() {
				mu.Lock()
				isDisposed := disposed
				mu.Unlock()
				if isDisposed || m.imIsStopped() {
					return
				}

				if err != nil {
					if errors.Is(err, context.DeadlineExceeded) || errors.Is(refreshCtx.Err(), context.DeadlineExceeded) {
						selector.SetRefreshStatus(imhStrPtr("Model refresh timed out; showing cached models."), "warning")
					} else {
						selector.SetRefreshStatus(imhStrPtr(fmt.Sprintf("Could not refresh model catalogs: %s", imhErrorMessage(err))), "warning")
					}
					m.imRequestRender()
					return
				}

				availableModels = append([]*ai.Model(nil), mr.GetAvailableSnapshot()...)
				availableModelIds = buildModelIDSet(availableModels)
				if !selectionChanged && len(sessionScoped) == 0 {
					currentEnabledIds = configuredEnabledIds(availableModels)
					selector.UpdateModels(availableModels, currentEnabledIds)
				} else {
					selector.UpdateModels(availableModels)
				}
				if currentEnabledIds != nil {
					updateSessionModels(currentEnabledIds)
				}

				if res.Aborted && errors.Is(refreshCtx.Err(), context.DeadlineExceeded) {
					selector.SetRefreshStatus(imhStrPtr("Model refresh timed out; showing cached models."), "warning")
				} else if res.Errors != nil && res.Errors.Len() > 0 {
					selector.SetRefreshStatus(imhStrPtr(fmt.Sprintf("Could not refresh %s; showing cached models.", strings.Join(res.Errors.Keys(), ", "))), "warning")
				} else {
					selector.SetRefreshStatus(imhStrPtr("Model catalogs refreshed."), "success")
				}
				m.imRequestRender()
			})
		})
	})
}

func (m *InteractiveMode) imselShowUserMessageSelector() {
	session := m.imSession()
	if session == nil {
		return
	}
	userMessages := session.GetUserMessagesForForking()
	if len(userMessages) == 0 {
		m.ShowStatus("No messages to fork from")
		return
	}

	items := make([]components.UserMessageItem, 0, len(userMessages))
	for _, um := range userMessages {
		items = append(items, components.UserMessageItem{
			Id:   um.EntryId,
			Text: um.Text,
		})
	}
	initialSelected := userMessages[len(userMessages)-1].EntryId

	m.imselShowDialog(func(done func()) {
		selector := components.NewUserMessageSelectorComponent(
			items,
			func(entryId string) {
				done()
				m.imSpawn(func() {
					if m.runtime == nil {
						return
					}
					m.imcBeginSessionMutation()
					res, err := m.runtime.Fork(m.bgCtx, entryId)
					m.imcEndSessionMutation()
					if err != nil {
						errMsg := imhErrorMessage(err)
						m.imPostUI(func() {
							m.ShowError(errMsg)
						})
						return
					}
					if res != nil && res.Cancelled {
						m.imPostUI(func() {
							m.imRequestRender()
						})
						return
					}
					var selectedText *string
					if res != nil {
						selectedText = res.SelectedText
					}
					m.imCallUI(m.bgCtx, func() {
						m.imsbRebindCurrentSession(m.bgCtx, &imsbRebindOptions{
							ShowInitialMessages: true,
							RestoreInputText:    selectedText,
						})
						m.ShowStatus("Forked to new session")
					})
				})
			},
			func() {
				done()
				m.imRequestRender()
			},
			initialSelected,
		)
		m.imlcShowTemporarySelector(selector, selector.Dispose)
		if ui := m.imCurrentUI(); ui != nil {
			ui.SetFocus(selector.GetMessageList())
		}
	})
}

func (m *InteractiveMode) imselShowTreeSelector(initialSelectedId ...string) {
	session := m.imSession()
	if session == nil || session.SessionManager == nil {
		return
	}
	sm := session.SessionManager
	tree := sm.GetTree()
	if len(tree) == 0 {
		m.ShowStatus("No entries in session")
		return
	}

	realLeafId := sm.GetLeafId()
	var initialSelected *string
	if len(initialSelectedId) > 0 && initialSelectedId[0] != "" {
		v := initialSelectedId[0]
		initialSelected = &v
	}

	var filterModes []components.FilterMode
	if settings := m.imSettingsManager(); settings != nil {
		filterModes = append(filterModes, settings.GetTreeFilterMode())
	}

	termRows := 24
	if ui := m.imCurrentUI(); ui != nil && ui.Terminal() != nil && ui.Terminal().Rows() > 0 {
		termRows = ui.Terminal().Rows()
	}

	m.imselShowDialog(func(done func()) {
		selector := components.NewTreeSelectorComponent(
			tree,
			realLeafId,
			termRows,
			func(entryId string) {
				if leaf := sm.GetLeafId(); leaf != nil && *leaf == entryId {
					done()
					m.ShowStatus("Already at this point")
					return
				}
				done()
				m.imSpawn(func() {
					m.imselNavigateTreeWithOptions(entryId)
				})
			},
			func() {
				m.imPostUI(func() {
					done()
					m.imRequestRender()
				})
			},
			func(entryId string, label *string) {
				_, _ = sm.AppendLabelChange(entryId, label)
			},
			initialSelected,
			filterModes...,
		)
		selector.OnCopy = func(text *string) {
			if text == nil || *text == "" {
				return
			}
			if err := utils.CopyToClipboard(*text); err != nil {
				m.ShowError(fmt.Sprintf("Failed to copy: %s", imhErrorMessage(err)))
				return
			}
			m.ShowStatus("Copied to clipboard")
		}
		m.imlcShowTemporarySelector(selector, selector.Dispose)
	})
}

func (m *InteractiveMode) imselNavigateTreeWithOptions(entryId string) {
	session := m.imSession()
	if session == nil || m.imIsStopped() {
		return
	}

	wantsSummary := false
	var customInstructions *string

	skipPrompt := false
	if sm := m.imSettingsManager(); sm != nil {
		skipPrompt = sm.GetBranchSummarySkipPrompt()
	}

	if !skipPrompt {
		uiCtx := m.imeuCreateExtensionUIContext()
		for {
			choice, err := uiCtx.Select(m.bgCtx, "Summarize branch?", []string{
				"No summary",
				"Summarize",
				"Summarize with custom prompt",
			})
			if err != nil || choice == nil {
				m.imselShowTreeSelector(entryId)
				return
			}
			wantsSummary = *choice != "No summary"
			if *choice == "Summarize with custom prompt" {
				edited, edErr := uiCtx.Editor(m.bgCtx, "Custom summarization instructions")
				if edErr != nil || edited == nil {
					continue
				}
				customInstructions = edited
			}
			break
		}
	}

	if session.IsStreaming() {
		m.imCallUI(m.bgCtx, func() {
			m.imqHandleDequeue()
		})
		_ = session.Abort(m.bgCtx)
	}

	if session.IsCompacting() {
		m.ShowError("Wait for the current compaction or tree navigation to finish before navigating the session tree.")
		return
	}

	var summaryIndicator *components.BranchSummaryStatusIndicator
	var prevEscapeHandler func()
	if wantsSummary {
		m.imCallUI(m.bgCtx, func() {
			m.mu.Lock()
			prevEscapeHandler = m.compactionEscapeHandler
			m.compactionEscapeHandler = func() {
				session.AbortBranchSummary()
			}
			m.mu.Unlock()

			summaryIndicator = components.NewBranchSummaryStatusIndicator(m.imCurrentUI())
			m.statusContainer.AddChild(summaryIndicator)
			m.imRequestRender()
		})
	}

	m.imcBeginSessionMutation()
	res, err := session.NavigateTree(m.bgCtx, entryId, &core.NavigateTreeOptions{
		Summarize:          wantsSummary,
		CustomInstructions: customInstructions,
	})
	m.imcEndSessionMutation()

	if summaryIndicator != nil {
		m.imCallUI(m.bgCtx, func() {
			m.mu.Lock()
			m.compactionEscapeHandler = prevEscapeHandler
			m.mu.Unlock()
			m.statusContainer.RemoveChild(summaryIndicator)
			summaryIndicator.Dispose()
			m.imRequestRender()
		})
	}

	if err != nil {
		errMsg := imhErrorMessage(err)
		m.imPostUI(func() {
			m.ShowError(errMsg)
		})
		return
	}
	if res == nil {
		return
	}
	if res.Aborted {
		m.imPostUI(func() {
			m.ShowStatus("Branch summarization cancelled")
		})
		m.imselShowTreeSelector(entryId)
		return
	}
	if res.Cancelled {
		m.imPostUI(func() {
			m.ShowStatus("Navigation cancelled")
		})
		return
	}

	m.imCallUI(m.bgCtx, func() {
		m.imtrRebuildTranscript()
		if res.EditorText != nil && m.editor != nil {
			m.editor.SetText(*res.EditorText)
		}
		m.imlcRefreshFooterSync(true)
		m.ShowStatus("Navigated to branch")
	})
}

func (m *InteractiveMode) imselShowSessionSelector() {
	session := m.imSession()
	if session == nil || session.SessionManager == nil {
		return
	}
	sm := session.SessionManager
	cwd := sm.GetCwd()
	sessionDir := sm.GetSessionDir()
	usesDefaultDir := sm.UsesDefaultSessionDir()
	currentFile := ""
	if sf := sm.GetSessionFile(); sf != nil {
		currentFile = *sf
	}

	currentLoader := func(onProgress core.SessionListProgress, ctx context.Context) ([]core.SessionInfo, error) {
		if onProgress != nil {
			return core.SessionManagerList(ctx, cwd, sessionDir, onProgress)
		}
		return core.SessionManagerList(ctx, cwd, sessionDir)
	}
	allLoader := func(onProgress core.SessionListProgress, ctx context.Context) ([]core.SessionInfo, error) {
		dir := ""
		if !usesDefaultDir {
			dir = sessionDir
		}
		if onProgress != nil {
			return core.SessionManagerListAll(ctx, dir, onProgress)
		}
		return core.SessionManagerListAll(ctx, dir)
	}

	var currentFiles []string
	if currentFile != "" {
		currentFiles = append(currentFiles, currentFile)
	}

	m.imselShowDialog(func(done func()) {
		showHint := true
		selector := components.NewSessionSelectorComponent(
			currentLoader,
			allLoader,
			func(sessionPath string) {
				done()
				m.imSpawn(func() {
					m.imselResumeSession(m.bgCtx, sessionPath)
				})
			},
			func() {
				done()
				m.imRequestRender()
			},
			func() {
				done()
				m.imcRequestShutdown()
			},
			func() {
				m.imRequestRender()
			},
			&components.SessionSelectorOptions{
				RenameSession: func(ctx context.Context, sessionPath string, nextName *string) error {
					next := ""
					if nextName != nil {
						next = strings.TrimSpace(*nextName)
					}
					if next == "" {
						return nil
					}
					mgr, err := core.SessionManagerOpen(sessionPath)
					if err != nil {
						return err
					}
					_, err = mgr.AppendSessionInfo(next)
					return err
				},
				ShowRenameHint: &showHint,
				Keybindings:    m.keybindings,
			},
			currentFiles...,
		)
		m.imlcShowTemporarySelector(selector, selector.Dispose)
	})
}

func (m *InteractiveMode) imselResumeSession(ctx context.Context, sessionPath string) {
	if ctx == nil {
		ctx = m.bgCtx
	}
	if m.runtime == nil {
		return
	}

	m.imCallUI(ctx, m.imlcStopAllStatusIndicators)

	var res *core.SwitchSessionResult
	var err error
	if entries := core.LoadEntriesFromFile(sessionPath); len(entries) > 0 {
		if header, ok := entries[0].(*core.SessionHeader); ok && header != nil && header.Cwd != "" {
			if _, statErr := os.Stat(header.Cwd); statErr != nil {
				resolved := sessionPath
				err = &core.MissingSessionCwdError{
					Issue: core.SessionCwdIssue{
						SessionFile: &resolved,
						SessionCwd:  header.Cwd,
						FallbackCwd: m.imCwd(),
					},
				}
			}
		}
	}
	if err == nil {
		m.imcBeginSessionMutation()
		res, err = m.runtime.SwitchSession(ctx, sessionPath)
		m.imcEndSessionMutation()
	}
	if err == nil {
		if res != nil && res.Cancelled {
			return
		}
		m.imCallUI(ctx, func() {
			m.imsbRebindCurrentSession(ctx, &imsbRebindOptions{
				ShowInitialMessages: true,
			})
			m.ShowStatus("Resumed session")
		})
		return
	}

	var missingErr *core.MissingSessionCwdError
	if errors.As(err, &missingErr) && missingErr != nil {
		choice := m.imselPromptMissingSessionCwd(ctx, missingErr)
		if choice == nil {
			m.imPostUI(func() {
				m.ShowStatus("Resume cancelled")
			})
			return
		}
		switch *choice {
		case core.MissingSessionCwdChoiceContinue:
			m.imcBeginSessionMutation()
			res2, err2 := m.runtime.SwitchSession(ctx, sessionPath, missingErr.Issue.FallbackCwd)
			m.imcEndSessionMutation()
			if err2 != nil {
				errMsg := fmt.Sprintf("Failed to resume session: %s", imhErrorMessage(err2))
				m.imPostUI(func() {
					m.ShowError(errMsg)
				})
				return
			}
			if res2 != nil && res2.Cancelled {
				return
			}
			m.imCallUI(ctx, func() {
				m.imsbRebindCurrentSession(ctx, &imsbRebindOptions{
					ShowInitialMessages: true,
				})
				m.ShowStatus(fmt.Sprintf("Resumed session in %s", missingErr.Issue.FallbackCwd))
			})
		case core.MissingSessionCwdChoiceFork:
			m.imselSwitchToForkedSession(ctx, sessionPath, missingErr.Issue.FallbackCwd)
		default:
			m.imPostUI(func() {
				m.ShowStatus("Resume cancelled")
			})
		}
		return
	}

	errMsg := fmt.Sprintf("Failed to resume session: %s", imhErrorMessage(err))
	m.imPostUI(func() {
		m.ShowError(errMsg)
	})
}

func (m *InteractiveMode) imselPromptMissingSessionCwd(ctx context.Context, missingErr *core.MissingSessionCwdError) *core.MissingSessionCwdChoice {
	if missingErr == nil {
		return nil
	}
	options := core.GetMissingSessionCwdOptions(missingErr.Issue)
	labels := make([]string, len(options))
	for i, opt := range options {
		labels[i] = opt.Label
	}
	sel, err := m.imeuCreateExtensionUIContext().Select(ctx, core.FormatMissingSessionCwdPrompt(missingErr.Issue), labels)
	if err != nil || sel == nil {
		return nil
	}
	for _, opt := range options {
		if opt.Label == *sel {
			return opt.Value
		}
	}
	return nil
}

func (m *InteractiveMode) imselSwitchToForkedSession(ctx context.Context, sourcePath string, targetCwd string) {
	session := m.imSession()
	sessionDir := ""
	if session != nil && session.SessionManager != nil {
		sessionDir = session.SessionManager.GetSessionDir()
	}
	m.imcBeginSessionMutation()
	forkedSM, err := core.SessionManagerForkFrom(sourcePath, targetCwd, sessionDir)
	if err != nil {
		m.imcEndSessionMutation()
		errMsg := fmt.Sprintf("Failed to fork session: %s", imhErrorMessage(err))
		m.imPostUI(func() {
			m.ShowError(errMsg)
		})
		return
	}
	forkedFile := forkedSM.GetSessionFile()
	if forkedFile == nil || *forkedFile == "" {
		m.imcEndSessionMutation()
		m.imPostUI(func() {
			m.ShowError("Failed to fork session: missing forked session file")
		})
		return
	}
	res, err := m.runtime.SwitchSession(ctx, *forkedFile)
	m.imcEndSessionMutation()
	if err != nil {
		errMsg := fmt.Sprintf("Failed to resume forked session: %s", imhErrorMessage(err))
		m.imPostUI(func() {
			m.ShowError(errMsg)
		})
		return
	}
	if res != nil && res.Cancelled {
		return
	}
	m.imCallUI(ctx, func() {
		m.imsbRebindCurrentSession(ctx, &imsbRebindOptions{
			ShowInitialMessages: true,
		})
		m.ShowStatus(fmt.Sprintf("Forked session to %s", targetCwd))
	})
}

func (m *InteractiveMode) imselMaybeWarnAnthropicSubscriptionAuth(ctx context.Context, model *ai.Model) {
	if m == nil || m.imIsStopped() {
		return
	}
	if sm := m.imSettingsManager(); sm != nil {
		w := sm.GetWarnings()
		if w.AnthropicExtraUsage != nil && !*w.AnthropicExtraUsage {
			return
		}
	}
	m.mu.Lock()
	alreadyShown := m.hasShownAnthropicWarning
	m.mu.Unlock()
	if alreadyShown {
		return
	}
	session := m.imSession()
	mr := m.imModelRuntime()
	if session == nil || mr == nil {
		return
	}
	if model == nil {
		model = session.Model()
	}
	if model == nil || string(model.Provider) != "anthropic" {
		return
	}

	if ctx == nil {
		ctx = m.bgCtx
	}
	if chk, err := mr.CheckAuth(ctx, "anthropic", nil); err == nil && chk != nil && chk.Type == ai.AuthTypeOAuth {
		m.mu.Lock()
		m.hasShownAnthropicWarning = true
		m.mu.Unlock()
		m.ShowWarning(imhAnthropicSubscriptionAuthWarning)
		return
	}
	auth, err := mr.GetAuth(ctx, string(model.Provider), nil)
	if err != nil || !imhIsAnthropicSubscriptionAuthKey(auth.Auth.ApiKey) {
		return
	}
	m.mu.Lock()
	m.hasShownAnthropicWarning = true
	m.mu.Unlock()
	m.ShowWarning(imhAnthropicSubscriptionAuthWarning)
}

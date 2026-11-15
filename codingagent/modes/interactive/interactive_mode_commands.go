package interactive

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/components"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/tui"
)

func (m *InteractiveMode) imcmdHandleSlashCommand(ctx context.Context, text string) (bool, error) {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "/") {
		return false, nil
	}
	if ctx == nil {
		ctx = m.bgCtx
	}

	body := strings.TrimPrefix(trimmed, "/")
	if body == "" {
		return false, nil
	}

	name := body
	args := ""
	if idx := strings.IndexAny(body, " \t\n\r"); idx != -1 {
		name = body[:idx]
		args = strings.TrimSpace(body[idx+1:])
	}

	switch name {
	case "settings":
		m.imselShowSettingsSelector()
		return true, nil

	case "model":
		if args != "" {
			m.imcmdHandleModelArgCommand(ctx, args)
		} else {
			m.imselShowModelSelector("")
		}
		return true, nil

	case "thinking":
		if args != "" {
			m.imcmdHandleThinkingArgCommand(args)
		} else {
			m.imselShowThinkingSelector()
		}
		return true, nil

	case "scoped-models":
		m.imselShowScopedModelsSelector()
		return true, nil

	case "theme":
		if args != "" {
			m.imcmdHandleThemeArgCommand(args)
		} else {
			m.imselShowThemeSelector()
		}
		return true, nil

	case "export":
		m.imcmdHandleExportCommand(args)
		return true, nil

	case "import":
		m.imcmdHandleImportCommand(ctx, args)
		return true, nil

	case "share":
		m.imcmdHandleShareCommand(ctx)
		return true, nil

	case "copy":
		m.iminCopySelectionOrLastAssistantMessage()
		return true, nil

	case "name":
		m.imcmdHandleNameCommand(args)
		return true, nil

	case "session":
		m.imcmdHandleSessionInfoCommand()
		return true, nil

	case "changelog":
		m.imcmdHandleChangelogCommand()
		return true, nil

	case "hotkeys":
		m.imcmdHandleHotkeysCommand()
		return true, nil

	case "fork":
		m.imselShowUserMessageSelector()
		return true, nil

	case "clone":
		m.imcmdHandleCloneCommand(ctx)
		return true, nil

	case "tree":
		m.imselShowTreeSelector()
		return true, nil

	case "login":
		m.imaHandleLoginCommand(ctx, args)
		return true, nil

	case "logout":
		m.imaHandleLogoutCommand(ctx, args)
		return true, nil

	case "new":
		_ = m.imcmdHandleNewSessionCommand()
		return true, nil

	case "compact":
		m.imcmdHandleCompactCommand(ctx, args)
		return true, nil

	case "resume":
		m.imselShowSessionSelector()
		return true, nil

	case "reload":
		m.imcmdHandleReloadCommand(ctx)
		return true, nil

	case "trust":
		m.imselShowTrustSelector()
		return true, nil

	case "bug":
		m.imcmdHandleBugCommand(ctx, args)
		return true, nil

	case "quit", "exit":
		m.imcRequestShutdown()
		return true, nil
	}

	if runner := m.imExtensionRunner(); runner != nil {
		if extCmd := runner.GetCommand(name); extCmd != nil && extCmd.Handler != nil {
			err := extCmd.Handler(ctx, args, runner.CreateCommandContext())
			if err != nil {
				return true, err
			}
			return true, nil
		}
	}

	return false, nil
}

func (m *InteractiveMode) imcmdHandleModelArgCommand(ctx context.Context, query string) {
	session := m.imSession()
	mr := m.imModelRuntime()
	if session == nil || mr == nil {
		return
	}

	models := mr.GetModels(nil)
	trimmed := strings.TrimSpace(query)
	lower := strings.ToLower(trimmed)

	var exact []*ai.Model
	var prefix []*ai.Model
	for _, mod := range models {
		if mod == nil {
			continue
		}
		full := strings.ToLower(string(mod.Provider) + "/" + mod.Id)
		idLower := strings.ToLower(mod.Id)
		if full == lower || idLower == lower {
			exact = append(exact, mod)
		} else if strings.Contains(full, lower) || strings.Contains(strings.ToLower(mod.Name), lower) {
			prefix = append(prefix, mod)
		}
	}

	var target *ai.Model
	if len(exact) == 1 {
		target = exact[0]
	} else if len(exact) == 0 && len(prefix) == 1 {
		target = prefix[0]
	}

	if target == nil {
		m.imselShowModelSelector(trimmed)
		return
	}

	m.imcBeginSessionMutation()
	err := session.SetModel(ctx, target)
	m.imcEndSessionMutation()
	if err != nil {
		m.imPostUI(func() {
			m.ShowError(imhErrorMessage(err))
		})
		return
	}
	m.imPostUI(func() {
		m.imlcUpdateEditorBorderColor()
		m.imlcRefreshFooterSync(false)
		m.imcReportProgramStatus()
		m.ShowStatus(fmt.Sprintf("Switched model to %s/%s", target.Provider, target.Id))
	})
}

func (m *InteractiveMode) imcmdHandleThinkingArgCommand(rawLevel string) {
	session := m.imSession()
	if session == nil {
		return
	}
	if !session.SupportsThinking() {
		m.imPostUI(func() {
			m.ShowWarning("Current model does not support thinking levels")
		})
		return
	}

	wanted := strings.ToLower(strings.TrimSpace(rawLevel))
	avail := session.GetAvailableThinkingLevels()
	var matched *agent.ThinkingLevel
	for _, lvl := range avail {
		if strings.ToLower(string(lvl)) == wanted {
			copyLvl := lvl
			matched = &copyLvl
			break
		}
	}
	if matched == nil {
		var names []string
		for _, lvl := range avail {
			names = append(names, string(lvl))
		}
		m.imPostUI(func() {
			m.ShowError(fmt.Sprintf("Invalid thinking level %q. Available: %s", rawLevel, strings.Join(names, ", ")))
		})
		return
	}

	m.imcBeginSessionMutation()
	session.SetThinkingLevel(*matched)
	m.imcEndSessionMutation()
	m.imPostUI(func() {
		m.imlcUpdateEditorBorderColor()
		m.imlcRefreshFooterSync(false)
		m.ShowStatus(fmt.Sprintf("Thinking level: %s", string(*matched)))
	})
}

func (m *InteractiveMode) imcmdHandleThemeArgCommand(rawTheme string) {
	name := strings.TrimSpace(rawTheme)
	res := m.imeuCreateExtensionUIContext().SetThemeByName(name)
	if !res.Success {
		errMsg := fmt.Sprintf("Failed to switch theme to %q", name)
		if res.Error != nil && *res.Error != "" {
			errMsg = *res.Error
		}
		m.imPostUI(func() {
			m.ShowError(errMsg)
		})
		return
	}
	if sm := m.imSettingsManager(); sm != nil {
		sm.SetTheme(name)
	}
	m.imPostUI(func() {
		m.ShowStatus(fmt.Sprintf("Theme switched to %s", name))
	})
}

func (m *InteractiveMode) imcmdHandleExportCommand(argPath string) {
	session := m.imSession()
	if session == nil {
		return
	}
	targetPath := strings.TrimSpace(argPath)
	if targetPath != "" {
		targetPath = imhStripPathQuotesAndSpaces(targetPath)
		targetPath = imhExpandTildePath(targetPath)
		if !filepath.IsAbs(targetPath) {
			targetPath = filepath.Join(m.imCwd(), targetPath)
		}
	}

	exportedPath, err := session.ExportToHtml(targetPath)
	if err != nil {
		m.imPostUI(func() {
			m.ShowError(fmt.Sprintf("Export failed: %v", err))
		})
		return
	}
	m.imPostUI(func() {
		m.ShowStatus(fmt.Sprintf("Session exported to %s", imhNormalizePathForDisplay(exportedPath)))
	})
}

func (m *InteractiveMode) imcmdHandleImportCommand(ctx context.Context, argPath string) {
	if m.runtime == nil {
		return
	}
	targetPath := imhStripPathQuotesAndSpaces(argPath)
	if targetPath == "" {
		m.imPostUI(func() {
			m.ShowError("Usage: /import <path>")
		})
		return
	}
	targetPath = imhExpandTildePath(targetPath)
	if !filepath.IsAbs(targetPath) {
		targetPath = filepath.Join(m.imCwd(), targetPath)
	}

	res, err := m.runtime.ImportFromJsonl(ctx, targetPath)
	if err != nil {
		m.imPostUI(func() {
			m.ShowError(fmt.Sprintf("Import failed: %v", err))
		})
		return
	}
	if res != nil && res.Cancelled {
		m.imPostUI(func() {
			m.ShowStatus("Import cancelled")
		})
		return
	}

	m.imCallUI(ctx, func() {
		m.imsbRebindCurrentSession(ctx, &imsbRebindOptions{
			ShowInitialMessages: true,
		})
		m.ShowStatus(fmt.Sprintf("Imported session from %s", imhNormalizePathForDisplay(targetPath)))
	})
}

func (m *InteractiveMode) imcmdHandleShareCommand(ctx context.Context) {
	session := m.imSession()
	if session == nil {
		return
	}

	var edComp tui.Component
	if c, ok := m.editor.(tui.Component); ok {
		edComp = c
	}

	ShareSession(ctx, &SessionShareContext{
		Session:         session,
		UI:              m.imCurrentUI(),
		EditorContainer: m.editorContainer,
		Editor:          edComp,
		ShowStatus: func(msg string) {
			m.imPostUI(func() { m.ShowStatus(msg) })
		},
		ShowError: func(msg string) {
			m.imPostUI(func() { m.ShowError(msg) })
		},
	})
}

func (m *InteractiveMode) imcmdHandleNameCommand(argName string) {
	session := m.imSession()
	if session == nil {
		return
	}
	trimmed := strings.TrimSpace(argName)
	if trimmed == "" {
		current := "(unnamed)"
		if n := m.imcCachedSessionName(); n != nil && strings.TrimSpace(*n) != "" {
			current = *n
		}
		m.imPostUI(func() {
			m.ShowStatus("Session name: " + current)
		})
		return
	}

	m.imcBeginSessionMutation()
	session.SetSessionName(trimmed)
	m.imcSetCachedSessionName(trimmed)
	m.imcEndSessionMutation()
	m.imPostUI(func() {
		m.imcUpdateTerminalTitle()
		m.imcReportProgramStatus()
		m.imlcRefreshFooterSync(true)
		m.ShowStatus("Session named: " + trimmed)
	})
}

func (m *InteractiveMode) imcmdHandleSessionInfoCommand() {
	session := m.imSession()
	if session == nil {
		return
	}
	m.imcBeginSessionMutation()
	stats := session.GetSessionStats()
	sessionName := session.SessionName()
	m.imcEndSessionMutation()

	var lines []string
	lines = append(lines, fmt.Sprintf("Session ID: %s", stats.SessionId))
	if sessionName != nil && strings.TrimSpace(*sessionName) != "" {
		lines = append(lines, fmt.Sprintf("Name: %s", *sessionName))
	}
	if stats.SessionFile != nil && *stats.SessionFile != "" {
		lines = append(lines, fmt.Sprintf("File: %s", imhNormalizePathForDisplay(*stats.SessionFile)))
	} else {
		lines = append(lines, "File: (in-memory)")
	}
	lines = append(lines, fmt.Sprintf(
		"Messages: %d total (%d user, %d assistant, %d tool calls, %d tool results)",
		stats.TotalMessages, stats.UserMessages, stats.AssistantMessages, stats.ToolCalls, stats.ToolResults,
	))
	lines = append(lines, fmt.Sprintf(
		"Tokens: %d total (%d input, %d output, %d cache read, %d cache write)",
		stats.Tokens.Total, stats.Tokens.Input, stats.Tokens.Output, stats.Tokens.CacheRead, stats.Tokens.CacheWrite,
	))
	lines = append(lines, fmt.Sprintf("Cost: $%.4f", stats.Cost))
	if stats.ContextUsage != nil {
		tok, hasTok := stats.ContextUsage.Tokens.Get()
		pct, hasPct := stats.ContextUsage.Percent.Get()
		if hasTok && hasPct {
			lines = append(lines, fmt.Sprintf("Context: %d / %d tokens (%.1f%%)", tok, stats.ContextUsage.ContextWindow, pct))
		} else {
			lines = append(lines, fmt.Sprintf("Context window: %d tokens", stats.ContextUsage.ContextWindow))
		}
	}

	content := strings.Join(lines, "\n")
	m.imPostUI(func() {
		chat := m.chatContainer
		if chat == nil {
			m.ShowStatus(content)
			return
		}
		th := theme.CurrentTheme()
		pad := m.imOutputPad()
		chat.AddChild(tui.NewSpacer(1))
		chat.AddChild(components.NewDynamicBorder())
		chat.AddChild(tui.NewText(th.Fg(theme.FgAccent, th.Bold("Session Info")), pad, 0, nil))
		chat.AddChild(tui.NewText(th.Fg(theme.FgText, content), pad, 0, nil))
		chat.AddChild(components.NewDynamicBorder())
		m.imRequestRender()
	})
}

func (m *InteractiveMode) imcmdHandleChangelogCommand() {
	changelogPath := utils.GetChangelogPath()
	if changelogPath == "" {
		m.imPostUI(func() {
			m.ShowWarning("No changelog found")
		})
		return
	}
	entries, err := utils.ParseChangelog(changelogPath)
	if err != nil {
		m.imPostUI(func() {
			m.ShowError(fmt.Sprintf("Failed to read changelog: %v", err))
		})
		return
	}
	if len(entries) == 0 {
		m.imPostUI(func() {
			m.ShowStatus("Changelog is empty")
		})
		return
	}

	limit := min(len(entries), 10)
	formatted := m.imcFormatChangelogEntries(entries[:limit])
	if formatted == "" {
		return
	}

	m.imPostUI(func() {
		chat := m.chatContainer
		if chat == nil {
			return
		}
		chat.AddChild(tui.NewSpacer(1))
		chat.AddChild(components.NewDynamicBorder())
		chat.AddChild(tui.NewMarkdown(formatted, 1, 1, theme.GetMarkdownTheme(), nil, nil))
		chat.AddChild(components.NewDynamicBorder())
		m.imRequestRender()
	})
}

func (m *InteractiveMode) imcmdHandleHotkeysCommand() {
	m.imPostUI(func() {
		chat := m.chatContainer
		if chat == nil {
			return
		}
		kb := m.keybindings
		th := theme.CurrentTheme()
		pad := m.imOutputPad()

		type hotkeyRow struct {
			key  string
			desc string
		}
		actions := []struct {
			action tui.Keybinding
			desc   string
		}{
			{core.KeybindingAppInterrupt, "Cancel/abort current generation or command"},
			{core.KeybindingAppClear, "Clear editor"},
			{core.KeybindingAppExit, "Exit when editor is empty"},
			{core.KeybindingAppSuspend, "Suspend to background"},
			{core.KeybindingAppThinkingCycle, "Cycle thinking level"},
			{core.KeybindingAppThinkingSave, "Save thinking level as default"},
			{core.KeybindingAppModelCycleForward, "Cycle to next model"},
			{core.KeybindingAppModelCycleBackward, "Cycle to previous model"},
			{core.KeybindingAppModelSelect, "Open model selector"},
			{core.KeybindingAppToolsExpand, "Toggle tool output expansion"},
			{core.KeybindingAppThinkingToggle, "Toggle thinking block visibility"},
			{core.KeybindingAppEditorExternal, "Open external editor"},
			{core.KeybindingAppMessageCopy, "Copy selection or last assistant message"},
			{core.KeybindingAppMessageFollowUp, "Queue follow-up message"},
			{core.KeybindingAppMessageDequeue, "Edit queued messages in editor"},
			{core.KeybindingAppClipboardPasteImage, "Paste image, file path, or text from clipboard"},
			{core.KeybindingAppSessionNew, "Start new session"},
			{core.KeybindingAppSessionTree, "Open session tree selector"},
			{core.KeybindingAppSessionFork, "Fork from a previous user message"},
			{core.KeybindingAppSessionResume, "Resume a different session"},
		}

		var lines []string
		lines = append(lines, th.Fg(theme.FgAccent, th.Bold("Keyboard Shortcuts")))
		for _, item := range actions {
			k := imhAppKeyDisplay(kb, item.action)
			lines = append(lines, fmt.Sprintf("  %-18s %s", th.Fg(theme.FgAccent, k), th.Fg(theme.FgText, item.desc)))
		}

		if runner := m.imExtensionRunner(); runner != nil {
			var cfg tui.KeybindingsConfig
			if kb != nil {
				cfg = kb.GetEffectiveConfig()
			}
			shortcuts := runner.GetShortcuts(cfg)
			if len(shortcuts) > 0 {
				var keys []string
				for k := range shortcuts {
					keys = append(keys, string(k))
				}
				sort.Strings(keys)
				lines = append(lines, "")
				lines = append(lines, th.Fg(theme.FgAccent, th.Bold("Extension Shortcuts")))
				for _, k := range keys {
					sc := shortcuts[tui.KeyId(k)]
					desc := imhDerefStr(sc.Description)
					lines = append(lines, fmt.Sprintf("  %-18s %s", th.Fg(theme.FgAccent, k), th.Fg(theme.FgText, desc)))
				}
			}
		}

		chat.AddChild(tui.NewSpacer(1))
		chat.AddChild(components.NewDynamicBorder())
		chat.AddChild(tui.NewText(strings.Join(lines, "\n"), pad, 0, nil))
		chat.AddChild(components.NewDynamicBorder())
		m.imRequestRender()
	})
}

func (m *InteractiveMode) imcmdHandleCloneCommand(ctx context.Context) {
	session := m.imSession()
	if m.runtime == nil || session == nil || session.SessionManager == nil {
		return
	}
	m.imcBeginSessionMutation()
	leafID := session.SessionManager.GetLeafId()
	if leafID == nil || strings.TrimSpace(*leafID) == "" {
		m.imcEndSessionMutation()
		m.imPostUI(func() {
			m.ShowWarning("Nothing to clone in an empty session")
		})
		return
	}

	res, err := m.runtime.Fork(ctx, *leafID, &core.ForkSessionOptions{Position: "at"})
	m.imcEndSessionMutation()
	if err != nil {
		m.imPostUI(func() {
			m.ShowError(fmt.Sprintf("Clone failed: %v", err))
		})
		return
	}
	if res != nil && res.Cancelled {
		m.imPostUI(func() {
			m.ShowStatus("Clone cancelled")
		})
		return
	}

	m.imCallUI(ctx, func() {
		m.imsbRebindCurrentSession(ctx, &imsbRebindOptions{
			ShowInitialMessages: true,
		})
		m.ShowStatus("Cloned session at current position")
	})
}

func (m *InteractiveMode) imcmdHandleNewSessionCommand() error {
	if m.runtime == nil {
		return nil
	}
	ctx := m.bgCtx
	m.imcBeginSessionMutation()
	res, err := m.runtime.NewSession(ctx)
	m.imcEndSessionMutation()
	if err != nil {
		m.imPostUI(func() {
			m.ShowError(fmt.Sprintf("Failed to start new session: %v", err))
		})
		return err
	}
	if res != nil && res.Cancelled {
		m.imPostUI(func() {
			m.ShowStatus("New session cancelled")
		})
		return nil
	}

	m.imCallUI(ctx, func() {
		m.imsbRebindCurrentSession(ctx, &imsbRebindOptions{
			ShowInitialMessages: true,
		})
		m.ShowStatus("Started new session")
	})
	return nil
}

func (m *InteractiveMode) imcmdHandleCompactCommand(ctx context.Context, customInstructions string) {
	session := m.imSession()
	if session == nil {
		return
	}
	if session.IsCompacting() {
		m.imPostUI(func() {
			m.ShowWarning("Compaction is already running")
		})
		return
	}

	compactCtx, cancel := context.WithCancel(ctx)
	m.mu.Lock()
	m.compactionCancel = cancel
	m.compactionEscapeHandler = func() {
		cancel()
		session.AbortCompaction()
	}
	m.mu.Unlock()

	defer func() {
		cancel()
		m.mu.Lock()
		m.compactionCancel = nil
		m.compactionEscapeHandler = nil
		m.mu.Unlock()
	}()

	var args []string
	if strings.TrimSpace(customInstructions) != "" {
		args = append(args, customInstructions)
	}
	m.imcBeginSessionMutation()
	_, compactErr := session.Compact(compactCtx, args...)
	m.imcEndSessionMutation()
	if compactErr != nil {
		m.imPostUI(func() {
			m.ShowError(fmt.Sprintf("Compaction failed: %v", compactErr))
		})
	}
}

func (m *InteractiveMode) imcmdHandleReloadCommand(ctx context.Context) {
	if err := m.imlcReloadRuntime(ctx); err != nil {
		m.imPostUI(func() {
			m.ShowError(fmt.Sprintf("Reload failed: %v", err))
		})
		return
	}
	m.imPostUI(func() {
		m.ShowLoadedResources(&ShowLoadedResourcesOptions{
			Force:                     true,
			ShowDiagnosticsWhenSilent: true,
		})
		m.ShowStatus("Reloaded keybindings, extensions, skills, prompts, and themes")
	})
}

func (m *InteractiveMode) imcmdHandleBugCommand(ctx context.Context, hint string) {
	session := m.imSession()
	if session == nil {
		return
	}

	var edComp tui.Component
	if c, ok := m.editor.(tui.Component); ok {
		edComp = c
	}
	var hintPtr *string
	if strings.TrimSpace(hint) != "" {
		h := strings.TrimSpace(hint)
		hintPtr = &h
	}

	ReportBug(ctx, &BugReportContext{
		Session:         session,
		UI:              m.imCurrentUI(),
		EditorContainer: m.editorContainer,
		Editor:          edComp,
		Keybindings:     m.keybindings,
		ShowStatus: func(msg string) {
			m.imPostUI(func() { m.ShowStatus(msg) })
		},
		ShowError: func(msg string) {
			m.imPostUI(func() { m.ShowError(msg) })
		},
	}, hintPtr)
}

func (m *InteractiveMode) imcmdHandleDebugCommand() {
	ui := m.imCurrentUI()
	session := m.imSession()
	sm := m.imSessionManager()

	var cols, rows int
	if ui != nil && ui.Terminal() != nil {
		cols = ui.Terminal().Columns()
		rows = ui.Terminal().Rows()
	}

	var baseLines []string
	baseLines = append(baseLines, fmt.Sprintf("UI mode: %s | Terminal: %dx%d", string(m.imUiMode()), cols, rows))
	if session != nil {
		modelID := "(none)"
		if mod := session.Model(); mod != nil {
			modelID = string(mod.Provider) + "/" + mod.Id
		}
		baseLines = append(baseLines, fmt.Sprintf(
			"Model: %s | Thinking: %s | Streaming: %v | Compacting: %v",
			modelID, string(session.ThinkingLevel()), session.IsStreaming(), session.IsCompacting(),
		))
	}
	if sm != nil {
		m.imcBeginSessionMutation()
		leaf := imhDerefStr(sm.GetLeafId())
		if leaf == "" {
			leaf = "(none)"
		}
		baseLines = append(baseLines, fmt.Sprintf(
			"Session: %s | Branch entries: %d | Total entries: %d | Leaf: %s",
			sm.GetSessionId(), len(sm.GetBranch()), len(sm.GetEntries()), leaf,
		))
		if sf := sm.GetSessionFile(); sf != nil {
			baseLines = append(baseLines, "File: "+imhNormalizePathForDisplay(*sf))
		}
		m.imcEndSessionMutation()
	}

	m.imPostUI(func() {
		lines := make([]string, len(baseLines))
		copy(lines, baseLines)
		if curUI := m.imCurrentUI(); curUI != nil {
			lines = append(lines, fmt.Sprintf("Full redraws: %d | Overlay: %v", curUI.FullRedraws(), curUI.HasOverlay()))
		}
		chat := m.chatContainer
		if chat != nil {
			lines = append(lines, fmt.Sprintf("Chat children: %d", len(chat.Children())))
		}
		lines = append(lines, fmt.Sprintf("PID: %d", os.Getpid()))
		debugText := strings.Join(lines, "\n")
		if chat == nil {
			m.ShowStatus(debugText)
			return
		}
		th := theme.CurrentTheme()
		pad := m.imOutputPad()
		chat.AddChild(tui.NewSpacer(1))
		chat.AddChild(components.NewDynamicBorder())
		chat.AddChild(tui.NewText(th.Fg(theme.FgAccent, th.Bold("Debug Info")), pad, 0, nil))
		chat.AddChild(tui.NewText(th.Fg(theme.FgDim, debugText), pad, 0, nil))
		chat.AddChild(components.NewDynamicBorder())
		m.imRequestRender()
	})
}

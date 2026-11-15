package interactive

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/components"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/tui"
)

func (m *InteractiveMode) iminSetupAutocomplete() {
	m.mu.Lock()
	ed := m.editor
	fdPath := m.fdPath
	customFactory := m.customAutocompleteFactory
	m.mu.Unlock()

	if ed == nil {
		return
	}

	var commands []tui.AutocompleteCommand
	seen := make(map[string]struct{})

	for _, cmd := range core.BuiltinSlashCommands {
		if cmd.Name == "" || cmd.Name == "mcp" {
			continue
		}
		if _, ok := seen[cmd.Name]; ok {
			continue
		}
		seen[cmd.Name] = struct{}{}
		desc := cmd.Description
		sc := &tui.SlashCommand{
			Name:         cmd.Name,
			Description:  &desc,
			ArgumentHint: imhCloneStrPtr(cmd.ArgumentHint),
		}
		switch cmd.Name {
		case "thinking":
			sc.GetArgumentCompletions = m.iminThinkingArgumentCompletions
		case "theme":
			sc.GetArgumentCompletions = m.iminThemeArgumentCompletions
		case "model":
			sc.GetArgumentCompletions = m.iminModelArgumentCompletions
		}
		commands = append(commands, sc)
	}

	if runner := m.imExtensionRunner(); runner != nil {
		for _, rc := range runner.GetRegisteredCommands() {
			if rc.Name == "" {
				continue
			}
			if _, ok := seen[rc.Name]; ok {
				continue
			}
			seen[rc.Name] = struct{}{}
			rcCopy := rc
			sc := &tui.SlashCommand{
				Name:        rcCopy.InvocationName,
				Description: imhCloneStrPtr(rcCopy.Description),
			}
			if rcCopy.InvocationName == "" {
				sc.Name = rcCopy.Name
			}
			if rcCopy.GetArgumentCompletions != nil {
				sc.GetArgumentCompletions = func(argPrefix string) ([]tui.AutocompleteItem, error) {
					return rcCopy.GetArgumentCompletions(m.bgCtx, argPrefix)
				}
			}
			commands = append(commands, sc)
		}
	}

	session := m.imSession()
	if session != nil {
		for _, pt := range session.PromptTemplates() {
			if pt.Name == "" {
				continue
			}
			if _, ok := seen[pt.Name]; ok {
				continue
			}
			seen[pt.Name] = struct{}{}
			desc := pt.Description
			if desc != "" && pt.SourceInfo.Source != "" {
				desc = fmt.Sprintf("(%s) %s", pt.SourceInfo.Source, desc)
			}
			var descPtr *string
			if desc != "" {
				descPtr = &desc
			}
			commands = append(commands, &tui.SlashCommand{
				Name:         pt.Name,
				Description:  descPtr,
				ArgumentHint: imhCloneStrPtr(pt.ArgumentHint),
			})
		}
	}

	sm := m.imSettingsManager()
	rl := m.imResourceLoader()
	if (sm == nil || sm.GetEnableSkillCommands()) && rl != nil {
		skillsRes := rl.GetSkills()
		for _, sk := range skillsRes.Skills {
			if sk.Name == "" {
				continue
			}
			cmdName := "skill:" + sk.Name
			if _, ok := seen[cmdName]; ok {
				continue
			}
			seen[cmdName] = struct{}{}
			desc := sk.Description
			var descPtr *string
			if desc != "" {
				descPtr = &desc
			}
			commands = append(commands, &tui.SlashCommand{
				Name:        cmdName,
				Description: descPtr,
			})
		}
	}

	baseProvider := tui.NewCombinedAutocompleteProvider(commands, m.imCwd(), fdPath)
	var finalProvider tui.AutocompleteProvider = baseProvider
	if customFactory != nil {
		if wrapped := customFactory(baseProvider); wrapped != nil {
			finalProvider = wrapped
		}
	}

	ed.SetAutocompleteProvider(finalProvider)
	m.mu.Lock()
	m.autocompleteProvider = finalProvider
	m.mu.Unlock()
}

func (m *InteractiveMode) iminThinkingArgumentCompletions(prefix string) ([]tui.AutocompleteItem, error) {
	session := m.imSession()
	if session == nil {
		return nil, nil
	}
	levels := session.GetAvailableThinkingLevels()
	if len(levels) == 0 {
		return nil, nil
	}
	lower := strings.ToLower(strings.TrimSpace(prefix))
	var items []tui.AutocompleteItem
	for _, lvl := range levels {
		s := string(lvl)
		if lower == "" || strings.HasPrefix(strings.ToLower(s), lower) {
			items = append(items, tui.AutocompleteItem{Value: s, Label: s})
		}
	}
	return items, nil
}

func (m *InteractiveMode) iminThemeArgumentCompletions(prefix string) ([]tui.AutocompleteItem, error) {
	themes := theme.GetAvailableThemesWithPaths()
	lower := strings.ToLower(strings.TrimSpace(prefix))
	var items []tui.AutocompleteItem
	for _, t := range themes {
		if lower == "" || strings.HasPrefix(strings.ToLower(t.Name), lower) {
			var desc *string
			if t.Path != "" {
				d := t.Path
				desc = &d
			}
			items = append(items, tui.AutocompleteItem{Value: t.Name, Label: t.Name, Description: desc})
		}
	}
	return items, nil
}

func (m *InteractiveMode) iminModelArgumentCompletions(prefix string) ([]tui.AutocompleteItem, error) {
	mr := m.imModelRuntime()
	if mr == nil {
		return nil, nil
	}
	models := mr.GetModels(nil)
	lower := strings.ToLower(strings.TrimSpace(prefix))
	var items []tui.AutocompleteItem
	for _, mod := range models {
		if mod == nil {
			continue
		}
		full := string(mod.Provider) + "/" + mod.Id
		if lower == "" || strings.Contains(strings.ToLower(full), lower) || strings.Contains(strings.ToLower(mod.Name), lower) {
			desc := mod.Name
			items = append(items, tui.AutocompleteItem{Value: full, Label: full, Description: &desc})
		}
		if len(items) >= 30 {
			break
		}
	}
	return items, nil
}

func (m *InteractiveMode) iminSetupKeyHandlers() {
	m.mu.Lock()
	customEd := m.defaultEditor
	m.mu.Unlock()

	if customEd == nil {
		return
	}

	customEd.OnEscape = m.iminHandleEscape
	customEd.OnCtrlD = func() {
		m.imcRequestShutdown()
	}
	customEd.OnPasteImage = m.iminPasteFromClipboard
	customEd.OnExtensionShortcut = m.iminHandleExtensionShortcut

	customEd.OnAction(core.KeybindingAppClear, m.iminHandleCtrlC)
	customEd.OnAction(core.KeybindingAppSuspend, m.iminSuspendProcess)
	customEd.OnAction(core.KeybindingAppThinkingCycle, m.iminCycleThinkingLevel)
	customEd.OnAction(core.KeybindingAppThinkingSave, m.iminSaveThinkingLevel)
	customEd.OnAction(core.KeybindingAppModelCycleForward, func() {
		m.imSpawn(func() { m.iminCycleModel("forward") })
	})
	customEd.OnAction(core.KeybindingAppModelCycleBackward, func() {
		m.imSpawn(func() { m.iminCycleModel("backward") })
	})
	customEd.OnAction(core.KeybindingAppModelSelect, func() {
		m.imSpawn(func() { m.imselShowModelSelector("") })
	})
	customEd.OnAction(core.KeybindingAppToolsExpand, m.iminToggleToolOutput)
	customEd.OnAction(core.KeybindingAppThinkingToggle, m.iminToggleThinkingBlock)
	customEd.OnAction(core.KeybindingAppEditorExternal, func() {
		m.imSpawn(m.iminOpenExternalEditor)
	})
	customEd.OnAction(core.KeybindingAppMessageCopy, func() {
		m.imSpawn(m.iminCopySelectionOrLastAssistantMessage)
	})
	customEd.OnAction(core.KeybindingAppMessageFollowUp, m.iminHandleFollowUpKey)
	customEd.OnAction(core.KeybindingAppMessageDequeue, m.imqHandleDequeue)
	customEd.OnAction(core.KeybindingAppSessionNew, func() {
		m.imSpawn(func() { _ = m.imcmdHandleNewSessionCommand() })
	})
	customEd.OnAction(core.KeybindingAppSessionTree, func() {
		m.imSpawn(func() { m.imselShowTreeSelector() })
	})
	customEd.OnAction(core.KeybindingAppSessionFork, func() {
		m.imSpawn(m.imselShowUserMessageSelector)
	})
	customEd.OnAction(core.KeybindingAppSessionResume, func() {
		m.imSpawn(m.imselShowSessionSelector)
	})
}

func (m *InteractiveMode) iminHandleExtensionShortcut(data string) bool {
	runner := m.imExtensionRunner()
	if runner == nil {
		return false
	}
	var cfg tui.KeybindingsConfig
	if m.keybindings != nil {
		cfg = m.keybindings.GetEffectiveConfig()
	}
	shortcuts := runner.GetShortcuts(cfg)
	for keyID, sc := range shortcuts {
		if tui.MatchesKey(data, keyID) && sc.Handler != nil {
			handler := sc.Handler
			m.imSpawn(func() {
				if err := handler(m.bgCtx, runner.CreateContext()); err != nil {
					m.imsbShowExtensionError(core.ExtensionError{
						ExtensionPath: sc.ExtensionPath,
						Event:         "shortcut",
						Error:         err.Error(),
					})
				}
			})
			return true
		}
	}
	return false
}

func (m *InteractiveMode) iminHandleEscape() {
	m.mu.Lock()
	bashHandler := m.bashEscapeHandler
	compHandler := m.compactionEscapeHandler
	retryHandler := m.retryEscapeHandler
	ed := m.editor
	m.mu.Unlock()

	if bashHandler != nil {
		bashHandler()
		return
	}
	if compHandler != nil {
		compHandler()
		return
	}
	if retryHandler != nil {
		retryHandler()
		return
	}

	session := m.imSession()
	if session != nil && session.IsStreaming() {
		m.imSpawn(func() {
			_ = session.Abort(m.bgCtx)
		})
		return
	}

	if ed != nil && strings.TrimSpace(ed.GetText()) == "" {
		sm := m.imSettingsManager()
		action := core.DoubleEscapeActionTree
		if sm != nil {
			action = sm.GetDoubleEscapeAction()
		}
		if action == core.DoubleEscapeActionNone {
			return
		}

		now := time.Now()
		m.mu.Lock()
		elapsed := now.Sub(m.lastEscapeTime)
		if !m.lastEscapeTime.IsZero() && elapsed < 500*time.Millisecond {
			m.lastEscapeTime = time.Time{}
			if m.doubleEscapeConfirmTimer != nil {
				m.doubleEscapeConfirmTimer.Stop()
				m.doubleEscapeConfirmTimer = nil
			}
			m.mu.Unlock()

			switch action {
			case core.DoubleEscapeActionFork:
				m.imSpawn(m.imselShowUserMessageSelector)
			default:
				m.imSpawn(func() { m.imselShowTreeSelector() })
			}
			return
		}

		m.lastEscapeTime = now
		if m.doubleEscapeConfirmTimer != nil {
			m.doubleEscapeConfirmTimer.Stop()
		}
		m.doubleEscapeConfirmTimer = time.AfterFunc(500*time.Millisecond, func() {
			m.mu.Lock()
			m.lastEscapeTime = time.Time{}
			m.doubleEscapeConfirmTimer = nil
			m.mu.Unlock()
		})
		m.mu.Unlock()
	}
}

// ClearEditor clears the prompt input buffer and resets bash-mode border state.
func (m *InteractiveMode) ClearEditor() {
	m.mu.Lock()
	ed := m.editor
	m.mu.Unlock()
	if ed != nil {
		ed.SetText("")
	}
	m.iminUpdateBashModeFromEditorText("")
	m.imRequestRender()
}

func (m *InteractiveMode) iminHandleCtrlC() {
	now := time.Now()
	m.mu.Lock()
	prev := m.lastSigintTime
	withinWindow := !prev.IsZero() && now.Sub(prev) < 500*time.Millisecond
	if withinWindow {
		m.lastSigintTime = time.Time{}
	} else {
		m.lastSigintTime = now
	}
	m.mu.Unlock()

	if withinWindow {
		m.imcRequestShutdown()
		return
	}
	m.ClearEditor()
}

func (m *InteractiveMode) iminSetupEditorSubmitHandler() {
	m.mu.Lock()
	ed := m.editor
	m.mu.Unlock()
	if ed == nil {
		return
	}

	imhSetEditorOnChange(ed, func(text string) {
		m.iminUpdateBashModeFromEditorText(text)
	})

	imhSetEditorOnSubmit(ed, func(text string) {
		trimmed := strings.TrimSpace(text)
		if trimmed == "" {
			return
		}

		if text != "" {
			ed.AddToHistory(text)
		}
		ed.SetText("")
		m.iminUpdateBashModeFromEditorText("")

		m.imSpawn(func() {
			m.iminProcessSubmittedText(m.bgCtx, text)
		})
	})
}

func (m *InteractiveMode) iminUpdateBashModeFromEditorText(text string) {
	isBash := strings.HasPrefix(text, "!")
	m.mu.Lock()
	changed := m.isBashMode != isBash
	m.isBashMode = isBash
	m.mu.Unlock()
	if changed {
		m.imlcUpdateEditorBorderColor()
		m.imRequestRender()
	}
}

func (m *InteractiveMode) iminProcessSubmittedText(ctx context.Context, text string) {
	if m.imIsStopped() {
		return
	}
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return
	}
	if ctx == nil {
		ctx = m.bgCtx
	}

	session := m.imSession()
	if session == nil {
		return
	}

	// Extension "input" hook gets first shot at raw input (including bash & commands).
	if runner := m.imExtensionRunner(); runner != nil {
		res := runner.EmitInput(ctx, text, nil, core.InputSourceInteractive)
		if res.Action == "handled" {
			return
		}
		if res.Action == "transform" {
			text = res.Text
			trimmed = strings.TrimSpace(text)
			if trimmed == "" {
				return
			}
		}
	}

	// Shell escape commands: "!" (included in LLM context) or "!!" (excluded).
	if strings.HasPrefix(trimmed, "!!") || strings.HasPrefix(trimmed, "!") {
		exclude := strings.HasPrefix(trimmed, "!!")
		cmd := strings.TrimSpace(strings.TrimPrefix(trimmed, "!!"))
		if !exclude {
			cmd = strings.TrimSpace(strings.TrimPrefix(trimmed, "!"))
		}
		if cmd == "" {
			return
		}
		if session.IsBashRunning() {
			m.imPostUI(func() {
				m.ShowWarning("A bash command is already running. Press Esc to cancel it first.")
			})
			return
		}
		m.imcBeginSessionMutation()
		m.iminExecuteBashCommand(ctx, cmd, exclude)
		m.imcEndSessionMutation()
		return
	}

	// Built-in & extension slash commands.
	if strings.HasPrefix(trimmed, "/") {
		handled, err := m.imcmdHandleSlashCommand(ctx, trimmed)
		if err != nil {
			m.imPostUI(func() {
				m.ShowError(imhErrorMessage(err))
			})
			return
		}
		if handled {
			return
		}
	}

	// Expand skill command or prompt template before sending to session.
	submitText := text
	if session.IsCompacting() {
		m.imPostUI(func() {
			m.imqQueueCompactionMessage(submitText, "steer")
		})
		return
	}

	if session.IsStreaming() {
		if _, err := session.Steer(ctx, submitText, nil, &core.QueueInputOptions{Source: core.InputSourceInteractive}); err != nil {
			m.imPostUI(func() {
				m.ShowError(imhErrorMessage(err))
			})
		}
		return
	}

	m.imcBeginSessionMutation()
	err := session.Prompt(ctx, submitText, &core.PromptOptions{Source: core.InputSourceInteractive})
	m.imcEndSessionMutation()
	if err != nil {
		m.imPostUI(func() {
			m.ShowError(imhErrorMessage(err))
		})
	}
}

func (m *InteractiveMode) iminHandleFollowUpKey() {
	m.mu.Lock()
	ed := m.editor
	m.mu.Unlock()
	if ed == nil {
		return
	}
	text := ed.GetText()
	if strings.TrimSpace(text) == "" {
		return
	}
	ed.AddToHistory(text)
	ed.SetText("")
	m.iminUpdateBashModeFromEditorText("")

	m.imSpawn(func() {
		m.imqHandleFollowUpSubmission(m.bgCtx, text)
	})
}

func (m *InteractiveMode) iminExecuteBashCommand(ctx context.Context, command string, excludeFromContext bool) {
	session := m.imSession()
	if session == nil {
		return
	}
	if ctx == nil {
		ctx = m.bgCtx
	}

	bashCtx, cancel := context.WithCancel(ctx)
	var bc *components.BashExecutionComponent

	m.imCallUI(ctx, func() {
		m.mu.Lock()
		chat := m.chatContainer
		expanded := m.toolExpanded
		m.mu.Unlock()

		ui := m.imCurrentUI()
		bc = components.NewBashExecutionComponent(command, ui, excludeFromContext, m.imOutputPad())
		bc.SetExpanded(expanded)
		if chat != nil {
			chat.AddChild(bc)
		}

		m.mu.Lock()
		m.bashComponent = bc
		m.bashCancel = cancel
		m.bashEscapeHandler = func() {
			cancel()
			session.AbortBash()
		}
		m.mu.Unlock()
		m.imRequestRender()
	})

	defer func() {
		cancel()
		m.imPostUI(func() {
			m.mu.Lock()
			if m.bashComponent == bc {
				m.bashComponent = nil
				m.bashCancel = nil
				m.bashEscapeHandler = nil
			}
			m.mu.Unlock()
		})
	}()

	res, err := session.ExecuteBash(bashCtx, command, func(chunk string) {
		if bc == nil || chunk == "" {
			return
		}
		m.imPostUI(func() {
			bc.AppendOutput(chunk)
			m.imRequestRender()
		})
	}, &core.ExecuteBashOptions{
		ExcludeFromContext: excludeFromContext,
	})

	if err != nil {
		m.imPostUI(func() {
			if bc != nil {
				if res.Output != "" && bc.GetOutput() == "" {
					bc.AppendOutput(res.Output)
				}
				var trunc *core.TruncationResult
				if res.Truncated {
					trunc = &core.TruncationResult{Truncated: true}
				}
				bc.SetComplete(res.ExitCode, res.Cancelled, trunc, res.FullOutputPath)
			}
			m.ShowError(imhErrorMessage(err))
			m.imRequestRender()
		})
		return
	}

	m.imPostUI(func() {
		if bc != nil {
			var trunc *core.TruncationResult
			if res.Truncated {
				trunc = &core.TruncationResult{Truncated: true}
			}
			bc.SetComplete(res.ExitCode, res.Cancelled, trunc, res.FullOutputPath)
			m.imRequestRender()
		}
	})
}

func (m *InteractiveMode) iminSuspendProcess() {
	if runtime.GOOS == "windows" {
		return
	}

	sigTSTP := iminPlatformSIGTSTP()
	sigCONT := iminPlatformSIGCONT()

	ui := m.imCurrentUI()
	if ui != nil {
		ui.Stop(nil)
	}

	contCh := make(chan os.Signal, 1)
	signal.Notify(contCh, sigCONT)
	defer signal.Stop(contCh)

	signal.Reset(sigTSTP)
	if proc, err := os.FindProcess(os.Getpid()); err == nil {
		_ = proc.Signal(sigTSTP)
	}

	select {
	case <-contCh:
	case <-time.After(150 * time.Millisecond):
	}

	if ui != nil {
		ui.Start()
		ui.RequestRender(imhBoolPtr(true))
	}
}

func iminPlatformSIGTSTP() syscall.Signal {
	if runtime.GOOS == "linux" {
		return syscall.Signal(20)
	}
	return syscall.Signal(18)
}

func iminPlatformSIGCONT() syscall.Signal {
	if runtime.GOOS == "linux" {
		return syscall.Signal(18)
	}
	return syscall.Signal(19)
}

func (m *InteractiveMode) iminCycleThinkingLevel() {
	session := m.imSession()
	if session == nil {
		return
	}
	if !session.SupportsThinking() {
		m.imPostUI(func() {
			m.ShowStatus("Current model does not support thinking levels")
		})
		return
	}
	m.imcBeginSessionMutation()
	next := session.CycleThinkingLevel()
	m.imcEndSessionMutation()
	if next == nil {
		return
	}
	m.imPostUI(func() {
		m.imlcUpdateEditorBorderColor()
		m.imlcRefreshFooterSync(false)
		m.ShowStatus(fmt.Sprintf("Thinking level: %s", string(*next)))
	})
}

func (m *InteractiveMode) iminSaveThinkingLevel() {
	session := m.imSession()
	sm := m.imSettingsManager()
	if session == nil || sm == nil {
		return
	}
	model := session.Model()
	level := session.ThinkingLevel()
	if model == nil {
		sm.SetDefaultThinkingLevel(level)
		m.ShowStatus(fmt.Sprintf("Saved default thinking level: %s", string(level)))
		return
	}
	sm.SetModelThinkingLevel(string(model.Provider), model.Id, level)
	m.ShowStatus(fmt.Sprintf("Saved thinking level %s for %s/%s", string(level), string(model.Provider), model.Id))
}

func (m *InteractiveMode) iminCycleModel(direction string) {
	session := m.imSession()
	if session == nil {
		return
	}
	m.imcBeginSessionMutation()
	res, err := session.CycleModel(m.bgCtx, direction)
	m.imcEndSessionMutation()
	if err != nil {
		m.imPostUI(func() {
			m.ShowError(imhErrorMessage(err))
		})
		return
	}
	m.imPostUI(func() {
		if res == nil || res.Model == nil {
			m.ShowStatus("No other available models to cycle to")
			return
		}
		m.imlcUpdateEditorBorderColor()
		m.imlcRefreshFooterSync(false)
		m.imcReportProgramStatus()
		msg := fmt.Sprintf("Switched model to %s/%s", res.Model.Provider, res.Model.Id)
		if res.ThinkingLevel != "" {
			msg += fmt.Sprintf(" (thinking: %s)", string(res.ThinkingLevel))
		}
		m.ShowStatus(msg)
	})
}

func (m *InteractiveMode) iminToggleToolOutput() {
	m.mu.Lock()
	m.toolExpanded = !m.toolExpanded
	expanded := m.toolExpanded
	chat := m.chatContainer
	m.mu.Unlock()

	if chat != nil {
		for _, child := range chat.Children() {
			if exp, ok := imhIsExpandable(child); ok {
				exp.SetExpanded(expanded)
			}
		}
	}

	state := "collapsed"
	if expanded {
		state = "expanded"
	}
	m.ShowStatus("Tool outputs " + state)
}

func (m *InteractiveMode) iminToggleThinkingBlock() {
	m.mu.Lock()
	m.hideThinkingBlock = !m.hideThinkingBlock
	hide := m.hideThinkingBlock
	chat := m.chatContainer
	m.mu.Unlock()

	if sm := m.imSettingsManager(); sm != nil {
		sm.SetHideThinkingBlock(hide)
	}
	if chat != nil {
		for _, child := range chat.Children() {
			if am, ok := child.(*components.AssistantMessageComponent); ok && am != nil {
				am.SetHideThinkingBlock(hide)
			}
		}
	}

	if hide {
		m.ShowStatus("Thinking blocks hidden")
	} else {
		m.ShowStatus("Thinking blocks shown")
	}
}

func (m *InteractiveMode) iminOpenExternalEditor() {
	sm := m.imSettingsManager()
	cmdStr := ""
	if sm != nil {
		cmdStr = sm.GetExternalEditorCommand()
	}
	if strings.TrimSpace(cmdStr) == "" {
		cmdStr = os.Getenv("VISUAL")
	}
	if strings.TrimSpace(cmdStr) == "" {
		cmdStr = os.Getenv("EDITOR")
	}
	if strings.TrimSpace(cmdStr) == "" {
		m.imPostUI(func() {
			m.ShowWarning("No external editor configured ($VISUAL or $EDITOR or settings.externalEditor)")
		})
		return
	}

	var currentText string
	m.imCallUI(m.bgCtx, func() {
		if ed := m.editor; ed != nil {
			currentText = ed.GetText()
		}
	})

	ui := m.imCurrentUI()
	if ui != nil {
		m.imCallUI(m.bgCtx, func() {
			ui.Stop(nil)
		})
	}

	result := components.EditInExternalEditor(components.ExternalEditorOptions{
		Command: cmdStr,
		Content: currentText,
	})

	if ui != nil {
		m.imCallUI(m.bgCtx, func() {
			ui.Start()
			ui.RequestRender(imhBoolPtr(true))
		})
	}

	m.imPostUI(func() {
		if result.Status != components.ExternalEditorStatusComplete {
			msg := result.Message
			if msg == "" {
				msg = "External editor failed"
			}
			m.ShowError(msg)
			return
		}
		if m.editor != nil {
			m.editor.SetText(result.Content)
		}
		m.imRequestRender()
	})
}

func (m *InteractiveMode) iminCopySelectionOrLastAssistantMessage() {
	ui := m.imCurrentUI()
	if selUI, ok := ui.(interface {
		HasActiveSelection() bool
		CopyActiveSelectionToClipboard() (bool, error)
	}); ok && selUI != nil && selUI.HasActiveSelection() {
		var copied bool
		var copyErr error
		m.imCallUI(m.bgCtx, func() {
			copied, copyErr = selUI.CopyActiveSelectionToClipboard()
		})
		m.imPostUI(func() {
			if copyErr != nil {
				m.ShowError(imhErrorMessage(copyErr))
				return
			}
			if copied {
				m.ShowStatus("Copied selection to clipboard")
			}
		})
		return
	}

	session := m.imSession()
	if session == nil {
		return
	}
	last := session.GetLastAssistantText()
	if last == nil || strings.TrimSpace(*last) == "" {
		m.imPostUI(func() {
			m.ShowWarning("No assistant message to copy")
		})
		return
	}
	if err := utils.CopyToClipboard(*last); err != nil {
		m.imPostUI(func() {
			m.ShowError(imhErrorMessage(err))
		})
		return
	}
	m.imPostUI(func() {
		m.ShowStatus("Copied last assistant message to clipboard")
	})
}

func (m *InteractiveMode) iminPasteFromClipboard() {
	sm := m.imSettingsManager()

	// 1. Check clipboard file paths first (Finder/Explorer copied files).
	if paths, err := utils.ReadClipboardFilePaths(); err == nil && len(paths) > 0 {
		m.iminInsertFilePathsToEditor(paths)
		return
	}

	// 2. Check clipboard image data unless blocked or unsupported.
	if sm == nil || !sm.GetBlockImages() {
		img, err := utils.ReadClipboardImage()
		if err != nil {
			m.ShowWarning(imhErrorMessage(err))
		} else if img != nil && len(img.Bytes) > 0 {
			saveBytes := img.Bytes
			mimeType := img.MimeType
			if sm == nil || sm.GetImageAutoResize() {
				if resized, rErr := utils.ResizeImage(img.Bytes, img.MimeType, nil); rErr == nil && resized != nil {
					if decoded, dErr := base64.StdEncoding.DecodeString(resized.Data); dErr == nil && len(decoded) > 0 {
						saveBytes = decoded
						if resized.MimeType != "" {
							mimeType = resized.MimeType
						}
					}
				}
			}
			ext := utils.ExtensionForImageMimeType(mimeType)
			targetPath := imhParseClipboardImageSavePath(ext)
			if wErr := os.WriteFile(targetPath, saveBytes, 0o600); wErr != nil {
				m.ShowError(fmt.Sprintf("Failed to save clipboard image: %v", wErr))
				return
			}
			m.iminInsertFilePathsToEditor([]string{targetPath})
			return
		}
	} else {
		// Still probe if an image was attempted while images are blocked to inform user cleanly.
		if img, err := utils.ReadClipboardImage(); err == nil && img != nil && len(img.Bytes) > 0 {
			m.ShowWarning("Images are blocked by settings (blockImages)")
			return
		}
	}

	// 3. Fallback to plain text clipboard paste.
	if text := utils.ReadClipboardText(); text != nil && *text != "" {
		m.iminInsertTextToEditor(*text)
		return
	}

	m.ShowStatus("Clipboard does not contain an image, file path, or text")
}

func (m *InteractiveMode) iminInsertFilePathsToEditor(paths []string) {
	m.mu.Lock()
	ed := m.editor
	m.mu.Unlock()
	if ed == nil || len(paths) == 0 {
		return
	}

	var quoted []string
	for _, p := range paths {
		if strings.ContainsAny(p, "\x00\r\n") {
			continue
		}
		quoted = append(quoted, imhQuoteIfNeeded(p))
	}
	if len(quoted) == 0 {
		return
	}

	insert := strings.Join(quoted, " ") + " "
	if needsSpace := m.iminNeedsLeadingSpaceAtCursor(ed); needsSpace {
		insert = " " + insert
	}
	ed.InsertTextAtCursor(insert)
	m.imRequestRender()
}

func (m *InteractiveMode) iminNeedsLeadingSpaceAtCursor(ed imEditor) bool {
	if ed == nil {
		return false
	}
	cp, ok := ed.(interface{ GetCursor() tui.EditorCursor })
	if !ok {
		return false
	}
	lp, ok := ed.(interface{ GetLines() []string })
	if !ok {
		return false
	}
	cur := cp.GetCursor()
	lines := lp.GetLines()
	if cur.Line < 0 || cur.Line >= len(lines) {
		return false
	}
	line := lines[cur.Line]
	if cur.Col <= 0 || cur.Col > len(line) {
		return false
	}
	prev := line[cur.Col-1]
	return prev != ' ' && prev != '\t'
}

func (m *InteractiveMode) iminInsertTextToEditor(text string) {
	m.mu.Lock()
	ed := m.editor
	m.mu.Unlock()
	if ed == nil || text == "" {
		return
	}
	ed.InsertTextAtCursor(text)
	m.imRequestRender()
}

func (m *InteractiveMode) iminCheckProcessImageAttachments(paths []string) []*ai.ImageContent {
	sm := m.imSettingsManager()
	if sm != nil && sm.GetBlockImages() {
		return nil
	}
	var out []*ai.ImageContent
	for _, raw := range paths {
		cleaned := imhStripPathQuotesAndSpaces(raw)
		mime, ok := imhIsImagePath(cleaned)
		if !ok {
			continue
		}
		rawBytes, err := os.ReadFile(cleaned)
		if err != nil {
			continue
		}
		if sm == nil || sm.GetImageAutoResize() {
			if resized, err := utils.ResizeImage(rawBytes, mime, nil); err == nil && resized != nil {
				out = append(out, &ai.ImageContent{
					Type:     "image",
					Data:     resized.Data,
					MimeType: resized.MimeType,
				})
				continue
			}
		}
		out = append(out, &ai.ImageContent{
			Type:     "image",
			Data:     base64.StdEncoding.EncodeToString(rawBytes),
			MimeType: mime,
		})
	}
	return slices.Clip(out)
}

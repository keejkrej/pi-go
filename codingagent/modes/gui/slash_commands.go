// Slash command registry, autocomplete integration, command execution, and
// modal selector launchers for GuiMode.
package gui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/gui"
)

// HandleSlashCommand parses and executes a `/command [args]` line if handled by GUI mode.
// Prompt templates, `skill:<name>`, and extension commands return false so AgentSession.Prompt
// handles their native expansion and execution.
func (m *GuiMode) HandleSlashCommand(ctx context.Context, raw string) bool {
	if m == nil {
		return false
	}
	if ctx == nil {
		ctx = m.currentCtx()
	}

	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, "/") {
		return false
	}
	body := strings.TrimSpace(trimmed[1:])
	if body == "" {
		return false
	}

	name := body
	args := ""
	if idx := strings.IndexAny(body, " \t\n\r"); idx >= 0 {
		name = body[:idx]
		args = strings.TrimSpace(body[idx+1:])
	}
	lower := strings.ToLower(name)

	switch lower {
	case "quit", "exit":
		m.postUI(func() { m.Stop() })
		return true

	case "new", "clear":
		m.newSession(ctx)
		return true

	case "compact":
		m.runCompactCommand(ctx, args)
		return true

	case "model":
		m.runModelCommand(ctx, args)
		return true

	case "scoped-models":
		m.postUI(func() { m.ShowScopedModelsSelector() })
		return true

	case "thinking":
		m.runThinkingCommand(args)
		return true

	case "settings":
		m.postUI(func() { m.ShowSettingsSelector() })
		return true

	case "theme":
		m.runThemeCommand(args)
		return true

	case "resume":
		m.postUI(func() { m.ShowSessionSelector() })
		return true

	case "session":
		m.runSessionStatsCommand()
		return true

	case "name":
		m.runNameCommand(args)
		return true

	case "tree":
		m.postUI(func() { m.ShowTreeSelector() })
		return true

	case "fork":
		m.postUI(func() { m.ShowForkSelector() })
		return true

	case "clone":
		m.runCloneCommand(ctx)
		return true

	case "copy":
		m.postUI(func() { m.copyLastAssistantMessage() })
		return true

	case "export":
		m.runExportCommand(args)
		return true

	case "import":
		m.runImportCommand(ctx, args)
		return true

	case "share":
		m.runShareCommand()
		return true

	case "bug":
		m.runBugReportCommand(args)
		return true

	case "hotkeys", "help":
		m.postUI(func() { m.ShowHotkeysDialog() })
		return true

	case "changelog":
		m.runChangelogCommand()
		return true

	case "login":
		m.runLoginCommand(ctx, args)
		return true

	case "logout":
		m.runLogoutCommand(ctx, args)
		return true

	case "reload":
		m.runReloadCommand(ctx)
		return true

	case "trust":
		m.runTrustCommand(ctx)
		return true

	case "skills":
		m.runSkillsListCommand()
		return true

	case "prompts":
		m.runPromptsListCommand()
		return true

	case "debug":
		m.runDebugCommand()
		return true

	default:
		// Let AgentSession.Prompt handle prompt templates, /skill:name, and extension slash commands.
		return false
	}
}

func (m *GuiMode) buildSlashCommandList() []gui.SlashCommand {
	cmds := []gui.SlashCommand{
		{Name: "settings", Description: "Open settings menu"},
		{
			Name:        "model",
			Description: "Select model (opens selector menu)",
			GetArgumentCompletions: func(prefix string) []gui.AutocompleteItem {
				return m.gscModelCompletions(prefix)
			},
		},
		{Name: "scoped-models", Description: "Enable/disable models for Ctrl+P cycling"},
		{
			Name:        "thinking",
			Description: "Set or select reasoning thinking level",
			GetArgumentCompletions: func(prefix string) []gui.AutocompleteItem {
				return m.gscThinkingCompletions(prefix)
			},
		},
		{
			Name:        "theme",
			Description: "Select color theme",
			GetArgumentCompletions: func(prefix string) []gui.AutocompleteItem {
				return m.gscThemeCompletions(prefix)
			},
		},
		{Name: "export", Description: "Export session to HTML or JSONL file"},
		{Name: "import", Description: "Import a session from a JSONL file"},
		{Name: "share", Description: "Export/share current session"},
		{Name: "copy", Description: "Copy last assistant message to clipboard"},
		{Name: "name", Description: "Set session display name"},
		{Name: "session", Description: "Show session info, token usage, and cost breakdown"},
		{Name: "changelog", Description: "Show changelog entries"},
		{Name: "hotkeys", Description: "Show all keyboard shortcuts"},
		{Name: "help", Description: "Show keyboard shortcuts and help"},
		{Name: "fork", Description: "Create a new session from a previous user message"},
		{Name: "tree", Description: "Navigate to a different point in session tree"},
		{Name: "clone", Description: "Clone the current session into a new session"},
		{Name: "login", Description: "Login with OAuth provider"},
		{Name: "logout", Description: "Logout from OAuth provider"},
		{Name: "new", Description: "Start a new session"},
		{Name: "clear", Description: "Clear transcript and start a new session"},
		{Name: "compact", Description: "Manually compact session context"},
		{Name: "resume", Description: "Resume a different session"},
		{Name: "reload", Description: "Reload keybindings, extensions, skills, prompts, and themes"},
		{Name: "trust", Description: "Configure project trust level"},
		{Name: "skills", Description: "List loaded skills"},
		{Name: "prompts", Description: "List loaded prompt templates"},
		{Name: "bug", Description: "Create a redacted bug report archive"},
		{Name: "debug", Description: "Show debug runtime information"},
		{Name: "quit", Description: "Quit pi"},
		{Name: "exit", Description: "Quit pi"},
	}

	seen := make(map[string]struct{}, len(cmds))
	for _, c := range cmds {
		seen[c.Name] = struct{}{}
	}

	sess := m.Session()
	if sess == nil {
		return cmds
	}

	for _, pt := range sess.PromptTemplates() {
		if _, dup := seen[pt.Name]; !dup {
			seen[pt.Name] = struct{}{}
			cmds = append(cmds, gui.SlashCommand{
				Name:        pt.Name,
				Description: pt.Description,
			})
		}
	}

	if runner := sess.ExtensionRunner(); runner != nil {
		for _, rc := range runner.GetRegisteredCommands() {
			if _, dup := seen[rc.InvocationName]; !dup {
				seen[rc.InvocationName] = struct{}{}
				desc := ""
				if rc.Description != nil {
					desc = *rc.Description
				}
				cmds = append(cmds, gui.SlashCommand{
					Name:        rc.InvocationName,
					Description: desc,
				})
			}
		}
	}

	enableSkills := sess.SettingsManager == nil || sess.SettingsManager.GetEnableSkillCommands()
	if enableSkills && sess.ResourceLoader() != nil {
		for _, sk := range sess.ResourceLoader().GetSkills().Skills {
			cmdName := "skill:" + sk.Name
			if _, dup := seen[cmdName]; !dup {
				seen[cmdName] = struct{}{}
				cmds = append(cmds, gui.SlashCommand{
					Name:        cmdName,
					Description: sk.Description,
				})
			}
		}
	}

	return cmds
}

func (m *GuiMode) gscModelCompletions(prefix string) []gui.AutocompleteItem {
	sess := m.Session()
	if sess == nil || sess.ModelRegistry() == nil {
		return nil
	}
	lower := strings.ToLower(strings.TrimSpace(prefix))
	var items []gui.AutocompleteItem
	for _, mod := range sess.ModelRegistry().GetAvailable() {
		if mod == nil {
			continue
		}
		full := fmt.Sprintf("%s/%s", mod.Provider, mod.Id)
		if lower == "" || strings.Contains(strings.ToLower(full), lower) || strings.Contains(strings.ToLower(mod.Name), lower) {
			items = append(items, gui.AutocompleteItem{
				Value:       full,
				Label:       full,
				Description: mod.Name,
			})
		}
	}
	return items
}

func (m *GuiMode) gscThinkingCompletions(prefix string) []gui.AutocompleteItem {
	sess := m.Session()
	if sess == nil {
		return nil
	}
	lower := strings.ToLower(strings.TrimSpace(prefix))
	var items []gui.AutocompleteItem
	for _, lvl := range sess.GetAvailableThinkingLevels() {
		s := string(lvl)
		if lower == "" || strings.HasPrefix(strings.ToLower(s), lower) {
			items = append(items, gui.AutocompleteItem{
				Value: s,
				Label: s,
			})
		}
	}
	return items
}

func (m *GuiMode) gscThemeCompletions(prefix string) []gui.AutocompleteItem {
	lower := strings.ToLower(strings.TrimSpace(prefix))
	var items []gui.AutocompleteItem
	for _, ti := range theme.GetAvailableThemesWithPaths() {
		if lower == "" || strings.Contains(strings.ToLower(ti.Name), lower) {
			items = append(items, gui.AutocompleteItem{
				Value: ti.Name,
				Label: ti.Name,
			})
		}
	}
	return items
}

func (m *GuiMode) newSession(ctx context.Context) {
	if m.runtime == nil {
		return
	}
	if ctx == nil {
		ctx = m.currentCtx()
	}
	res, err := m.runtime.NewSession(ctx)
	if err != nil {
		m.postUI(func() { m.ShowError("Failed to start new session: " + err.Error()) })
		return
	}
	if res != nil && res.Cancelled {
		m.postUI(func() { m.ShowInfo("Session", "New session cancelled") })
		return
	}
	m.postUI(func() { m.handleSessionReplaced() })
}

func (m *GuiMode) runCompactCommand(ctx context.Context, instructions string) {
	sess := m.Session()
	if sess == nil {
		return
	}
	var instArg []string
	if strings.TrimSpace(instructions) != "" {
		instArg = []string{strings.TrimSpace(instructions)}
	}
	m.postUI(func() {
		if m.statusBanner != nil {
			m.statusBanner.SetCompacting(true)
		}
	})
	res, err := sess.Compact(ctx, instArg...)
	m.postUI(func() {
		if m.statusBanner != nil {
			m.statusBanner.SetCompacting(false)
		}
		if err != nil {
			m.ShowError("Compaction failed: " + err.Error())
			return
		}
		if res != nil {
			m.ShowInfo("Compacted", fmt.Sprintf("Compacted context (%s tokens before)", FormatTokens(res.TokensBefore)))
		}
		m.updateFooterAndTitle()
	})
}

func (m *GuiMode) runModelCommand(ctx context.Context, args string) {
	sess := m.Session()
	if sess == nil {
		return
	}
	if strings.TrimSpace(args) == "" {
		m.postUI(func() { m.ShowModelSelector() })
		return
	}
	reg := sess.ModelRegistry()
	if reg == nil {
		m.postUI(func() { m.ShowError("Model registry unavailable") })
		return
	}
	q := strings.TrimSpace(args)
	var matched *ai.Model
	if prov, id, ok := strings.Cut(q, "/"); ok {
		matched = reg.Find(strings.TrimSpace(prov), strings.TrimSpace(id))
	}
	if matched == nil {
		lowerQ := strings.ToLower(q)
		for _, candidate := range reg.GetAvailable() {
			if candidate == nil {
				continue
			}
			full := strings.ToLower(fmt.Sprintf("%s/%s", candidate.Provider, candidate.Id))
			if strings.ToLower(candidate.Id) == lowerQ || full == lowerQ || strings.Contains(full, lowerQ) {
				matched = candidate
				break
			}
		}
	}
	if matched == nil {
		m.postUI(func() { m.ShowError("Model not found: " + args) })
		return
	}
	if err := sess.SetModel(ctx, matched); err != nil {
		m.postUI(func() { m.ShowError(err.Error()) })
		return
	}
	m.postUI(func() {
		m.updateFooterAndTitle()
		m.ShowInfo("Model", fmt.Sprintf("Selected %s/%s", matched.Provider, matched.Id))
	})
}

func (m *GuiMode) runThinkingCommand(args string) {
	sess := m.Session()
	if sess == nil {
		return
	}
	if strings.TrimSpace(args) == "" {
		m.postUI(func() { m.ShowThinkingSelector() })
		return
	}
	target := agent.ThinkingLevel(strings.ToLower(strings.TrimSpace(args)))
	avail := sess.GetAvailableThinkingLevels()
	valid := false
	for _, lvl := range avail {
		if lvl == target {
			valid = true
			break
		}
	}
	if !valid {
		m.postUI(func() { m.ShowError("Unsupported thinking level: " + args) })
		return
	}
	sess.SetThinkingLevel(target)
	m.postUI(func() {
		m.updateFooterAndTitle()
		m.ShowInfo("Thinking", "Thinking level set to "+string(target))
	})
}

func (m *GuiMode) runThemeCommand(args string) {
	if strings.TrimSpace(args) == "" {
		m.postUI(func() { m.ShowThemeSelector() })
		return
	}
	name := strings.TrimSpace(args)
	m.postUI(func() {
		res, ts := ApplyThemeToHost(m.host, name)
		if !res.Success {
			m.ShowError("Failed to apply theme: " + res.Error)
			return
		}
		if sess := m.Session(); sess != nil && sess.SettingsManager != nil {
			sess.SettingsManager.SetTheme(name)
		}
		if ts != nil {
			m.applyThemeSet(ts)
		}
		m.ShowInfo("Theme", "Applied theme "+name)
	})
}

func (m *GuiMode) runSessionStatsCommand() {
	sess := m.Session()
	if sess == nil {
		return
	}
	stats := sess.GetSessionStats()
	var breakdown []core.UsageCostBreakdownEntry
	if sess.SessionManager != nil {
		breakdown = core.GetUsageCostBreakdown(sess.SessionManager.GetEntries())
	}
	totals := core.UsageTotals{
		Input:      stats.Tokens.Input,
		Output:     stats.Tokens.Output,
		CacheRead:  stats.Tokens.CacheRead,
		CacheWrite: stats.Tokens.CacheWrite,
		Cost:       stats.Cost,
	}

	m.postUI(func() {
		th := m.currentGuiTheme()
		card := NewCostSummaryCard(totals, breakdown, WithCardTheme(th))
		m.appendCard("cost", m.nextEntryID("cost"), card)
		var info []string
		info = append(info, "Session ID: "+stats.SessionId)
		if stats.SessionFile != nil {
			info = append(info, "File: "+*stats.SessionFile)
		}
		if name := sess.SessionName(); name != nil && *name != "" {
			info = append(info, "Name: "+*name)
		}
		info = append(info, fmt.Sprintf("Messages: %d (user=%d assistant=%d toolCalls=%d)",
			stats.TotalMessages, stats.UserMessages, stats.AssistantMessages, stats.ToolCalls))
		m.ShowInfo("Session Info", strings.Join(info, "\n"))
	})
}

func (m *GuiMode) runNameCommand(args string) {
	sess := m.Session()
	if sess == nil {
		return
	}
	trimmed := strings.TrimSpace(args)
	if trimmed == "" {
		cur := "(unnamed)"
		if n := sess.SessionName(); n != nil && *n != "" {
			cur = *n
		}
		m.postUI(func() { m.ShowInfo("Session Name", cur) })
		return
	}
	sess.SetSessionName(trimmed)
	m.setCachedSessionName(trimmed)
	m.postUI(func() {
		m.updateFooterAndTitle()
		m.ShowInfo("Session Name", "Session renamed to "+trimmed)
	})
}

func (m *GuiMode) runCloneCommand(ctx context.Context) {
	sess := m.Session()
	if m.runtime == nil || sess == nil || sess.SessionManager == nil {
		return
	}
	leaf := sess.SessionManager.GetLeafId()
	if leaf == nil || *leaf == "" {
		m.newSession(ctx)
		return
	}
	res, err := m.runtime.Fork(ctx, *leaf, &core.ForkSessionOptions{Position: "at"})
	if err != nil {
		m.postUI(func() { m.ShowError("Failed to clone session: " + err.Error()) })
		return
	}
	if res != nil && res.Cancelled {
		m.postUI(func() { m.ShowInfo("Clone", "Clone cancelled") })
		return
	}
	m.postUI(func() {
		m.handleSessionReplaced()
		m.ShowInfo("Clone", "Cloned current session into a new session")
	})
}

func (m *GuiMode) runExportCommand(args string) {
	sess := m.Session()
	if sess == nil {
		return
	}
	target := strings.TrimSpace(args)
	if strings.HasSuffix(strings.ToLower(target), ".jsonl") {
		out, err := sess.ExportToJsonl(target)
		m.postUI(func() {
			if err != nil {
				m.ShowError("Export failed: " + err.Error())
				return
			}
			m.ShowInfo("Exported JSONL", out)
		})
		return
	}

	var themePtr *string
	if m.uiCtx != nil && m.uiCtx.Theme() != nil {
		n := m.uiCtx.Theme().Name
		themePtr = &n
	}
	out, err := sess.ExportToHtml(target, &core.ExportToHtmlOptions{ThemeName: themePtr})
	m.postUI(func() {
		if err != nil {
			m.ShowError("Export failed: " + err.Error())
			return
		}
		m.ShowInfo("Exported HTML", out)
	})
}

func (m *GuiMode) runImportCommand(ctx context.Context, args string) {
	if m.runtime == nil {
		return
	}
	path := strings.TrimSpace(args)
	if path == "" {
		m.postUI(func() { m.ShowError("Usage: /import <path-to-session.jsonl>") })
		return
	}
	res, err := m.runtime.ImportFromJsonl(ctx, utils.ExpandTildePath(path))
	if err != nil {
		m.postUI(func() { m.ShowError("Import failed: " + err.Error()) })
		return
	}
	if res != nil && res.Cancelled {
		m.postUI(func() { m.ShowInfo("Import", "Session import cancelled") })
		return
	}
	m.postUI(func() {
		m.handleSessionReplaced()
		m.ShowInfo("Imported", "Imported session from "+path)
	})
}

func (m *GuiMode) runShareCommand() {
	sess := m.Session()
	if sess == nil {
		return
	}
	outName := fmt.Sprintf("pi-share-%s.html", time.Now().Format("20060102-150405"))
	outPath := filepath.Join(os.TempDir(), outName)
	exported, err := sess.ExportToHtml(outPath)
	m.postUI(func() {
		if err != nil {
			m.ShowError("Share export failed: " + err.Error())
			return
		}
		_ = gmWriteClipboardText(m.host, exported)
		m.ShowInfo("Session Share Ready", fmt.Sprintf("Exported standalone HTML to %s (path copied to clipboard)", exported))
	})
}

func (m *GuiMode) runBugReportCommand(hint string) {
	sess := m.Session()
	if sess == nil {
		return
	}
	var hintPtr *string
	if strings.TrimSpace(hint) != "" {
		cp := strings.TrimSpace(hint)
		hintPtr = &cp
	}
	var exts []core.Extension
	if rl := sess.ResourceLoader(); rl != nil {
		exts = rl.GetExtensions().Extensions
	}
	var globalSettings, projectSettings core.Settings
	if sess.SettingsManager != nil {
		globalSettings = sess.SettingsManager.GetGlobalSettings()
		projectSettings = sess.SettingsManager.GetProjectSettings()
	}

	meta := core.CollectBugReportMetadata(core.CollectBugReportMetadataOptions{
		Hint:            hintPtr,
		SessionId:       sess.SessionId(),
		Cwd:             sess.Cwd(),
		IncludeSession:  true,
		MessageCount:    len(sess.Messages()),
		Model:           sess.Model(),
		ModelRuntime:    sess.ModelRuntime(),
		ThinkingLevel:   sess.ThinkingLevel(),
		Extensions:      exts,
		GlobalSettings:  globalSettings,
		ProjectSettings: projectSettings,
	})
	diag := core.CollectBugReportDiagnostics(sess.SessionManager)
	var sessJsonl *string
	if sess.SessionManager != nil {
		raw := core.SerializeSessionBranch(sess.SessionManager)
		sessJsonl = &raw
	}
	bundle := core.BugReportBundle{
		Metadata:     meta,
		Diagnostics:  diag,
		SessionJsonl: sessJsonl,
	}
	archivePath := filepath.Join(sess.Cwd(), core.BugReportArchiveFileName(meta.Id))
	err := core.WriteBugReportArchive(bundle, archivePath)
	m.postUI(func() {
		if err != nil {
			m.ShowError("Failed to create bug report archive: " + err.Error())
			return
		}
		m.ShowInfo("Bug Report Created", archivePath)
	})
}

func (m *GuiMode) runChangelogCommand() {
	entries, err := utils.ParseChangelog(utils.GetChangelogPath())
	m.postUI(func() {
		if err != nil || len(entries) == 0 {
			m.ShowInfo("Changelog", fmt.Sprintf("pi v%s (no local CHANGELOG.md found)", utils.VERSION))
			return
		}
		limit := min(3, len(entries))
		var sb strings.Builder
		for i := 0; i < limit; i++ {
			sb.WriteString(utils.NormalizeChangelogLinksForEntry(entries[i].Content, entries[i]))
			sb.WriteString("\n\n")
		}
		m.ShowInfo("Changelog", strings.TrimSpace(sb.String()))
	})
}

func (m *GuiMode) runReloadCommand(ctx context.Context) {
	sess := m.Session()
	if sess == nil {
		return
	}
	if err := sess.Reload(ctx); err != nil {
		m.postUI(func() { m.ShowError("Reload failed: " + err.Error()) })
		return
	}
	m.postUI(func() {
		m.keybindings = gmLoadKeybindings()
		if m.editor != nil {
			m.editor.SetKeybindings(m.keybindings)
		}
		m.RefreshAutocomplete()
		m.rebuildTranscript()
		m.updateFooterAndTitle()
		m.ShowInfo("Reloaded", "Reloaded extensions, skills, prompts, themes, and keybindings")
	})
}

func (m *GuiMode) runTrustCommand(ctx context.Context) {
	sess := m.Session()
	if sess == nil {
		return
	}
	opts := core.GetProjectTrustOptions(sess.Cwd())
	m.postUI(func() {
		m.ShowTrustSelector(ctx, sess.Cwd(), opts)
	})
}

func (m *GuiMode) runSkillsListCommand() {
	sess := m.Session()
	if sess == nil || sess.ResourceLoader() == nil {
		m.postUI(func() { m.ShowInfo("Skills", "No skills loaded") })
		return
	}
	skills := sess.ResourceLoader().GetSkills().Skills
	m.postUI(func() {
		if len(skills) == 0 {
			m.ShowInfo("Skills", "No skills loaded")
			return
		}
		var lines []string
		for _, sk := range skills {
			lines = append(lines, fmt.Sprintf("/skill:%s — %s", sk.Name, sk.Description))
		}
		m.ShowInfo(fmt.Sprintf("Skills (%d)", len(skills)), strings.Join(lines, "\n"))
	})
}

func (m *GuiMode) runPromptsListCommand() {
	sess := m.Session()
	if sess == nil {
		m.postUI(func() { m.ShowInfo("Prompts", "No prompt templates loaded") })
		return
	}
	prompts := sess.PromptTemplates()
	m.postUI(func() {
		if len(prompts) == 0 {
			m.ShowInfo("Prompts", "No prompt templates loaded")
			return
		}
		var lines []string
		for _, pt := range prompts {
			lines = append(lines, fmt.Sprintf("/%s — %s", pt.Name, pt.Description))
		}
		m.ShowInfo(fmt.Sprintf("Prompt Templates (%d)", len(prompts)), strings.Join(lines, "\n"))
	})
}

func (m *GuiMode) runDebugCommand() {
	sess := m.Session()
	if sess == nil {
		return
	}
	m.postUI(func() {
		modelDesc := "(none)"
		if mod := sess.Model(); mod != nil {
			modelDesc = fmt.Sprintf("%s/%s", mod.Provider, mod.Id)
		}
		sessFile := "(in-memory)"
		if sf := sess.SessionFile(); sf != nil {
			sessFile = *sf
		}
		info := strings.Join([]string{
			"Version: " + utils.VERSION,
			"Cwd: " + sess.Cwd(),
			"SessionId: " + sess.SessionId(),
			"SessionFile: " + sessFile,
			"Model: " + modelDesc,
			"ThinkingLevel: " + string(sess.ThinkingLevel()),
			fmt.Sprintf("Tools (%d): %s", len(sess.GetActiveToolNames()), strings.Join(sess.GetActiveToolNames(), ", ")),
		}, "\n")
		m.ShowInfo("Debug Info", info)
	})
}

func (m *GuiMode) runLoginCommand(ctx context.Context, args string) {
	sess := m.Session()
	if sess == nil || sess.ModelRegistry() == nil || sess.ModelRegistry().GetRuntime() == nil {
		m.postUI(func() { m.ShowError("Model runtime unavailable for login") })
		return
	}
	rt := sess.ModelRegistry().GetRuntime()
	entries := m.gscOAuthProviders(ctx, rt)
	if len(entries) == 0 {
		m.postUI(func() { m.ShowInfo("Login", "No OAuth providers available") })
		return
	}

	if target := strings.TrimSpace(args); target != "" {
		lower := strings.ToLower(target)
		for _, entry := range entries {
			if strings.ToLower(entry.ID) == lower || strings.ToLower(entry.Name) == lower {
				m.postUI(func() { m.startOAuthLoginFlow(ctx, rt, entry) })
				return
			}
		}
	}

	m.postUI(func() {
		dlg := NewOAuthSelectorDialog(OAuthSelectorConfig{
			Mode:      "login",
			Providers: entries,
			Theme:     m.currentGuiTheme(),
			OnSelect: func(entry OAuthProviderEntry) {
				m.startOAuthLoginFlow(ctx, rt, entry)
			},
		})
		m.setActiveDialog(dlg.Show(m.host))
	})
}

func (m *GuiMode) startOAuthLoginFlow(ctx context.Context, rt *core.ModelRuntime, entry OAuthProviderEntry) {
	loginDlg := NewLoginDialog(LoginDialogConfig{
		ProviderName: entry.Name,
		Host:         m.host,
		Theme:        m.currentGuiTheme(),
	})
	m.setActiveDialog(loginDlg.Show(m.host))

	go func() {
		_, err := rt.Login(ctx, entry.ID, ai.AuthTypeOAuth, loginDlg, nil)
		m.postUI(func() {
			if err != nil {
				loginDlg.Complete(false, err.Error())
				m.ShowError("Login failed: " + err.Error())
				return
			}
			loginDlg.Complete(true, "Logged in to "+entry.Name)
			m.updateFooterAndTitle()
			m.ShowInfo("Login", "Authenticated with "+entry.Name)
		})
	}()
}

func (m *GuiMode) runLogoutCommand(ctx context.Context, args string) {
	sess := m.Session()
	if sess == nil || sess.ModelRegistry() == nil || sess.ModelRegistry().GetRuntime() == nil {
		m.postUI(func() { m.ShowError("Model runtime unavailable for logout") })
		return
	}
	rt := sess.ModelRegistry().GetRuntime()
	entries := m.gscOAuthProviders(ctx, rt)
	var loggedIn []OAuthProviderEntry
	for _, entry := range entries {
		if entry.LoggedIn {
			loggedIn = append(loggedIn, entry)
		}
	}
	if len(loggedIn) == 0 {
		m.postUI(func() { m.ShowInfo("Logout", "No logged-in OAuth providers") })
		return
	}

	doLogout := func(entry OAuthProviderEntry) {
		err := rt.Logout(ctx, entry.ID, nil)
		m.postUI(func() {
			if err != nil {
				m.ShowError("Logout failed: " + err.Error())
				return
			}
			m.updateFooterAndTitle()
			m.ShowInfo("Logout", "Logged out from "+entry.Name)
		})
	}

	if target := strings.TrimSpace(args); target != "" {
		lower := strings.ToLower(target)
		for _, entry := range loggedIn {
			if strings.ToLower(entry.ID) == lower || strings.ToLower(entry.Name) == lower {
				doLogout(entry)
				return
			}
		}
	}

	m.postUI(func() {
		dlg := NewOAuthSelectorDialog(OAuthSelectorConfig{
			Mode:      "logout",
			Providers: loggedIn,
			Theme:     m.currentGuiTheme(),
			OnSelect: func(entry OAuthProviderEntry) {
				go doLogout(entry)
			},
		})
		m.setActiveDialog(dlg.Show(m.host))
	})
}

func (m *GuiMode) gscOAuthProviders(ctx context.Context, rt *core.ModelRuntime) []OAuthProviderEntry {
	if rt == nil {
		return nil
	}
	creds, _ := rt.ListCredentials(ctx, nil)
	credByProv := make(map[string]ai.CredentialInfo, len(creds))
	for _, c := range creds {
		credByProv[c.ProviderId] = c
	}

	var entries []OAuthProviderEntry
	for _, prov := range rt.GetProviders() {
		if prov == nil || prov.Auth.OAuth == nil {
			continue
		}
		name := prov.Name
		if name == "" {
			name = prov.Id
		}
		c, hasCred := credByProv[prov.Id]
		credLabel := ""
		if hasCred {
			credLabel = string(c.Type)
		}
		entries = append(entries, OAuthProviderEntry{
			ID:         prov.Id,
			Name:       name,
			LoggedIn:   hasCred,
			Credential: credLabel,
		})
	}
	return entries
}

// ============================================================================
// Modal Selector Dialog Openers
// ============================================================================

// ShowModelSelector opens the searchable model selector overlay.
func (m *GuiMode) ShowModelSelector() {
	sess := m.Session()
	if m == nil || m.host == nil || sess == nil || sess.ModelRegistry() == nil {
		return
	}
	dlg := NewModelSelectorDialog(ModelSelectorConfig{
		Models:          sess.ModelRegistry().GetAvailable(),
		ScopedModels:    sess.ScopedModels(),
		CurrentModel:    sess.Model(),
		SettingsManager: sess.SettingsManager,
		ModelRegistry:   sess.ModelRegistry(),
		Theme:           m.currentGuiTheme(),
		OnSelect: func(model *ai.Model, thinkingOverride *agent.ThinkingLevel) {
			if model == nil {
				return
			}
			go func() {
				if err := sess.SetModel(m.currentCtx(), model); err != nil {
					m.postUI(func() { m.ShowError(err.Error()) })
					return
				}
				if thinkingOverride != nil {
					sess.SetThinkingLevel(*thinkingOverride)
				}
				m.postUI(func() { m.updateFooterAndTitle() })
			}()
		},
	})
	m.setActiveDialog(dlg.Show(m.host))
}

// ShowThinkingSelector opens the reasoning / thinking level selector overlay.
func (m *GuiMode) ShowThinkingSelector() {
	sess := m.Session()
	if m == nil || m.host == nil || sess == nil {
		return
	}
	dlg := NewThinkingSelectorDialog(ThinkingSelectorConfig{
		CurrentLevel:    sess.ThinkingLevel(),
		AvailableLevels: sess.GetAvailableThinkingLevels(),
		Theme:           m.currentGuiTheme(),
		OnSelect: func(level agent.ThinkingLevel) {
			sess.SetThinkingLevel(level)
			m.updateFooterAndTitle()
		},
	})
	m.setActiveDialog(dlg.Show(m.host))
}

// ShowSessionSelector opens the session resume/switch/rename/delete selector overlay.
func (m *GuiMode) ShowSessionSelector() {
	sess := m.Session()
	if m == nil || m.host == nil || sess == nil || sess.SessionManager == nil {
		return
	}
	cwd := sess.Cwd()
	sessDir := sess.SessionManager.GetSessionDir()
	sessions, _ := LoadSessionSelectorItems(m.currentCtx(), cwd, sessDir, false)
	curFile := ""
	if sf := sess.SessionFile(); sf != nil {
		curFile = *sf
	}

	dlg := NewSessionSelectorDialog(SessionSelectorConfig{
		Sessions:           sessions,
		CurrentSessionFile: curFile,
		CurrentSessionId:   sess.SessionId(),
		Cwd:                cwd,
		SessionDir:         sessDir,
		Theme:              m.currentGuiTheme(),
		OnSelect: func(chosen core.SessionInfo) {
			if m.runtime == nil {
				return
			}
			go func() {
				res, err := m.runtime.SwitchSession(m.currentCtx(), chosen.Path)
				if err != nil {
					m.postUI(func() { m.ShowError("Failed to switch session: " + err.Error()) })
					return
				}
				if res != nil && res.Cancelled {
					return
				}
				m.postUI(func() { m.handleSessionReplaced() })
			}()
		},
		OnRename: func(chosen core.SessionInfo, newName string) error {
			sm, err := core.SessionManagerOpen(chosen.Path)
			if err != nil {
				return err
			}
			_, err = sm.AppendSessionInfo(newName)
			if err == nil && chosen.Path == curFile {
				sess.SetSessionName(newName)
				m.setCachedSessionName(newName)
				m.updateFooterAndTitle()
			}
			return err
		},
		OnDelete: func(chosen core.SessionInfo) error {
			return os.Remove(chosen.Path)
		},
		OnToggleScope: func(allProjects bool) ([]core.SessionInfo, error) {
			return LoadSessionSelectorItems(m.currentCtx(), cwd, sessDir, allProjects)
		},
	})
	m.setActiveDialog(dlg.Show(m.host))
}

// ShowTreeSelector opens the conversation tree navigation selector overlay.
func (m *GuiMode) ShowTreeSelector() {
	sess := m.Session()
	if m == nil || m.host == nil || sess == nil || sess.SessionManager == nil {
		return
	}
	leaf := ""
	if lid := sess.SessionManager.GetLeafId(); lid != nil {
		leaf = *lid
	}
	filterMode := core.TreeFilterModeDefault
	if sess.SettingsManager != nil {
		filterMode = sess.SettingsManager.GetTreeFilterMode()
	}

	dlg := NewTreeSelectorDialog(TreeSelectorConfig{
		Nodes:         sess.SessionManager.GetTree(),
		CurrentLeafId: leaf,
		FilterMode:    filterMode,
		Theme:         m.currentGuiTheme(),
		OnSelect: func(entryId string) {
			go func() {
				res, err := sess.NavigateTree(m.currentCtx(), entryId)
				if err != nil {
					m.postUI(func() { m.ShowError("Tree navigation failed: " + err.Error()) })
					return
				}
				if res != nil && res.Cancelled {
					return
				}
				m.postUI(func() {
					m.rebuildTranscript()
					if res != nil && res.EditorText != nil && m.editor != nil {
						m.editor.SetText(*res.EditorText)
					}
					m.updateFooterAndTitle()
				})
			}()
		},
		OnLabel: func(entryId string, label *string) {
			_, _ = sess.SessionManager.AppendLabelChange(entryId, label)
		},
		OnFilterChange: func(mode core.TreeFilterMode) {
			if sess.SettingsManager != nil {
				sess.SettingsManager.SetTreeFilterMode(mode)
			}
		},
	})
	m.setActiveDialog(dlg.Show(m.host))
}

// ShowSettingsSelector opens the categorized settings overlay.
func (m *GuiMode) ShowSettingsSelector() {
	sess := m.Session()
	if m == nil || m.host == nil || sess == nil || sess.SettingsManager == nil {
		return
	}
	var themes []string
	for _, ti := range theme.GetAvailableThemesWithPaths() {
		themes = append(themes, ti.Name)
	}
	var models []*ai.Model
	if sess.ModelRegistry() != nil {
		models = sess.ModelRegistry().GetAvailable()
	}

	dlg := NewSettingsSelectorDialog(SettingsSelectorConfig{
		SettingsManager: sess.SettingsManager,
		Session:         sess,
		AvailableThemes: themes,
		AvailableModels: models,
		Theme:           m.currentGuiTheme(),
		OnChange: func(id, value string) {
			ApplySettingsChange(sess.SettingsManager, sess, id, value)
			if id == "hideThinkingBlock" {
				m.mu.Lock()
				hide := sess.SettingsManager.GetHideThinkingBlock()
				m.hideThinking = hide
				entries := append([]gmTranscriptEntry(nil), m.entries...)
				m.mu.Unlock()
				for _, e := range entries {
					if ac, ok := e.card.(*AssistantMessageCard); ok {
						ac.SetHideThinkingBlock(hide)
					}
				}
			}
			m.updateFooterAndTitle()
		},
		OnThemePreview: func(themeName string) {
			_, ts := ApplyThemeToHost(m.host, themeName)
			if ts != nil {
				m.applyThemeSet(ts)
			}
		},
	})
	m.setActiveDialog(dlg.Show(m.host))
}

// ShowThemeSelector opens the theme selector overlay with live preview.
func (m *GuiMode) ShowThemeSelector() {
	if m == nil || m.host == nil {
		return
	}
	cur := "dark"
	if sess := m.Session(); sess != nil && sess.SettingsManager != nil {
		if t := sess.SettingsManager.GetTheme(); t != nil && *t != "" {
			cur = *t
		}
	} else if th := theme.CurrentTheme(); th != nil && th.Name != "" {
		cur = th.Name
	}

	dlg := NewThemeSelectorDialog(ThemeSelectorConfig{
		Themes:       theme.GetAvailableThemesWithPaths(),
		CurrentTheme: cur,
		Host:         m.host,
		Theme:        m.currentGuiTheme(),
		OnPreview: func(name string) {
			_, ts := ApplyThemeToHost(m.host, name)
			if ts != nil {
				m.applyThemeSet(ts)
			}
		},
		OnSelect: func(name string) {
			if sess := m.Session(); sess != nil && sess.SettingsManager != nil {
				sess.SettingsManager.SetTheme(name)
			}
			_, ts := ApplyThemeToHost(m.host, name)
			if ts != nil {
				m.applyThemeSet(ts)
			}
		},
	})
	m.setActiveDialog(dlg.Show(m.host))
}

// ShowScopedModelsSelector opens the Ctrl+P scoped model selection overlay.
func (m *GuiMode) ShowScopedModelsSelector() {
	sess := m.Session()
	if m == nil || m.host == nil || sess == nil || sess.ModelRegistry() == nil {
		return
	}
	dlg := NewScopedModelsSelectorDialog(ScopedModelsSelectorConfig{
		AllModels:       sess.ModelRegistry().GetAvailable(),
		ScopedModels:    sess.ScopedModels(),
		SettingsManager: sess.SettingsManager,
		Theme:           m.currentGuiTheme(),
		OnSave: func(scoped []core.ScopedModel, persist bool) {
			sess.SetScopedModels(scoped)
			if persist && sess.SettingsManager != nil {
				var patterns []string
				for _, sc := range scoped {
					if sc.Model != nil {
						patterns = append(patterns, fmt.Sprintf("%s/%s", sc.Model.Provider, sc.Model.Id))
					}
				}
				sess.SettingsManager.SetEnabledModels(patterns)
			}
			m.ShowInfo("Scoped Models", fmt.Sprintf("Updated %d scoped model(s)", len(scoped)))
		},
	})
	m.setActiveDialog(dlg.Show(m.host))
}

// ShowForkSelector opens the user-message fork selector overlay.
func (m *GuiMode) ShowForkSelector() {
	sess := m.Session()
	if m == nil || m.host == nil || sess == nil || m.runtime == nil {
		return
	}
	msgs := sess.GetUserMessagesForForking()
	if len(msgs) == 0 {
		m.ShowInfo("Fork", "No user messages available to fork from")
		return
	}
	dlg := NewForkSelectorDialog(ForkSelectorConfig{
		Messages: msgs,
		Theme:    m.currentGuiTheme(),
		OnSelect: func(msg core.ForkableUserMessage) {
			go func() {
				res, err := m.runtime.Fork(m.currentCtx(), msg.EntryId)
				if err != nil {
					m.postUI(func() { m.ShowError("Fork failed: " + err.Error()) })
					return
				}
				if res != nil && res.Cancelled {
					return
				}
				m.postUI(func() {
					m.handleSessionReplaced()
					if res != nil && res.SelectedText != nil && m.editor != nil {
						m.editor.SetText(*res.SelectedText)
					}
				})
			}()
		},
	})
	m.setActiveDialog(dlg.Show(m.host))
}

// ShowTrustSelector opens the project trust selector overlay.
func (m *GuiMode) ShowTrustSelector(ctx context.Context, cwd string, options []core.ProjectTrustOption) {
	if m == nil || m.host == nil || len(options) == 0 {
		return
	}
	store := core.NewProjectTrustStore(utils.GetAgentDir())
	dlg := NewTrustSelectorDialog(TrustSelectorConfig{
		Cwd:     cwd,
		Title:   "Trust Project Resources?",
		Message: fmt.Sprintf("Project directory %s contains local settings, extensions, or skills.", cwd),
		Options: options,
		Theme:   m.currentGuiTheme(),
		OnSelect: func(option core.ProjectTrustOption) {
			if len(option.Updates) > 0 {
				_ = store.SetMany(option.Updates)
			} else {
				trusted := option.Trusted
				_ = store.Set(cwd, &trusted)
			}
			if sess := m.Session(); sess != nil {
				if sess.SettingsManager != nil {
					sess.SettingsManager.SetProjectTrusted(option.Trusted)
				}
				if option.Trusted {
					go func() {
						_ = sess.Reload(ctx)
						m.postUI(func() {
							m.RefreshAutocomplete()
							m.updateFooterAndTitle()
						})
					}()
				}
			}
		},
	})
	m.setActiveDialog(dlg.Show(m.host))
}

// ShowHotkeysDialog opens the keyboard shortcuts reference overlay.
func (m *GuiMode) ShowHotkeysDialog() {
	if m == nil || m.host == nil {
		return
	}
	dlg := NewHotkeysDialog(HotkeysDialogConfig{
		Title:   "Keyboard Shortcuts",
		Entries: DefaultHotkeys(),
		Theme:   m.currentGuiTheme(),
	})
	m.setActiveDialog(dlg.Show(m.host))
}

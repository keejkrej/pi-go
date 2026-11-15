package gui

import (
	"context"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/gui"
)

func tselNewHostTester(t *testing.T) (*gui.GUI, *ui.Tester) {
	t.Helper()
	host := gui.New()
	host.SetRootView(func(c *ui.Context) {
		ui.Column(c).FillWidth().Children(func() {
			ui.Text(c, "base-workspace")
		})
	})
	tt := ui.NewTester(host.View, 960, 720)
	return host, tt
}

func tselStrPtr(s string) *string { return &s }

func TestModelAndThinkingSelectors(t *testing.T) {
	host, tt := tselNewHostTester(t)

	m1 := &ai.Model{BaseModel: ai.BaseModel{Id: "claude-sonnet-4", Name: "Claude Sonnet 4", Provider: "anthropic"}, ContextWindow: 200000, Reasoning: true}
	m2 := &ai.Model{BaseModel: ai.BaseModel{Id: "gpt-4o", Name: "GPT-4o", Provider: "openai", Input: []ai.ModelModality{ai.ModelModalityText, ai.ModelModalityImage}}, ContextWindow: 128000}
	high := ai.ModelThinkingLevelHigh

	var chosenModel *ai.Model
	var chosenOverride *agent.ThinkingLevel
	dlg := NewModelSelectorDialog(ModelSelectorConfig{
		Models:       []*ai.Model{m1, m2},
		ScopedModels: []core.ScopedModel{{Model: m2, ThinkingLevel: &high}},
		CurrentModel: m1,
		OnSelect: func(m *ai.Model, ov *agent.ThinkingLevel) {
			chosenModel = m
			chosenOverride = ov
		},
	})

	dh := dlg.Show(host)
	tt.Frame()
	if !dh.Visible() || !tt.HasText("Select Model") || !tt.HasText("Claude Sonnet 4") {
		t.Fatalf("expected ModelSelectorDialog visible, got %v", tt.Texts())
	}

	// Switch to Scoped tab -> only GPT-4o remains.
	if err := tt.Click("Scoped (1)"); err != nil {
		t.Fatalf("click Scoped pill: %v", err)
	}
	tt.Frame()
	if len(dlg.FilteredModels()) != 1 || dlg.FilteredModels()[0].Id != "gpt-4o" {
		t.Fatalf("expected only scoped model gpt-4o, got %+v", dlg.FilteredModels())
	}

	if err := tt.Click("Select"); err != nil {
		t.Fatalf("click Select: %v", err)
	}
	tt.Frame()
	if chosenModel == nil || chosenModel.Id != "gpt-4o" {
		t.Fatalf("expected gpt-4o selected, got %+v", chosenModel)
	}
	if chosenOverride == nil || *chosenOverride != ai.ModelThinkingLevelHigh {
		t.Fatalf("expected high override, got %v", chosenOverride)
	}
	if dh.Visible() || host.HasOverlay() {
		t.Fatal("expected dialog overlay closed after selection")
	}

	// Thinking selector
	var selectedLvl agent.ThinkingLevel
	thinkDlg := NewThinkingSelectorDialog(ThinkingSelectorConfig{
		CurrentLevel: ai.ModelThinkingLevelMedium,
		OnSelect: func(lvl agent.ThinkingLevel) {
			selectedLvl = lvl
		},
	})
	thHandle := thinkDlg.Show(host)
	tt.Frame()
	if !tt.HasText("Select Thinking Level") || !tt.HasText("Balanced reasoning") {
		t.Fatalf("expected thinking descriptions rendered, got %v", tt.Texts())
	}
	if err := tt.Click("high"); err != nil {
		t.Fatalf("click high: %v", err)
	}
	tt.Frame()
	if selectedLvl != ai.ModelThinkingLevelHigh {
		t.Fatalf("expected high thinking selected, got %q", selectedLvl)
	}
	if thHandle.Visible() {
		t.Fatal("expected thinking selector closed after selection")
	}
}

func TestSessionSelectorSearchScopeRenameForkAndDelete(t *testing.T) {
	tmp := t.TempDir()
	sm, err := core.SessionManagerCreate(tmp, tmp)
	if err != nil {
		t.Fatalf("SessionManagerCreate: %v", err)
	}
	if _, err := sm.AppendMessage(&ai.UserMessage{
		Role:      "user",
		Content:   ai.UserMessageContent{Text: tselStrPtr("Refactor GUI overlay")},
		Timestamp: time.Now().UnixMilli(),
	}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	sessFile := sm.GetSessionFile()
	if sessFile == nil || *sessFile == "" {
		t.Fatal("expected persisted session file")
	}

	items, err := LoadSessionSelectorItems(context.Background(), tmp, tmp, false)
	if err != nil || len(items) != 1 {
		t.Fatalf("LoadSessionSelectorItems: len=%d err=%v", len(items), err)
	}

	host, tt := tselNewHostTester(t)
	var resumed, forked *core.SessionInfo
	var toggledAll []bool
	dlg := NewSessionSelectorDialog(SessionSelectorConfig{
		Sessions:           items,
		CurrentSessionFile: *sessFile,
		Cwd:                tmp,
		SessionDir:         tmp,
		OnToggleScope: func(all bool) ([]core.SessionInfo, error) {
			toggledAll = append(toggledAll, all)
			return items, nil
		},
		OnFork: func(s core.SessionInfo) {
			cp := s
			forked = &cp
		},
		OnSelect: func(s core.SessionInfo) {
			cp := s
			resumed = &cp
		},
	})

	dh := dlg.Show(host)
	tt.Frame()
	if !tt.HasText("Resume Session") || !tt.HasText("Refactor GUI overlay") {
		t.Fatalf("expected session in list, got %v", tt.Texts())
	}

	if err := tt.Click("All Projects"); err != nil {
		t.Fatalf("click All Projects: %v", err)
	}
	tt.Frame()
	if !slices.Equal(toggledAll, []bool{true}) {
		t.Fatalf("expected OnToggleScope(true), got %v", toggledAll)
	}

	// Inline rename persists via SessionManagerOpen + AppendSessionInfo fallback.
	if err := tt.Click("Rename"); err != nil {
		t.Fatalf("click Rename: %v", err)
	}
	tt.Frame()
	tt.Type("Renamed GUI Work")
	if err := tt.Click("Save"); err != nil {
		t.Fatalf("click Save rename: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Renamed GUI Work") {
		t.Fatalf("expected renamed session title, got %v", tt.Texts())
	}

	// Verify Fork button works cleanly.
	if err := tt.Click("Fork"); err != nil {
		t.Fatalf("click Fork: %v", err)
	}
	tt.Frame()
	if forked == nil || forked.Path != *sessFile {
		t.Fatalf("expected fork callback on %q, got %+v", *sessFile, forked)
	}
	if dh.Visible() {
		t.Fatal("expected dialog closed on Fork")
	}

	// Re-open to test Resume & two-step Delete.
	dlg2 := NewSessionSelectorDialog(SessionSelectorConfig{
		Sessions: items,
		OnSelect: func(s core.SessionInfo) {
			cp := s
			resumed = &cp
		},
	})
	dlg2.Show(host)
	tt.Frame()
	if err := tt.Click("Resume"); err != nil {
		t.Fatalf("click Resume: %v", err)
	}
	tt.Frame()
	if resumed == nil || resumed.Id != items[0].Id {
		t.Fatalf("expected resumed callback, got %+v", resumed)
	}

	// Test two-step Delete removes temp file.
	dlg3 := NewSessionSelectorDialog(SessionSelectorConfig{Sessions: items})
	dlg3.Show(host)
	tt.Frame()
	if err := tt.Click("Delete"); err != nil {
		t.Fatalf("click Delete: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Confirm Delete") {
		t.Fatal("expected two-step delete confirmation prompt")
	}
	if err := tt.Click("Confirm Delete"); err != nil {
		t.Fatalf("click Confirm Delete: %v", err)
	}
	tt.Frame()
	if _, statErr := os.Stat(*sessFile); !os.IsNotExist(statErr) {
		t.Fatalf("expected deleted session file %s to not exist, err=%v", *sessFile, statErr)
	}
	if err := tt.Click("Cancel"); err != nil {
		t.Fatalf("close session selector: %v", err)
	}
}

func TestTreeSelectorFilterFoldLabelAndSelect(t *testing.T) {
	u1 := &core.SessionMessageEntry{
		SessionEntryBase: core.SessionEntryBase{Id: "e-user-1", Type: "message"},
		Message:          &ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tselStrPtr("Investigate crash")}},
	}
	a1 := &core.SessionMessageEntry{
		SessionEntryBase: core.SessionEntryBase{Id: "e-asst-1", Type: "message", ParentId: tselStrPtr("e-user-1")},
		Message:          &ai.AssistantMessage{Role: "assistant", Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "Reading logs..."}}},
	}
	t1 := &core.SessionMessageEntry{
		SessionEntryBase: core.SessionEntryBase{Id: "e-tool-1", Type: "message", ParentId: tselStrPtr("e-asst-1")},
		Message:          &ai.ToolResultMessage{Role: "toolResult", ToolName: "read", Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "log content"}}},
	}

	nodes := []core.SessionTreeNode{{
		Entry: u1,
		Children: []core.SessionTreeNode{{
			Entry:    a1,
			Label:    tselStrPtr("checkpoint"),
			Children: []core.SessionTreeNode{{Entry: t1}},
		}},
	}}

	host, tt := tselNewHostTester(t)
	var selectedEntry, labeledId string
	var labeledText *string
	dlg := NewTreeSelectorDialog(TreeSelectorConfig{
		Nodes:         nodes,
		CurrentLeafId: "e-tool-1",
		FilterMode:    core.TreeFilterModeDefault,
		OnSelect: func(id string) {
			selectedEntry = id
		},
		OnLabel: func(id string, lbl *string) {
			labeledId = id
			labeledText = lbl
		},
	})

	dh := dlg.Show(host)
	tt.Frame()
	if !tt.HasText("Investigate crash") || !tt.HasText("tool:read") || !tt.HasText("#checkpoint") {
		t.Fatalf("expected tree nodes rendered, got %v", tt.Texts())
	}

	// Switch to User Only -> assistant and tool hidden.
	if err := tt.Click("User Only"); err != nil {
		t.Fatalf("click User Only: %v", err)
	}
	tt.Frame()
	if tt.HasText("Reading logs") || !tt.HasText("Investigate crash") {
		t.Fatalf("expected User Only filter active, got %v", tt.Texts())
	}

	// Edit Label on e-user-1.
	if err := tt.Click("Edit Label"); err != nil {
		t.Fatalf("click Edit Label: %v", err)
	}
	tt.Frame()
	tt.Type("root-bug")
	if err := tt.Click("Save"); err != nil {
		t.Fatalf("click Save label: %v", err)
	}
	tt.Frame()
	if labeledId != "e-user-1" || labeledText == nil || *labeledText != "root-bug" {
		t.Fatalf("unexpected label callback: id=%q lbl=%v", labeledId, labeledText)
	}

	// Navigate to selected node.
	if err := tt.Click("Navigate Here"); err != nil {
		t.Fatalf("click Navigate Here: %v", err)
	}
	tt.Frame()
	if selectedEntry != "e-user-1" || dh.Visible() {
		t.Fatalf("expected navigation to e-user-1 and close, got %q visible=%v", selectedEntry, dh.Visible())
	}
}

func TestSettingsSelectorTogglesEnumsSubmenusAndApply(t *testing.T) {
	sm := core.SettingsManagerInMemory()
	sm.SetCompactionEnabled(true)
	sm.SetSteeringMode(agent.QueueModeOneAtATime)
	sm.SetDefaultThinkingLevel(ai.ModelThinkingLevelLow)

	ApplySettingsChange(sm, nil, "defaultModel", "anthropic/claude-sonnet-4")
	ApplySettingsChange(sm, nil, "quietStartup", "header")
	if p := sm.GetDefaultProvider(); p == nil || *p != "anthropic" {
		t.Fatalf("expected defaultProvider=anthropic, got %v", p)
	}
	if !sm.GetQuietStartup().Header {
		t.Fatal("expected quietStartup=header")
	}

	host, tt := tselNewHostTester(t)
	var changes []string
	dlg := NewSettingsSelectorDialog(SettingsSelectorConfig{
		SettingsManager: sm,
		AvailableThemes: []string{"dark", "light", "system"},
		OnChange: func(id, value string) {
			changes = append(changes, id+"="+value)
		},
	})

	dlg.Show(host)
	tt.Frame()
	if !tt.HasText("Auto-Compact") || !tt.HasText("Thinking Level") {
		t.Fatalf("expected settings items rendered, got %v", tt.Texts())
	}

	// Open Thinking Level submenu -> pick "high" -> verify SettingsManager updated.
	if err := tt.Click("Open Thinking Level"); err != nil {
		t.Fatalf("click Open Thinking Level: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Settings · Thinking Level") {
		t.Fatalf("expected submenu header, got %v", tt.Texts())
	}
	if err := tt.Click("high"); err != nil {
		t.Fatalf("pick high thinking: %v", err)
	}
	tt.Frame()
	if lvl := sm.GetDefaultThinkingLevel(); lvl == nil || *lvl != ai.ModelThinkingLevelHigh {
		t.Fatalf("expected persisted thinking level=high, got %v", lvl)
	}
	if !slices.Contains(changes, "thinkingLevel=high") {
		t.Fatalf("expected thinkingLevel=high callback, got %v", changes)
	}
	if err := tt.Click("Done"); err != nil {
		t.Fatalf("click Done: %v", err)
	}
}

func TestThemeSelectorLivePreviewAndCancelRevert(t *testing.T) {
	host, tt := tselNewHostTester(t)
	ApplyThemeToHost(host, "dark")
	initialTheme := host.Theme()
	if initialTheme == nil || initialTheme.Dark != true {
		t.Fatalf("expected initial dark theme, got %+v", initialTheme)
	}

	var previews []string
	var committed string
	var cancelled bool
	dlg := NewThemeSelectorDialog(ThemeSelectorConfig{
		Host:         host,
		CurrentTheme: "dark",
		OnPreview: func(name string) {
			previews = append(previews, name)
		},
		OnSelect: func(name string) {
			committed = name
		},
		OnCancel: func() {
			cancelled = true
		},
	})

	dh := dlg.Show(host)
	tt.Frame()
	dlg.Highlight("light")
	tt.Frame()

	if host.Theme() == nil || host.Theme().Dark {
		t.Fatalf("expected live preview to switch host to light theme, got %+v", host.Theme())
	}
	if !slices.Contains(previews, "light") {
		t.Fatalf("expected OnPreview(light), got %v", previews)
	}

	// Cancelling must revert host back to "dark".
	if err := tt.Click("Cancel"); err != nil {
		t.Fatalf("click Cancel: %v", err)
	}
	tt.Frame()
	if !cancelled || dh.Visible() {
		t.Fatalf("expected cancelled=true and closed overlay, got cancelled=%v visible=%v", cancelled, dh.Visible())
	}
	if host.Theme() == nil || !host.Theme().Dark {
		t.Fatalf("expected host theme reverted to dark on cancel, got %+v", host.Theme())
	}

	// Selecting light commits cleanly.
	dlg2 := NewThemeSelectorDialog(ThemeSelectorConfig{
		Host:         host,
		CurrentTheme: "dark",
		OnSelect: func(name string) {
			committed = name
		},
	})
	dlg2.Show(host)
	tt.Frame()
	if err := tt.Click("Filter..."); err != nil {
		t.Fatalf("focus filter: %v", err)
	}
	tt.Type("light")
	tt.Frame()
	if err := tt.Click("light"); err != nil {
		t.Fatalf("click light theme: %v", err)
	}
	tt.Frame()
	if committed != "light" || host.Theme() == nil || host.Theme().Dark {
		t.Fatalf("expected committed light theme, got committed=%q dark=%v", committed, host.Theme().Dark)
	}
}

func TestScopedModelsSelectorToggleFilterAndPersist(t *testing.T) {
	m1 := &ai.Model{BaseModel: ai.BaseModel{Id: "claude-sonnet-4", Name: "Sonnet", Provider: "anthropic"}, Reasoning: true}
	m2 := &ai.Model{BaseModel: ai.BaseModel{Id: "gpt-4o", Name: "GPT-4o", Provider: "openai"}}
	sm := core.SettingsManagerInMemory()

	host, tt := tselNewHostTester(t)
	var saved []core.ScopedModel
	var savedPersist bool
	dlg := NewScopedModelsSelectorDialog(ScopedModelsSelectorConfig{
		AllModels:       []*ai.Model{m1, m2},
		ScopedModels:    []core.ScopedModel{{Model: m1}},
		SettingsManager: sm,
		OnSave: func(sc []core.ScopedModel, persist bool) {
			saved = sc
			savedPersist = persist
		},
	})

	dlg.Show(host)
	tt.Frame()
	if err := tt.Click("Enable All"); err != nil {
		t.Fatalf("click Enable All: %v", err)
	}
	if err := tt.Click("Persist to settings"); err != nil {
		t.Fatalf("click Persist checkbox: %v", err)
	}
	if err := tt.Click("Save (2)"); err != nil {
		t.Fatalf("click Save (2): %v", err)
	}
	tt.Frame()

	if len(saved) != 2 || !savedPersist {
		t.Fatalf("expected 2 saved scoped models with persist=true, got len=%d persist=%v", len(saved), savedPersist)
	}
	if patterns := sm.GetEnabledModels(); len(patterns) != 2 {
		t.Fatalf("expected persisted enabledModels in SettingsManager, got %v", patterns)
	}
}

func TestOAuthLoginTrustForkExtensionAndHotkeysDialogs(t *testing.T) {
	host, tt := tselNewHostTester(t)

	// 1. OAuthSelectorDialog
	var chosenProv string
	oauthDlg := NewOAuthSelectorDialog(OAuthSelectorConfig{
		Mode: "login",
		Providers: []OAuthProviderEntry{
			{ID: "anthropic", Name: "Anthropic", LoggedIn: true},
			{ID: "github-copilot", Name: "GitHub Copilot", LoggedIn: false},
		},
		OnSelect: func(p OAuthProviderEntry) {
			chosenProv = p.ID
		},
	})
	oauthDlg.Show(host)
	tt.Frame()
	if err := tt.Click("GitHub Copilot"); err != nil {
		t.Fatalf("click provider: %v", err)
	}
	tt.Frame()
	if chosenProv != "github-copilot" {
		t.Fatalf("expected github-copilot selected, got %q", chosenProv)
	}

	// 2. LoginDialog concurrent Prompt/Notify/Complete under -race
	loginDlg := NewLoginDialog(LoginDialogConfig{ProviderName: "GitHub Copilot", Host: host})
	loginHandle := loginDlg.Show(host)
	tt.Frame()

	loginDlg.Notify(&ai.AuthEventDeviceCode{
		Type:            ai.AuthEventKindDeviceCode,
		UserCode:        "ABCD-1234",
		VerificationUri: "https://github.com/login/device",
	})
	tt.Frame()
	if !tt.HasText("ABCD-1234") {
		t.Fatalf("expected device code displayed, got %v", tt.Texts())
	}

	var wg sync.WaitGroup
	var promptAns string
	var promptErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		promptAns, promptErr = loginDlg.Prompt(context.Background(), &ai.AuthPromptText{
			Type:    ai.AuthPromptKindText,
			Message: "Enter organization slug",
		})
	}()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		tt.Frame()
		if tt.HasText("Enter organization slug") {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !tt.HasText("Enter organization slug") {
		t.Fatalf("prompt text never appeared: %v", tt.Texts())
	}
	tt.Type("my-org")
	if err := tt.Click("Submit"); err != nil {
		t.Fatalf("click Submit on login prompt: %v", err)
	}
	wg.Wait()
	if promptErr != nil || promptAns != "my-org" {
		t.Fatalf("expected 'my-org', got ans=%q err=%v", promptAns, promptErr)
	}
	loginDlg.Complete(true, "Signed in cleanly")
	tt.Frame()
	if !tt.HasText("Signed in cleanly") {
		t.Fatalf("expected completion message, got %v", tt.Texts())
	}
	if err := tt.Click("Close"); err != nil {
		t.Fatalf("click Close on login: %v", err)
	}
	tt.Frame()
	if loginHandle.Visible() {
		t.Fatal("expected LoginDialog overlay closed")
	}

	// Verify Prompt aborts with context.Canceled on Cancel().
	loginDlg2 := NewLoginDialog(LoginDialogConfig{ProviderName: "OAuth", Host: host})
	loginDlg2.Show(host)
	tt.Frame()
	wg.Add(1)
	var cancelErr error
	go func() {
		defer wg.Done()
		_, cancelErr = loginDlg2.Prompt(context.Background(), &ai.AuthPromptSecret{
			Type:    ai.AuthPromptKindSecret,
			Message: "API Key",
		})
	}()
	cancelDeadline := time.Now().Add(time.Second)
	for time.Now().Before(cancelDeadline) {
		tt.Frame()
		if tt.HasText("API Key") {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	loginDlg2.Cancel()
	wg.Wait()
	if cancelErr != context.Canceled {
		t.Fatalf("expected context.Canceled after Cancel(), got %v", cancelErr)
	}

	// 3. TrustSelectorDialog
	var trustOpt *core.ProjectTrustOption
	trustDlg := NewTrustSelectorDialog(TrustSelectorConfig{
		Cwd: "/tmp/repo",
		Options: []core.ProjectTrustOption{
			{Label: "Trust once", Trusted: true},
			{Label: "Untrusted", Trusted: false},
		},
		OnSelect: func(opt core.ProjectTrustOption) {
			cp := opt
			trustOpt = &cp
		},
	})
	trustDlg.Show(host)
	tt.Frame()
	if err := tt.Click("Trust once"); err != nil {
		t.Fatalf("click trust: %v", err)
	}
	tt.Frame()
	if trustOpt == nil || !trustOpt.Trusted {
		t.Fatalf("expected trusted option selected, got %+v", trustOpt)
	}

	// 4. ForkSelectorDialog
	var forkId string
	forkDlg := NewForkSelectorDialog(ForkSelectorConfig{
		Messages: []core.ForkableUserMessage{{EntryId: "entry-99", Text: "Try alternate fix"}},
		OnSelect: func(m core.ForkableUserMessage) { forkId = m.EntryId },
	})
	forkDlg.Show(host)
	tt.Frame()
	if err := tt.Click("Try alternate fix"); err != nil {
		t.Fatalf("click fork msg: %v", err)
	}
	tt.Frame()
	if forkId != "entry-99" {
		t.Fatalf("expected entry-99, got %q", forkId)
	}

	// 5. Extension modals (Select, Confirm, Input, Editor)
	var extChoice string
	NewExtensionSelectDialog(ExtensionSelectConfig{
		Title:    "Pick Action",
		Options:  []string{"alpha", "beta"},
		OnSelect: func(s string) { extChoice = s },
	}).Show(host)
	tt.Frame()
	_ = tt.Click("beta")
	tt.Frame()
	if extChoice != "beta" {
		t.Fatalf("expected beta, got %q", extChoice)
	}

	var confirmed *bool
	NewExtensionConfirmDialog(ExtensionConfirmConfig{
		Title:     "Deploy?",
		Message:   "Push release tag?",
		OnConfirm: func(b bool) { confirmed = &b },
	}).Show(host)
	tt.Frame()
	if err := tt.Click("Confirm"); err != nil {
		t.Fatalf("click Confirm: %v", err)
	}
	tt.Frame()
	if confirmed == nil || !*confirmed {
		t.Fatalf("expected confirmed=true, got %v", confirmed)
	}

	var inputVal string
	inDlg := NewExtensionInputDialog(ExtensionInputConfig{
		Title:        "Tag Name",
		InitialValue: "v1.",
		OnSubmit:     func(v string) { inputVal = v },
	})
	inDlg.Show(host)
	tt.Frame()
	tt.Type("5.2")
	_ = tt.Click("Submit")
	tt.Frame()
	if !strings.Contains(inputVal, "5.2") {
		t.Fatalf("expected submitted input containing 5.2, got %q", inputVal)
	}

	var editorText string
	edDlg := NewExtensionEditorDialog(ExtensionEditorConfig{
		Title:       "Edit Notes",
		InitialText: "Line 1",
		OnSubmit:    func(s string) { editorText = s },
	})
	edDlg.Show(host)
	tt.Frame()
	edDlg.SetText("Updated Notes")
	tt.Frame()
	if err := tt.Click("Save"); err != nil {
		t.Fatalf("click Save editor: %v", err)
	}
	tt.Frame()
	if editorText != "Updated Notes" {
		t.Fatalf("expected 'Updated Notes', got %q", editorText)
	}

	// 6. HotkeysDialog
	var closedHotkeys bool
	hkDlg := NewHotkeysDialog(HotkeysDialogConfig{
		OnClose: func() { closedHotkeys = true },
	})
	hkDlg.Show(host)
	tt.Frame()
	if !tt.HasText("Keyboard Shortcuts") || !tt.HasText("Ctrl+P") {
		t.Fatalf("expected default hotkeys rendered, got %v", tt.Texts())
	}
	if err := tt.Click("Close"); err != nil {
		t.Fatalf("click Close hotkeys: %v", err)
	}
	tt.Frame()
	if !closedHotkeys {
		t.Fatal("expected OnClose callback fired on Close")
	}
}

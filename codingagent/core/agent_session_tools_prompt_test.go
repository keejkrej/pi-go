package core_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/compat"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/internal/testkit"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

func tastpStrPtr(v string) *string { return &v }
func tastpBoolPtr(v bool) *bool    { return &v }

func tastpCreateSession(t *testing.T, opts *core.CreateAgentSessionOptions) (*core.AgentSession, *providers.FauxProviderRegistration, func()) {
	t.Helper()
	ctx := context.Background()
	tmpDir := t.TempDir()

	faux := compat.RegisterFauxProvider(&providers.RegisterFauxProviderOptions{
		Models: []providers.FauxModelDefinition{{Id: "faux-1"}},
	})
	authStorage := core.AuthStorageInMemory()
	key := "faux-key"
	if _, err := authStorage.Modify(ctx, string(faux.GetModel().Provider), func(ai.Credential) (ai.Credential, error) {
		return &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Key: &key}, nil
	}, nil); err != nil {
		faux.Unregister()
		t.Fatalf("AuthStorage.Modify failed: %v", err)
	}

	modelRegistry, err := testkit.CreateInMemoryModelRegistry(ctx, authStorage)
	if err != nil {
		faux.Unregister()
		t.Fatalf("CreateInMemoryModelRegistry failed: %v", err)
	}
	modelRuntime, err := testkit.GetModelRuntime(modelRegistry)
	if err != nil {
		faux.Unregister()
		t.Fatalf("GetModelRuntime failed: %v", err)
	}
	model := faux.GetModel()
	apiStr := string(model.Api)
	baseUrl := model.BaseUrl
	if err := modelRegistry.RegisterProvider(string(model.Provider), core.ProviderConfigInput{
		BaseUrl: &baseUrl,
		ApiKey:  &key,
		Api:     &apiStr,
		Models: []core.ProviderModelConfig{
			&core.ProviderChatModelConfig{
				ProviderModelConfigBase: core.ProviderModelConfigBase{
					Id:      model.Id,
					Name:    model.Name,
					Api:     &apiStr,
					BaseUrl: &baseUrl,
					Input:   model.Input,
					Cost:    model.Cost,
				},
				Reasoning:     model.Reasoning,
				ContextWindow: model.ContextWindow,
				MaxTokens:     model.MaxTokens,
			},
		},
	}); err != nil {
		modelRuntime.Close()
		faux.Unregister()
		t.Fatalf("RegisterProvider failed: %v", err)
	}

	if opts == nil {
		opts = &core.CreateAgentSessionOptions{}
	}
	if opts.Cwd == "" {
		opts.Cwd = tmpDir
	}
	if opts.AgentDir == "" {
		opts.AgentDir = tmpDir
	}
	if opts.AuthStorage == nil {
		opts.AuthStorage = authStorage
	}
	if opts.ModelRegistry == nil {
		opts.ModelRegistry = modelRegistry
	}
	if opts.SettingsManager == nil {
		opts.SettingsManager = core.SettingsManagerInMemory()
	}
	if opts.SessionManager == nil {
		opts.SessionManager = core.SessionManagerInMemory(tmpDir)
	}
	if opts.ResourceLoader == nil {
		opts.ResourceLoader = testkit.CreateTestResourceLoader()
	}
	if opts.Model == nil {
		opts.Model = faux.GetModel()
	}

	res, err := core.CreateAgentSession(ctx, opts)
	if err != nil {
		modelRegistry.UnregisterProvider(string(model.Provider))
		modelRuntime.Close()
		faux.Unregister()
		t.Fatalf("CreateAgentSession failed: %v", err)
	}
	return res.Session, faux, func() {
		res.Session.Dispose()
		modelRegistry.UnregisterProvider(string(model.Provider))
		modelRuntime.Close()
		faux.Unregister()
	}
}

func tastpJoinNames(names []string) string {
	return strings.Join(names, ",")
}

func tastpCustomToolDef(name string, opts ...func(*core.ToolDefinition)) core.ToolDefinition {
	def := core.ToolDefinition{
		Name:        name,
		Label:       name,
		Description: name + " description",
		Parameters:  typebox.Object(nil),
		Execute: func(ctx context.Context, toolCallId string, params *jsonx.Object, onUpdate agent.AgentToolUpdateCallback, extCtx core.ExtensionToolContext) (agent.AgentToolResult, error) {
			return agent.AgentToolResult{
				Content: ai.ContentList{&ai.TextContent{Type: "text", Text: name + " ok"}},
			}, nil
		},
	}
	for _, fn := range opts {
		fn(&def)
	}
	return def
}

func TestAgentSessionDefaultToolsSetting(t *testing.T) {
	ctx := context.Background()

	t.Run("uses the configured list as the initial built-in selection", func(t *testing.T) {
		sm := core.SettingsManagerInMemory(&core.Settings{
			DefaultTools: []string{"read", "grep", "find"},
		})
		s, _, cleanup := tastpCreateSession(t, &core.CreateAgentSessionOptions{
			SettingsManager: sm,
		})
		defer cleanup()

		got := tastpJoinNames(s.GetActiveToolNames())
		if got != "read,grep,find" {
			t.Fatalf("expected active tools [read,grep,find], got %s", got)
		}
	})

	t.Run("can select powershell instead of bash", func(t *testing.T) {
		sm := core.SettingsManagerInMemory(&core.Settings{
			DefaultTools: []string{"read", "powershell"},
		})
		s, _, cleanup := tastpCreateSession(t, &core.CreateAgentSessionOptions{
			SettingsManager: sm,
		})
		defer cleanup()

		if got := tastpJoinNames(s.GetActiveToolNames()); got != "read,powershell" {
			t.Fatalf("expected [read,powershell], got %s", got)
		}
	})

	t.Run("keeps SDK custom tools enabled and respects defaultActive=false", func(t *testing.T) {
		sm := core.SettingsManagerInMemory(&core.Settings{
			DefaultTools: []string{"read"},
		})
		activeCustom := tastpCustomToolDef("custom_active")
		inactiveCustom := tastpCustomToolDef("custom_inactive", func(d *core.ToolDefinition) {
			d.DefaultActive = tastpBoolPtr(false)
		})
		s, _, cleanup := tastpCreateSession(t, &core.CreateAgentSessionOptions{
			SettingsManager: sm,
			CustomTools:     []core.ToolDefinition{activeCustom, inactiveCustom},
		})
		defer cleanup()

		got := tastpJoinNames(s.GetActiveToolNames())
		if got != "read,custom_active" {
			t.Fatalf("expected [read,custom_active], got %s", got)
		}
	})

	t.Run("activates an inactive custom tool with +name", func(t *testing.T) {
		sm := core.SettingsManagerInMemory(&core.Settings{
			DefaultTools: []string{"read", "+custom_inactive"},
		})
		inactiveCustom := tastpCustomToolDef("custom_inactive", func(d *core.ToolDefinition) {
			d.DefaultActive = tastpBoolPtr(false)
		})
		s, _, cleanup := tastpCreateSession(t, &core.CreateAgentSessionOptions{
			SettingsManager: sm,
			CustomTools:     []core.ToolDefinition{inactiveCustom},
		})
		defer cleanup()

		if got := tastpJoinNames(s.GetActiveToolNames()); got != "read,custom_inactive" {
			t.Fatalf("expected [read,custom_inactive], got %s", got)
		}
	})

	t.Run("preserves explicit tool option precedence", func(t *testing.T) {
		sm := core.SettingsManagerInMemory(&core.Settings{
			DefaultTools: []string{"read", "grep"},
		})
		s, _, cleanup := tastpCreateSession(t, &core.CreateAgentSessionOptions{
			SettingsManager: sm,
			ToolNames:       []string{"read", "edit", "write"},
		})
		defer cleanup()

		if got := tastpJoinNames(s.GetActiveToolNames()); got != "read,edit,write" {
			t.Fatalf("expected explicit ToolNames to override defaultTools, got %s", got)
		}
	})

	t.Run("applies +name and -name tool options to the default selection", func(t *testing.T) {
		sm := core.SettingsManagerInMemory(&core.Settings{
			DefaultTools: []string{"read", "bash", "edit"},
		})
		s, _, cleanup := tastpCreateSession(t, &core.CreateAgentSessionOptions{
			SettingsManager: sm,
			ToolNames:       []string{"-bash", "+ls", "+grep"},
		})
		defer cleanup()

		got := tastpJoinNames(s.GetActiveToolNames())
		if got != "read,edit,ls,grep" {
			t.Fatalf("expected [read,edit,ls,grep], got %s", got)
		}
	})

	t.Run("reload applies newly added defaultTools while keeping explicit modifiers", func(t *testing.T) {
		tmpDir := t.TempDir()
		agentDir := filepath.Join(tmpDir, "agent")
		if err := os.MkdirAll(agentDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"defaultTools":["read","bash"]}`), 0o644); err != nil {
			t.Fatal(err)
		}
		sm := core.SettingsManagerCreate(tmpDir, agentDir)
		s, _, cleanup := tastpCreateSession(t, &core.CreateAgentSessionOptions{
			Cwd:             tmpDir,
			AgentDir:        agentDir,
			SettingsManager: sm,
			ToolNames:       []string{"-bash"},
		})
		defer cleanup()

		if got := tastpJoinNames(s.GetActiveToolNames()); got != "read" {
			t.Fatalf("expected initial [read], got %s", got)
		}

		// Update defaultTools in settings.json and reload.
		if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"defaultTools":["read","bash","grep"]}`), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := s.Reload(ctx); err != nil {
			t.Fatalf("Reload failed: %v", err)
		}
		// "-bash" remains removed; newly added "grep" becomes active!
		got := tastpJoinNames(s.GetActiveToolNames())
		if strings.Contains(got, "bash") || !strings.Contains(got, "grep") || !strings.Contains(got, "read") {
			t.Fatalf("expected read and grep without bash after Reload, got %s", got)
		}
	})

	t.Run("resume restores transcript loadout and applies options/excludes", func(t *testing.T) {
		tmpDir := t.TempDir()
		sessionMgr, err := core.SessionManagerCreate(tmpDir, tmpDir)
		if err != nil {
			t.Fatalf("SessionManagerCreate failed: %v", err)
		}
		sm := core.SettingsManagerInMemory()

		s1, faux1, cleanup1 := tastpCreateSession(t, &core.CreateAgentSessionOptions{
			Cwd:             tmpDir,
			AgentDir:        tmpDir,
			SessionManager:  sessionMgr,
			SettingsManager: sm,
		})
		faux1.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("one"), nil)},
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("two"), nil)},
		})
		if err := s1.Prompt(ctx, "hello"); err != nil {
			t.Fatalf("s1 Prompt 1 failed: %v", err)
		}
		s1.SetActiveToolsByName([]string{"read", "grep"})
		if err := s1.Prompt(ctx, "use grep"); err != nil {
			t.Fatalf("s1 Prompt 2 failed: %v", err)
		}
		sf := s1.SessionFile()
		cleanup1()

		// Resume saved session: transcript loadout [read, grep] should be restored.
		reopened, err := core.SessionManagerOpen(*sf)
		if err != nil {
			t.Fatalf("SessionManagerOpen failed: %v", err)
		}
		s2, _, cleanup2 := tastpCreateSession(t, &core.CreateAgentSessionOptions{
			Cwd:             tmpDir,
			AgentDir:        tmpDir,
			SessionManager:  reopened,
			SettingsManager: sm,
		})
		if got := tastpJoinNames(s2.GetActiveToolNames()); got != "read,grep" {
			t.Fatalf("expected restored loadout [read,grep], got %s", got)
		}
		cleanup2()

		// Resume with ExcludeTools removes excluded tool from restored loadout.
		reopened2, err := core.SessionManagerOpen(*sf)
		if err != nil {
			t.Fatalf("SessionManagerOpen 2 failed: %v", err)
		}
		s3, _, cleanup3 := tastpCreateSession(t, &core.CreateAgentSessionOptions{
			Cwd:             tmpDir,
			AgentDir:        tmpDir,
			SessionManager:  reopened2,
			SettingsManager: sm,
			ExcludeTools:    []string{"grep"},
		})
		if got := tastpJoinNames(s3.GetActiveToolNames()); got != "read" {
			t.Fatalf("expected [read] after ExcludeTools=[grep], got %s", got)
		}
		cleanup3()

		// Resume with explicit "+ls" applies modifier to recorded loadout.
		reopened3, err := core.SessionManagerOpen(*sf)
		if err != nil {
			t.Fatalf("SessionManagerOpen 3 failed: %v", err)
		}
		s4, _, cleanup4 := tastpCreateSession(t, &core.CreateAgentSessionOptions{
			Cwd:             tmpDir,
			AgentDir:        tmpDir,
			SessionManager:  reopened3,
			SettingsManager: sm,
			ToolNames:       []string{"+ls"},
		})
		gotNames := s4.GetActiveToolNames()
		// Mirror TS default-tools-setting.test.ts: compare sorted active tool names.
		for i := 0; i < len(gotNames); i++ {
			for j := i + 1; j < len(gotNames); j++ {
				if gotNames[j] < gotNames[i] {
					gotNames[i], gotNames[j] = gotNames[j], gotNames[i]
				}
			}
		}
		if got := tastpJoinNames(gotNames); got != "grep,ls,read" {
			t.Fatalf("expected sorted [grep,ls,read] with +ls modifier, got %s", got)
		}
		cleanup4()
	})

	t.Run("applies through service-based session creation", func(t *testing.T) {
		tmpDir := t.TempDir()
		faux := compat.RegisterFauxProvider(&providers.RegisterFauxProviderOptions{
			Models: []providers.FauxModelDefinition{{Id: "faux-1"}},
		})
		defer faux.Unregister()
		auth := core.AuthStorageInMemory()
		key := "faux-key"
		_, _ = auth.Modify(ctx, string(faux.GetModel().Provider), func(ai.Credential) (ai.Credential, error) {
			return &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Key: &key}, nil
		}, nil)
		sm := core.SettingsManagerInMemory(&core.Settings{
			DefaultTools: []string{"read", "find"},
		})
		svc, err := core.CreateAgentSessionServices(ctx, &core.CreateAgentSessionServicesOptions{
			Cwd:             tmpDir,
			AgentDir:        tmpDir,
			AuthStorage:     auth,
			SettingsManager: sm,
			ResourceLoader:  testkit.CreateTestResourceLoader(),
		})
		if err != nil {
			t.Fatalf("CreateAgentSessionServices failed: %v", err)
		}
		defer svc.ModelRuntime.Close()
		res, err := core.CreateAgentSessionFromServices(ctx, core.CreateAgentSessionFromServicesOptions{
			Services:       svc,
			SessionManager: core.SessionManagerInMemory(tmpDir),
			Model:          faux.GetModel(),
		})
		if err != nil {
			t.Fatalf("CreateAgentSessionFromServices failed: %v", err)
		}
		defer res.Session.Dispose()

		if got := tastpJoinNames(res.Session.GetActiveToolNames()); got != "read,find" {
			t.Fatalf("expected [read,find] via service-based creation, got %s", got)
		}
	})
}

func TestAgentSessionSystemPromptUpdates(t *testing.T) {
	ctx := context.Background()

	t.Run("declares the prompt and tools once and reuses them across resume", func(t *testing.T) {
		tmpDir := t.TempDir()
		sm, err := core.SessionManagerCreate(tmpDir, tmpDir)
		if err != nil {
			t.Fatalf("SessionManagerCreate failed: %v", err)
		}

		h1, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Cwd:            tmpDir,
			SessionManager: sm,
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness failed: %v", err)
		}
		var sysCount1 int
		var sawToolNames [][]string
		h1.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseFactory(func(reqCtx context.Context, tc *ai.TranscriptContext, opts *ai.SimpleStreamOptions, state *providers.FauxProviderState, model *ai.Model) (*ai.AssistantMessage, error) {
				var names []string
				for _, tool := range ai.GetCurrentTools(tc.Messages) {
					names = append(names, tool.Name)
				}
				sawToolNames = append(sawToolNames, names)
				for _, m := range tc.Messages {
					if _, ok := m.(*ai.SystemMessage); ok {
						sysCount1++
					}
				}
				return providers.FauxAssistantMessage(providers.FauxAssistantText("one"), nil), nil
			}),
			providers.FauxResponseFactory(func(reqCtx context.Context, tc *ai.TranscriptContext, opts *ai.SimpleStreamOptions, state *providers.FauxProviderState, model *ai.Model) (*ai.AssistantMessage, error) {
				var c int
				for _, m := range tc.Messages {
					if _, ok := m.(*ai.SystemMessage); ok {
						c++
					}
				}
				if c != 1 {
					t.Errorf("expected still 1 system message on turn 2, got %d", c)
				}
				return providers.FauxAssistantMessage(providers.FauxAssistantText("two"), nil), nil
			}),
		})

		if err := h1.Session.Prompt(ctx, "first"); err != nil {
			t.Fatalf("Prompt 1 failed: %v", err)
		}
		if err := h1.Session.Prompt(ctx, "second"); err != nil {
			t.Fatalf("Prompt 2 failed: %v", err)
		}
		if sysCount1 != 1 {
			t.Fatalf("expected 1 system message on first prompt, got %d", sysCount1)
		}
		if len(sawToolNames) == 0 || tastpJoinNames(sawToolNames[0]) != "read,bash,edit,write" {
			t.Fatalf("unexpected tools declared on first request: %v", sawToolNames)
		}
		sf := *h1.Session.SessionFile()
		h1.Cleanup()

		reopened, err := core.SessionManagerOpen(sf)
		if err != nil {
			t.Fatalf("SessionManagerOpen failed: %v", err)
		}
		h2, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Cwd:            tmpDir,
			SessionManager: reopened,
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness resume failed: %v", err)
		}
		defer h2.Cleanup()

		var resumeSysMsgs []*ai.SystemMessage
		h2.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseFactory(func(reqCtx context.Context, tc *ai.TranscriptContext, opts *ai.SimpleStreamOptions, state *providers.FauxProviderState, model *ai.Model) (*ai.AssistantMessage, error) {
				for _, m := range tc.Messages {
					if sm, ok := m.(*ai.SystemMessage); ok {
						resumeSysMsgs = append(resumeSysMsgs, sm)
					}
				}
				return providers.FauxAssistantMessage(providers.FauxAssistantText("resumed"), nil), nil
			}),
		})
		if err := h2.Session.Prompt(ctx, "third"); err != nil {
			t.Fatalf("resumed Prompt failed: %v", err)
		}
		if len(resumeSysMsgs) != 1 {
			b, _ := jsonx.Stringify(resumeSysMsgs)
			t.Fatalf("expected resumed session to reuse single system declaration without extra patch, got %d: %s", len(resumeSysMsgs), b)
		}
	})

	t.Run("does not patch the prompt when the active tools are only reordered", func(t *testing.T) {
		h, err := testkit.CreateSuiteHarness(ctx)
		if err != nil {
			t.Fatalf("CreateSuiteHarness failed: %v", err)
		}
		defer h.Cleanup()

		var systemCounts []int
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseFactory(func(reqCtx context.Context, tc *ai.TranscriptContext, opts *ai.SimpleStreamOptions, state *providers.FauxProviderState, model *ai.Model) (*ai.AssistantMessage, error) {
				c := 0
				for _, m := range tc.Messages {
					if _, ok := m.(*ai.SystemMessage); ok {
						c++
					}
				}
				systemCounts = append(systemCounts, c)
				return providers.FauxAssistantMessage(providers.FauxAssistantText("one"), nil), nil
			}),
			providers.FauxResponseFactory(func(reqCtx context.Context, tc *ai.TranscriptContext, opts *ai.SimpleStreamOptions, state *providers.FauxProviderState, model *ai.Model) (*ai.AssistantMessage, error) {
				c := 0
				for _, m := range tc.Messages {
					if _, ok := m.(*ai.SystemMessage); ok {
						c++
					}
				}
				systemCounts = append(systemCounts, c)
				return providers.FauxAssistantMessage(providers.FauxAssistantText("two"), nil), nil
			}),
		})

		if err := h.Session.Prompt(ctx, "first"); err != nil {
			t.Fatalf("Prompt 1 failed: %v", err)
		}
		h.Session.SetActiveToolsByName([]string{"write", "edit", "bash", "read"})
		if err := h.Session.Prompt(ctx, "second"); err != nil {
			t.Fatalf("Prompt 2 failed: %v", err)
		}
		if len(systemCounts) != 2 || systemCounts[0] != 1 || systemCounts[1] != 1 {
			t.Fatalf("expected no extra system patch on reorder (counts=%v)", systemCounts)
		}
	})

	t.Run("opens a transcript without a system message and declares the prompt on the first request", func(t *testing.T) {
		tmpDir := t.TempDir()
		sm := core.SessionManagerInMemory(tmpDir)
		_, _ = sm.AppendMessage(&ai.UserMessage{
			Role:      "user",
			Content:   ai.UserMessageContent{Text: tastpStrPtr("legacy user")},
			Timestamp: 1000,
		})
		_, _ = sm.AppendMessage(providers.FauxAssistantMessage(providers.FauxAssistantText("legacy assistant"), nil))

		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			SessionManager: sm,
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness failed: %v", err)
		}
		defer h.Cleanup()

		var sawSystem bool
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseFactory(func(reqCtx context.Context, tc *ai.TranscriptContext, opts *ai.SimpleStreamOptions, state *providers.FauxProviderState, model *ai.Model) (*ai.AssistantMessage, error) {
				sawSystem = ai.GetCurrentSystemPrompt(tc.Messages) != ""
				return providers.FauxAssistantMessage(providers.FauxAssistantText("ok"), nil), nil
			}),
		})
		if err := h.Session.Prompt(ctx, "next"); err != nil {
			t.Fatalf("Prompt failed: %v", err)
		}
		if !sawSystem {
			t.Fatalf("expected system prompt declared on first request after legacy transcript")
		}
	})

	t.Run("setActiveTools emits prompt sections and tool changes before the next request", func(t *testing.T) {
		first := tastpCustomToolDef("first", func(d *core.ToolDefinition) {
			d.PromptSnippet = tastpStrPtr("first prompt snippet")
			d.PromptGuidelines = []string{"Use first carefully."}
		})
		second := tastpCustomToolDef("second", func(d *core.ToolDefinition) {
			d.PromptSnippet = tastpStrPtr("second prompt snippet")
			d.PromptGuidelines = []string{"Use second carefully."}
		})
		s, faux, cleanup := tastpCreateSession(t, &core.CreateAgentSessionOptions{
			NoTools:                core.NoToolsBuiltin,
			CustomTools:            []core.ToolDefinition{first, second},
			InitialActiveToolNames: []string{"first"},
		})
		defer cleanup()

		var prompts []string
		var toolSets [][]string
		faux.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseFactory(func(reqCtx context.Context, tc *ai.TranscriptContext, opts *ai.SimpleStreamOptions, state *providers.FauxProviderState, model *ai.Model) (*ai.AssistantMessage, error) {
				prompts = append(prompts, ai.GetCurrentSystemPrompt(tc.Messages))
				var names []string
				for _, tool := range ai.GetCurrentTools(tc.Messages) {
					names = append(names, tool.Name)
				}
				toolSets = append(toolSets, names)
				return providers.FauxAssistantMessage(providers.FauxAssistantText("one"), nil), nil
			}),
			providers.FauxResponseFactory(func(reqCtx context.Context, tc *ai.TranscriptContext, opts *ai.SimpleStreamOptions, state *providers.FauxProviderState, model *ai.Model) (*ai.AssistantMessage, error) {
				prompts = append(prompts, ai.GetCurrentSystemPrompt(tc.Messages))
				var names []string
				for _, tool := range ai.GetCurrentTools(tc.Messages) {
					names = append(names, tool.Name)
				}
				toolSets = append(toolSets, names)
				return providers.FauxAssistantMessage(providers.FauxAssistantText("two"), nil), nil
			}),
		})

		if err := s.Prompt(ctx, "turn 1"); err != nil {
			t.Fatalf("Prompt 1 failed: %v", err)
		}
		s.SetActiveToolsByName([]string{"second"})
		if err := s.Prompt(ctx, "turn 2"); err != nil {
			t.Fatalf("Prompt 2 failed: %v", err)
		}

		if len(prompts) != 2 || len(toolSets) != 2 {
			t.Fatalf("expected 2 requests, got prompts=%d tools=%d", len(prompts), len(toolSets))
		}
		if !strings.Contains(prompts[0], "first prompt snippet") || !strings.Contains(prompts[0], "Use first carefully.") {
			t.Fatalf("expected first tool prompt snippet & guidelines in turn 1, got:\n%s", prompts[0])
		}
		if strings.Contains(prompts[1], "first prompt snippet") || !strings.Contains(prompts[1], "second prompt snippet") ||
			strings.Contains(prompts[1], "Use first carefully.") || !strings.Contains(prompts[1], "Use second carefully.") {
			t.Fatalf("expected turn 2 system prompt to swap to second tool, got:\n%s", prompts[1])
		}
		if tastpJoinNames(toolSets[0]) != "first,second" || tastpJoinNames(toolSets[1]) != "second" {
			t.Fatalf("expected tool sets [first,second] then [second], got %v", toolSets)
		}
	})
}

func TestAgentSessionBeforeAgentStartSectionsAndReloadPendingTools(t *testing.T) {
	ctx := context.Background()

	t.Run("preserves before_agent_start Sections and PromptGuidelines in prompts and across turns", func(t *testing.T) {
		tmpDir := t.TempDir()
		extResult := testkit.CreateTestExtensionsResult()
		extResult.Extensions = []core.Extension{
			{
				Path: "mem:sections",
				Handlers: map[string][]any{
					"before_agent_start": {
						core.ExtensionHandler[core.BeforeAgentStartEvent, core.BeforeAgentStartEventResult](func(_ context.Context, ev *core.BeforeAgentStartEvent, _ core.ExtensionContext) (*core.BeforeAgentStartEventResult, error) {
							if ev.SystemPromptOptions.Sections == nil {
								ev.SystemPromptOptions.Sections = omap.NewMap[string, string]()
							}
							ev.SystemPromptOptions.Sections.Set("mcp_servers", "- fake (codemode)")
							ev.SystemPromptOptions.PromptGuidelines = append(ev.SystemPromptOptions.PromptGuidelines, "Always obey test MCP guideline.")
							return nil, nil
						}),
					},
				},
			},
		}
		loader := testkit.CreateTestResourceLoader(&testkit.CreateTestResourceLoaderOptions{
			ExtensionsResult: &extResult,
		})
		s, faux, cleanup := tastpCreateSession(t, &core.CreateAgentSessionOptions{
			Cwd:            tmpDir,
			AgentDir:       tmpDir,
			NoTools:        core.NoToolsBuiltin,
			ResourceLoader: loader,
			CustomTools:    []core.ToolDefinition{tastpCustomToolDef("echo")},
		})
		defer cleanup()

		var turnPrompts []string
		var liveSysPrompts []string
		faux.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseFactory(func(_ context.Context, tc *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
				turnPrompts = append(turnPrompts, ai.GetCurrentSystemPrompt(tc.Messages))
				liveSysPrompts = append(liveSysPrompts, s.SystemPrompt())
				return providers.FauxAssistantMessage(providers.FauxAssistantBlock{
					Block: providers.FauxToolCall("echo", jsonx.NewObject(), nil),
				}, nil), nil
			}),
			providers.FauxResponseFactory(func(_ context.Context, tc *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
				turnPrompts = append(turnPrompts, ai.GetCurrentSystemPrompt(tc.Messages))
				liveSysPrompts = append(liveSysPrompts, s.SystemPrompt())
				return providers.FauxAssistantMessage(providers.FauxAssistantText("done"), nil), nil
			}),
		})

		if err := s.Prompt(ctx, "hello"); err != nil {
			t.Fatalf("Prompt failed: %v", err)
		}
		if len(turnPrompts) != 2 || len(liveSysPrompts) != 2 {
			t.Fatalf("expected 2 turns, got turnPrompts=%d liveSysPrompts=%d", len(turnPrompts), len(liveSysPrompts))
		}
		for i, p := range turnPrompts {
			if !strings.Contains(p, "<mcp_servers>\n- fake (codemode)\n</mcp_servers>") {
				t.Fatalf("turn %d projected system prompt missing <mcp_servers> section:\n%s", i, p)
			}
			if !strings.Contains(p, "Always obey test MCP guideline.") {
				t.Fatalf("turn %d projected system prompt missing PromptGuidelines:\n%s", i, p)
			}
		}
		for i, p := range liveSysPrompts {
			if !strings.Contains(p, "<mcp_servers>\n- fake (codemode)\n</mcp_servers>") ||
				!strings.Contains(p, "Always obey test MCP guideline.") {
				t.Fatalf("turn %d Session.SystemPrompt() missing Sections or PromptGuidelines:\n%s", i, p)
			}
		}
	})

	t.Run("reload preserves pendingToolOrder so deferred tools re-activate even when another extension hides its own tool", func(t *testing.T) {
		tmpDir := t.TempDir()
		var stage int
		var deferredAPI core.ExtensionAPI

		loader := core.NewDefaultResourceLoader(core.DefaultResourceLoaderOptions{
			Cwd:               tmpDir,
			AgentDir:          tmpDir,
			NoExtensions:      true,
			NoSkills:          true,
			NoPromptTemplates: true,
			NoThemes:          true,
			NoContextFiles:    true,
			ExtensionFactories: []core.InlineExtension{
				{
					Name: "inline:mcp-deferred",
					Factory: func(pi core.ExtensionAPI) error {
						deferredAPI = pi
						if stage == 0 {
							def := tastpCustomToolDef("mcp__docs__search", func(d *core.ToolDefinition) {
								d.DefaultActive = tastpBoolPtr(false)
							})
							pi.RegisterTool(def)
						}
						return nil
					},
				},
				{
					Name: "inline:drop-loader",
					Factory: func(pi core.ExtensionAPI) error {
						pi.RegisterTool(tastpCustomToolDef("loader"))
						pi.OnSessionStart(func(_ context.Context, _ *core.SessionStartEvent, _ core.ExtensionContext) (*core.NoResult, error) {
							var next []string
							for _, name := range pi.GetActiveTools() {
								if name != "loader" {
									next = append(next, name)
								}
							}
							pi.SetActiveTools(next)
							return nil, nil
						})
						return nil
					},
				},
			},
		})
		defer loader.Close()
		if err := loader.Reload(ctx); err != nil {
			t.Fatalf("initial resource loader Reload failed: %v", err)
		}

		s, _, cleanup := tastpCreateSession(t, &core.CreateAgentSessionOptions{
			Cwd:            tmpDir,
			AgentDir:       tmpDir,
			ResourceLoader: loader,
		})
		defer cleanup()

		// Activate the defaultActive=false tool during the session before reload.
		s.SetActiveToolsByName([]string{"read", "bash", "mcp__docs__search"})
		if !slices.Contains(s.GetActiveToolNames(), "mcp__docs__search") {
			t.Fatalf("expected mcp__docs__search active before reload, got %v", s.GetActiveToolNames())
		}

		// Simulate reload where session_start hides "loader" and the MCP server connects asynchronously afterwards.
		stage = 1
		if err := s.BindExtensions(ctx, core.ExtensionBindings{OnError: func(core.ExtensionError) {}}); err != nil {
			t.Fatalf("BindExtensions failed: %v", err)
		}
		if err := s.Reload(ctx); err != nil {
			t.Fatalf("AgentSession.Reload failed: %v", err)
		}
		if slices.Contains(s.GetActiveToolNames(), "loader") {
			t.Fatalf("expected drop-loader extension to hide loader on reload session_start, got %v", s.GetActiveToolNames())
		}

		// Asynchronous post-reload registration of the defaultActive=false tool must restore it to active.
		deferredAPI.RegisterTool(tastpCustomToolDef("mcp__docs__search", func(d *core.ToolDefinition) {
			d.DefaultActive = tastpBoolPtr(false)
		}))

		activeAfter := s.GetActiveToolNames()
		if !slices.Contains(activeAfter, "mcp__docs__search") {
			t.Fatalf("expected mcp__docs__search to re-activate after post-reload registration, got %v", activeAfter)
		}
		if slices.Contains(activeAfter, "loader") {
			t.Fatalf("expected loader to stay inactive after mcp__docs__search re-activates, got %v", activeAfter)
		}
	})
}

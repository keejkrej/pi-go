package gui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/compat"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/gui"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/typebox"
	"github.com/keejkrej/pi-go/tui"
)

func tgmBoolPtr(v bool) *bool { return &v }
func tgmIntPtr(v int) *int    { return &v }
func tgmStrPtr(v string) *string {
	return &v
}

type tgmFixtureOpts struct {
	tools         []*agent.AgentTool
	inMemory      bool
	withResources bool
}

type tgmFixture struct {
	runtime    *core.AgentSessionRuntime
	faux       *providers.FauxProviderRegistration
	provider   string
	cwd        string
	agentDir   string
	sessionDir string
}

func tgmNewFixture(t *testing.T, opts *tgmFixtureOpts) *tgmFixture {
	t.Helper()
	ctx := context.Background()
	tempRoot := t.TempDir()
	cwd := filepath.Join(tempRoot, "cwd")
	agentDir := filepath.Join(tempRoot, "agent")
	sessionDir := filepath.Join(tempRoot, "sessions")
	for _, d := range []string{cwd, agentDir, sessionDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("MkdirAll(%s): %v", d, err)
		}
	}

	withResources := false
	var extraTools []*agent.AgentTool
	inMemory := false
	if opts != nil {
		withResources = opts.withResources
		extraTools = opts.tools
		inMemory = opts.inMemory
	}

	if withResources {
		promptDir := filepath.Join(agentDir, "prompts")
		skillDir := filepath.Join(agentDir, "skills", "demo-skill")
		for _, d := range []string{promptDir, skillDir} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatalf("MkdirAll(%s): %v", d, err)
			}
		}
		if err := os.WriteFile(filepath.Join(promptDir, "greet.md"), []byte("---\ndescription: Friendly greeting template\n---\nHello from template: $1\n"), 0o644); err != nil {
			t.Fatalf("WriteFile prompt template: %v", err)
		}
		if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: demo-skill\ndescription: Demo skill description\n---\nDemo skill body instructions.\n"), 0o644); err != nil {
			t.Fatalf("WriteFile SKILL.md: %v", err)
		}
	}

	provName := t.Name() + "-" + time.Now().Format("150405.000000000")
	provName = strings.ReplaceAll(provName, "/", "-")
	fauxReg := compat.RegisterFauxProvider(&providers.RegisterFauxProviderOptions{
		Provider: tgmStrPtr(provName),
		Models: []providers.FauxModelDefinition{
			{
				Id:            "faux-1",
				Name:          tgmStrPtr("Faux One"),
				Reasoning:     tgmBoolPtr(false),
				Input:         []string{"text", "image"},
				ContextWindow: tgmIntPtr(128_000),
				MaxTokens:     tgmIntPtr(4096),
			},
			{
				Id:            "faux-2",
				Name:          tgmStrPtr("Faux Two"),
				Reasoning:     tgmBoolPtr(true),
				Input:         []string{"text", "image"},
				ContextWindow: tgmIntPtr(200_000),
				MaxTokens:     tgmIntPtr(8192),
			},
		},
	})
	t.Cleanup(func() {
		if fauxReg != nil && fauxReg.Unregister != nil {
			fauxReg.Unregister()
		}
	})

	settingsBytes, err := jsonx.Marshal(map[string]any{
		"defaultProvider": provName,
		"defaultModel":    "faux-1",
	})
	if err != nil {
		t.Fatalf("Marshal settings: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), settingsBytes, 0o644); err != nil {
		t.Fatalf("WriteFile settings.json: %v", err)
	}

	authStorage := core.AuthStorageCreate(filepath.Join(agentDir, "auth.json"))
	fauxKey := "faux-key"
	if _, err := authStorage.Modify(ctx, provName, func(ai.Credential) (ai.Credential, error) {
		return &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Key: &fauxKey}, nil
	}, nil); err != nil {
		t.Fatalf("AuthStorage.Modify: %v", err)
	}

	providerModels := make([]core.ProviderModelConfig, 0, len(fauxReg.Models))
	for _, m := range fauxReg.Models {
		if m == nil {
			continue
		}
		modelApi := string(m.Api)
		modelBaseUrl := m.BaseUrl
		providerModels = append(providerModels, &core.ProviderChatModelConfig{
			ProviderModelConfigBase: core.ProviderModelConfigBase{
				Id:      m.Id,
				Name:    m.Name,
				Api:     &modelApi,
				BaseUrl: &modelBaseUrl,
				Input:   m.Input,
				Cost:    m.Cost,
			},
			Reasoning:     m.Reasoning,
			ContextWindow: m.ContextWindow,
			MaxTokens:     m.MaxTokens,
		})
	}
	apiStr := fauxReg.Api
	var baseUrl string
	if len(fauxReg.Models) > 0 && fauxReg.Models[0] != nil {
		baseUrl = fauxReg.Models[0].BaseUrl
	}

	createRuntime := func(c context.Context, args core.AgentSessionRuntimeCreateArgs) (*core.CreateAgentSessionRuntimeResult, error) {
		services, err := core.CreateAgentSessionServices(c, &core.CreateAgentSessionServicesOptions{
			Cwd:         args.Cwd,
			AgentDir:    args.AgentDir,
			AuthStorage: authStorage,
			ResourceLoaderOptions: &core.DefaultResourceLoaderOptions{
				NoExtensions:      true,
				NoSkills:          !withResources,
				NoPromptTemplates: !withResources,
				NoThemes:          true,
			},
		})
		if err != nil {
			return nil, err
		}
		if err := services.ModelRegistry.RegisterProvider(provName, core.ProviderConfigInput{
			BaseUrl: &baseUrl,
			ApiKey:  &fauxKey,
			Api:     &apiStr,
			Models:  providerModels,
		}); err != nil {
			return nil, err
		}
		res, err := core.CreateAgentSessionFromServices(c, core.CreateAgentSessionFromServicesOptions{
			Services:          services,
			SessionManager:    args.SessionManager,
			SessionStartEvent: args.SessionStartEvent,
			Tools:             extraTools,
		})
		if err != nil {
			return nil, err
		}
		return &core.CreateAgentSessionRuntimeResult{
			CreateAgentSessionResult: *res,
			Services:                 services,
			Diagnostics:              services.Diagnostics,
		}, nil
	}

	var sm *core.SessionManager
	if inMemory {
		sm = core.SessionManagerInMemory(cwd)
	} else {
		sm, err = core.SessionManagerCreate(cwd, sessionDir)
		if err != nil {
			t.Fatalf("SessionManagerCreate: %v", err)
		}
	}

	rt, err := core.CreateAgentSessionRuntime(ctx, createRuntime, core.CreateAgentSessionRuntimeOptions{
		Cwd:            cwd,
		AgentDir:       agentDir,
		SessionManager: sm,
	})
	if err != nil {
		t.Fatalf("CreateAgentSessionRuntime: %v", err)
	}
	t.Cleanup(func() {
		_ = rt.Dispose(context.Background())
	})

	return &tgmFixture{
		runtime:    rt,
		faux:       fauxReg,
		provider:   provName,
		cwd:        cwd,
		agentDir:   agentDir,
		sessionDir: sessionDir,
	}
}

type tgmDesktopCapture struct {
	mu         sync.Mutex
	clipboard  []string
	noticedMsg []string
}

func tgmNewModeAndTester(t *testing.T, rt *core.AgentSessionRuntime, opts *GuiModeOptions) (*GuiMode, *ui.Tester, *tgmDesktopCapture) {
	t.Helper()
	cap := &tgmDesktopCapture{}
	cfg := GuiModeOptions{
		SkipWindow: true,
		Desktop: &gui.DesktopHooks{
			WriteClipboardText: func(text string) error {
				cap.mu.Lock()
				cap.clipboard = append(cap.clipboard, text)
				cap.mu.Unlock()
				return nil
			},
			Notify: func(nopts mygo.NotificationOptions) error {
				cap.mu.Lock()
				cap.noticedMsg = append(cap.noticedMsg, nopts.Title+": "+nopts.Body)
				cap.mu.Unlock()
				return nil
			},
		},
	}
	if opts != nil {
		cfg = *opts
		cfg.SkipWindow = true
		if cfg.Desktop == nil {
			cfg.Desktop = &gui.DesktopHooks{
				WriteClipboardText: func(text string) error {
					cap.mu.Lock()
					cap.clipboard = append(cap.clipboard, text)
					cap.mu.Unlock()
					return nil
				},
			}
		}
	}
	mode := NewGuiMode(rt, &cfg)
	tt := ui.NewTester(mode.GUI().View, 1100, 800)
	t.Cleanup(func() {
		mode.Stop()
	})
	return mode, tt, cap
}

func tgmWaitUntil(t *testing.T, mode *GuiMode, tt *ui.Tester, timeout time.Duration, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if mode != nil {
			mode.FlushPending()
		}
		if tt != nil {
			tt.Frame()
		}
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			var texts []string
			if tt != nil {
				texts = tt.Texts()
			}
			t.Fatalf("timed out waiting for %s; visible texts=%v", desc, texts)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestGuiModeRunAndInitialLifecycle(t *testing.T) {
	fx := tgmNewFixture(t, nil)
	fx.faux.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("initial reply ready"), nil)},
	})

	readyCh := make(chan *GuiMode, 1)
	var shutdownCalled atomic.Int32
	fallback := "requested model fallback used"
	initPrompt := "bootstrap task"
	darkTheme := "dark"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- RunGuiMode(ctx, fx.runtime, &GuiModeOptions{
			SkipWindow:           true,
			MigratedProviders:    []string{"legacy-anthropic"},
			ModelFallbackMessage: &fallback,
			InitialMessage:       &initPrompt,
			InitialThemeSetting:  &darkTheme,
			StartupDiagnostics: []core.AgentSessionRuntimeDiagnostic{
				{Type: "warning", Message: "sample startup warning"},
			},
			OnReady: func(mode *GuiMode) {
				mode.OnShutdown(func() {
					shutdownCalled.Add(1)
				})
				readyCh <- mode
			},
		})
	}()

	var mode *GuiMode
	select {
	case mode = <-readyCh:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for OnReady")
	}

	tt := ui.NewTester(mode.GUI().View, 1100, 800)
	tgmWaitUntil(t, mode, tt, 5*time.Second, "initial assistant reply and startup notices", func() bool {
		return tt.HasText("initial reply ready") &&
			tt.HasText("sample startup warning") &&
			tt.HasText("requested model fallback used")
	})

	if mode.Session() != fx.runtime.Session {
		t.Fatal("expected mode.Session() to match active runtime session")
	}
	if mode.Runtime() != fx.runtime {
		t.Fatal("expected mode.Runtime() to match runtime")
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("RunGuiMode returned error after cancel: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for RunGuiMode exit")
	}

	if shutdownCalled.Load() != 1 {
		t.Fatalf("expected OnShutdown called once, got %d", shutdownCalled.Load())
	}
}

func TestGuiModePromptStreamingAndToolExecution(t *testing.T) {
	toolStarted := make(chan struct{})
	releaseTool := make(chan struct{})
	echoTool := &agent.AgentTool{
		Name:        "demo_echo",
		Label:       "Demo Echo",
		Description: "Streams progress and finishes",
		Parameters:  typebox.Object(nil),
		Execute: func(ctx context.Context, toolCallId string, params any, onUpdate agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
			if onUpdate != nil {
				onUpdate(agent.AgentToolResult{
					Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "streaming tool chunk"}},
				})
			}
			close(toolStarted)
			select {
			case <-releaseTool:
			case <-ctx.Done():
				return agent.AgentToolResult{}, ctx.Err()
			}
			return agent.AgentToolResult{
				Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "demo tool completed"}},
			}, nil
		},
	}

	fx := tgmNewFixture(t, &tgmFixtureOpts{
		tools: []*agent.AgentTool{echoTool},
	})

	stopTool := ai.StopReasonToolUse
	fx.faux.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
				providers.FauxThinking("considering how to answer"),
				providers.FauxToolCall("demo_echo", jsonx.NewObject(), nil),
			}, &providers.FauxAssistantMessageOptions{
				StopReason: &stopTool,
			}),
		},
		providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
				providers.FauxThinking("internal thought process"),
				providers.FauxText("final streamed answer"),
			}, nil),
		},
	})

	mode, tt, _ := tgmNewModeAndTester(t, fx.runtime, nil)
	tt.Frame()

	// Submit prompt through focused Editor.
	tt.Type("please run tool")
	tt.Key(0, ui.KeyEnter)

	select {
	case <-toolStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for tool execution to start")
	}

	tgmWaitUntil(t, mode, tt, 3*time.Second, "tool card and stop button during execution", func() bool {
		return tt.HasText("demo_echo") && tt.HasText("streaming tool chunk") && tt.HasText("Stop")
	})

	close(releaseTool)

	tgmWaitUntil(t, mode, tt, 5*time.Second, "final assistant answer after tool completion", func() bool {
		return !mode.Session().IsStreaming() && tt.HasText("final streamed answer") && tt.HasText("faux-1")
	})

	// Toggle expanded tools (Ctrl+O) and thinking visibility (Ctrl+T).
	tt.Key(ui.Ctrl, ui.KeyO)
	tt.Frame()
	if !tt.HasText("demo tool completed") {
		t.Fatalf("expected expanded tool output visible after Ctrl+O, texts=%v", tt.Texts())
	}

	tt.Key(ui.Ctrl, ui.KeyT)
	tt.Frame()
	if !tt.HasText("internal thought process") {
		t.Fatalf("expected expanded thinking text visible after Ctrl+T, texts=%v", tt.Texts())
	}
}

func TestGuiModeQueuesSteeringFollowUpAndRestore(t *testing.T) {
	toolStarted := make(chan struct{})
	releaseTool := make(chan struct{})
	waitTool := &agent.AgentTool{
		Name:        "hold",
		Label:       "Hold",
		Description: "Hold execution",
		Parameters:  typebox.Object(nil),
		Execute: func(ctx context.Context, toolCallId string, params any, onUpdate agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
			close(toolStarted)
			select {
			case <-releaseTool:
			case <-ctx.Done():
				return agent.AgentToolResult{}, ctx.Err()
			}
			return agent.AgentToolResult{
				Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "hold done"}},
			}, nil
		},
	}

	fx := tgmNewFixture(t, &tgmFixtureOpts{
		tools: []*agent.AgentTool{waitTool},
	})
	stopTool := ai.StopReasonToolUse
	fx.faux.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
				providers.FauxToolCall("hold", jsonx.NewObject(), nil),
			}, &providers.FauxAssistantMessageOptions{
				StopReason: &stopTool,
			}),
		},
		providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(providers.FauxAssistantText("after steering"), nil),
		},
		providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(providers.FauxAssistantText("after follow-up"), nil),
		},
	})

	mode, tt, _ := tgmNewModeAndTester(t, fx.runtime, nil)
	tt.Frame()

	tt.Type("start long work")
	tt.Key(0, ui.KeyEnter)

	select {
	case <-toolStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for hold tool")
	}
	tgmWaitUntil(t, mode, tt, 3*time.Second, "streaming started", func() bool {
		return mode.Session().IsStreaming()
	})

	// While streaming, Enter queues steering and Alt+Enter queues follow-up.
	tt.Type("queued follow")
	tt.Key(ui.Alt, ui.KeyEnter)

	tgmWaitUntil(t, mode, tt, 3*time.Second, "follow-up banner rendered", func() bool {
		return mode.Session().PendingMessageCount() == 1 && tt.HasText("Follow-up (1)") && tt.HasText("queued follow")
	})

	// Clear Queue restores queued text into editor, then refocus editor to re-submit.
	if err := tt.Click("Clear Queue"); err != nil {
		t.Fatalf("Click Clear Queue: %v", err)
	}
	tt.Frame()
	if got := mode.editor.Text(); !strings.Contains(got, "queued follow") {
		t.Fatalf("expected restored follow-up text in editor, got %q", got)
	}
	if err := tt.Click("Prompt editor"); err != nil {
		t.Fatalf("Click Prompt editor: %v", err)
	}
	tt.Frame()

	// Re-submit as follow-up and also steer.
	tt.Key(ui.Alt, ui.KeyEnter)
	tt.Frame()
	tt.Type("urgent steer")
	tt.Key(0, ui.KeyEnter)
	tgmWaitUntil(t, mode, tt, 3*time.Second, "steering queued", func() bool {
		return mode.Session().PendingMessageCount() >= 2
	})

	close(releaseTool)
	tgmWaitUntil(t, mode, tt, 5*time.Second, "all queued turns settled", func() bool {
		return !mode.Session().IsStreaming() && tt.HasText("after follow-up")
	})
}

func TestGuiModeBashAndEscapeAbort(t *testing.T) {
	toolStarted := make(chan struct{})
	abortedCh := make(chan struct{})
	blockTool := &agent.AgentTool{
		Name:        "block_until_abort",
		Label:       "Block",
		Description: "Blocks until cancelled",
		Parameters:  typebox.Object(nil),
		Execute: func(ctx context.Context, toolCallId string, params any, onUpdate agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
			close(toolStarted)
			<-ctx.Done()
			close(abortedCh)
			return agent.AgentToolResult{}, ctx.Err()
		},
	}

	fx := tgmNewFixture(t, &tgmFixtureOpts{
		tools: []*agent.AgentTool{blockTool},
	})
	stopTool := ai.StopReasonToolUse
	fx.faux.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
				providers.FauxToolCall("block_until_abort", jsonx.NewObject(), nil),
			}, &providers.FauxAssistantMessageOptions{
				StopReason: &stopTool,
			}),
		},
		providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(providers.FauxAssistantText("should not reach"), nil),
		},
	})

	mode, tt, _ := tgmNewModeAndTester(t, fx.runtime, nil)
	tt.Frame()

	// Execute !bash command via Editor.
	tt.Type("!echo gui-bash-output")
	tt.Key(0, ui.KeyEnter)

	tgmWaitUntil(t, mode, tt, 5*time.Second, "bash command output visible", func() bool {
		return !mode.Session().IsBashRunning() && tt.HasText("gui-bash-output")
	})

	// Execute !!bash command (excluded from LLM context).
	tt.Type("!!echo excluded-bash-output")
	tt.Key(0, ui.KeyEnter)

	tgmWaitUntil(t, mode, tt, 5*time.Second, "excluded bash output visible", func() bool {
		return !mode.Session().IsBashRunning() && tt.HasText("excluded-bash-output")
	})

	var sawIncluded, sawExcluded bool
	for _, msg := range mode.Session().Messages() {
		if bm, ok := msg.(*core.BashExecutionMessage); ok && bm != nil {
			excluded := bm.ExcludeFromContext != nil && *bm.ExcludeFromContext
			if strings.Contains(bm.Output, "gui-bash-output") && !excluded {
				sawIncluded = true
			}
			if strings.Contains(bm.Output, "excluded-bash-output") && excluded {
				sawExcluded = true
			}
		}
	}
	if !sawIncluded || !sawExcluded {
		t.Fatalf("expected both bash messages recorded properly (included=%v excluded=%v)", sawIncluded, sawExcluded)
	}

	// Start agent turn and abort via Escape.
	tt.Type("start abortable task")
	tt.Key(0, ui.KeyEnter)

	select {
	case <-toolStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for block tool")
	}

	tt.Key(0, ui.KeyEscape)
	select {
	case <-abortedCh:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Escape abort")
	}

	tgmWaitUntil(t, mode, tt, 5*time.Second, "session idle after Escape abort", func() bool {
		return !mode.Session().IsStreaming()
	})
}

func TestGuiModeSlashCommandsAndTemplates(t *testing.T) {
	fx := tgmNewFixture(t, &tgmFixtureOpts{
		withResources: true,
	})
	fx.faux.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("copied assistant output"), nil)},
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("template reply ok"), nil)},
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("skill reply ok"), nil)},
	})

	mode, tt, cap := tgmNewModeAndTester(t, fx.runtime, nil)
	tt.Frame()

	// Normal turn first so /copy, /export, /session have transcript content.
	tt.Type("hello")
	tt.Key(0, ui.KeyEnter)
	tgmWaitUntil(t, mode, tt, 5*time.Second, "first reply", func() bool {
		return !mode.Session().IsStreaming() && tt.HasText("copied assistant output")
	})

	// Test prompt template expansion via slash command & Editor submit.
	tt.Type("/greet world")
	tt.Key(0, ui.KeyEnter)
	tgmWaitUntil(t, mode, tt, 5*time.Second, "template prompt response", func() bool {
		return !mode.Session().IsStreaming() && tt.HasText("template reply ok")
	})

	ctx := context.Background()

	// Test skill expansion dispatched via Editor -> Prompt.
	tt.Type("/skill:demo-skill run checks")
	tt.Key(0, ui.KeyEnter)
	tgmWaitUntil(t, mode, tt, 5*time.Second, "skill response", func() bool {
		return !mode.Session().IsStreaming() && tt.HasText("skill reply ok")
	})

	// Test informational & state slash commands.
	for _, cmd := range []string{
		"/name desktop-test-session",
		"/session",
		"/model faux-2",
		"/thinking high",
		"/theme light",
		"/theme dark",
		"/copy",
		"/skills",
		"/prompts",
		"/debug",
	} {
		if !mode.HandleSlashCommand(ctx, cmd) {
			t.Fatalf("expected slash command %q handled", cmd)
		}
		mode.FlushPending()
		tt.Frame()
	}

	if got := mode.Session().SessionName(); got == nil || *got != "desktop-test-session" {
		t.Fatalf("expected session name updated, got %v", got)
	}
	if mode.Session().Model() == nil || mode.Session().Model().Id != "faux-2" {
		t.Fatalf("expected model switched to faux-2, got %+v", mode.Session().Model())
	}
	if mode.Session().ThinkingLevel() != ai.ModelThinkingLevelHigh {
		t.Fatalf("expected thinking level high, got %v", mode.Session().ThinkingLevel())
	}

	cap.mu.Lock()
	copied := append([]string(nil), cap.clipboard...)
	cap.mu.Unlock()
	if len(copied) == 0 || !strings.Contains(copied[len(copied)-1], "skill reply ok") {
		t.Fatalf("expected /copy clipboard to contain last assistant reply, got %v", copied)
	}

	// Test /export to temp path.
	exportPath := filepath.Join(t.TempDir(), "session.html")
	if !mode.HandleSlashCommand(ctx, "/export "+exportPath) {
		t.Fatal("expected /export handled")
	}
	if _, err := os.Stat(exportPath); err != nil {
		t.Fatalf("expected exported html at %s: %v", exportPath, err)
	}

	// Test /hotkeys modal opens via HandleSlashCommand and closes via Escape.
	if !mode.HandleSlashCommand(ctx, "/hotkeys") {
		t.Fatal("expected /hotkeys handled")
	}
	tt.Frame()
	if !mode.GUI().HasOverlay() || !tt.HasText("Close") {
		t.Fatalf("expected hotkeys modal visible, texts=%v", tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if mode.GUI().HasOverlay() {
		t.Fatal("expected Escape to dismiss hotkeys modal")
	}

	// Test /reload, double-Escape tree shortcut when idle, /clone, and /new + /clear.
	if !mode.HandleSlashCommand(ctx, "/reload") {
		t.Fatal("expected /reload handled")
	}
	mode.FlushPending()

	// Double Escape while editor is empty opens Tree selector (default doubleEscapeAction="tree").
	if err := tt.Click("Prompt editor"); err != nil {
		t.Fatalf("Click Prompt editor: %v", err)
	}
	tt.Key(0, ui.KeyEscape)
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if !mode.GUI().HasOverlay() || !tt.HasText("Session Tree") {
		t.Fatalf("expected double-Escape to open Session Tree, texts=%v", tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()

	// Test /clone then /clear & /new resetting transcript cleanly.
	if !mode.HandleSlashCommand(ctx, "/clone") {
		t.Fatal("expected /clone handled")
	}
	mode.FlushPending()
	if !mode.HandleSlashCommand(ctx, "/clear") {
		t.Fatal("expected /clear handled")
	}
	tgmWaitUntil(t, mode, tt, 3*time.Second, "fresh empty session after /clear", func() bool {
		return len(mode.Session().Messages()) == 0
	})
	if !mode.HandleSlashCommand(ctx, "/new") {
		t.Fatal("expected /new handled")
	}
	tgmWaitUntil(t, mode, tt, 3*time.Second, "fresh empty session after /new", func() bool {
		return len(mode.Session().Messages()) == 0
	})

	// Unknown command should return false so user text stays dispatchable.
	if mode.HandleSlashCommand(ctx, "/nonexistent-cmd") {
		t.Fatal("expected unknown slash command to return false")
	}

	// /quit stops GuiMode cleanly.
	if !mode.HandleSlashCommand(ctx, "/quit") {
		t.Fatal("expected /quit handled")
	}
}

func TestGuiModeSelectorsAndModalOverlays(t *testing.T) {
	fx := tgmNewFixture(t, nil)
	fx.faux.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("turn alpha"), nil)},
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("turn beta"), nil)},
	})

	mode, tt, _ := tgmNewModeAndTester(t, fx.runtime, nil)
	tt.Frame()

	// Seed two user turns so /tree and /fork have nodes.
	tt.Type("first user question")
	tt.Key(0, ui.KeyEnter)
	tgmWaitUntil(t, mode, tt, 5*time.Second, "turn alpha done", func() bool {
		return !mode.Session().IsStreaming() && tt.HasText("turn alpha")
	})

	tt.Type("second user question")
	tt.Key(0, ui.KeyEnter)
	tgmWaitUntil(t, mode, tt, 5*time.Second, "turn beta done", func() bool {
		return !mode.Session().IsStreaming() && tt.HasText("turn beta")
	})

	// 1. Model selector overlay -> filter "faux-2" + click Select.
	mode.ShowModelSelector()
	tt.Frame()
	if !mode.GUI().HasOverlay() || !tt.HasText("Select Model") {
		t.Fatalf("expected ModelSelectorDialog visible, texts=%v", tt.Texts())
	}
	tt.Type("faux-2")
	tt.Frame()
	if err := tt.Click("Select"); err != nil {
		t.Fatalf("Click Select: %v", err)
	}
	tgmWaitUntil(t, mode, tt, 3*time.Second, "faux-2 active after selector", func() bool {
		return mode.Session().Model() != nil && mode.Session().Model().Id == "faux-2"
	})

	// 2. Thinking selector overlay -> select high.
	mode.ShowThinkingSelector()
	tt.Frame()
	if !mode.GUI().HasOverlay() || !tt.HasText("Select Thinking Level") {
		t.Fatalf("expected ThinkingSelectorDialog visible, texts=%v", tt.Texts())
	}
	if err := tt.Click("high"); err != nil {
		t.Fatalf("Click high: %v", err)
	}
	tt.Frame()
	if mode.Session().ThinkingLevel() != ai.ModelThinkingLevelHigh {
		t.Fatalf("expected thinking level high, got %v", mode.Session().ThinkingLevel())
	}

	// 3. Settings selector -> Done closes it cleanly.
	mode.ShowSettingsSelector()
	tt.Frame()
	if !mode.GUI().HasOverlay() || !tt.HasText("Done") {
		t.Fatalf("expected SettingsSelectorDialog visible, texts=%v", tt.Texts())
	}
	if err := tt.Click("Done"); err != nil {
		t.Fatalf("Click Done: %v", err)
	}
	tt.Frame()
	if mode.GUI().HasOverlay() {
		t.Fatal("expected settings modal dismissed")
	}

	// 4. Scoped models selector, Theme selector, Session selector, Tree selector, Trust selector, Hotkeys, Fork.
	mode.ShowScopedModelsSelector()
	tt.Frame()
	if !mode.GUI().HasOverlay() {
		t.Fatal("expected ScopedModelsSelectorDialog visible")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()

	mode.ShowThemeSelector()
	tt.Frame()
	if !mode.GUI().HasOverlay() || !tt.HasText("Apply Theme") {
		t.Fatalf("expected ThemeSelectorDialog visible, texts=%v", tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()

	mode.ShowSessionSelector()
	tgmWaitUntil(t, mode, tt, 3*time.Second, "SessionSelectorDialog loaded", func() bool {
		return mode.GUI().HasOverlay() && tt.HasText("Cancel")
	})
	tt.Key(0, ui.KeyEscape)
	tt.Frame()

	mode.ShowTreeSelector()
	tt.Frame()
	if !mode.GUI().HasOverlay() {
		t.Fatal("expected TreeSelectorDialog visible")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()

	mode.ShowTrustSelector(context.Background(), fx.cwd, []core.ProjectTrustOption{
		{Label: "Trust workspace", Trusted: true},
		{Label: "Keep untrusted", Trusted: false},
	})
	tt.Frame()
	if !mode.GUI().HasOverlay() || !tt.HasText("Confirm") {
		t.Fatalf("expected TrustSelectorDialog visible, texts=%v", tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()

	mode.ShowHotkeysDialog()
	tt.Frame()
	if !mode.GUI().HasOverlay() || !tt.HasText("Close") {
		t.Fatalf("expected HotkeysDialog visible, texts=%v", tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()

	// Fork selector forks first message into editor & branched session.
	mode.ShowForkSelector()
	tt.Frame()
	if !mode.GUI().HasOverlay() || !tt.HasText("Fork") {
		t.Fatalf("expected ForkSelectorDialog visible, texts=%v", tt.Texts())
	}
	if err := tt.Click("Fork"); err != nil {
		t.Fatalf("Click Fork: %v", err)
	}
	tgmWaitUntil(t, mode, tt, 3*time.Second, "fork completes", func() bool {
		return !mode.GUI().HasOverlay() && mode.editor.Text() != ""
	})
}

func TestGuiModeExtensionUIContextAndRenderers(t *testing.T) {
	var customToolRendered atomic.Int32
	var customMsgRendered atomic.Int32

	echoTool := &agent.AgentTool{
		Name:        "custom_gui_tool",
		Label:       "Custom GUI Tool",
		Description: "Custom renderer test tool",
		Parameters:  typebox.Object(nil),
		Execute: func(ctx context.Context, toolCallId string, params any, onUpdate agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
			return agent.AgentToolResult{
				Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "custom result payload"}},
			}, nil
		},
	}

	fx := tgmNewFixture(t, &tgmFixtureOpts{
		tools: []*agent.AgentTool{echoTool},
	})
	stopTool := ai.StopReasonToolUse
	fx.faux.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
				providers.FauxToolCall("custom_gui_tool", jsonx.NewObject(), nil),
			}, &providers.FauxAssistantMessageOptions{
				StopReason: &stopTool,
			}),
		},
		providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(providers.FauxAssistantText("custom tool finished"), nil),
		},
	})

	mode, tt, _ := tgmNewModeAndTester(t, fx.runtime, nil)
	mode.RegisterToolRenderer("custom_gui_tool", func(c *ui.Context, snap ToolExecutionSnapshot, th *gui.GuiTheme) ui.Element {
		customToolRendered.Add(1)
		return ui.Text(c, "rendered-custom-tool:"+snap.ToolName)
	})
	mode.RegisterCustomMessageRenderer("custom-banner", func(c *ui.Context, msg *core.CustomMessage, expanded bool, th *gui.GuiTheme) ui.Element {
		customMsgRendered.Add(1)
		return ui.Text(c, "rendered-custom-message:"+msg.CustomType)
	})
	tt.Frame()

	tt.Type("run custom renderer tool")
	tt.Key(0, ui.KeyEnter)
	tgmWaitUntil(t, mode, tt, 5*time.Second, "custom tool renderer output visible", func() bool {
		return !mode.Session().IsStreaming() && tt.HasText("rendered-custom-tool:custom_gui_tool")
	})

	// Append custom message card & tool card directly through session/card registration to verify renderer invocation.
	mode.appendCard("custom", "custom:1", NewCustomMessageCard(&core.CustomMessage{
		CustomType: "custom-banner",
		Content:    ai.UserMessageContent{Text: tgmStrPtr("hello custom")},
		Display:    true,
	},
		WithCustomRenderer(mode.customMessageRenderers["custom-banner"]),
		WithCustomTheme(mode.currentGuiTheme()),
	))
	tt.Frame()
	if !tt.HasText("rendered-custom-message:custom-banner") || customMsgRendered.Load() == 0 || customToolRendered.Load() == 0 {
		t.Fatalf("expected custom tool/message renderers invoked (tool=%d msg=%d), texts=%v", customToolRendered.Load(), customMsgRendered.Load(), tt.Texts())
	}

	// Verify ExtensionUIContext synchronous helpers + widgets + banners.
	uiCtx := mode.uiCtx
	if uiCtx == nil {
		t.Fatal("expected mode.uiCtx initialized")
	}
	uiCtx.Notify("Extension hello", "info")
	uiCtx.SetStatus("ext-status", tgmStrPtr("Synced"))
	uiCtx.SetWidgetLines("ext-top", []string{"Top widget line"}, &core.ExtensionWidgetOptions{Placement: core.WidgetPlacementAboveEditor})
	uiCtx.SetWidgetLines("ext-bottom", []string{"Bottom widget line"}, &core.ExtensionWidgetOptions{Placement: core.WidgetPlacementBelowEditor})
	uiCtx.SetWorkingMessage("Thinking hard...")
	uiCtx.SetHiddenThinkingLabel("hidden-tag")
	uiCtx.SetToolsExpanded(true)
	uiCtx.SetEditorText("prefilled text")
	if got := uiCtx.GetEditorText(); got != "prefilled text" {
		t.Fatalf("GetEditorText() = %q, want prefilled text", got)
	}
	if !uiCtx.GetToolsExpanded() {
		t.Fatal("expected toolsExpanded == true")
	}
	if res := uiCtx.SetThemeByName("light"); !res.Success {
		t.Fatalf("SetThemeByName(light) failed: %+v", res)
	}
	uiCtx.SetTitle("Custom Window Title")
	mode.FlushPending()
	tt.Frame()
	if !tt.HasText("Top widget line") || !tt.HasText("Bottom widget line") {
		t.Fatalf("expected extension widgets rendered, texts=%v", tt.Texts())
	}

	// Verify async ExtensionUIContext Confirm + Select + Input modals resolve cleanly via UI interaction.
	confirmCh := make(chan bool, 1)
	go func() {
		ok, _ := uiCtx.Confirm(context.Background(), "Confirm Action", "Proceed?", nil)
		confirmCh <- ok
	}()
	tgmWaitUntil(t, mode, tt, 3*time.Second, "ExtensionConfirmDialog visible", func() bool {
		return mode.GUI().HasOverlay() && tt.HasText("Confirm Action")
	})
	if err := tt.Click("Confirm"); err != nil {
		t.Fatalf("Click Confirm: %v", err)
	}
	tt.Frame()
	select {
	case ok := <-confirmCh:
		if !ok {
			t.Fatal("expected Confirm == true")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for Confirm result")
	}

	selectCh := make(chan *string, 1)
	go func() {
		choice, _ := uiCtx.Select(context.Background(), "Pick Option", []string{"Alpha", "Beta"}, nil)
		selectCh <- choice
	}()
	tgmWaitUntil(t, mode, tt, 3*time.Second, "ExtensionSelectDialog visible", func() bool {
		return mode.GUI().HasOverlay() && tt.HasText("Pick Option")
	})
	if err := tt.Click("Beta"); err != nil {
		t.Fatalf("Click Beta: %v", err)
	}
	tt.Frame()
	select {
	case choice := <-selectCh:
		if choice == nil || *choice != "Beta" {
			t.Fatalf("expected Beta selected, got %v", choice)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for Select result")
	}

	// Verify Input() and Editor() extension modals.
	inputCh := make(chan *string, 1)
	go func() {
		val, _ := uiCtx.Input(context.Background(), "Enter Token", tgmStrPtr("type here"))
		inputCh <- val
	}()
	tgmWaitUntil(t, mode, tt, 3*time.Second, "ExtensionInputDialog visible", func() bool {
		return mode.GUI().HasOverlay() && tt.HasText("Enter Token")
	})
	tt.Type("secret-val")
	tt.Frame()
	if err := tt.Click("Submit"); err != nil {
		t.Fatalf("Click Submit on Input: %v", err)
	}
	tt.Frame()
	select {
	case val := <-inputCh:
		if val == nil || *val != "secret-val" {
			t.Fatalf("expected Input=secret-val, got %v", val)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for Input result")
	}

	editorCh := make(chan *string, 1)
	go func() {
		val, _ := uiCtx.Editor(context.Background(), "Edit Note", "initial note")
		editorCh <- val
	}()
	tgmWaitUntil(t, mode, tt, 3*time.Second, "ExtensionEditorDialog visible", func() bool {
		return mode.GUI().HasOverlay() && tt.HasText("Edit Note")
	})
	if err := tt.Click("Save"); err != nil {
		t.Fatalf("Click Save on Editor: %v", err)
	}
	tt.Frame()
	select {
	case val := <-editorCh:
		if val == nil || *val != "initial note" {
			t.Fatalf("expected Editor='initial note', got %v", val)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for Editor result")
	}

	// Verify Custom() renders & closes cleanly via Close button.
	customDoneCh := make(chan any, 1)
	go func() {
		res, _ := uiCtx.Custom(context.Background(), func(tui.TUI, *theme.Theme, *tui.KeybindingsManager, func(any)) (tui.Component, error) {
			return tui.NewText("Custom component line", 0, 0, nil), nil
		})
		customDoneCh <- res
	}()
	tgmWaitUntil(t, mode, tt, 3*time.Second, "Extension Custom overlay visible", func() bool {
		return mode.GUI().HasOverlay() && tt.HasText("Custom component line")
	})
	if err := tt.Click("Close"); err != nil {
		t.Fatalf("Click Close on Custom: %v", err)
	}
	tt.Frame()
	select {
	case <-customDoneCh:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for Custom overlay close")
	}
}

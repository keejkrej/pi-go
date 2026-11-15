// Ported from packages/coding-agent/test/suite/agent-session-runtime.test.ts (pi v1.5.2).

package core_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/compat"
	"github.com/keejkrej/pi-go/ai/providers"
	core "github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/internal/testkit"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/typebox"
)

func tsasrBoolPtr(v bool) *bool { return &v }

func tsasrIntPtr(v int) *int { return &v }

func tsasrStrPtr(v string) *string { return &v }

type tsasrRuntimeFixture struct {
	runtime    *core.AgentSessionRuntime
	faux       *providers.FauxProviderRegistration
	provider   string
	cwd        string
	agentDir   string
	sessionDir string
	models     []*ai.Model
}

type tsasrFixtureOptions struct {
	tools              []*agent.AgentTool
	inMemory           bool
	extensionFactories []core.InlineExtension
	bindExtensions     bool
}

func tsasrCreateRuntimeFixture(t *testing.T, ctx context.Context, opts *tsasrFixtureOptions) *tsasrRuntimeFixture {
	t.Helper()
	tempRoot := t.TempDir()
	cwd := filepath.Join(tempRoot, "cwd")
	agentDir := filepath.Join(tempRoot, "agent")
	sessionDir := filepath.Join(tempRoot, "sessions")
	for _, d := range []string{cwd, agentDir, sessionDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("MkdirAll(%s): %v", d, err)
		}
	}

	provName := fmt.Sprintf("faux-rt-%d", time.Now().UnixNano())
	fauxReg := compat.RegisterFauxProvider(&providers.RegisterFauxProviderOptions{
		Provider: tsasrStrPtr(provName),
		Models: []providers.FauxModelDefinition{
			{
				Id:            "faux-1",
				Name:          tsasrStrPtr("Faux 1"),
				Reasoning:     tsasrBoolPtr(false),
				Input:         []string{"text", "image"},
				ContextWindow: tsasrIntPtr(128_000),
				MaxTokens:     tsasrIntPtr(4096),
			},
			{
				Id:            "faux-2",
				Name:          tsasrStrPtr("Faux 2"),
				Reasoning:     tsasrBoolPtr(true),
				Input:         []string{"text", "image"},
				ContextWindow: tsasrIntPtr(200_000),
				MaxTokens:     tsasrIntPtr(8192),
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

	var extraTools []*agent.AgentTool
	var extensionFactories []core.InlineExtension
	inMemory := false
	bindExtensions := false
	if opts != nil {
		extraTools = opts.tools
		inMemory = opts.inMemory
		extensionFactories = opts.extensionFactories
		bindExtensions = opts.bindExtensions
	}

	createRuntime := func(c context.Context, args core.AgentSessionRuntimeCreateArgs) (*core.CreateAgentSessionRuntimeResult, error) {
		services, err := core.CreateAgentSessionServices(c, &core.CreateAgentSessionServicesOptions{
			Cwd:         args.Cwd,
			AgentDir:    args.AgentDir,
			AuthStorage: authStorage,
			ResourceLoaderOptions: &core.DefaultResourceLoaderOptions{
				NoExtensions:       true,
				NoSkills:           true,
				NoPromptTemplates:  true,
				NoThemes:           true,
				ExtensionFactories: extensionFactories,
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

	runtime, err := core.CreateAgentSessionRuntime(ctx, createRuntime, core.CreateAgentSessionRuntimeOptions{
		Cwd:            cwd,
		AgentDir:       agentDir,
		SessionManager: sm,
	})
	if err != nil {
		t.Fatalf("CreateAgentSessionRuntime: %v", err)
	}
	t.Cleanup(func() {
		_ = runtime.Dispose(context.Background())
	})
	if bindExtensions || len(extensionFactories) > 0 {
		if err := runtime.Session.BindExtensions(ctx, core.ExtensionBindings{}); err != nil {
			t.Fatalf("BindExtensions: %v", err)
		}
	}

	return &tsasrRuntimeFixture{
		runtime:    runtime,
		faux:       fauxReg,
		provider:   provName,
		cwd:        cwd,
		agentDir:   agentDir,
		sessionDir: sessionDir,
		models:     fauxReg.Models,
	}
}

func tsasrGetUserTexts(s *core.AgentSession) []string {
	var out []string
	for _, m := range s.Messages() {
		if um, ok := m.(*ai.UserMessage); ok && um != nil {
			out = append(out, testkit.GetMessageText(um))
		}
	}
	return out
}

func tsasrReadPersistedRoles(t *testing.T, sessionPath string) []string {
	t.Helper()
	sm, err := core.SessionManagerOpen(sessionPath)
	if err != nil {
		t.Fatalf("SessionManagerOpen(%s): %v", sessionPath, err)
	}
	var roles []string
	for _, entry := range sm.GetEntries() {
		if me, ok := entry.(*core.SessionMessageEntry); ok && me != nil {
			switch me.Message.(type) {
			case *ai.SystemMessage:
				roles = append(roles, "system")
			case *ai.UserMessage:
				roles = append(roles, "user")
			case *ai.AssistantMessage:
				roles = append(roles, "assistant")
			case *ai.ToolResultMessage:
				roles = append(roles, "toolResult")
			}
		}
	}
	return roles
}

func TestAgentSessionRuntimeCharacterization(t *testing.T) {
	ctx := context.Background()

	t.Run("newSession switches to a fresh session", func(t *testing.T) {
		fx := tsasrCreateRuntimeFixture(t, ctx, nil)
		fx.faux.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("turn 1"), nil)},
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("turn 2"), nil)},
		})

		if err := fx.runtime.Session.Prompt(ctx, "hello 1"); err != nil {
			t.Fatalf("Prompt 1: %v", err)
		}
		firstFile := fx.runtime.Session.SessionFile()

		res, err := fx.runtime.NewSession(ctx)
		if err != nil {
			t.Fatalf("NewSession: %v", err)
		}
		if res.Cancelled {
			t.Fatal("expected NewSession Cancelled == false")
		}
		if got := len(fx.runtime.Session.Messages()); got != 0 {
			t.Fatalf("expected 0 messages in fresh session, got %d", got)
		}
		if secondFile := fx.runtime.Session.SessionFile(); firstFile != nil && secondFile != nil && *firstFile == *secondFile {
			t.Fatalf("expected new session file path, got same %s", *secondFile)
		}

		if err := fx.runtime.Session.Prompt(ctx, "hello 2"); err != nil {
			t.Fatalf("Prompt 2: %v", err)
		}
		if got := testkit.GetMessageText(fx.runtime.Session.Messages()[len(fx.runtime.Session.Messages())-1]); got != "turn 2" {
			t.Fatalf("expected 'turn 2', got %q", got)
		}
	})

	t.Run("switchSession settles active tool execution on outgoing session before replacement", func(t *testing.T) {
		toolStarted := make(chan struct{})
		var once sync.Once
		blockTool := &agent.AgentTool{
			Name:        "block",
			Label:       "Block",
			Description: "Blocks until cancelled",
			Parameters:  typebox.Object(nil),
			Execute: func(toolCtx context.Context, _ string, _ any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
				once.Do(func() { close(toolStarted) })
				<-toolCtx.Done()
				return agent.AgentToolResult{
					Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "interrupted cleanly"}},
				}, nil
			},
		}

		fx := tsasrCreateRuntimeFixture(t, ctx, &tsasrFixtureOptions{
			tools: []*agent.AgentTool{blockTool},
		})
		fx.faux.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("seed reply"), nil)},
		})
		if err := fx.runtime.Session.Prompt(ctx, "seed"); err != nil {
			t.Fatalf("seed Prompt: %v", err)
		}
		targetFile := fx.runtime.Session.SessionFile()
		if targetFile == nil {
			t.Fatal("expected non-nil target SessionFile")
		}

		if _, err := fx.runtime.NewSession(ctx); err != nil {
			t.Fatalf("NewSession: %v", err)
		}

		stopTool := ai.StopReasonToolUse
		fx.faux.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(
					providers.FauxAssistantBlock{Block: providers.FauxToolCall("block", jsonx.NewObject(), nil)},
					&providers.FauxAssistantMessageOptions{StopReason: &stopTool},
				),
			},
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantText("aborted turn end"), nil),
			},
		})

		outgoing := fx.runtime.Session
		promptErrCh := make(chan error, 1)
		go func() {
			promptErrCh <- outgoing.Prompt(ctx, "start blocking tool")
		}()

		select {
		case <-toolStarted:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for block tool to start")
		}

		switchRes, err := fx.runtime.SwitchSession(ctx, *targetFile)
		if err != nil {
			t.Fatalf("SwitchSession: %v", err)
		}
		if switchRes.Cancelled {
			t.Fatal("expected SwitchSession Cancelled == false")
		}
		<-promptErrCh

		if fx.runtime.Session.SessionFile() == nil || *fx.runtime.Session.SessionFile() != *targetFile {
			t.Fatalf("expected active session file %s, got %v", *targetFile, fx.runtime.Session.SessionFile())
		}
		outgoingFile := outgoing.SessionFile()
		if outgoingFile == nil {
			t.Fatal("expected outgoing session file to be persisted after settling tool call")
		}
		roles := tsasrReadPersistedRoles(t, *outgoingFile)
		// Strip optional system message if present, verify user->assistant->toolResult->assistant or user->assistant->toolResult.
		if len(roles) > 0 && roles[0] == "system" {
			roles = roles[1:]
		}
		if len(roles) < 3 || roles[0] != "user" || roles[1] != "assistant" || roles[2] != "toolResult" {
			t.Fatalf("expected settled outgoing roles starting with [user, assistant, toolResult], got %v", roles)
		}
	})

	t.Run("leaves no copy behind when import fails on missing cwd and succeeds with cwdOverride", func(t *testing.T) {
		fx := tsasrCreateRuntimeFixture(t, ctx, nil)
		importDir := filepath.Join(fx.agentDir, "import")
		if err := os.MkdirAll(importDir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		importPath := filepath.Join(importDir, "orphan.jsonl")
		headerBytes, _ := jsonx.Marshal(map[string]any{
			"type":      "session",
			"version":   3,
			"id":        "orphan-id",
			"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
			"cwd":       filepath.Join(fx.agentDir, "renamed-away-missing"),
		})
		msgBytes, _ := jsonx.Marshal(map[string]any{
			"type":      "message",
			"id":        "m1",
			"parentId":  nil,
			"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
			"message": map[string]any{
				"role":      "user",
				"content":   "imported message",
				"timestamp": time.Now().UnixMilli(),
			},
		})
		if err := os.WriteFile(importPath, []byte(string(headerBytes)+"\n"+string(msgBytes)+"\n"), 0o644); err != nil {
			t.Fatalf("WriteFile importPath: %v", err)
		}

		_, err := fx.runtime.ImportFromJsonl(ctx, importPath)
		var missingErr *core.MissingSessionCwdError
		if !errors.As(err, &missingErr) {
			t.Fatalf("expected *core.MissingSessionCwdError, got %v", err)
		}
		if _, statErr := os.Stat(filepath.Join(fx.sessionDir, "orphan.jsonl")); !os.IsNotExist(statErr) {
			t.Fatalf("expected no orphan.jsonl copy left in %s, stat err=%v", fx.sessionDir, statErr)
		}

		res, err := fx.runtime.ImportFromJsonl(ctx, importPath, fx.runtime.Cwd())
		if err != nil {
			t.Fatalf("ImportFromJsonl with cwdOverride: %v", err)
		}
		if res.Cancelled {
			t.Fatal("expected Cancelled == false")
		}
		destFile := fx.runtime.Session.SessionFile()
		if destFile == nil || filepath.Dir(*destFile) != fx.sessionDir {
			t.Fatalf("expected imported session inside %s, got %v", fx.sessionDir, destFile)
		}
		if _, statErr := os.Stat(*destFile); statErr != nil {
			t.Fatalf("expected imported session file to exist: %v", statErr)
		}
	})

	t.Run("preserves existing session file when imported filename collides", func(t *testing.T) {
		fx := tsasrCreateRuntimeFixture(t, ctx, nil)
		existingPath := filepath.Join(fx.sessionDir, "colliding.jsonl")
		existingContent := "existing sentinel content\n"
		if err := os.WriteFile(existingPath, []byte(existingContent), 0o644); err != nil {
			t.Fatalf("WriteFile existing: %v", err)
		}

		importDir := filepath.Join(fx.agentDir, "import2")
		if err := os.MkdirAll(importDir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		importPath := filepath.Join(importDir, "colliding.jsonl")
		headerBytes, _ := jsonx.Marshal(map[string]any{
			"type":      "session",
			"version":   3,
			"id":        "collide-id",
			"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
			"cwd":       fx.cwd,
		})
		if err := os.WriteFile(importPath, append(headerBytes, '\n'), 0o644); err != nil {
			t.Fatalf("WriteFile importPath: %v", err)
		}

		if _, err := fx.runtime.ImportFromJsonl(ctx, importPath); err != nil {
			t.Fatalf("ImportFromJsonl: %v", err)
		}

		destFile := fx.runtime.Session.SessionFile()
		if destFile == nil || *destFile == existingPath {
			t.Fatalf("expected unique non-colliding path, got %v", destFile)
		}
		gotExisting, err := os.ReadFile(existingPath)
		if err != nil || string(gotExisting) != existingContent {
			t.Fatalf("expected colliding.jsonl preserved, got %q (err=%v)", string(gotExisting), err)
		}
	})

	t.Run("fork reports unflushed session before any assistant response", func(t *testing.T) {
		fx := tsasrCreateRuntimeFixture(t, ctx, nil)
		entryId, err := fx.runtime.Session.SessionManager.AppendCustomEntry("seed", map[string]any{"ok": true})
		if err != nil {
			t.Fatalf("AppendCustomEntry: %v", err)
		}

		_, err = fx.runtime.Fork(ctx, entryId, &core.ForkSessionOptions{Position: "at"})
		if err == nil || !strings.Contains(err.Error(), "has not been saved yet") {
			t.Fatalf("expected unflushed session error, got %v", err)
		}
	})

	t.Run("fork duplicates the active branch into a new persisted session file", func(t *testing.T) {
		fx := tsasrCreateRuntimeFixture(t, ctx, nil)
		fx.faux.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("reply 1"), nil)},
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("reply 2"), nil)},
		})
		if err := fx.runtime.Session.Prompt(ctx, "prompt 1"); err != nil {
			t.Fatalf("Prompt 1: %v", err)
		}
		if err := fx.runtime.Session.Prompt(ctx, "prompt 2"); err != nil {
			t.Fatalf("Prompt 2: %v", err)
		}

		origFile := fx.runtime.Session.SessionFile()
		if origFile == nil {
			t.Fatal("expected persisted SessionFile")
		}

		var firstUserId string
		for _, entry := range fx.runtime.Session.SessionManager.GetEntries() {
			if me, ok := entry.(*core.SessionMessageEntry); ok && me != nil {
				if um, ok := me.Message.(*ai.UserMessage); ok && um != nil && testkit.GetMessageText(um) == "prompt 1" {
					firstUserId = me.Id
					break
				}
			}
		}
		if firstUserId == "" {
			t.Fatal("failed to locate first user entry ID")
		}

		forkRes, err := fx.runtime.Fork(ctx, firstUserId, &core.ForkSessionOptions{Position: "at"})
		if err != nil {
			t.Fatalf("Fork: %v", err)
		}
		if forkRes.Cancelled {
			t.Fatal("expected Fork Cancelled == false")
		}
		forkFile := fx.runtime.Session.SessionFile()
		if forkFile == nil || *forkFile == *origFile {
			t.Fatalf("expected new forked session file distinct from %s, got %v", *origFile, forkFile)
		}

		userTexts := tsasrGetUserTexts(fx.runtime.Session)
		if len(userTexts) != 1 || userTexts[0] != "prompt 1" {
			t.Fatalf("expected forked transcript userTexts ['prompt 1'], got %#v", userTexts)
		}
	})

	t.Run("fork duplicates the active branch when session is in-memory", func(t *testing.T) {
		fx := tsasrCreateRuntimeFixture(t, ctx, &tsasrFixtureOptions{inMemory: true})
		fx.faux.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("reply 1"), nil)},
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("reply 2"), nil)},
		})
		if err := fx.runtime.Session.Prompt(ctx, "prompt 1"); err != nil {
			t.Fatalf("Prompt 1: %v", err)
		}
		if err := fx.runtime.Session.Prompt(ctx, "prompt 2"); err != nil {
			t.Fatalf("Prompt 2: %v", err)
		}

		var firstUserId string
		for _, entry := range fx.runtime.Session.SessionManager.GetEntries() {
			if me, ok := entry.(*core.SessionMessageEntry); ok && me != nil {
				if um, ok := me.Message.(*ai.UserMessage); ok && um != nil && testkit.GetMessageText(um) == "prompt 1" {
					firstUserId = me.Id
					break
				}
			}
		}
		if firstUserId == "" {
			t.Fatal("failed to locate first user entry ID")
		}

		forkRes, err := fx.runtime.Fork(ctx, firstUserId, &core.ForkSessionOptions{Position: "at"})
		if err != nil {
			t.Fatalf("Fork (in-memory): %v", err)
		}
		if forkRes.Cancelled {
			t.Fatal("expected Cancelled == false")
		}
		userTexts := tsasrGetUserTexts(fx.runtime.Session)
		if len(userTexts) != 1 || userTexts[0] != "prompt 1" {
			t.Fatalf("expected in-memory forked userTexts ['prompt 1'], got %#v", userTexts)
		}
	})

	t.Run("fork rejects unknown or non-user entry IDs", func(t *testing.T) {
		fx := tsasrCreateRuntimeFixture(t, ctx, nil)
		fx.faux.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("reply 1"), nil)},
		})
		if err := fx.runtime.Session.Prompt(ctx, "prompt 1"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}

		if _, err := fx.runtime.Fork(ctx, "does-not-exist", &core.ForkSessionOptions{Position: "at"}); err == nil || !strings.Contains(err.Error(), "Invalid entry ID for forking") {
			t.Fatalf("expected 'Invalid entry ID for forking' for missing ID, got %v", err)
		}

		var assistantId string
		for _, entry := range fx.runtime.Session.SessionManager.GetEntries() {
			if me, ok := entry.(*core.SessionMessageEntry); ok && me != nil {
				if _, ok := me.Message.(*ai.AssistantMessage); ok {
					assistantId = me.Id
					break
				}
			}
		}
		if assistantId == "" {
			t.Fatal("expected assistant entry ID")
		}
		if _, err := fx.runtime.Fork(ctx, assistantId, &core.ForkSessionOptions{Position: "before"}); err == nil || !strings.Contains(err.Error(), "Invalid entry ID for forking") {
			t.Fatalf("expected 'Invalid entry ID for forking' for non-user entry with before position, got %v", err)
		}
	})

	t.Run("cross-cwd switchSession updates runtime cwd", func(t *testing.T) {
		fx := tsasrCreateRuntimeFixture(t, ctx, nil)

		otherCwd := filepath.Join(fx.agentDir, "other-cwd")
		otherSessionDir := filepath.Join(fx.agentDir, "other-sessions")
		if err := os.MkdirAll(otherCwd, 0o755); err != nil {
			t.Fatalf("MkdirAll otherCwd: %v", err)
		}
		if err := os.MkdirAll(otherSessionDir, 0o755); err != nil {
			t.Fatalf("MkdirAll otherSessionDir: %v", err)
		}
		otherSm, err := core.SessionManagerCreate(otherCwd, otherSessionDir)
		if err != nil {
			t.Fatalf("SessionManagerCreate(otherCwd): %v", err)
		}
		if _, err := otherSm.AppendMessage(testkit.UserMsg("other dir prompt")); err != nil {
			t.Fatalf("AppendMessage user: %v", err)
		}
		if _, err := otherSm.AppendMessage(providers.FauxAssistantMessage(providers.FauxAssistantText("other dir reply"), nil)); err != nil {
			t.Fatalf("AppendMessage assistant: %v", err)
		}
		otherFile := otherSm.GetSessionFile()
		if otherFile == nil {
			t.Fatal("expected persisted otherSessionFile")
		}

		if _, err := fx.runtime.SwitchSession(ctx, *otherFile); err != nil {
			t.Fatalf("SwitchSession(otherCwd): %v", err)
		}

		gotReal, err1 := filepath.EvalSymlinks(fx.runtime.Cwd())
		wantReal, err2 := filepath.EvalSymlinks(otherCwd)
		if err1 != nil || err2 != nil || gotReal != wantReal {
			t.Fatalf("expected runtime Cwd %q (%v), got %q (%v)", wantReal, err2, gotReal, err1)
		}
	})

	t.Run("switchSession restores model and thinking level from destination session", func(t *testing.T) {
		fx := tsasrCreateRuntimeFixture(t, ctx, nil)
		faux2 := fx.faux.GetModelId("faux-2")
		if faux2 == nil {
			t.Fatal("expected faux-2 model")
		}

		if err := fx.runtime.Session.SetModel(ctx, faux2); err != nil {
			t.Fatalf("SetModel(faux-2): %v", err)
		}
		fx.runtime.Session.SetThinkingLevel(agent.ThinkingLevelHigh)
		fx.faux.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("high reply"), nil)},
		})
		if err := fx.runtime.Session.Prompt(ctx, "hi"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		savedFile := fx.runtime.Session.SessionFile()
		if savedFile == nil {
			t.Fatal("expected saved SessionFile")
		}

		if _, err := fx.runtime.NewSession(ctx); err != nil {
			t.Fatalf("NewSession: %v", err)
		}
		fx.runtime.Session.SetThinkingLevel(agent.ThinkingLevelOff)

		if _, err := fx.runtime.SwitchSession(ctx, *savedFile); err != nil {
			t.Fatalf("SwitchSession(savedFile): %v", err)
		}
		if m := fx.runtime.Session.Model(); m == nil || m.Id != "faux-2" {
			t.Fatalf("expected restored model faux-2, got %#v", m)
		}
		if tl := fx.runtime.Session.ThinkingLevel(); tl != agent.ThinkingLevelHigh {
			t.Fatalf("expected restored thinkingLevel high, got %q", tl)
		}
	})

	t.Run("extension session_before_switch / session_before_fork / message_end hooks", func(t *testing.T) {
		t.Run("persists assistant message modified by message_end hook", func(t *testing.T) {
			fx := tsasrCreateRuntimeFixture(t, ctx, &tsasrFixtureOptions{
				extensionFactories: []core.InlineExtension{
					{
						Name: "rewrite-cost-ext",
						Factory: func(pi core.ExtensionAPI) error {
							pi.OnMessageEnd(func(_ context.Context, ev *core.MessageEndEvent, _ core.ExtensionContext) (*core.MessageEndEventResult, error) {
								am, ok := ev.Message.(*ai.AssistantMessage)
								if !ok || am == nil {
									return nil, nil
								}
								usage := am.Usage
								usage.Cost.Total = 0.123
								cp := &ai.AssistantMessage{
									Role:                  am.Role,
									Content:               am.Content,
									Api:                   am.Api,
									Provider:              am.Provider,
									Model:                 am.Model,
									ResponseModel:         am.ResponseModel,
									ResponseId:            am.ResponseId,
									ProviderThinkingLevel: am.ProviderThinkingLevel,
									ThinkingLevel:         am.ThinkingLevel,
									Diagnostics:           am.Diagnostics,
									Usage:                 usage,
									StopReason:            am.StopReason,
									Deferred:              am.Deferred,
									ErrorMessage:          am.ErrorMessage,
									RawStopReason:         am.RawStopReason,
									EndTurn:               am.EndTurn,
									Timestamp:             am.Timestamp,
									DurationMs:            am.DurationMs,
								}
								return &core.MessageEndEventResult{Message: cp}, nil
							})
							return nil
						},
					},
				},
			})

			fx.faux.SetResponses([]providers.FauxResponseStep{
				providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("costed reply"), nil)},
			})
			if err := fx.runtime.Session.Prompt(ctx, "hello cost"); err != nil {
				t.Fatalf("Prompt: %v", err)
			}

			var sessionTotal float64
			var foundSession bool
			for _, m := range fx.runtime.Session.Messages() {
				if am, ok := m.(*ai.AssistantMessage); ok && am != nil {
					sessionTotal = am.Usage.Cost.Total
					foundSession = true
				}
			}
			if !foundSession || sessionTotal != 0.123 {
				t.Fatalf("expected session assistant cost 0.123, got (found=%v, total=%v)", foundSession, sessionTotal)
			}

			var persistedTotal float64
			var foundPersisted bool
			for _, entry := range fx.runtime.Session.SessionManager.GetBranch() {
				if me, ok := entry.(*core.SessionMessageEntry); ok && me != nil {
					if am, ok := me.Message.(*ai.AssistantMessage); ok && am != nil {
						persistedTotal = am.Usage.Cost.Total
						foundPersisted = true
					}
				}
			}
			if !foundPersisted || persistedTotal != 0.123 {
				t.Fatalf("expected persisted assistant cost 0.123, got (found=%v, total=%v)", foundPersisted, persistedTotal)
			}
		})

		t.Run("runs and cancels session_before_switch, session_shutdown, and session_start on newSession and switchSession", func(t *testing.T) {
			var mu sync.Mutex
			var events []string
			var cancelSwitch bool

			fx := tsasrCreateRuntimeFixture(t, ctx, &tsasrFixtureOptions{
				extensionFactories: []core.InlineExtension{
					{
						Name: "switch-lifecycle-ext",
						Factory: func(pi core.ExtensionAPI) error {
							pi.OnSessionStart(func(_ context.Context, ev *core.SessionStartEvent, _ core.ExtensionContext) (*core.NoResult, error) {
								mu.Lock()
								prev := ""
								if ev.PreviousSessionFile != nil {
									prev = filepath.Base(*ev.PreviousSessionFile)
								}
								events = append(events, fmt.Sprintf("start:%s:%s", ev.Reason, prev))
								mu.Unlock()
								return nil, nil
							})
							pi.OnSessionBeforeSwitch(func(_ context.Context, ev *core.SessionBeforeSwitchEvent, _ core.ExtensionContext) (*core.SessionBeforeSwitchResult, error) {
								mu.Lock()
								shouldCancel := cancelSwitch
								target := ""
								if ev.TargetSessionFile != nil {
									target = filepath.Base(*ev.TargetSessionFile)
								}
								events = append(events, fmt.Sprintf("before_switch:%s:%s:cancel=%v", ev.Reason, target, shouldCancel))
								mu.Unlock()
								if shouldCancel {
									return &core.SessionBeforeSwitchResult{Cancel: true}, nil
								}
								return nil, nil
							})
							pi.OnSessionShutdown(func(_ context.Context, ev *core.SessionShutdownEvent, _ core.ExtensionContext) (*core.NoResult, error) {
								mu.Lock()
								target := ""
								if ev.TargetSessionFile != nil {
									target = filepath.Base(*ev.TargetSessionFile)
								}
								events = append(events, fmt.Sprintf("shutdown:%s:%s", ev.Reason, target))
								mu.Unlock()
								return nil, nil
							})
							return nil
						},
					},
				},
			})

			fx.faux.SetResponses([]providers.FauxResponseStep{
				providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("first"), nil)},
			})
			if err := fx.runtime.Session.Prompt(ctx, "hello 1"); err != nil {
				t.Fatalf("Prompt: %v", err)
			}
			firstFilePtr := fx.runtime.Session.SessionFile()
			if firstFilePtr == nil {
				t.Fatal("expected first SessionFile")
			}
			firstFile := *firstFilePtr

			// 1. Cancel NewSession.
			mu.Lock()
			cancelSwitch = true
			mu.Unlock()
			newRes, err := fx.runtime.NewSession(ctx)
			if err != nil {
				t.Fatalf("cancelled NewSession: %v", err)
			}
			if !newRes.Cancelled {
				t.Fatal("expected Cancelled=true when session_before_switch cancels newSession")
			}
			if got := fx.runtime.Session.SessionFile(); got == nil || *got != firstFile {
				t.Fatalf("expected original SessionFile %q preserved on cancelled NewSession, got %v", firstFile, got)
			}

			// 2. Allow NewSession, then bindExtensions to emit session_start.
			mu.Lock()
			cancelSwitch = false
			mu.Unlock()
			newRes, err = fx.runtime.NewSession(ctx)
			if err != nil {
				t.Fatalf("NewSession: %v", err)
			}
			if newRes.Cancelled {
				t.Fatal("expected Cancelled=false on NewSession")
			}
			if err := fx.runtime.Session.BindExtensions(ctx, core.ExtensionBindings{}); err != nil {
				t.Fatalf("BindExtensions after NewSession: %v", err)
			}
			secondFilePtr := fx.runtime.Session.SessionFile()
			if secondFilePtr == nil || *secondFilePtr == firstFile {
				t.Fatalf("expected distinct second SessionFile, got %v", secondFilePtr)
			}
			secondFile := *secondFilePtr

			// 3. Cancel SwitchSession back to firstFile.
			mu.Lock()
			cancelSwitch = true
			mu.Unlock()
			swRes, err := fx.runtime.SwitchSession(ctx, firstFile)
			if err != nil {
				t.Fatalf("cancelled SwitchSession: %v", err)
			}
			if !swRes.Cancelled {
				t.Fatal("expected Cancelled=true when session_before_switch cancels switchSession")
			}
			if got := fx.runtime.Session.SessionFile(); got == nil || *got != secondFile {
				t.Fatalf("expected second SessionFile %q preserved on cancelled SwitchSession, got %v", secondFile, got)
			}

			// 4. Allow SwitchSession back to firstFile, then bindExtensions to emit session_start.
			mu.Lock()
			cancelSwitch = false
			mu.Unlock()
			swRes, err = fx.runtime.SwitchSession(ctx, firstFile)
			if err != nil {
				t.Fatalf("SwitchSession: %v", err)
			}
			if swRes.Cancelled {
				t.Fatal("expected Cancelled=false on SwitchSession")
			}
			if err := fx.runtime.Session.BindExtensions(ctx, core.ExtensionBindings{}); err != nil {
				t.Fatalf("BindExtensions after SwitchSession: %v", err)
			}

			mu.Lock()
			gotEvents := slices.Clone(events)
			mu.Unlock()
			wantEvents := []string{
				"start:startup:",
				"before_switch:new::cancel=true",
				"before_switch:new::cancel=false",
				"shutdown:new:" + filepath.Base(secondFile),
				"start:new:" + filepath.Base(firstFile),
				"before_switch:resume:" + filepath.Base(firstFile) + ":cancel=true",
				"before_switch:resume:" + filepath.Base(firstFile) + ":cancel=false",
				"shutdown:resume:" + filepath.Base(firstFile),
				"start:resume:" + filepath.Base(secondFile),
			}
			if !slices.Equal(gotEvents, wantEvents) {
				t.Fatalf("lifecycle events mismatch:\n got: %v\nwant: %v", gotEvents, wantEvents)
			}
		})

		t.Run("runs and cancels session_before_fork, session_shutdown, and session_start on fork", func(t *testing.T) {
			var mu sync.Mutex
			var events []string
			var cancelFork bool

			fx := tsasrCreateRuntimeFixture(t, ctx, &tsasrFixtureOptions{
				extensionFactories: []core.InlineExtension{
					{
						Name: "fork-lifecycle-ext",
						Factory: func(pi core.ExtensionAPI) error {
							pi.OnSessionStart(func(_ context.Context, ev *core.SessionStartEvent, _ core.ExtensionContext) (*core.NoResult, error) {
								mu.Lock()
								prev := ""
								if ev.PreviousSessionFile != nil {
									prev = filepath.Base(*ev.PreviousSessionFile)
								}
								events = append(events, fmt.Sprintf("start:%s:%s", ev.Reason, prev))
								mu.Unlock()
								return nil, nil
							})
							pi.OnSessionBeforeFork(func(_ context.Context, ev *core.SessionBeforeForkEvent, _ core.ExtensionContext) (*core.SessionBeforeForkResult, error) {
								mu.Lock()
								shouldCancel := cancelFork
								events = append(events, fmt.Sprintf("before_fork:%s:%s:cancel=%v", ev.EntryId, ev.Position, shouldCancel))
								mu.Unlock()
								if shouldCancel {
									return &core.SessionBeforeForkResult{Cancel: true}, nil
								}
								return nil, nil
							})
							pi.OnSessionShutdown(func(_ context.Context, ev *core.SessionShutdownEvent, _ core.ExtensionContext) (*core.NoResult, error) {
								mu.Lock()
								events = append(events, fmt.Sprintf("shutdown:%s", ev.Reason))
								mu.Unlock()
								return nil, nil
							})
							return nil
						},
					},
				},
			})

			fx.faux.SetResponses([]providers.FauxResponseStep{
				providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("forkable reply"), nil)},
			})
			if err := fx.runtime.Session.Prompt(ctx, "prompt to fork"); err != nil {
				t.Fatalf("Prompt: %v", err)
			}
			origFilePtr := fx.runtime.Session.SessionFile()
			if origFilePtr == nil {
				t.Fatal("expected orig SessionFile")
			}
			origFile := *origFilePtr

			var userEntryId string
			for _, entry := range fx.runtime.Session.SessionManager.GetEntries() {
				if me, ok := entry.(*core.SessionMessageEntry); ok && me != nil {
					if um, ok := me.Message.(*ai.UserMessage); ok && um != nil {
						userEntryId = me.Id
						break
					}
				}
			}
			if userEntryId == "" {
				t.Fatal("failed to locate user entry ID")
			}

			// 1. Cancel fork.
			mu.Lock()
			cancelFork = true
			mu.Unlock()
			forkRes, err := fx.runtime.Fork(ctx, userEntryId, &core.ForkSessionOptions{Position: "at"})
			if err != nil {
				t.Fatalf("cancelled Fork: %v", err)
			}
			if !forkRes.Cancelled {
				t.Fatal("expected Cancelled=true on cancelled Fork")
			}
			if got := fx.runtime.Session.SessionFile(); got == nil || *got != origFile {
				t.Fatalf("expected original SessionFile %q preserved on cancelled Fork, got %v", origFile, got)
			}

			// 2. Allow fork, then bindExtensions to emit session_start.
			mu.Lock()
			cancelFork = false
			mu.Unlock()
			forkRes, err = fx.runtime.Fork(ctx, userEntryId, &core.ForkSessionOptions{Position: "at"})
			if err != nil {
				t.Fatalf("Fork: %v", err)
			}
			if forkRes.Cancelled {
				t.Fatal("expected Cancelled=false on Fork")
			}
			if err := fx.runtime.Session.BindExtensions(ctx, core.ExtensionBindings{}); err != nil {
				t.Fatalf("BindExtensions after Fork: %v", err)
			}
			if got := fx.runtime.Session.SessionFile(); got == nil || *got == origFile {
				t.Fatalf("expected new SessionFile after Fork, got %v", got)
			}

			mu.Lock()
			gotEvents := slices.Clone(events)
			mu.Unlock()
			wantEvents := []string{
				"start:startup:",
				fmt.Sprintf("before_fork:%s:at:cancel=true", userEntryId),
				fmt.Sprintf("before_fork:%s:at:cancel=false", userEntryId),
				"shutdown:fork",
				"start:fork:" + filepath.Base(origFile),
			}
			if !slices.Equal(gotEvents, wantEvents) {
				t.Fatalf("fork lifecycle events mismatch:\n got: %v\nwant: %v", gotEvents, wantEvents)
			}
		})
	})
}

// Ported from packages/coding-agent/test/agent-session-retry.test.ts (pi v1.5.2).

package core_test

import (
	"context"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/compat"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/internal/testkit"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

func tasrtFloatPtr(v float64) *float64 { return &v }
func tasrtIntPtr(v int) *int           { return &v }
func tasrtBoolPtr(v bool) *bool        { return &v }

func tasrtAssistantMessage(text string, stopReason ai.StopReason, errMsg string, content ...ai.Content) *ai.AssistantMessage {
	if len(content) == 0 {
		content = ai.ContentList{&ai.TextContent{Type: "text", Text: text}}
	}
	var errPtr *string
	if errMsg != "" {
		e := errMsg
		errPtr = &e
	}
	return &ai.AssistantMessage{
		Role:         "assistant",
		Content:      content,
		Api:          "anthropic-messages",
		Provider:     "anthropic",
		Model:        "mock",
		Usage:        ai.Usage{},
		StopReason:   stopReason,
		ErrorMessage: errPtr,
		Timestamp:    time.Now().UnixMilli(),
	}
}

type tasrtSessionOpts struct {
	failCount                  int
	maxRetries                 int
	maxAgentDelayMs            float64
	delayAssistantMessageEndMs int
}

func tasrtCreateRetrySession(t *testing.T, opts tasrtSessionOpts) (*core.AgentSession, func() int) {
	t.Helper()
	ctx := context.Background()
	tempDir := t.TempDir()
	var callCount atomic.Int32

	model := compat.GetModel(compat.BuiltinProvider("anthropic"), "claude-sonnet-4-5")
	if model == nil {
		t.Fatalf("GetModel returned nil")
	}

	ag := agent.NewAgent(&agent.AgentOptions{
		GetApiKey:    func(_ context.Context, _ string) (string, error) { return "test-key", nil },
		InitialState: &agent.AgentInitialState{Model: model, SystemPrompt: "Test", Tools: []*agent.AgentTool{}},
		StreamFn: func(_ context.Context, _ *ai.Model, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
			n := int(callCount.Add(1))
			stream := ai.NewAssistantMessageEventStream()
			go func() {
				if n <= opts.failCount {
					msg := tasrtAssistantMessage("", ai.StopReasonError, "overloaded_error")
					stream.Push(&ai.AssistantMessageEventStart{Type: "start", Partial: tasrtAssistantMessage("", ai.StopReasonError, "overloaded_error")})
					stream.Push(&ai.AssistantMessageEventError{Type: "error", Reason: ai.StopReasonError, Error: msg})
				} else {
					msg := tasrtAssistantMessage("Success", ai.StopReasonStop, "")
					stream.Push(&ai.AssistantMessageEventStart{Type: "start", Partial: tasrtAssistantMessage("", ai.StopReasonStop, "")})
					stream.Push(&ai.AssistantMessageEventDone{Type: "done", Reason: ai.StopReasonStop, Message: msg})
				}
			}()
			return stream, nil
		},
	})

	sessionManager := core.SessionManagerInMemory()
	settingsManager := core.SettingsManagerCreate(tempDir, tempDir)
	authStorage := core.AuthStorageCreate(filepath.Join(tempDir, "auth.json"))
	modelRegistry, err := testkit.CreateModelRegistry(ctx, authStorage, &tempDir)
	if err != nil {
		t.Fatalf("CreateModelRegistry: %v", err)
	}
	_, _ = authStorage.Modify(ctx, "anthropic", func(_ ai.Credential) (ai.Credential, error) {
		k := "test-key"
		return &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Key: &k}, nil
	}, nil)

	maxAgentDelay := opts.maxAgentDelayMs
	if maxAgentDelay == 0 {
		maxAgentDelay = 60000
	}
	settingsManager.ApplyOverrides(&core.Settings{
		Retry: &core.RetrySettings{
			Enabled:         tasrtBoolPtr(true),
			MaxRetries:      tasrtIntPtr(opts.maxRetries),
			BaseDelayMs:     tasrtFloatPtr(1),
			MaxAgentDelayMs: tasrtFloatPtr(maxAgentDelay),
		},
	})

	runtime, err := testkit.GetModelRuntime(modelRegistry)
	if err != nil {
		t.Fatalf("GetModelRuntime: %v", err)
	}
	t.Cleanup(func() { runtime.Close() })
	session := core.NewAgentSession(core.AgentSessionConfig{
		Agent:           ag,
		SessionManager:  sessionManager,
		SettingsManager: settingsManager,
		Cwd:             tempDir,
		ModelRuntime:    runtime,
		ResourceLoader:  testkit.CreateTestResourceLoader(),
	})

	if opts.delayAssistantMessageEndMs > 0 {
		d := time.Duration(opts.delayAssistantMessageEndMs) * time.Millisecond
		session.Subscribe(func(event core.AgentSessionEvent) {
			if me, ok := event.(*core.AgentSessionEventMessageEnd); ok && me.Message != nil && me.Message.GetRole() == "assistant" {
				time.Sleep(d)
			}
		})
	}
	return session, func() int { return int(callCount.Load()) }
}

func TestAgentSessionRetry(t *testing.T) {
	ctx := context.Background()

	t.Run("retries after a transient error and succeeds", func(t *testing.T) {
		session, getCalls := tasrtCreateRetrySession(t, tasrtSessionOpts{failCount: 1, maxRetries: 3})
		defer session.Dispose()

		var mu sync.Mutex
		var events []string
		session.Subscribe(func(ev core.AgentSessionEvent) {
			mu.Lock()
			defer mu.Unlock()
			switch e := ev.(type) {
			case *core.AgentSessionEventAutoRetryStart:
				events = append(events, "start:"+strconv.Itoa(e.Attempt))
			case *core.AgentSessionEventAutoRetryEnd:
				events = append(events, "end:success="+strconv.FormatBool(e.Success))
			}
		})

		if err := session.Prompt(ctx, "Test"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		if got := getCalls(); got != 2 {
			t.Fatalf("callCount = %d, want 2", got)
		}
		mu.Lock()
		gotEvents := slices.Clone(events)
		mu.Unlock()
		if !slices.Equal(gotEvents, []string{"start:1", "end:success=true"}) {
			t.Fatalf("events = %v, want [start:1 end:success=true]", gotEvents)
		}
		if session.IsRetrying() {
			t.Fatalf("expected IsRetrying() == false")
		}
	})

	t.Run("exhausts max retries and emits failure", func(t *testing.T) {
		session, getCalls := tasrtCreateRetrySession(t, tasrtSessionOpts{failCount: 99, maxRetries: 2})
		defer session.Dispose()

		var mu sync.Mutex
		var events []string
		session.Subscribe(func(ev core.AgentSessionEvent) {
			mu.Lock()
			defer mu.Unlock()
			switch e := ev.(type) {
			case *core.AgentSessionEventAutoRetryStart:
				events = append(events, "start:"+strconv.Itoa(e.Attempt))
			case *core.AgentSessionEventAutoRetryEnd:
				events = append(events, "end:success="+strconv.FormatBool(e.Success))
			}
		})

		_ = session.Prompt(ctx, "Test")
		if got := getCalls(); got != 3 {
			t.Fatalf("callCount = %d, want 3", got)
		}
		mu.Lock()
		gotEvents := slices.Clone(events)
		mu.Unlock()
		if !slices.Contains(gotEvents, "start:1") || !slices.Contains(gotEvents, "start:2") || !slices.Contains(gotEvents, "end:success=false") {
			t.Fatalf("events = %v, expected start:1, start:2, end:success=false", gotEvents)
		}
		if session.IsRetrying() {
			t.Fatalf("expected IsRetrying() == false")
		}
	})

	t.Run("caps agent retry delay", func(t *testing.T) {
		session, _ := tasrtCreateRetrySession(t, tasrtSessionOpts{failCount: 4, maxRetries: 5, maxAgentDelayMs: 5})
		defer session.Dispose()

		var mu sync.Mutex
		var delays []int64
		session.Subscribe(func(ev core.AgentSessionEvent) {
			if e, ok := ev.(*core.AgentSessionEventAutoRetryStart); ok {
				mu.Lock()
				delays = append(delays, e.DelayMs)
				mu.Unlock()
			}
		})

		_ = session.Prompt(ctx, "Test")
		mu.Lock()
		got := slices.Clone(delays)
		mu.Unlock()
		if !slices.Equal(got, []int64{1, 2, 4, 5}) {
			t.Fatalf("delays = %v, want [1 2 4 5]", got)
		}
	})

	t.Run("prompt waits for retry completion even when assistant message_end handling is delayed", func(t *testing.T) {
		session, getCalls := tasrtCreateRetrySession(t, tasrtSessionOpts{failCount: 1, maxRetries: 3, delayAssistantMessageEndMs: 40})
		defer session.Dispose()

		if err := session.Prompt(ctx, "Test"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		if got := getCalls(); got != 2 {
			t.Fatalf("callCount = %d, want 2", got)
		}
		if session.IsRetrying() {
			t.Fatalf("expected IsRetrying() == false")
		}
	})

	t.Run("retries provider network_error failures", func(t *testing.T) {
		tempDir := t.TempDir()
		var callCount atomic.Int32
		model := compat.GetModel(compat.BuiltinProvider("anthropic"), "claude-sonnet-4-5")

		ag := agent.NewAgent(&agent.AgentOptions{
			GetApiKey:    func(_ context.Context, _ string) (string, error) { return "test-key", nil },
			InitialState: &agent.AgentInitialState{Model: model, SystemPrompt: "Test", Tools: []*agent.AgentTool{}},
			StreamFn: func(_ context.Context, _ *ai.Model, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
				n := callCount.Add(1)
				stream := ai.NewAssistantMessageEventStream()
				go func() {
					if n == 1 {
						msg := tasrtAssistantMessage("", ai.StopReasonError, "Provider finish_reason: network_error")
						stream.Push(&ai.AssistantMessageEventStart{Type: "start", Partial: tasrtAssistantMessage("", ai.StopReasonError, "Provider finish_reason: network_error")})
						stream.Push(&ai.AssistantMessageEventError{Type: "error", Reason: ai.StopReasonError, Error: msg})
						return
					}
					msg := tasrtAssistantMessage("Recovered after retry", ai.StopReasonStop, "")
					stream.Push(&ai.AssistantMessageEventStart{Type: "start", Partial: tasrtAssistantMessage("", ai.StopReasonStop, "")})
					stream.Push(&ai.AssistantMessageEventDone{Type: "done", Reason: ai.StopReasonStop, Message: msg})
				}()
				return stream, nil
			},
		})

		sessionManager := core.SessionManagerInMemory()
		settingsManager := core.SettingsManagerCreate(tempDir, tempDir)
		authStorage := core.AuthStorageCreate(filepath.Join(tempDir, "auth.json"))
		modelRegistry, _ := testkit.CreateModelRegistry(ctx, authStorage, &tempDir)
		_, _ = authStorage.Modify(ctx, "anthropic", func(_ ai.Credential) (ai.Credential, error) {
			k := "test-key"
			return &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Key: &k}, nil
		}, nil)
		settingsManager.ApplyOverrides(&core.Settings{
			Retry: &core.RetrySettings{Enabled: tasrtBoolPtr(true), MaxRetries: tasrtIntPtr(3), BaseDelayMs: tasrtFloatPtr(1)},
		})
		runtime, _ := testkit.GetModelRuntime(modelRegistry)
		t.Cleanup(func() { runtime.Close() })
		session := core.NewAgentSession(core.AgentSessionConfig{
			Agent:           ag,
			SessionManager:  sessionManager,
			SettingsManager: settingsManager,
			Cwd:             tempDir,
			ModelRuntime:    runtime,
			ResourceLoader:  testkit.CreateTestResourceLoader(),
		})
		defer session.Dispose()

		var mu sync.Mutex
		var events []string
		session.Subscribe(func(ev core.AgentSessionEvent) {
			mu.Lock()
			defer mu.Unlock()
			switch e := ev.(type) {
			case *core.AgentSessionEventAutoRetryStart:
				events = append(events, "start:"+strconv.Itoa(e.Attempt))
			case *core.AgentSessionEventAutoRetryEnd:
				events = append(events, "end:success="+strconv.FormatBool(e.Success))
			}
		})

		if err := session.Prompt(ctx, "Test"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		if got := callCount.Load(); got != 2 {
			t.Fatalf("callCount = %d, want 2", got)
		}
		mu.Lock()
		gotEvents := slices.Clone(events)
		mu.Unlock()
		if !slices.Equal(gotEvents, []string{"start:1", "end:success=true"}) {
			t.Fatalf("events = %v, want [start:1 end:success=true]", gotEvents)
		}
	})

	t.Run("prompt waits for full agent loop when retry produces tool calls", func(t *testing.T) {
		tempDir := t.TempDir()
		var callCount atomic.Int32
		var toolExecuted atomic.Bool

		props := omap.NewMap[string, *typebox.Schema]()
		props.Set("text", typebox.String())
		echoTool := &agent.AgentTool{
			Name:        "echo",
			Label:       "Echo",
			Description: "Echo text back",
			Parameters:  typebox.Object(props),
			Execute: func(_ context.Context, _ string, _ any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
				toolExecuted.Store(true)
				return agent.AgentToolResult{
					Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "echoed"}},
				}, nil
			},
		}

		model := compat.GetModel(compat.BuiltinProvider("anthropic"), "claude-sonnet-4-5")
		ag := agent.NewAgent(&agent.AgentOptions{
			GetApiKey:    func(_ context.Context, _ string) (string, error) { return "test-key", nil },
			InitialState: &agent.AgentInitialState{Model: model, SystemPrompt: "Test", Tools: []*agent.AgentTool{}},
			StreamFn: func(_ context.Context, _ *ai.Model, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
				n := callCount.Add(1)
				stream := ai.NewAssistantMessageEventStream()
				go func() {
					switch n {
					case 1:
						msg := tasrtAssistantMessage("", ai.StopReasonError, "overloaded_error")
						stream.Push(&ai.AssistantMessageEventStart{Type: "start", Partial: tasrtAssistantMessage("", ai.StopReasonError, "overloaded_error")})
						stream.Push(&ai.AssistantMessageEventError{Type: "error", Reason: ai.StopReasonError, Error: msg})
					case 2:
						args := jsonx.NewObject()
						args.Set("text", "hello")
						msg := tasrtAssistantMessage("", ai.StopReasonToolUse, "",
							&ai.TextContent{Type: "text", Text: "Looking that up now."},
							&ai.ToolCall{Type: "toolCall", Id: "call_1", Name: "echo", Arguments: args},
						)
						stream.Push(&ai.AssistantMessageEventStart{Type: "start", Partial: tasrtAssistantMessage("", ai.StopReasonToolUse, "")})
						stream.Push(&ai.AssistantMessageEventDone{Type: "done", Reason: ai.StopReasonToolUse, Message: msg})
					default:
						msg := tasrtAssistantMessage("Final answer.", ai.StopReasonStop, "")
						stream.Push(&ai.AssistantMessageEventStart{Type: "start", Partial: tasrtAssistantMessage("", ai.StopReasonStop, "")})
						stream.Push(&ai.AssistantMessageEventDone{Type: "done", Reason: ai.StopReasonStop, Message: msg})
					}
				}()
				return stream, nil
			},
		})

		sessionManager := core.SessionManagerInMemory()
		settingsManager := core.SettingsManagerCreate(tempDir, tempDir)
		authStorage := core.AuthStorageCreate(filepath.Join(tempDir, "auth.json"))
		modelRegistry, _ := testkit.CreateModelRegistry(ctx, authStorage, &tempDir)
		_, _ = authStorage.Modify(ctx, "anthropic", func(_ ai.Credential) (ai.Credential, error) {
			k := "test-key"
			return &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Key: &k}, nil
		}, nil)
		settingsManager.ApplyOverrides(&core.Settings{
			Retry: &core.RetrySettings{Enabled: tasrtBoolPtr(true), MaxRetries: tasrtIntPtr(3), BaseDelayMs: tasrtFloatPtr(1)},
		})
		runtime, _ := testkit.GetModelRuntime(modelRegistry)
		t.Cleanup(func() { runtime.Close() })
		session := core.NewAgentSession(core.AgentSessionConfig{
			Agent:             ag,
			SessionManager:    sessionManager,
			SettingsManager:   settingsManager,
			Cwd:               tempDir,
			ModelRuntime:      runtime,
			ResourceLoader:    testkit.CreateTestResourceLoader(),
			BaseToolsOverride: map[string]*agent.AgentTool{"echo": echoTool},
		})
		defer session.Dispose()

		if err := session.Prompt(ctx, "Test"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		if got := callCount.Load(); got != 3 {
			t.Fatalf("callCount = %d, want 3", got)
		}
		if !toolExecuted.Load() {
			t.Fatalf("expected tool to have executed")
		}
		if session.IsStreaming() {
			t.Fatalf("expected IsStreaming() == false")
		}
		if err := session.Prompt(ctx, "Follow-up"); err != nil {
			t.Fatalf("follow-up Prompt: %v", err)
		}
		if got := callCount.Load(); got != 4 {
			t.Fatalf("callCount after follow-up = %d, want 4", got)
		}
	})
}

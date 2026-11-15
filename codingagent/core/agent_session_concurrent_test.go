// Ported from packages/coding-agent/test/agent-session-concurrent.test.ts (pi v1.5.2).

package core_test

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"sync"
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

func tascoAssistantMessage(text string, stopReason ai.StopReason, content ...ai.Content) *ai.AssistantMessage {
	if len(content) == 0 {
		content = ai.ContentList{&ai.TextContent{Type: "text", Text: text}}
	}
	return &ai.AssistantMessage{
		Role:     "assistant",
		Content:  content,
		Api:      "anthropic-messages",
		Provider: "anthropic",
		Model:    "mock",
		Usage: ai.Usage{
			Input:       1,
			Output:      1,
			TotalTokens: 2,
			Cost:        ai.UsageCost{},
		},
		StopReason: stopReason,
		Timestamp:  time.Now().UnixMilli(),
	}
}

func tascoCreateBlockingSession(t *testing.T) (*core.AgentSession, string) {
	t.Helper()
	ctx := context.Background()
	tempDir := t.TempDir()
	model := compat.GetModel(compat.BuiltinProvider("anthropic"), "claude-sonnet-4-5")
	if model == nil {
		t.Fatalf("GetModel returned nil")
	}

	ag := agent.NewAgent(&agent.AgentOptions{
		GetApiKey: func(_ context.Context, _ string) (string, error) { return "test-key", nil },
		InitialState: &agent.AgentInitialState{
			Model:        model,
			SystemPrompt: "Test",
			Tools:        []*agent.AgentTool{},
		},
		StreamFn: func(streamCtx context.Context, _ *ai.Model, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
			stream := ai.NewAssistantMessageEventStream()
			go func() {
				stream.Push(&ai.AssistantMessageEventStart{
					Type:    "start",
					Partial: tascoAssistantMessage("", ai.StopReasonStop),
				})
				<-streamCtx.Done()
				stream.Push(&ai.AssistantMessageEventError{
					Type:   "error",
					Reason: ai.StopReasonAborted,
					Error:  tascoAssistantMessage("Aborted", ai.StopReasonAborted),
				})
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
	_, err = authStorage.Modify(ctx, "anthropic", func(_ ai.Credential) (ai.Credential, error) {
		k := "test-key"
		return &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Key: &k}, nil
	}, nil)
	if err != nil {
		t.Fatalf("Modify: %v", err)
	}
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
	return session, tempDir
}

func tascoWaitForStreaming(t *testing.T, session *core.AgentSession) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !session.IsStreaming() {
		if time.Now().After(deadline) {
			t.Fatalf("Timed out waiting for streaming")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestAgentSessionConcurrentPromptGuard(t *testing.T) {
	ctx := context.Background()

	t.Run("returns error when prompt() called while streaming", func(t *testing.T) {
		session, _ := tascoCreateBlockingSession(t)
		defer session.Dispose()

		firstErr := make(chan error, 1)
		go func() {
			firstErr <- session.Prompt(ctx, "First message")
		}()
		tascoWaitForStreaming(t, session)

		if !session.IsStreaming() {
			t.Fatalf("expected IsStreaming() == true")
		}

		err := session.Prompt(ctx, "Second message")
		if err == nil || !strings.Contains(err.Error(), "Agent is already processing. Specify streamingBehavior ('steer' or 'followUp') to queue the message.") {
			t.Fatalf("unexpected error from concurrent Prompt: %v", err)
		}

		_ = session.Abort(ctx)
		<-firstErr
	})

	t.Run("allows steer() while streaming", func(t *testing.T) {
		session, _ := tascoCreateBlockingSession(t)
		defer session.Dispose()

		firstErr := make(chan error, 1)
		go func() {
			firstErr <- session.Prompt(ctx, "First message")
		}()
		tascoWaitForStreaming(t, session)

		disp, err := session.Steer(ctx, "Steering message", nil)
		if err != nil || disp != core.QueuedInputDispositionQueued {
			t.Fatalf("Steer = (%v, %v), want (queued, nil)", disp, err)
		}
		if got := session.PendingMessageCount(); got != 1 {
			t.Fatalf("PendingMessageCount = %d, want 1", got)
		}

		_ = session.Abort(ctx)
		<-firstErr
	})

	t.Run("allows followUp() while streaming", func(t *testing.T) {
		session, _ := tascoCreateBlockingSession(t)
		defer session.Dispose()

		firstErr := make(chan error, 1)
		go func() {
			firstErr <- session.Prompt(ctx, "First message")
		}()
		tascoWaitForStreaming(t, session)

		disp, err := session.FollowUp(ctx, "Follow-up message", nil)
		if err != nil || disp != core.QueuedInputDispositionQueued {
			t.Fatalf("FollowUp = (%v, %v), want (queued, nil)", disp, err)
		}
		if got := session.PendingMessageCount(); got != 1 {
			t.Fatalf("PendingMessageCount = %d, want 1", got)
		}

		_ = session.Abort(ctx)
		<-firstErr
	})

	t.Run("extension-origin steering messages while streaming", func(t *testing.T) {
		tempDir := t.TempDir()
		model := compat.GetModel(compat.BuiltinProvider("anthropic"), "claude-sonnet-4-5")
		if model == nil {
			t.Fatalf("GetModel returned nil")
		}

		var mu sync.Mutex
		var sawSteeringMessage bool
		var lastInputSource core.InputSource
		var queueEvents []core.AgentSessionEventQueueUpdate
		var firstOnce sync.Once
		firstStarted := make(chan struct{})
		releaseFirst := make(chan struct{})

		ag := agent.NewAgent(&agent.AgentOptions{
			GetApiKey: func(_ context.Context, _ string) (string, error) { return "test-key", nil },
			InitialState: &agent.AgentInitialState{
				Model:        model,
				SystemPrompt: "Test",
				Tools:        []*agent.AgentTool{},
			},
			StreamFn: func(streamCtx context.Context, _ *ai.Model, tc *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
				stream := ai.NewAssistantMessageEventStream()
				go func() {
					var hasSteer bool
					if tc != nil {
						for _, msg := range tc.Messages {
							if um, ok := msg.(*ai.UserMessage); ok && um != nil && testkit.GetMessageText(um) == "Steer from extension" {
								hasSteer = true
							}
						}
					}
					if hasSteer {
						mu.Lock()
						sawSteeringMessage = true
						mu.Unlock()
						stream.Push(&ai.AssistantMessageEventStart{Type: "start", Partial: tascoAssistantMessage("", ai.StopReasonStop)})
						stream.Push(&ai.AssistantMessageEventDone{Type: "done", Reason: ai.StopReasonStop, Message: tascoAssistantMessage("Steered", ai.StopReasonStop)})
						return
					}

					stream.Push(&ai.AssistantMessageEventStart{Type: "start", Partial: tascoAssistantMessage("", ai.StopReasonStop)})
					firstOnce.Do(func() { close(firstStarted) })
					select {
					case <-releaseFirst:
						stream.Push(&ai.AssistantMessageEventDone{Type: "done", Reason: ai.StopReasonStop, Message: tascoAssistantMessage("First done", ai.StopReasonStop)})
					case <-streamCtx.Done():
						stream.Push(&ai.AssistantMessageEventError{
							Type:   "error",
							Reason: ai.StopReasonAborted,
							Error:  tascoAssistantMessage("Aborted", ai.StopReasonAborted),
						})
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
		_, err = authStorage.Modify(ctx, "anthropic", func(_ ai.Credential) (ai.Credential, error) {
			k := "test-key"
			return &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Key: &k}, nil
		}, nil)
		if err != nil {
			t.Fatalf("Modify: %v", err)
		}
		runtime, err := testkit.GetModelRuntime(modelRegistry)
		if err != nil {
			t.Fatalf("GetModelRuntime: %v", err)
		}
		t.Cleanup(func() { runtime.Close() })

		var extAPI core.ExtensionAPI
		extRes, err := testkit.CreateTestExtensionsResultWithFactories(
			ctx,
			tempDir,
			core.InlineExtension{
				Name: "ext-api-capture",
				Factory: func(pi core.ExtensionAPI) error {
					extAPI = pi
					return nil
				},
			},
			core.InlineExtension{
				Name: "ext-input-tracker",
				Factory: func(pi core.ExtensionAPI) error {
					pi.OnInput(func(_ context.Context, ev *core.InputEvent, _ core.ExtensionContext) (*core.InputEventResult, error) {
						mu.Lock()
						lastInputSource = ev.Source
						mu.Unlock()
						return nil, nil
					})
					return nil
				},
			},
		)
		if err != nil {
			t.Fatalf("CreateTestExtensionsResultWithFactories: %v", err)
		}

		session := core.NewAgentSession(core.AgentSessionConfig{
			Agent:           ag,
			SessionManager:  sessionManager,
			SettingsManager: settingsManager,
			Cwd:             tempDir,
			ModelRuntime:    runtime,
			ResourceLoader: testkit.CreateTestResourceLoader(&testkit.CreateTestResourceLoaderOptions{
				ExtensionsResult: &extRes,
			}),
		})
		defer session.Dispose()

		unsub := session.Subscribe(func(ev core.AgentSessionEvent) {
			if qu, ok := ev.(*core.AgentSessionEventQueueUpdate); ok && qu != nil {
				mu.Lock()
				queueEvents = append(queueEvents, *qu)
				mu.Unlock()
			}
		})
		defer unsub()

		firstErr := make(chan error, 1)
		go func() {
			firstErr <- session.Prompt(ctx, "First message")
		}()

		select {
		case <-firstStarted:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for initial stream start")
		}
		tascoWaitForStreaming(t, session)

		if extAPI == nil {
			t.Fatal("expected extension API to be captured")
		}
		extAPI.SendUserMessage("Steer from extension", &core.SendUserMessageOptions{
			DeliverAs: "steer",
		})

		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if session.PendingMessageCount() == 1 {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		if got := session.PendingMessageCount(); got != 1 {
			close(releaseFirst)
			<-firstErr
			t.Fatalf("PendingMessageCount = %d, want 1", got)
		}
		if !slices.Contains(session.GetSteeringMessages(), "Steer from extension") {
			close(releaseFirst)
			<-firstErr
			t.Fatalf("GetSteeringMessages = %v, expected 'Steer from extension'", session.GetSteeringMessages())
		}
		mu.Lock()
		gotSource := lastInputSource
		var sawQueueEvent bool
		for _, qe := range queueEvents {
			if slices.Contains(qe.Steering, "Steer from extension") {
				sawQueueEvent = true
			}
		}
		mu.Unlock()
		if gotSource != core.InputSourceExtension {
			close(releaseFirst)
			<-firstErr
			t.Fatalf("lastInputSource = %q, want %q", gotSource, core.InputSourceExtension)
		}
		if !sawQueueEvent {
			close(releaseFirst)
			<-firstErr
			t.Fatal("expected queue_update event containing 'Steer from extension'")
		}

		close(releaseFirst)
		if err := <-firstErr; err != nil {
			t.Fatalf("Prompt failed: %v", err)
		}
		mu.Lock()
		sawSteer := sawSteeringMessage
		mu.Unlock()
		if !sawSteer {
			t.Fatal("expected steering message to be delivered to subsequent stream call")
		}
	})

	t.Run("allows prompt() after previous completes", func(t *testing.T) {
		tempDir := t.TempDir()
		model := compat.GetModel(compat.BuiltinProvider("anthropic"), "claude-sonnet-4-5")
		ag := agent.NewAgent(&agent.AgentOptions{
			GetApiKey: func(_ context.Context, _ string) (string, error) { return "test-key", nil },
			InitialState: &agent.AgentInitialState{
				Model:        model,
				SystemPrompt: "Test",
				Tools:        []*agent.AgentTool{},
			},
			StreamFn: func(_ context.Context, _ *ai.Model, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
				stream := ai.NewAssistantMessageEventStream()
				go func() {
					stream.Push(&ai.AssistantMessageEventStart{Type: "start", Partial: tascoAssistantMessage("", ai.StopReasonStop)})
					stream.Push(&ai.AssistantMessageEventDone{Type: "done", Reason: ai.StopReasonStop, Message: tascoAssistantMessage("Done", ai.StopReasonStop)})
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

		if err := session.Prompt(ctx, "First message"); err != nil {
			t.Fatalf("first Prompt: %v", err)
		}
		if session.IsStreaming() {
			t.Fatalf("expected IsStreaming() == false after completion")
		}
		if err := session.Prompt(ctx, "Second message"); err != nil {
			t.Fatalf("second Prompt: %v", err)
		}
	})

	t.Run("persists prior messages before tool execution events", func(t *testing.T) {
		tempDir := t.TempDir()
		model := compat.GetModel(compat.BuiltinProvider("anthropic"), "claude-sonnet-4-5")

		var mu sync.Mutex
		var snapshots [][]string

		sessionManager := core.SessionManagerInMemory()
		props := omap.NewMap[string, *typebox.Schema]()
		props.Set("q", typebox.String())
		dummyTool := &agent.AgentTool{
			Name:        "dummy",
			Label:       "dummy",
			Description: "Dummy tool",
			Parameters:  typebox.Object(props),
			Execute: func(_ context.Context, _ string, params any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
				mu.Lock()
				var roles []string
				for _, entry := range sessionManager.GetEntries() {
					if me, ok := entry.(*core.SessionMessageEntry); ok {
						roles = append(roles, me.Message.GetRole())
					}
				}
				snapshots = append(snapshots, roles)
				mu.Unlock()
				q := ""
				if obj, ok := params.(*jsonx.Object); ok && obj != nil {
					if v, ok := obj.Get("q"); ok {
						if s, ok := v.(string); ok {
							q = s
						}
					}
				}
				return agent.AgentToolResult{
					Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "result:" + q}},
				}, nil
			},
		}

		ag := agent.NewAgent(&agent.AgentOptions{
			GetApiKey: func(_ context.Context, _ string) (string, error) { return "test-key", nil },
			InitialState: &agent.AgentInitialState{
				Model:        model,
				SystemPrompt: "Test",
				Tools:        []*agent.AgentTool{dummyTool},
			},
			StreamFn: func(_ context.Context, tr *ai.Model, tc *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
				stream := ai.NewAssistantMessageEventStream()
				go func() {
					toolResults := 0
					if tc != nil {
						for _, m := range tc.Messages {
							if m.GetRole() == "toolResult" {
								toolResults++
							}
						}
					}
					if toolResults > 0 {
						doneMsg := tascoAssistantMessage("done", ai.StopReasonStop)
						stream.Push(&ai.AssistantMessageEventStart{Type: "start", Partial: tascoAssistantMessage("", ai.StopReasonStop)})
						stream.Push(&ai.AssistantMessageEventDone{Type: "done", Reason: ai.StopReasonStop, Message: doneMsg})
						return
					}
					args1 := jsonx.NewObject()
					args1.Set("q", "x")
					args2 := jsonx.NewObject()
					args2.Set("q", "y")
					toolMsg := tascoAssistantMessage("", ai.StopReasonToolUse,
						&ai.ToolCall{Type: "toolCall", Id: "toolu_1", Name: "dummy", Arguments: args1},
						&ai.ToolCall{Type: "toolCall", Id: "toolu_2", Name: "dummy", Arguments: args2},
					)
					stream.Push(&ai.AssistantMessageEventStart{Type: "start", Partial: tascoAssistantMessage("", ai.StopReasonToolUse)})
					stream.Push(&ai.AssistantMessageEventDone{Type: "done", Reason: ai.StopReasonToolUse, Message: toolMsg})
				}()
				return stream, nil
			},
		})
		settingsManager := core.SettingsManagerCreate(tempDir, tempDir)
		authStorage := core.AuthStorageCreate(filepath.Join(tempDir, "auth.json"))
		modelRegistry, _ := testkit.CreateModelRegistry(ctx, authStorage, &tempDir)
		_, _ = authStorage.Modify(ctx, "anthropic", func(_ ai.Credential) (ai.Credential, error) {
			k := "test-key"
			return &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Key: &k}, nil
		}, nil)
		runtime, _ := testkit.GetModelRuntime(modelRegistry)
		t.Cleanup(func() { runtime.Close() })
		session := core.NewAgentSession(core.AgentSessionConfig{
			Agent:             ag,
			SessionManager:    sessionManager,
			SettingsManager:   settingsManager,
			Cwd:               tempDir,
			ModelRuntime:      runtime,
			ResourceLoader:    testkit.CreateTestResourceLoader(),
			BaseToolsOverride: map[string]*agent.AgentTool{"dummy": dummyTool},
		})
		defer session.Dispose()

		if err := session.Prompt(ctx, "hi"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		_ = session.WaitForIdle(ctx)

		mu.Lock()
		defer mu.Unlock()
		if len(snapshots) != 2 {
			t.Fatalf("snapshots len = %d, want 2 (%#v)", len(snapshots), snapshots)
		}
		want := []string{"system", "user", "assistant"}
		for i, snap := range snapshots {
			if !slices.Equal(snap, want) {
				t.Fatalf("snapshot[%d] = %v, want %v", i, snap, want)
			}
		}
	})

	t.Run("persists message_end events in order with slow subscriber", func(t *testing.T) {
		tempDir := t.TempDir()
		model := compat.GetModel(compat.BuiltinProvider("anthropic"), "claude-sonnet-4-5")

		props := omap.NewMap[string, *typebox.Schema]()
		props.Set("q", typebox.String())
		dummyTool := &agent.AgentTool{
			Name:        "dummy",
			Label:       "dummy",
			Description: "Dummy tool",
			Parameters:  typebox.Object(props),
			Execute: func(_ context.Context, _ string, _ any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
				return agent.AgentToolResult{
					Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "result"}},
				}, nil
			},
		}

		ag := agent.NewAgent(&agent.AgentOptions{
			GetApiKey: func(_ context.Context, _ string) (string, error) { return "test-key", nil },
			InitialState: &agent.AgentInitialState{
				Model:        model,
				SystemPrompt: "Test",
				Tools:        []*agent.AgentTool{dummyTool},
			},
			StreamFn: func(_ context.Context, _ *ai.Model, tc *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
				stream := ai.NewAssistantMessageEventStream()
				go func() {
					hasToolResult := false
					if tc != nil {
						for _, m := range tc.Messages {
							if m.GetRole() == "toolResult" {
								hasToolResult = true
								break
							}
						}
					}
					if hasToolResult {
						doneMsg := tascoAssistantMessage("done", ai.StopReasonStop)
						stream.Push(&ai.AssistantMessageEventStart{Type: "start", Partial: tascoAssistantMessage("", ai.StopReasonStop)})
						stream.Push(&ai.AssistantMessageEventDone{Type: "done", Reason: ai.StopReasonStop, Message: doneMsg})
						return
					}
					args := jsonx.NewObject()
					args.Set("q", "x")
					toolMsg := tascoAssistantMessage("", ai.StopReasonToolUse,
						&ai.TextContent{Type: "text", Text: "calling tool"},
						&ai.ToolCall{Type: "toolCall", Id: "toolu_1", Name: "dummy", Arguments: args},
					)
					stream.Push(&ai.AssistantMessageEventStart{Type: "start", Partial: tascoAssistantMessage("", ai.StopReasonToolUse)})
					stream.Push(&ai.AssistantMessageEventDone{Type: "done", Reason: ai.StopReasonToolUse, Message: toolMsg})
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
		runtime, _ := testkit.GetModelRuntime(modelRegistry)
		t.Cleanup(func() { runtime.Close() })
		session := core.NewAgentSession(core.AgentSessionConfig{
			Agent:             ag,
			SessionManager:    sessionManager,
			SettingsManager:   settingsManager,
			Cwd:               tempDir,
			ModelRuntime:      runtime,
			ResourceLoader:    testkit.CreateTestResourceLoader(),
			BaseToolsOverride: map[string]*agent.AgentTool{"dummy": dummyTool},
		})
		defer session.Dispose()

		unsub := session.Subscribe(func(event core.AgentSessionEvent) {
			if me, ok := event.(*core.AgentSessionEventMessageEnd); ok && me.Message != nil && me.Message.GetRole() == "assistant" {
				time.Sleep(40 * time.Millisecond)
			}
		})
		defer unsub()

		if err := session.Prompt(ctx, "hi"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		_ = session.WaitForIdle(ctx)

		var roles []string
		for _, entry := range sessionManager.GetEntries() {
			if me, ok := entry.(*core.SessionMessageEntry); ok {
				roles = append(roles, me.Message.GetRole())
			}
		}
		want := []string{"system", "user", "assistant", "toolResult", "system", "assistant"}
		if !slices.Equal(roles, want) {
			t.Fatalf("roles = %v, want %v", roles, want)
		}
	})
}

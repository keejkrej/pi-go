// Ported from packages/coding-agent/test/sdk-stream-options.test.ts (pi v1.5.2).

package core_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/internal/testkit"
	"github.com/keejkrej/pi-go/internal/omap"
)

func tssoCreateModel(api string) *ai.Model {
	headers := omap.NewMap[string, string]()
	headers.Set("x-model", "model")
	return &ai.Model{
		BaseModel: ai.BaseModel{
			Id:       "capture-model",
			Name:     "Capture Model",
			Api:      api,
			Provider: "capture-provider",
			BaseUrl:  "https://capture.invalid/v1",
			Input:    []ai.ModelModality{ai.ModelModalityText},
			Headers:  headers,
		},
		Reasoning:     false,
		ContextWindow: 128000,
		MaxTokens:     4096,
	}
}

func tssoCreateDoneMessage(api string, promptTokens int) *ai.AssistantMessage {
	return &ai.AssistantMessage{
		Role:     "assistant",
		Content:  ai.ContentList{&ai.TextContent{Type: "text", Text: "ok"}},
		Api:      ai.Api(api),
		Provider: "capture-provider",
		Model:    "capture-model",
		Usage: ai.Usage{
			CacheRead:   promptTokens,
			TotalTokens: promptTokens,
		},
		StopReason: ai.StopReasonStop,
		Timestamp:  time.Now().UnixMilli(),
	}
}

func tssoCreateDoneStream(api string, promptTokens int) *ai.AssistantMessageEventStream {
	stream := ai.NewAssistantMessageEventStream()
	stream.Push(&ai.AssistantMessageEventDone{
		Type:    "done",
		Reason:  ai.StopReasonStop,
		Message: tssoCreateDoneMessage(api, promptTokens),
	})
	return stream
}

func tssoCaptureStreamOptions(
	t *testing.T,
	api string,
	settings *core.Settings,
	requestOptions *ai.SimpleStreamOptions,
) *ai.SimpleStreamOptions {
	return tssoCaptureStreamOptionsWithExtensions(t, api, settings, requestOptions, nil, nil)
}

func tssoCaptureStreamOptionsWithExtensions(
	t *testing.T,
	api string,
	settings *core.Settings,
	requestOptions *ai.SimpleStreamOptions,
	extensionFactories []core.InlineExtension,
	providerStreamEvent any,
) *ai.SimpleStreamOptions {
	t.Helper()
	ctx := context.Background()
	cwd := t.TempDir()
	agentDir := t.TempDir()

	model := tssoCreateModel(api)
	settingsManager := core.SettingsManagerInMemory(settings)
	rl := core.NewDefaultResourceLoader(core.DefaultResourceLoaderOptions{
		Cwd:                cwd,
		AgentDir:           agentDir,
		SettingsManager:    settingsManager,
		NoExtensions:       true,
		ExtensionFactories: extensionFactories,
	})
	if err := rl.Reload(ctx); err != nil {
		t.Fatalf("ResourceLoader.Reload: %v", err)
	}

	authStorage := core.AuthStorageCreate(filepath.Join(agentDir, "auth.json"))
	key := "test-api-key"
	if _, err := authStorage.Modify(ctx, string(model.Provider), func(ai.Credential) (ai.Credential, error) {
		return &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Key: &key}, nil
	}, nil); err != nil {
		t.Fatalf("authStorage.Modify: %v", err)
	}
	modelsPath := filepath.Join(agentDir, "models.json")
	modelRegistry, err := testkit.CreateModelRegistry(ctx, authStorage, &modelsPath)
	if err != nil {
		t.Fatalf("CreateModelRegistry: %v", err)
	}
	modelRuntime, err := testkit.GetModelRuntime(modelRegistry)
	if err != nil {
		t.Fatalf("GetModelRuntime: %v", err)
	}
	registered := false
	// Close ModelRuntime after UnregisterProvider and before t.TempDir() RemoveAll runs.
	t.Cleanup(func() {
		if registered {
			modelRegistry.UnregisterProvider(string(model.Provider))
		}
		modelRuntime.Close()
	})

	provHeaders := omap.NewMap[string, string]()
	provHeaders.Set("x-provider", "provider")

	var captured *ai.SimpleStreamOptions
	apiCopy := api
	if err := modelRegistry.RegisterProvider(string(model.Provider), core.ProviderConfigInput{
		Api:     &apiCopy,
		Headers: provHeaders,
		StreamSimple: func(_ context.Context, m *ai.Model, _ *ai.TranscriptContext, providerOptions *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
			captured = providerOptions
			if providerStreamEvent != nil && providerOptions != nil && providerOptions.OnProviderStreamEvent != nil {
				_ = providerOptions.OnProviderStreamEvent(providerStreamEvent, m)
			}
			return tssoCreateDoneStream(api, 0)
		},
	}); err != nil {
		t.Fatalf("RegisterProvider: %v", err)
	}
	registered = true

	sm, err := core.SessionManagerInMemoryWith(cwd, nil, nil)
	if err != nil {
		t.Fatalf("SessionManagerInMemoryWith: %v", err)
	}

	res, err := core.CreateAgentSession(ctx, &core.CreateAgentSessionOptions{
		Cwd:             cwd,
		AgentDir:        agentDir,
		Model:           model,
		ModelRuntime:    modelRuntime,
		SettingsManager: settingsManager,
		SessionManager:  sm,
		ResourceLoader:  rl,
	})
	if err != nil {
		t.Fatalf("CreateAgentSession: %v", err)
	}
	defer res.Session.Dispose()

	stream, err := res.Session.Agent.StreamFn()(
		ctx,
		model,
		ai.NormalizeContext(&ai.Context{Messages: []ai.Message{}}),
		requestOptions,
	)
	if err != nil {
		t.Fatalf("StreamFn: %v", err)
	}
	if _, err := stream.Result(ctx); err != nil {
		t.Fatalf("stream.Result: %v", err)
	}
	return captured
}

type tssoCacheWarmingFixture struct {
	session       *core.AgentSession
	providerCalls func() int
	dispose       func()
}

func tssoCreateCacheWarmingSession(t *testing.T, populate func(manager *core.SessionManager, model *ai.Model)) *tssoCacheWarmingFixture {
	t.Helper()
	ctx := context.Background()
	cwd := t.TempDir()
	agentDir := t.TempDir()

	base := tssoCreateModel("anthropic-messages")
	shortTtl := 300
	base.Cost = ai.ModelCost{
		ModelCostRates: ai.ModelCostRates{
			Input:      10,
			Output:     50,
			CacheRead:  0.25,
			CacheWrite: 12.5,
		},
	}
	base.PromptCache = &ai.ModelPromptCache{Short: &shortTtl}

	authStorage := core.AuthStorageCreate(filepath.Join(agentDir, "auth.json"))
	key := "test-api-key"
	if _, err := authStorage.Modify(ctx, string(base.Provider), func(ai.Credential) (ai.Credential, error) {
		return &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Key: &key}, nil
	}, nil); err != nil {
		t.Fatalf("authStorage.Modify: %v", err)
	}
	modelsPath := filepath.Join(agentDir, "models.json")
	modelRegistry, err := testkit.CreateModelRegistry(ctx, authStorage, &modelsPath)
	if err != nil {
		t.Fatalf("CreateModelRegistry: %v", err)
	}
	modelRuntime, err := testkit.GetModelRuntime(modelRegistry)
	if err != nil {
		t.Fatalf("GetModelRuntime: %v", err)
	}
	registered := false
	// Guarantee async ModelRuntime refresh stops before t.TempDir() RemoveAll runs.
	t.Cleanup(func() {
		if registered {
			modelRegistry.UnregisterProvider(string(base.Provider))
			registered = false
		}
		modelRuntime.Close()
	})

	providerCalls := 0
	apiCopy := base.Api
	if err := modelRegistry.RegisterProvider(string(base.Provider), core.ProviderConfigInput{
		Api: &apiCopy,
		StreamSimple: func(_ context.Context, _ *ai.Model, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
			providerCalls++
			return tssoCreateDoneStream(base.Api, 100000)
		},
	}); err != nil {
		t.Fatalf("RegisterProvider: %v", err)
	}
	registered = true

	sm, err := core.SessionManagerInMemoryWith(cwd, nil, nil)
	if err != nil {
		t.Fatalf("SessionManagerInMemoryWith: %v", err)
	}
	if populate != nil {
		populate(sm, base)
	}

	mode := core.CacheWarmingIdle
	res, err := core.CreateAgentSession(ctx, &core.CreateAgentSessionOptions{
		Cwd:             cwd,
		AgentDir:        agentDir,
		Model:           base,
		ModelRuntime:    modelRuntime,
		SettingsManager: core.SettingsManagerInMemory(&core.Settings{CacheWarming: &mode}),
		SessionManager:  sm,
	})
	if err != nil {
		t.Fatalf("CreateAgentSession: %v", err)
	}

	return &tssoCacheWarmingFixture{
		session: res.Session,
		providerCalls: func() int {
			return providerCalls
		},
		dispose: func() {
			res.Session.Dispose()
			if registered {
				modelRegistry.UnregisterProvider(string(base.Provider))
				registered = false
			}
			modelRuntime.Close()
		},
	}
}

func TestSdkStreamOptions(t *testing.T) {
	ctx := context.Background()

	t.Run("schedules cache warming after a completed session request", func(t *testing.T) {
		fix := tssoCreateCacheWarmingSession(t, nil)
		defer fix.dispose()

		if err := fix.session.Prompt(ctx, "test"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		st := fix.session.CacheWarmingStatus()
		if st == nil || st.NextWarmAt == nil || *st.NextWarmAt <= time.Now().UnixMilli() {
			t.Fatalf("expected NextWarmAt > now, got %#v", st)
		}

		// Equivalent shallow copies remain current, but removing the request prefix does not.
		msgs := fix.session.Agent.Messages()
		fix.session.Agent.ReplaceMessages(append([]agent.AgentMessage(nil), msgs...))
		mCopy := *fix.session.Agent.Model()
		fix.session.Agent.SetModel(&mCopy)
		st = fix.session.CacheWarmingStatus()
		if st == nil || st.NextWarmAt == nil || *st.NextWarmAt <= time.Now().UnixMilli() {
			t.Fatalf("expected NextWarmAt > now after shallow copy, got %#v", st)
		}

		fix.session.Agent.ReplaceMessages(msgs[1:])
		st = fix.session.CacheWarmingStatus()
		if st == nil || st.Reason == nil || *st.Reason != "conversation context changed" {
			t.Fatalf("expected reason 'conversation context changed', got %#v", st)
		}
	})

	t.Run("waits for the next request instead of restoring cache warming", func(t *testing.T) {
		fix := tssoCreateCacheWarmingSession(t, func(manager *core.SessionManager, model *ai.Model) {
			_, _ = manager.AppendModelChange(string(model.Provider), model.Id)
			_, _ = manager.AppendThinkingLevelChange("off")
			uText := "test"
			_, _ = manager.AppendMessage(&ai.UserMessage{
				Role:      "user",
				Content:   ai.UserMessageContent{Text: &uText},
				Timestamp: time.Now().UnixMilli() - 60000,
			})
			assistant := tssoCreateDoneMessage(model.Api, 100000)
			assistant.Timestamp = time.Now().UnixMilli() - 59000
			_, _ = manager.AppendMessage(assistant)
			_, _ = manager.AppendUsage("cache_warm", string(model.Provider), model.Id, assistant.Usage)
		})
		defer fix.dispose()

		if fix.providerCalls() != 0 {
			t.Fatalf("providerCalls = %d, want 0", fix.providerCalls())
		}
		st := fix.session.CacheWarmingStatus()
		if st == nil || st.State != "inactive" || st.Reason == nil || *st.Reason != "waiting for first request" {
			t.Fatalf("CacheWarmingStatus = %#v, want inactive / waiting for first request", st)
		}
	})

	t.Run("forwards httpIdleTimeoutMs as timeoutMs for OpenAI Codex", func(t *testing.T) {
		tVal := core.TimeoutSettingMs(1234)
		options := tssoCaptureStreamOptions(t, "openai-codex-responses", &core.Settings{HttpIdleTimeoutMs: &tVal}, nil)
		if options == nil || options.TimeoutMs == nil || *options.TimeoutMs != 1234 {
			t.Fatalf("TimeoutMs = %v, want 1234", options)
		}
	})

	t.Run("defaults timeoutMs from httpIdleTimeoutMs for all providers", func(t *testing.T) {
		tVal := core.TimeoutSettingMs(1234)
		options := tssoCaptureStreamOptions(t, "openai-completions", &core.Settings{HttpIdleTimeoutMs: &tVal}, nil)
		if options == nil || options.TimeoutMs == nil || *options.TimeoutMs != 1234 {
			t.Fatalf("TimeoutMs = %v, want 1234", options)
		}
	})

	t.Run("lets request timeoutMs override httpIdleTimeoutMs for OpenAI Codex", func(t *testing.T) {
		tVal := core.TimeoutSettingMs(1234)
		reqTimeout := int64(0)
		options := tssoCaptureStreamOptions(
			t,
			"openai-codex-responses",
			&core.Settings{HttpIdleTimeoutMs: &tVal},
			&ai.SimpleStreamOptions{StreamOptions: ai.StreamOptions{ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{TimeoutMs: &reqTimeout}}},
		)
		if options == nil || options.TimeoutMs == nil || *options.TimeoutMs != 0 {
			t.Fatalf("TimeoutMs = %v, want 0", options)
		}
	})

	t.Run("forwards websocketConnectTimeoutMs from settings", func(t *testing.T) {
		tVal := core.TimeoutSettingMs(1234)
		options := tssoCaptureStreamOptions(t, "openai-codex-responses", &core.Settings{WebsocketConnectTimeoutMs: &tVal}, nil)
		if options == nil || options.WebsocketConnectTimeoutMs == nil || *options.WebsocketConnectTimeoutMs != 1234 {
			t.Fatalf("WebsocketConnectTimeoutMs = %v, want 1234", options)
		}
	})

	t.Run("lets request websocketConnectTimeoutMs override settings", func(t *testing.T) {
		tVal := core.TimeoutSettingMs(1234)
		reqWs := int64(0)
		options := tssoCaptureStreamOptions(
			t,
			"openai-codex-responses",
			&core.Settings{WebsocketConnectTimeoutMs: &tVal},
			&ai.SimpleStreamOptions{StreamOptions: ai.StreamOptions{WebsocketConnectTimeoutMs: &reqWs}},
		)
		if options == nil || options.WebsocketConnectTimeoutMs == nil || *options.WebsocketConnectTimeoutMs != 0 {
			t.Fatalf("WebsocketConnectTimeoutMs = %v, want 0", options)
		}
	})

	t.Run("forwards provider retry settings", func(t *testing.T) {
		maxRetries := 2
		maxRetryDelayMs := float64(3000)
		options := tssoCaptureStreamOptions(
			t,
			"openai-completions",
			&core.Settings{
				Retry: &core.RetrySettings{
					Provider: &core.ProviderRetrySettings{
						MaxRetries:      &maxRetries,
						MaxRetryDelayMs: &maxRetryDelayMs,
					},
				},
			},
			nil,
		)
		if options == nil || options.MaxRetries == nil || *options.MaxRetries != 2 {
			t.Fatalf("MaxRetries = %v, want 2", options)
		}
		if options.MaxRetryDelayMs == nil || *options.MaxRetryDelayMs != 3000 {
			t.Fatalf("MaxRetryDelayMs = %v, want 3000", options)
		}
	})

	t.Run("forwards provider stream events to extensions", func(t *testing.T) {
		providerEvent := map[string]any{
			"openrouter_metadata": map[string]any{"strategy": "direct"},
		}
		var extensionEvents []core.ProviderStreamEvent

		options := tssoCaptureStreamOptionsWithExtensions(
			t,
			"openai-completions",
			nil,
			nil,
			[]core.InlineExtension{
				{
					Name: "provider-stream-listener",
					Factory: func(pi core.ExtensionAPI) error {
						pi.OnProviderStreamEvent(func(_ context.Context, ev *core.ProviderStreamEvent, _ core.ExtensionContext) (*core.NoResult, error) {
							if ev != nil {
								extensionEvents = append(extensionEvents, *ev)
							}
							return nil, nil
						})
						return nil
					},
				},
			},
			providerEvent,
		)

		if options == nil || options.OnProviderStreamEvent == nil {
			t.Fatalf("expected OnProviderStreamEvent callback on captured options, got %#v", options)
		}
		if len(extensionEvents) != 1 {
			t.Fatalf("expected 1 provider_stream_event, got %d (%#v)", len(extensionEvents), extensionEvents)
		}
		ev := extensionEvents[0]
		if ev.Type != "provider_stream_event" || ev.Provider != "capture-provider" || ev.Api != "openai-completions" || ev.Model != "capture-model" {
			t.Fatalf("unexpected provider_stream_event metadata: %#v", ev)
		}
		m, ok := ev.Data.(map[string]any)
		if !ok {
			t.Fatalf("expected map data, got %T", ev.Data)
		}
		meta, _ := m["openrouter_metadata"].(map[string]any)
		if meta["strategy"] != "direct" {
			t.Fatalf("unexpected provider_stream_event data: %#v", ev.Data)
		}
	})

	t.Run("runs before_provider_headers on assembled headers without forwarding the transform", func(t *testing.T) {
		explicitVal := "explicit"
		explicitHeaders := omap.NewMap[string, *string]()
		explicitHeaders.Set("x-explicit", &explicitVal)
		options := tssoCaptureStreamOptionsWithExtensions(
			t,
			"openai-completions",
			nil,
			&ai.SimpleStreamOptions{
				StreamOptions: ai.StreamOptions{
					ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
						Headers: explicitHeaders,
					},
				},
			},
			[]core.InlineExtension{
				{
					Name: "before-provider-headers",
					Factory: func(pi core.ExtensionAPI) error {
						pi.OnBeforeProviderHeaders(func(_ context.Context, ev *core.BeforeProviderHeadersEvent, _ core.ExtensionContext) (*core.NoResult, error) {
							if ev != nil && ev.Headers != nil {
								deref := func(key string) string {
									v, _ := ev.Headers.Get(key)
									if v == nil {
										return ""
									}
									return *v
								}
								hookVal := deref("x-provider") + ":" + deref("x-model") + ":" + deref("x-explicit")
								ev.Headers.Set("x-hook", &hookVal)
							}
							return nil, nil
						})
						return nil
					},
				},
			},
			nil,
		)

		if options == nil || options.Headers == nil {
			t.Fatalf("expected captured Headers, got %#v", options)
		}
		for k, want := range map[string]string{
			"x-provider": "provider",
			"x-model":    "model",
			"x-explicit": "explicit",
			"x-hook":     "provider:model:explicit",
		} {
			got, ok := options.Headers.Get(k)
			if !ok || got == nil || *got != want {
				var gotStr string
				if got != nil {
					gotStr = *got
				}
				t.Fatalf("header %q = (%q, %v), want %q", k, gotStr, ok, want)
			}
		}
	})
}

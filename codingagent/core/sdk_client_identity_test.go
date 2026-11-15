// Ported from packages/coding-agent/test/sdk-client-identity.test.ts (pi v1.5.2).

package core_test

import (
	"context"
	"os"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/internal/testkit"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/omap"
)

func tsdkciCreateModel(provider, baseUrl, id string) *ai.Model {
	if id == "" {
		id = provider + "-test-model"
	}
	return &ai.Model{
		BaseModel: ai.BaseModel{
			Id:       id,
			Name:     provider + " Test Model",
			Api:      "openai-completions",
			Provider: ai.ProviderId(provider),
			BaseUrl:  baseUrl,
			Input:    []ai.ModelModality{ai.ModelModalityText},
		},
		Reasoning:     false,
		ContextWindow: 128000,
		MaxTokens:     4096,
	}
}

func tsdkciCreateDoneStream() *ai.AssistantMessageEventStream {
	stream := ai.NewAssistantMessageEventStream()
	msg := &ai.AssistantMessage{
		Role:       "assistant",
		Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: "ok"}},
		Api:        "openai-completions",
		Provider:   "capture-provider",
		Model:      "capture-model",
		StopReason: ai.StopReasonStop,
	}
	stream.Push(&ai.AssistantMessageEventDone{
		Type:    "done",
		Reason:  ai.StopReasonStop,
		Message: msg,
	})
	return stream
}

type tsdkciCaptureOpts struct {
	telemetryEnabled *bool
	providerHeaders  map[string]string
	sessionId        string
}

func tsdkciCaptureOptions(t *testing.T, model *ai.Model, opts tsdkciCaptureOpts) *ai.SimpleStreamOptions {
	t.Helper()
	ctx := context.Background()
	cwd := t.TempDir()
	agentDir := t.TempDir()

	settingsManager := core.SettingsManagerCreate(cwd, agentDir)
	if opts.telemetryEnabled != nil && !*opts.telemetryEnabled {
		settingsManager.SetEnableInstallTelemetry(false)
	}

	key := "test-api-key"
	authStorage := core.AuthStorageInMemory(map[string]ai.Credential{
		string(model.Provider): &ai.ApiKeyCredential{
			Type: ai.AuthTypeApiKey,
			Key:  &key,
		},
	})
	modelRegistry, err := testkit.CreateInMemoryModelRegistry(ctx, authStorage)
	if err != nil {
		t.Fatalf("CreateInMemoryModelRegistry: %v", err)
	}
	modelRuntime, err := testkit.GetModelRuntime(modelRegistry)
	if err != nil {
		t.Fatalf("GetModelRuntime: %v", err)
	}
	registered := false
	t.Cleanup(func() {
		if registered {
			modelRegistry.UnregisterProvider(string(model.Provider))
		}
		modelRuntime.Close()
	})

	var provHeaders *omap.Map[string, string]
	if len(opts.providerHeaders) > 0 {
		provHeaders = omap.NewMap[string, string]()
		for k, v := range opts.providerHeaders {
			provHeaders.Set(k, v)
		}
	}

	var capturedOptions *ai.SimpleStreamOptions
	apiStr := model.Api
	if err := modelRegistry.RegisterProvider(string(model.Provider), core.ProviderConfigInput{
		Api:     &apiStr,
		Headers: provHeaders,
		StreamSimple: func(_ context.Context, _ *ai.Model, _ *ai.TranscriptContext, providerOptions *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
			capturedOptions = providerOptions
			return tsdkciCreateDoneStream()
		},
	}); err != nil {
		t.Fatalf("RegisterProvider: %v", err)
	}
	registered = true
	sessionManager, err := core.SessionManagerInMemoryWith(cwd, nil, nil)
	if err != nil {
		t.Fatalf("SessionManagerInMemoryWith: %v", err)
	}
	if opts.sessionId != "" {
		sid := opts.sessionId
		if _, err := sessionManager.NewSession(&core.NewSessionOptions{Id: &sid}); err != nil {
			t.Fatalf("NewSession: %v", err)
		}
	}

	res, err := core.CreateAgentSession(ctx, &core.CreateAgentSessionOptions{
		Cwd:             cwd,
		AgentDir:        agentDir,
		Model:           model,
		ModelRuntime:    modelRuntime,
		SettingsManager: settingsManager,
		SessionManager:  sessionManager,
	})
	if err != nil {
		t.Fatalf("CreateAgentSession: %v", err)
	}
	defer res.Session.Dispose()

	sid := res.Session.SessionId()
	stream, err := res.Session.Agent.StreamFn()(
		ctx,
		model,
		ai.NormalizeContext(&ai.Context{Messages: []ai.Message{}}),
		&ai.SimpleStreamOptions{
			StreamOptions: ai.StreamOptions{
				SessionId: &sid,
			},
		},
	)
	if err != nil {
		t.Fatalf("StreamFn: %v", err)
	}
	if _, err := stream.Result(ctx); err != nil {
		t.Fatalf("stream.Result: %v", err)
	}
	return capturedOptions
}

func TestSdkClientIdentity(t *testing.T) {
	if prevTelemetry, hadTelemetry := os.LookupEnv("PI_TELEMETRY"); hadTelemetry {
		_ = os.Unsetenv("PI_TELEMETRY")
		t.Cleanup(func() { _ = os.Setenv("PI_TELEMETRY", prevTelemetry) })
	}

	t.Run("identifies pi with a detailed User-Agent and attribution as a CLI agent", func(t *testing.T) {
		model := tsdkciCreateModel("openrouter", "https://openrouter.ai/api/v1", "")
		options := tsdkciCaptureOptions(t, model, tsdkciCaptureOpts{})
		if options == nil || options.Identity == nil {
			t.Fatalf("expected captured Identity, got %#v", options)
		}
		if options.Identity.UserAgent.Val != ai.UserAgentModeDetailed {
			t.Fatalf("UserAgent = %v, want detailed", options.Identity.UserAgent.Val)
		}
		if options.Identity.Version == nil || *options.Identity.Version != utils.VERSION {
			t.Fatalf("Version = %v, want %q", options.Identity.Version, utils.VERSION)
		}
		if attr, ok := options.Identity.Attribution.Get(); !ok || attr.Name != ai.PiAttribution.Name {
			t.Fatalf("Attribution = %#v, want PiAttribution", options.Identity.Attribution)
		}

		headers := ai.GetAttributionHeaders(ai.ClientIdentityTarget{
			Provider: string(model.Provider),
			BaseUrl:  model.BaseUrl,
		}, options.Identity)
		if got, _ := headers.Get("HTTP-Referer"); got != "https://pi.dev" {
			t.Fatalf("HTTP-Referer = %q, want https://pi.dev", got)
		}
		if got, _ := headers.Get("X-OpenRouter-Title"); got != "pi" {
			t.Fatalf("X-OpenRouter-Title = %q, want pi", got)
		}
		if got, _ := headers.Get("X-OpenRouter-Categories"); got != "cli-agent" {
			t.Fatalf("X-OpenRouter-Categories = %q, want cli-agent", got)
		}
	})

	t.Run("turns attribution off when telemetry is disabled", func(t *testing.T) {
		model := tsdkciCreateModel("nvidia", "https://integrate.api.nvidia.com/v1", "")
		disabled := false
		options := tsdkciCaptureOptions(t, model, tsdkciCaptureOpts{telemetryEnabled: &disabled})
		if options == nil || options.Identity == nil {
			t.Fatalf("expected captured Identity, got %#v", options)
		}
		if !options.Identity.Attribution.IsNull() {
			t.Fatalf("expected null Attribution when telemetry disabled, got %#v", options.Identity.Attribution)
		}
		headers := ai.GetAttributionHeaders(ai.ClientIdentityTarget{
			Provider: string(model.Provider),
			BaseUrl:  model.BaseUrl,
		}, options.Identity)
		if headers != nil && headers.Len() != 0 {
			t.Fatalf("expected no attribution headers, got %d", headers.Len())
		}
	})

	t.Run("turns attribution off with PI_TELEMETRY=0", func(t *testing.T) {
		t.Setenv("PI_TELEMETRY", "0")
		model := tsdkciCreateModel("openrouter", "https://openrouter.ai/api/v1", "")
		options := tsdkciCaptureOptions(t, model, tsdkciCaptureOpts{})
		if options == nil || options.Identity == nil {
			t.Fatalf("expected captured Identity, got %#v", options)
		}
		if !options.Identity.Attribution.IsNull() {
			t.Fatalf("expected null Attribution with PI_TELEMETRY=0, got %#v", options.Identity.Attribution)
		}
	})

	t.Run("adds OpenCode session headers for custom OpenCode providers", func(t *testing.T) {
		model := tsdkciCreateModel("custom-opencode", "https://opencode.ai/zen/v1", "")
		options := tsdkciCaptureOptions(t, model, tsdkciCaptureOpts{sessionId: "opencode-session"})
		if options == nil || options.Headers == nil {
			t.Fatalf("expected captured Headers, got %#v", options)
		}
		got, ok := options.Headers.Get("x-opencode-session")
		if !ok || got == nil || *got != "opencode-session" {
			t.Fatalf("x-opencode-session = %v (ok=%v), want opencode-session", got, ok)
		}
	})

	t.Run("lets configured OpenCode headers override the session header", func(t *testing.T) {
		model := tsdkciCreateModel("opencode", "https://opencode.ai/zen/v1", "")
		options := tsdkciCaptureOptions(t, model, tsdkciCaptureOpts{
			sessionId: "opencode-session",
			providerHeaders: map[string]string{
				"x-opencode-session": "configured-session",
			},
		})
		if options == nil || options.Headers == nil {
			t.Fatalf("expected captured Headers, got %#v", options)
		}
		got, ok := options.Headers.Get("x-opencode-session")
		if !ok || got == nil || *got != "configured-session" {
			t.Fatalf("x-opencode-session = %v (ok=%v), want configured-session", got, ok)
		}
	})
}

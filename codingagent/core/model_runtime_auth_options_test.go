// Ported from packages/coding-agent/test/model-runtime-auth-options.test.ts (pi v1.5.2).

package core

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/compat"
	"github.com/keejkrej/pi-go/internal/omap"
)

func tmraoStrPtr(s string) *string { return &s }
func tmraoBoolPtr(b bool) *bool    { return &b }

func tmraoNewRuntime(t *testing.T, creds ai.CredentialStore) *ModelRuntime {
	t.Helper()
	t.Setenv("PI_OFFLINE", "1")
	if creds == nil {
		creds = ai.NewInMemoryCredentialStore()
	}
	rt, err := CreateModelRuntime(t.Context(), &CreateModelRuntimeOptions{
		Credentials:     creds,
		ModelsPath:      tmraoStrPtr(filepath.Join(t.TempDir(), "nonexistent-models.json")),
		ModelsStore:     NewInMemoryCodingAgentModelsStore(),
		RefreshOnCreate: tmraoBoolPtr(true),
	})
	if err != nil {
		t.Fatalf("CreateModelRuntime: %v", err)
	}
	t.Cleanup(rt.Close)
	return rt
}

func TestModelRuntimeAuthOptionsGetAvailableScopedError(t *testing.T) {
	rt := tmraoNewRuntime(t, nil)

	brokenModel := &ai.Model{
		BaseModel: ai.BaseModel{
			Id:       "broken-m",
			Name:     "Broken",
			Api:      "openai-completions",
			Provider: "broken-auth-prov",
			BaseUrl:  "https://broken.example.com/v1",
			Input:    []ai.ModelModality{ai.ModelModalityText},
		},
		ContextWindow: 128000,
		MaxTokens:     4096,
	}
	healthyModel := &ai.Model{
		BaseModel: ai.BaseModel{
			Id:       "healthy-m",
			Name:     "Healthy",
			Api:      "openai-completions",
			Provider: "healthy-auth-prov",
			BaseUrl:  "https://healthy.example.com/v1",
			Input:    []ai.ModelModality{ai.ModelModalityText},
		},
		ContextWindow: 128000,
		MaxTokens:     4096,
	}

	if err := rt.RegisterNativeProvider(&ai.Provider{
		Id:   "broken-auth-prov",
		Name: "Broken Auth",
		Auth: ai.ProviderAuth{
			ApiKey: &ai.ApiKeyAuth{
				Name: "API key",
				Check: func(context.Context, ai.ApiKeyCheckInput) (*ai.AuthCheck, error) {
					return nil, errors.New("boom auth check")
				},
			},
		},
		GetModels: func() []*ai.Model { return []*ai.Model{brokenModel} },
	}); err != nil {
		t.Fatalf("RegisterNativeProvider broken: %v", err)
	}
	if err := rt.RegisterNativeProvider(&ai.Provider{
		Id:   "healthy-auth-prov",
		Name: "Healthy Auth",
		Auth: ai.ProviderAuth{
			ApiKey: &ai.ApiKeyAuth{
				Name: "API key",
				Check: func(context.Context, ai.ApiKeyCheckInput) (*ai.AuthCheck, error) {
					src := "ok"
					return &ai.AuthCheck{Type: ai.AuthTypeApiKey, Source: &src}, nil
				},
				Resolve: func(context.Context, ai.ApiKeyResolveInput) (*ai.AuthResult, error) {
					k := "sk-healthy"
					src := "ok"
					return &ai.AuthResult{Auth: ai.ModelAuth{ApiKey: &k}, Source: &src}, nil
				},
			},
		},
		GetModels: func() []*ai.Model { return []*ai.Model{healthyModel} },
	}); err != nil {
		t.Fatalf("RegisterNativeProvider healthy: %v", err)
	}

	if _, err := rt.GetAvailable(t.Context(), nil, nil); err == nil || !strings.Contains(err.Error(), "boom auth check") {
		t.Fatalf("expected unscoped GetAvailable to surface auth check error, got %v", err)
	}
	avail, err := rt.GetAvailable(t.Context(), tmraoStrPtr("healthy-auth-prov"), nil)
	if err != nil {
		t.Fatalf("scoped GetAvailable failed: %v", err)
	}
	if len(avail) != 1 || avail[0].Id != "healthy-m" {
		t.Fatalf("expected [healthy-m], got %+v", avail)
	}
}

func TestModelRuntimeAuthOptionsEnvOverrideAndTransforms(t *testing.T) {
	rt := tmraoNewRuntime(t, nil)

	hdrs := omap.NewMap[string, string]()
	hdrs.Set("x-request-value", "$REQUEST_SCOPED_HEADER")

	var capturedKey *string
	var capturedHeaders ai.ProviderHeaders

	if err := rt.RegisterProvider("env-scoped-prov", ProviderConfigInput{
		BaseUrl: tmraoStrPtr("https://scoped.example.com/v1"),
		ApiKey:  tmraoStrPtr("$REQUEST_SCOPED_API_KEY"),
		Api:     tmraoStrPtr("openai-completions"),
		Headers: hdrs,
		Models: []ProviderModelConfig{
			&ProviderChatModelConfig{
				ProviderModelConfigBase: ProviderModelConfigBase{Id: "scoped-m", Name: "Scoped"},
				ContextWindow:           128000,
				MaxTokens:               4096,
			},
		},
		StreamSimple: func(_ context.Context, m *ai.Model, _ *ai.TranscriptContext, opts *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
			capturedKey = opts.ApiKey
			capturedHeaders = opts.Headers
			s := ai.NewAssistantMessageEventStream()
			msg := &ai.AssistantMessage{
				Role:       "assistant",
				Api:        ai.Api(m.Api),
				Provider:   m.Provider,
				Model:      m.Id,
				StopReason: ai.StopReasonStop,
				Content:    []ai.Content{&ai.TextContent{Type: "text", Text: "ok"}},
			}
			s.Push(&ai.AssistantMessageEventDone{
				Type:    "done",
				Reason:  ai.StopReasonStop,
				Message: msg,
			})
			s.End(msg)
			return s
		},
	}); err != nil {
		t.Fatalf("RegisterProvider: %v", err)
	}

	if _, err := rt.GetAuth(t.Context(), "env-scoped-prov", nil); err == nil || !strings.Contains(err.Error(), "REQUEST_SCOPED_API_KEY") {
		t.Fatalf("expected missing REQUEST_SCOPED_API_KEY error, got %v", err)
	}

	authRes, err := rt.GetAuth(t.Context(), "env-scoped-prov", &ai.AuthResolutionOverrides{
		Env: map[string]string{
			"REQUEST_SCOPED_API_KEY": "req-key",
			"REQUEST_SCOPED_HEADER":  "req-hdr",
		},
	})
	if err != nil {
		t.Fatalf("GetAuth with request env: %v", err)
	}
	if authRes == nil || authRes.Auth.ApiKey == nil || *authRes.Auth.ApiKey != "req-key" {
		t.Fatalf("expected req-key, got %+v", authRes)
	}
	if hv, ok := authRes.Auth.Headers.Get("x-request-value"); !ok || hv == nil || *hv != "req-hdr" {
		t.Fatalf("expected x-request-value=req-hdr, got (%v, %v)", hv, ok)
	}

	model := rt.GetModel("env-scoped-prov", "scoped-m")
	if model == nil {
		t.Fatal("missing scoped-m")
	}

	_, err = rt.CompleteSimple(t.Context(), model, &ai.Context{
		Messages: ai.MessageList{&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tmraoStrPtr("hi")}}},
	}, &ai.ModelsSimpleStreamOptions{
		SimpleStreamOptions: ai.SimpleStreamOptions{
			StreamOptions: ai.StreamOptions{
				ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
					ApiKey: tmraoStrPtr("explicit-override-key"),
					Env: map[string]string{
						"REQUEST_SCOPED_API_KEY": "req-key",
						"REQUEST_SCOPED_HEADER":  "req-hdr",
					},
				},
			},
		},
		ModelsRequestTransforms: ai.ModelsRequestTransforms{
			TransformHeaders: func(h ai.ProviderHeaders) (ai.ProviderHeaders, error) {
				out := omap.NewMap[string, *string]()
				if h != nil {
					for k, v := range h.All() {
						out.Set(k, v)
					}
				}
				added := "transformed"
				out.Set("x-transformed", &added)
				return out, nil
			},
		},
	})
	if err != nil {
		t.Fatalf("CompleteSimple: %v", err)
	}
	if capturedKey == nil || *capturedKey != "explicit-override-key" {
		t.Fatalf("expected explicit-override-key, got %v", capturedKey)
	}
	if tv, ok := capturedHeaders.Get("x-transformed"); !ok || tv == nil || *tv != "transformed" {
		t.Fatalf("expected transformed header, got %v %v", tv, ok)
	}
	if rv, ok := capturedHeaders.Get("x-request-value"); !ok || rv == nil || *rv != "req-hdr" {
		t.Fatalf("expected x-request-value=req-hdr, got %v %v", rv, ok)
	}
}

func TestModelRuntimeAuthOptionsOAuthSubscriptionAndRefreshCtx(t *testing.T) {
	creds := ai.NewInMemoryCredentialStore()
	exp := float64(time.Now().Add(time.Hour).UnixMilli())
	_, _ = creds.Modify(t.Context(), "sub-oauth", func(ai.Credential) (ai.Credential, error) {
		return &ai.OAuthCredential{Type: ai.AuthTypeOAuth, OAuthCredentials: ai.OAuthCredentials{Access: "a", Refresh: "r", Expires: exp}}, nil
	}, nil)
	_, _ = creds.Modify(t.Context(), "gen-oauth", func(ai.Credential) (ai.Credential, error) {
		return &ai.OAuthCredential{Type: ai.AuthTypeOAuth, OAuthCredentials: ai.OAuthCredentials{Access: "a", Refresh: "r", Expires: exp}}, nil
	}, nil)

	rt := tmraoNewRuntime(t, creds)
	makeOAuth := func(id string, isSub bool) *ai.Provider {
		return &ai.Provider{
			Id:   id,
			Name: id,
			Auth: ai.ProviderAuth{
				OAuth: &ai.OAuthAuth{
					Name:           id,
					IsSubscription: isSub,
					Login: func(context.Context, ai.AuthInteraction, *ai.LoginOptions) (*ai.OAuthCredential, error) {
						return &ai.OAuthCredential{Type: ai.AuthTypeOAuth, OAuthCredentials: ai.OAuthCredentials{Access: "a", Refresh: "r", Expires: exp}}, nil
					},
					Refresh: func(_ context.Context, c *ai.OAuthCredential) (*ai.OAuthCredential, error) { return c, nil },
					ToAuth: func(_ context.Context, c *ai.OAuthCredential) (ai.ModelAuth, error) {
						return ai.ModelAuth{ApiKey: &c.Access}, nil
					},
				},
			},
			GetModels: func() []*ai.Model {
				return []*ai.Model{{BaseModel: ai.BaseModel{Id: "m1", Name: "m1", Api: "openai-completions", Provider: ai.ProviderId(id), BaseUrl: "https://ex.com", Input: []ai.ModelModality{ai.ModelModalityText}}}}
			},
		}
	}
	if err := rt.RegisterNativeProvider(makeOAuth("sub-oauth", true)); err != nil {
		t.Fatal(err)
	}
	if err := rt.RegisterNativeProvider(makeOAuth("gen-oauth", false)); err != nil {
		t.Fatal(err)
	}
	// Also test legacy extension OAuth IsSubscription field.
	_, _ = creds.Modify(t.Context(), "ext-sub", func(ai.Credential) (ai.Credential, error) {
		return &ai.OAuthCredential{Type: ai.AuthTypeOAuth, OAuthCredentials: ai.OAuthCredentials{Access: "a", Refresh: "r", Expires: exp}}, nil
	}, nil)
	if err := rt.RegisterProvider("ext-sub", ProviderConfigInput{
		BaseUrl: tmraoStrPtr("https://ex.com/v1"),
		Api:     tmraoStrPtr("openai-completions"),
		Models:  []ProviderModelConfig{&ProviderChatModelConfig{ProviderModelConfigBase: ProviderModelConfigBase{Id: "m1", Name: "m1"}, ContextWindow: 8192, MaxTokens: 1024}},
		OAuth: &ExtensionOAuthConfig{
			Name:           "Ext Sub",
			IsSubscription: true,
			Login: func(context.Context, compat.OAuthLoginCallbacks) (ai.OAuthCredentials, error) {
				return ai.OAuthCredentials{Access: "a", Refresh: "r", Expires: exp}, nil
			},
			RefreshToken: func(_ context.Context, c ai.OAuthCredentials) (ai.OAuthCredentials, error) { return c, nil },
			GetApiKey:    func(c ai.OAuthCredentials) string { return c.Access },
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Refresh(t.Context(), nil); err != nil {
		t.Fatal(err)
	}

	if !rt.IsUsingOAuth("sub-oauth") || !rt.IsUsingSubscription("sub-oauth") {
		t.Fatalf("sub-oauth should report OAuth=true Subscription=true")
	}
	if !rt.IsUsingOAuth("gen-oauth") || rt.IsUsingSubscription("gen-oauth") {
		t.Fatalf("gen-oauth should report OAuth=true Subscription=false")
	}
	if !rt.IsUsingOAuth("ext-sub") || !rt.IsUsingSubscription("ext-sub") {
		t.Fatalf("ext-sub should report OAuth=true Subscription=true")
	}

	// Refresh honors caller cancellation.
	cancelCtx, cancel := context.WithCancel(t.Context())
	cancel()
	res, err := rt.Refresh(cancelCtx, nil)
	if err != nil {
		t.Fatalf("Refresh cancelled returned error instead of Aborted: %v", err)
	}
	if !res.Aborted {
		t.Fatal("expected Refresh(cancelledCtx) to return Aborted: true")
	}
}

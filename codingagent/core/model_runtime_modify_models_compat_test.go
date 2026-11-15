// Ported from packages/coding-agent/test/model-runtime-modify-models-compat.test.ts (pi v1.5.2).

package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/compat"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func tmrmmcStrPtr(s string) *string { return &s }
func tmrmmcBoolPtr(b bool) *bool    { return &b }

func TestModelRuntimeModifyModelsAndNativeCompat(t *testing.T) {
	t.Run("native provider merges models.json headers, overrides, and forwards deferred auth", func(t *testing.T) {
		t.Setenv("PI_OFFLINE", "1")
		dir := t.TempDir()
		modelsPath := filepath.Join(dir, "models.json")
		raw, _ := jsonx.MarshalIndent(jsonx.ObjectOf(
			"providers", jsonx.ObjectOf(
				"native-ext", jsonx.ObjectOf(
					"baseUrl", "https://native-proxy.example.com",
					"headers", jsonx.ObjectOf("x-native-file", "overlay"),
					"modelOverrides", jsonx.ObjectOf(
						"native-m1", jsonx.ObjectOf("name", "Overridden Native M1"),
					),
				),
			),
		), "  ")
		if err := os.WriteFile(modelsPath, raw, 0o600); err != nil {
			t.Fatal(err)
		}

		storePath := filepath.Join(dir, "models-store.json")
		rt, err := CreateModelRuntime(t.Context(), &CreateModelRuntimeOptions{
			Credentials:     ai.NewInMemoryCredentialStore(),
			ModelsPath:      &modelsPath,
			ModelsStorePath: &storePath,
			RefreshOnCreate: tmrmmcBoolPtr(true),
		})
		if err != nil {
			t.Fatalf("CreateModelRuntime: %v", err)
		}
		t.Cleanup(rt.Close)

		var fetchSeenKey *string
		var fetchSeenHdr ai.ProviderHeaders
		var cancelSeenKey *string
		var cancelCalled bool

		nativeModel := &ai.Model{
			BaseModel: ai.BaseModel{
				Id:       "native-m1",
				Name:     "Native M1",
				Api:      "openai-completions",
				Provider: "native-ext",
				BaseUrl:  "https://native-orig.example.com",
				Input:    []ai.ModelModality{ai.ModelModalityText},
			},
			ContextWindow: 128000,
			MaxTokens:     8192,
		}
		if err := rt.RegisterNativeProvider(&ai.Provider{
			Id:   "native-ext",
			Name: "Native Ext",
			Auth: ai.ProviderAuth{
				ApiKey: &ai.ApiKeyAuth{
					Name: "API key",
					Resolve: func(context.Context, ai.ApiKeyResolveInput) (*ai.AuthResult, error) {
						k := "native-key"
						s := "native"
						return &ai.AuthResult{Auth: ai.ModelAuth{ApiKey: &k}, Source: &s}, nil
					},
				},
			},
			GetModels: func() []*ai.Model { return []*ai.Model{nativeModel} },
			FetchDeferred: func(_ context.Context, m *ai.Model, _ *ai.DeferredHandle, opts *ai.DeferredFetchOptions) *ai.AssistantMessageEventStream {
				fetchSeenKey = opts.ApiKey
				fetchSeenHdr = opts.Headers
				es := ai.NewAssistantMessageEventStream()
				es.Push(&ai.AssistantMessageEventDone{
					Type:   "done",
					Reason: ai.StopReasonStop,
					Message: &ai.AssistantMessage{
						Role:       "assistant",
						Api:        ai.Api(m.Api),
						Provider:   m.Provider,
						Model:      m.Id,
						StopReason: ai.StopReasonStop,
						Content:    []ai.Content{&ai.TextContent{Type: "text", Text: "deferred-ok"}},
					},
				})
				return es
			},
			CancelDeferred: func(_ context.Context, _ *ai.Model, _ *ai.DeferredHandle, opts *ai.DeferredCancelOptions) error {
				cancelCalled = true
				cancelSeenKey = opts.ApiKey
				return nil
			},
		}); err != nil {
			t.Fatalf("RegisterNativeProvider: %v", err)
		}

		composed := rt.GetModel("native-ext", "native-m1")
		if composed == nil || composed.Name != "Overridden Native M1" || composed.BaseUrl != "https://native-proxy.example.com" {
			t.Fatalf("composed native model mismatch: %+v", composed)
		}

		handle := &ai.DeferredHandle{Provider: "native-ext", ModelId: "native-m1", Api: "openai-completions", Id: "h1"}
		fetched, err := rt.FetchDeferred(t.Context(), composed, handle, nil)
		if err != nil || fetched == nil {
			t.Fatalf("FetchDeferred: msg=%+v err=%v", fetched, err)
		}
		if fetchSeenKey == nil || *fetchSeenKey != "native-key" {
			t.Fatalf("expected native-key forwarded to FetchDeferred, got %v", fetchSeenKey)
		}
		if v, _ := fetchSeenHdr.Get("x-native-file"); v == nil || *v != "overlay" {
			t.Fatalf("expected x-native-file=overlay forwarded, got %v", v)
		}

		if err := rt.CancelDeferred(t.Context(), composed, handle, nil); err != nil || !cancelCalled || cancelSeenKey == nil || *cancelSeenKey != "native-key" {
			t.Fatalf("CancelDeferred mismatch: err=%v called=%v key=%v", err, cancelCalled, cancelSeenKey)
		}
	})

	t.Run("RefreshModels and legacy ExtensionOAuth.ModifyModels transform registered models across Refresh", func(t *testing.T) {
		t.Setenv("PI_OFFLINE", "1")
		dir := t.TempDir()
		modelsPath := filepath.Join(dir, "models.json")
		raw, _ := jsonx.MarshalIndent(jsonx.ObjectOf(
			"providers", jsonx.ObjectOf(
				"legacy-oauth-prov", jsonx.ObjectOf(
					"modelOverrides", jsonx.ObjectOf(
						"dyn-m1", jsonx.ObjectOf("name", "Static Override Wins"),
					),
				),
			),
		), "  ")
		if err := os.WriteFile(modelsPath, raw, 0o600); err != nil {
			t.Fatal(err)
		}

		creds := ai.NewInMemoryCredentialStore()
		_, _ = creds.Modify(t.Context(), "legacy-oauth-prov", func(ai.Credential) (ai.Credential, error) {
			return &ai.OAuthCredential{
				Type: ai.AuthTypeOAuth,
				OAuthCredentials: ai.OAuthCredentials{
					Access:  "oauth-mod-access",
					Refresh: "r",
					Expires: float64(time.Now().Add(time.Hour).UnixMilli()),
				},
			}, nil
		}, nil)

		storePath := filepath.Join(dir, "models-store.json")
		rt, err := CreateModelRuntime(t.Context(), &CreateModelRuntimeOptions{
			Credentials:     creds,
			ModelsPath:      &modelsPath,
			ModelsStorePath: &storePath,
			RefreshOnCreate: tmrmmcBoolPtr(true),
		})
		if err != nil {
			t.Fatalf("CreateModelRuntime: %v", err)
		}
		t.Cleanup(rt.Close)

		if err := rt.RegisterProvider("legacy-oauth-prov", ProviderConfigInput{
			BaseUrl: tmrmmcStrPtr("https://orig.example.com/v1"),
			Api:     tmrmmcStrPtr("openai-completions"),
			Models: []ProviderModelConfig{
				&ProviderChatModelConfig{ProviderModelConfigBase: ProviderModelConfigBase{Id: "dyn-m1", Name: "Orig 1"}, ContextWindow: 16000, MaxTokens: 2048},
			},
			RefreshModels: func(context.Context, *ai.RefreshModelsContext) ([]ProviderModelConfig, error) {
				return []ProviderModelConfig{
					&ProviderChatModelConfig{ProviderModelConfigBase: ProviderModelConfigBase{Id: "dyn-m1", Name: "Refreshed 1"}, ContextWindow: 32000, MaxTokens: 4096},
					&ProviderChatModelConfig{ProviderModelConfigBase: ProviderModelConfigBase{Id: "dyn-m2", Name: "Refreshed 2"}, ContextWindow: 32000, MaxTokens: 4096},
				}, nil
			},
			OAuth: &ExtensionOAuthConfig{
				Name: "Legacy OAuth",
				Login: func(context.Context, compat.OAuthLoginCallbacks) (ai.OAuthCredentials, error) {
					return ai.OAuthCredentials{Access: "oauth-mod-access", Refresh: "r", Expires: float64(time.Now().Add(time.Hour).UnixMilli())}, nil
				},
				RefreshToken: func(_ context.Context, c ai.OAuthCredentials) (ai.OAuthCredentials, error) { return c, nil },
				GetApiKey:    func(c ai.OAuthCredentials) string { return c.Access },
				ModifyModels: func(models []*ai.Model, cred ai.OAuthCredentials) []*ai.Model {
					out := make([]*ai.Model, len(models))
					for i, m := range models {
						cp := *m
						if cred.Access == "oauth-mod-access" {
							cp.BaseUrl = "https://oauth-modified.example.com/v1"
						}
						out[i] = &cp
					}
					return out
				},
			},
		}); err != nil {
			t.Fatalf("RegisterProvider: %v", err)
		}

		if _, err := rt.Refresh(t.Context(), nil); err != nil {
			t.Fatalf("Refresh: %v", err)
		}

		m1 := rt.GetModel("legacy-oauth-prov", "dyn-m1")
		m2 := rt.GetModel("legacy-oauth-prov", "dyn-m2")
		if m1 == nil || m2 == nil {
			t.Fatalf("expected dyn-m1 and dyn-m2 after RefreshModels, got m1=%v m2=%v", m1, m2)
		}
		if m1.BaseUrl != "https://oauth-modified.example.com/v1" || m2.BaseUrl != "https://oauth-modified.example.com/v1" {
			t.Fatalf("ModifyModels baseUrl not applied: m1=%q m2=%q", m1.BaseUrl, m2.BaseUrl)
		}
		if m1.Name != "Static Override Wins" {
			t.Fatalf("expected models.json override on top of RefreshModels/ModifyModels, got %q", m1.Name)
		}
	})
}

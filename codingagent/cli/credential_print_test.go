// Ported from packages/coding-agent/test/credential-print.test.ts (pi v1.5.2).

package cli

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/internal/js"
)

func tcpStrPtr(s string) *string { return &s }
func tcpBoolPtr(b bool) *bool    { return &b }

func tcpNewRuntime(t *testing.T, creds ai.CredentialStore) *core.ModelRuntime {
	t.Helper()
	t.Setenv("PI_OFFLINE", "1")
	for _, k := range []string{
		"ANTHROPIC_API_KEY", "ANTHROPIC_OAUTH_TOKEN", "ANTHROPIC_AUTH_TOKEN",
		"OPENAI_API_KEY", "OPENROUTER_API_KEY", "GOOGLE_API_KEY", "GEMINI_API_KEY",
	} {
		t.Setenv(k, "")
	}
	rt, err := core.CreateModelRuntime(t.Context(), &core.CreateModelRuntimeOptions{
		Credentials:       creds,
		ModelsPath:        tcpStrPtr(""),
		ModelsStore:       core.NewInMemoryCodingAgentModelsStore(),
		AllowModelNetwork: false,
		RefreshOnCreate:   tcpBoolPtr(false),
	})
	if err != nil {
		t.Fatalf("CreateModelRuntime: %v", err)
	}
	t.Cleanup(rt.Close)
	return rt
}

func tcpRegisterProvider(t *testing.T, rt *core.ModelRuntime, id, envVar, modelId string, refreshFn func(context.Context, *ai.OAuthCredential) (*ai.OAuthCredential, error)) {
	t.Helper()
	var auth ai.ProviderAuth
	if envVar != "" {
		auth.ApiKey = ai.EnvApiKeyAuth(id, []string{envVar})
	}
	if refreshFn != nil {
		auth.OAuth = &ai.OAuthAuth{
			Name:    id + " OAuth",
			Refresh: refreshFn,
			ToAuth: func(_ context.Context, cred *ai.OAuthCredential) (ai.ModelAuth, error) {
				return ai.ModelAuth{ApiKey: &cred.Access}, nil
			},
		}
	}
	err := rt.RegisterNativeProvider(&ai.Provider{
		Id:   id,
		Name: id,
		Auth: auth,
		GetModels: func() []*ai.Model {
			return []*ai.Model{
				{
					BaseModel: ai.BaseModel{
						Id:       modelId,
						Name:     modelId,
						Api:      "openai-completions",
						Provider: ai.ProviderId(id),
						BaseUrl:  "https://api.example.com/v1",
						Input:    []ai.ModelModality{ai.ModelModalityText},
					},
					ContextWindow: 128000,
					MaxTokens:     4096,
				},
			}
		},
		Stream: func(context.Context, *ai.Model, *ai.TranscriptContext, ai.ProviderStreamOptions) *ai.AssistantMessageEventStream {
			panic("not used")
		},
		StreamSimple: func(context.Context, *ai.Model, *ai.TranscriptContext, *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
			panic("not used")
		},
	})
	if err != nil {
		t.Fatalf("RegisterNativeProvider(%s): %v", id, err)
	}
}

func TestResolveCredentialForPrintApiKeyAndBearerToken(t *testing.T) {
	creds := ai.NewInMemoryCredentialStore()
	rt := tcpNewRuntime(t, creds)

	tcpRegisterProvider(t, rt, "p-apikey", "TCP_TEST_KEY_ONE", "shared-model", nil)
	_, _ = creds.Modify(t.Context(), "p-apikey", func(ai.Credential) (ai.Credential, error) {
		return &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Key: tcpStrPtr("sk-stored-1")}, nil
	}, nil)

	var refreshCount int32
	tcpRegisterProvider(t, rt, "p-oauth", "", "oauth-only-model", func(_ context.Context, c *ai.OAuthCredential) (*ai.OAuthCredential, error) {
		atomic.AddInt32(&refreshCount, 1)
		return &ai.OAuthCredential{
			Type: ai.AuthTypeOAuth,
			OAuthCredentials: ai.OAuthCredentials{
				Refresh: "new-ref",
				Access:  "refreshed-bearer-tok",
				Expires: float64(js.DateNow() + 3_600_000),
			},
		}, nil
	})
	// Store OAuth credential expiring in 10 minutes (< default 30m threshold).
	_, _ = creds.Modify(t.Context(), "p-oauth", func(ai.Credential) (ai.Credential, error) {
		return &ai.OAuthCredential{
			Type: ai.AuthTypeOAuth,
			OAuthCredentials: ai.OAuthCredentials{
				Refresh: "old-ref",
				Access:  "short-lived-tok",
				Expires: float64(js.DateNow() + 10*60_000),
			},
		}, nil
	}, nil)

	// 1. print-api-key by --provider succeeds for API key provider.
	got, err := ResolveCredentialForPrint(t.Context(), core.Args{Provider: tcpStrPtr("p-apikey")}, rt, AuthCommandKindApiKey, nil)
	if err != nil || got != "sk-stored-1" {
		t.Fatalf("print-api-key by provider = (%q, %v)", got, err)
	}

	// 2. print-api-key by --provider rejects OAuth-configured provider cleanly.
	if _, err := ResolveCredentialForPrint(t.Context(), core.Args{Provider: tcpStrPtr("p-oauth")}, rt, AuthCommandKindApiKey, nil); err == nil || !strings.Contains(err.Error(), "configured with OAuth") {
		t.Fatalf("expected OAuth error for print-api-key, got %v", err)
	}

	// 3. print-bearer-token rejects non-OAuth provider cleanly.
	if _, err := ResolveCredentialForPrint(t.Context(), core.Args{Provider: tcpStrPtr("p-apikey")}, rt, AuthCommandKindBearerToken, nil); err == nil || !strings.Contains(err.Error(), "not configured with an OAuth bearer token") {
		t.Fatalf("expected non-OAuth error for print-bearer-token, got %v", err)
	}

	// 4. print-bearer-token with short min-expiry (1m) returns existing token without refreshing.
	oneMin := int64(60_000)
	got, err = ResolveCredentialForPrint(t.Context(), core.Args{Model: tcpStrPtr("oauth-only-model")}, rt, AuthCommandKindBearerToken, &oneMin)
	if err != nil || got != "short-lived-tok" || atomic.LoadInt32(&refreshCount) != 0 {
		t.Fatalf("print-bearer-token with 1m minExpiry = (%q, %v, refresh=%d)", got, err, refreshCount)
	}

	// 5. print-bearer-token with default 30m min-expiry triggers OAuth refresh.
	got, err = ResolveCredentialForPrint(t.Context(), core.Args{Provider: tcpStrPtr("p-oauth")}, rt, AuthCommandKindBearerToken, nil)
	if err != nil || got != "refreshed-bearer-tok" || atomic.LoadInt32(&refreshCount) != 1 {
		t.Fatalf("print-bearer-token refresh = (%q, %v, refresh=%d)", got, err, refreshCount)
	}

	// 6. Ambiguous --model across two configured API key providers fails unless --provider is given.
	tcpRegisterProvider(t, rt, "p-apikey-2", "TCP_TEST_KEY_TWO", "shared-model", nil)
	_, _ = creds.Modify(t.Context(), "p-apikey-2", func(ai.Credential) (ai.Credential, error) {
		return &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Key: tcpStrPtr("sk-stored-2")}, nil
	}, nil)
	if _, err := ResolveCredentialForPrint(t.Context(), core.Args{Model: tcpStrPtr("shared-model")}, rt, AuthCommandKindApiKey, nil); err == nil || !strings.Contains(err.Error(), "Multiple configured providers matched") {
		t.Fatalf("expected ambiguous provider error, got %v", err)
	}

	// 7. Unknown provider reports clear message.
	if _, err := ResolveCredentialForPrint(t.Context(), core.Args{Provider: tcpStrPtr("no-such-prov")}, rt, AuthCommandKindApiKey, nil); err == nil || !strings.Contains(err.Error(), `Unknown provider "no-such-prov"`) {
		t.Fatalf("expected unknown provider error, got %v", err)
	}
}

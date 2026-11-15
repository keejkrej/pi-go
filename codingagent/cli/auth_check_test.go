// Ported from packages/coding-agent/test/auth-check.test.ts (pi v1.5.2).

package cli

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func tachkStrPtr(s string) *string { return &s }
func tachkBoolPtr(b bool) *bool    { return &b }

func tachkNewTestRuntime(t *testing.T, creds ai.CredentialStore) *core.ModelRuntime {
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
		ModelsPath:        tachkStrPtr(""),
		ModelsStore:       core.NewInMemoryCodingAgentModelsStore(),
		AllowModelNetwork: false,
		RefreshOnCreate:   tachkBoolPtr(false),
	})
	if err != nil {
		t.Fatalf("CreateModelRuntime: %v", err)
	}
	t.Cleanup(rt.Close)
	return rt
}

func tachkRegisterOAuthProvider(t *testing.T, rt *core.ModelRuntime, id string, refreshFn func(context.Context, *ai.OAuthCredential) (*ai.OAuthCredential, error)) {
	t.Helper()
	if err := rt.RegisterNativeProvider(&ai.Provider{
		Id:   id,
		Name: "Provider " + id,
		Auth: ai.ProviderAuth{
			OAuth: &ai.OAuthAuth{
				Name:    "OAuth",
				Refresh: refreshFn,
				ToAuth: func(_ context.Context, cred *ai.OAuthCredential) (ai.ModelAuth, error) {
					return ai.ModelAuth{ApiKey: &cred.Access}, nil
				},
			},
		},
		GetModels: func() []*ai.Model {
			return []*ai.Model{
				{
					BaseModel: ai.BaseModel{
						Id:       id + "-model",
						Name:     id + "-model",
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
	}); err != nil {
		t.Fatalf("RegisterNativeProvider: %v", err)
	}
}

func TestCheckProviderAuthStatusesAndJsonTags(t *testing.T) {
	creds := ai.NewInMemoryCredentialStore()
	rt := tachkNewTestRuntime(t, creds)

	// Unknown provider -> not_ready (provider_not_found).
	res, err := CheckProviderAuth(t.Context(), core.Args{Provider: tachkStrPtr("unknown-prov")}, rt)
	if err != nil {
		t.Fatalf("CheckProviderAuth(unknown): %v", err)
	}
	if res.Status != AuthCheckStatusNotReady || res.Reason == nil || *res.Reason != AuthCheckReasonProviderNotFound {
		t.Fatalf("unexpected unknown provider result: %+v", res)
	}

	var refreshCalls int32
	tachkRegisterOAuthProvider(t, rt, "oauth-test", func(_ context.Context, c *ai.OAuthCredential) (*ai.OAuthCredential, error) {
		atomic.AddInt32(&refreshCalls, 1)
		if c.Refresh == "broken-refresh" {
			return nil, errors.New("refresh rejected")
		}
		return &ai.OAuthCredential{
			Type: ai.AuthTypeOAuth,
			OAuthCredentials: ai.OAuthCredentials{
				Refresh: "fresh-refresh",
				Access:  "fresh-access",
				Expires: float64(js.DateNow() + 3_600_000),
			},
		}, nil
	})

	// Unconfigured provider -> not_ready (credentials_not_configured).
	res, err = CheckProviderAuth(t.Context(), core.Args{Provider: tachkStrPtr("oauth-test")}, rt)
	if err != nil {
		t.Fatalf("CheckProviderAuth(unconfigured): %v", err)
	}
	if res.Status != AuthCheckStatusNotReady || res.Reason == nil || *res.Reason != AuthCheckReasonCredentialsNotConfigured {
		t.Fatalf("unexpected unconfigured result: %+v", res)
	}

	// Store expired OAuth credential. With Refresh=false it stays ready and does not refresh.
	_, _ = creds.Modify(t.Context(), "oauth-test", func(ai.Credential) (ai.Credential, error) {
		return &ai.OAuthCredential{
			Type: ai.AuthTypeOAuth,
			OAuthCredentials: ai.OAuthCredentials{
				Refresh: "good-refresh",
				Access:  "stored-expired-access",
				Expires: float64(js.DateNow() - 10_000),
			},
		}, nil
	}, nil)

	res, err = CheckProviderAuth(t.Context(), core.Args{Model: tachkStrPtr("oauth-test/oauth-test-model")}, rt, &AuthCheckOptions{Refresh: false})
	if err != nil {
		t.Fatalf("CheckProviderAuth(no-refresh): %v", err)
	}
	if res.Status != AuthCheckStatusReady || res.Provider != "oauth-test" || res.AuthType == nil || *res.AuthType != ai.AuthTypeOAuth {
		t.Fatalf("unexpected no-refresh result: %+v", res)
	}
	if atomic.LoadInt32(&refreshCalls) != 0 {
		t.Fatalf("expected 0 refreshes with Refresh=false, got %d", refreshCalls)
	}

	// Ready JSON omits reason via ,omitzero.
	raw, err := jsonx.Marshal(res)
	if err != nil {
		t.Fatalf("jsonx.Marshal(AuthCheckResult): %v", err)
	}
	if string(raw) != `{"status":"ready","provider":"oauth-test","authType":"oauth"}` {
		t.Fatalf("unexpected JSON serialization: %s", raw)
	}

	// GetProviderCredential with Refresh=false returns stored access token verbatim.
	gotToken, err := GetProviderCredential(t.Context(), "oauth-test", rt, creds, AuthCheckOptions{Refresh: false})
	if err != nil || gotToken == nil || *gotToken != "stored-expired-access" {
		t.Fatalf("GetProviderCredential(no-refresh) = (%v, %v)", gotToken, err)
	}

	// Refresh=true refreshes the expired token and stays ready.
	res, err = CheckProviderAuth(t.Context(), core.Args{Provider: tachkStrPtr("oauth-test")}, rt, &AuthCheckOptions{Refresh: true})
	if err != nil || res.Status != AuthCheckStatusReady {
		t.Fatalf("CheckProviderAuth(refresh): (%+v, %v)", res, err)
	}
	if atomic.LoadInt32(&refreshCalls) != 1 {
		t.Fatalf("expected 1 refresh call, got %d", refreshCalls)
	}
	gotFresh, err := GetProviderCredential(t.Context(), "oauth-test", rt, creds, AuthCheckOptions{Refresh: true})
	if err != nil || gotFresh == nil || *gotFresh != "fresh-access" {
		t.Fatalf("GetProviderCredential(refresh) = (%v, %v)", gotFresh, err)
	}

	// Broken expired OAuth credential with Refresh=true -> invalid (invalid_state).
	_, _ = creds.Modify(t.Context(), "oauth-test", func(ai.Credential) (ai.Credential, error) {
		return &ai.OAuthCredential{
			Type: ai.AuthTypeOAuth,
			OAuthCredentials: ai.OAuthCredentials{
				Refresh: "broken-refresh",
				Access:  "stale-access",
				Expires: float64(js.DateNow() - 10_000),
			},
		}, nil
	}, nil)
	res, err = CheckProviderAuth(t.Context(), core.Args{Provider: tachkStrPtr("oauth-test")}, rt, &AuthCheckOptions{Refresh: true})
	if err != nil {
		t.Fatalf("CheckProviderAuth(broken): %v", err)
	}
	if res.Status != AuthCheckStatusInvalid || res.Reason == nil || *res.Reason != AuthCheckReasonInvalidState {
		t.Fatalf("expected invalid/invalid_state, got %+v", res)
	}
}

func TestCreateAuthCheckModelRuntime(t *testing.T) {
	creds := ai.NewInMemoryCredentialStore()
	rt, err := CreateAuthCheckModelRuntime(t.Context(), creds)
	if err != nil {
		t.Fatalf("CreateAuthCheckModelRuntime: %v", err)
	}
	defer rt.Close()
	if len(rt.GetProviders()) == 0 {
		t.Fatal("expected built-in providers in auth check runtime")
	}
}

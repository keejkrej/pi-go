package oauth

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/js"
)

// Note: ai/oauth is layer L3 and never imports ai/providers or ai/compat.
// Provider-specific lazy OAuth tests against built-in providers live in ai/providers.

type toaRoundTripFunc func(*http.Request) (*http.Response, error)

func (f toaRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type toaInteraction struct {
	handler func(context.Context, ai.AuthPrompt) (string, error)
}

func (i *toaInteraction) Notify(_ ai.AuthEvent) {}

func (i *toaInteraction) Prompt(ctx context.Context, prompt ai.AuthPrompt) (string, error) {
	if i.handler != nil {
		return i.handler(ctx, prompt)
	}
	return "", errors.New("unexpected prompt")
}

func toaResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestOAuthAuth_LazyOAuthDefersLoadingUntilAuthOperation(t *testing.T) {
	t.Cleanup(func() {
		RegisterBundledOAuthFlowLoaders(loadDefaultFlowLoaders())
	})

	var loads atomic.Int32
	lazyAuth := ai.LazyOAuth(ai.LazyOAuthInput{
		Name:           "Anthropic (Claude Pro/Max)",
		IsSubscription: true,
		Load: func() (*ai.OAuthAuth, error) {
			loads.Add(1)
			return LoadAnthropicOAuth()
		},
	})

	store := ai.NewInMemoryCredentialStore()
	models := ai.CreateModels(&ai.CreateModelsOptions{Credentials: store})
	anthName := "Anthropic Lazy"
	dummyStream := func(context.Context, *ai.Model, *ai.TranscriptContext, ai.ProviderStreamOptions) *ai.AssistantMessageEventStream {
		return ai.NewAssistantMessageEventStream()
	}
	provider, err := ai.CreateProvider(&ai.CreateProviderOptions{
		Id:   "anthropic-lazy",
		Name: &anthName,
		Auth: ai.ProviderAuth{OAuth: lazyAuth},
		Api:  ai.ProviderStreams{Stream: dummyStream},
		Models: []ai.AnyModel{
			&ai.Model{
				BaseModel: ai.BaseModel{
					Id:       "claude-test",
					Name:     "Claude Test",
					Api:      "anthropic-messages",
					Provider: "anthropic-lazy",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateProvider failed: %v", err)
	}
	models.SetProvider(provider)

	// Registration and static model listing do not trigger the OAuth flow loader.
	if len(models.GetModels(nil)) != 1 {
		t.Fatalf("expected 1 registered model")
	}
	if loads.Load() != 0 {
		t.Fatalf("loader invoked %d times before auth operation, want 0", loads.Load())
	}

	// Unauthenticated GetAvailable checks credential presence without loading the flow.
	avail, err := models.GetAvailable(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("GetAvailable failed: %v", err)
	}
	if len(avail) != 0 {
		t.Fatalf("expected 0 available models when unauthenticated, got %d", len(avail))
	}
	if loads.Load() != 0 {
		t.Fatalf("loader invoked %d times when unauthenticated, want 0", loads.Load())
	}

	// Logging in triggers loader once and persists credential in store.
	client := &http.Client{
		Transport: toaRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.String() != anthTokenUrl {
				return nil, errors.New("unexpected url")
			}
			return toaResponse(http.StatusOK, `{"access_token":"lazy-access-1","refresh_token":"lazy-refresh-1","expires_in":3600}`), nil
		}),
	}
	var lastAuthUrl string
	interaction := &toaInteraction{}
	interaction.handler = func(_ context.Context, prompt ai.AuthPrompt) (string, error) {
		switch p := prompt.(type) {
		case *ai.AuthPromptSelect:
			return "copy_code", nil
		case *ai.AuthPromptManualCode:
			// Use a valid code format; state mismatch doesn't trigger unless state present, so pass bare code.
			_ = p
			_ = lastAuthUrl
			return "auth-code-1", nil
		default:
			return "", errors.New("unexpected prompt")
		}
	}

	ctx := LoadWithHttpClient(context.Background(), client)
	cred, err := models.Login(ctx, "anthropic-lazy", ai.AuthTypeOAuth, interaction, nil)
	if err != nil {
		t.Fatalf("models.Login failed: %v", err)
	}
	if loads.Load() != 1 {
		t.Fatalf("loader invoked %d times after Login, want 1", loads.Load())
	}
	oauthCred, ok := cred.(*ai.OAuthCredential)
	if !ok || oauthCred.Access != "lazy-access-1" {
		t.Fatalf("unexpected stored credential: %+v", cred)
	}

	resolved, err := models.GetAuth(context.Background(), "anthropic-lazy", nil)
	if err != nil {
		t.Fatalf("models.GetAuth failed: %v", err)
	}
	if resolved == nil || resolved.Auth.ApiKey == nil || *resolved.Auth.ApiKey != "lazy-access-1" {
		t.Fatalf("unexpected GetAuth result: %+v", resolved)
	}
	// Cached loader should not be invoked again.
	if loads.Load() != 1 {
		t.Fatalf("loader invoked %d times after GetAuth, want cached 1", loads.Load())
	}
}

func TestOAuthAuth_RefreshesExpiredStoredCredentialViaModelsGetAuth(t *testing.T) {
	var seenRefresh atomic.Pointer[string]
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(raw))
		v := form.Get("refresh_token")
		seenRefresh.Store(&v)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"header-fresh","refresh_token":"header-rot","expires_in":3600}`)
	}))
	defer srv.Close()
	t.Setenv("KIMI_CODE_OAUTH_HOST", srv.URL)

	store := ai.NewInMemoryCredentialStore()
	if _, err := store.Modify(context.Background(), "kimi-coding", func(_ ai.Credential) (ai.Credential, error) {
		return &ai.OAuthCredential{
			Type: ai.AuthTypeOAuth,
			OAuthCredentials: ai.OAuthCredentials{
				Refresh: "stale-refresh",
				Access:  "stale-access",
				Expires: float64(js.DateNow()) - 60_000,
			},
		}, nil
	}, nil); err != nil {
		t.Fatalf("store.Modify failed: %v", err)
	}

	ai.RegisterLazyOAuthFlows()
	models := ai.CreateModels(&ai.CreateModelsOptions{Credentials: store})
	kimiName := "Kimi Coding"
	dummyStream := func(context.Context, *ai.Model, *ai.TranscriptContext, ai.ProviderStreamOptions) *ai.AssistantMessageEventStream {
		return ai.NewAssistantMessageEventStream()
	}
	provider, err := ai.CreateProvider(&ai.CreateProviderOptions{
		Id:   "kimi-coding",
		Name: &kimiName,
		Api:  ai.ProviderStreams{Stream: dummyStream},
		Auth: ai.ProviderAuth{
			OAuth: ai.LazyOAuth(ai.LazyOAuthInput{
				Name: "Kimi Coding",
				Load: LoadKimiCodingOAuth,
			}),
		},
		Models: []ai.AnyModel{
			&ai.Model{
				BaseModel: ai.BaseModel{
					Id:       "kimi-k2",
					Name:     "Kimi K2",
					Api:      "anthropic-messages",
					Provider: "kimi-coding",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateProvider failed: %v", err)
	}
	models.SetProvider(provider)

	res, err := models.GetAuth(context.Background(), "kimi-coding", nil)
	if err != nil {
		t.Fatalf("models.GetAuth failed: %v", err)
	}
	if got := seenRefresh.Load(); got == nil || *got != "stale-refresh" {
		t.Fatalf("refresh_token = %v, want stale-refresh", got)
	}
	if res == nil || res.Auth.Headers == nil {
		t.Fatalf("expected header auth from Kimi ToAuth, got %+v", res)
	}
	authHeader, ok := res.Auth.Headers.Get("Authorization")
	if !ok || authHeader == nil || *authHeader != "Bearer header-fresh" {
		t.Fatalf("Authorization header = %v (ok=%v), want Bearer header-fresh", authHeader, ok)
	}

	stored, err := store.Read(context.Background(), "kimi-coding", nil)
	if err != nil {
		t.Fatalf("store.Read failed: %v", err)
	}
	storedOAuth, ok := stored.(*ai.OAuthCredential)
	if !ok || storedOAuth.Access != "header-fresh" || storedOAuth.Refresh != "header-rot" {
		t.Fatalf("unexpected persisted credential after refresh: %+v", stored)
	}
}

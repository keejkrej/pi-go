package oauth

import (
	"context"
	"net/http"
	"testing"

	"github.com/keejkrej/pi-go/ai"
)

func TestLoad_BundledOAuthFlowLoadersReturnStaticFlows(t *testing.T) {
	t.Cleanup(func() {
		RegisterBundledOAuthFlowLoaders(loadDefaultFlowLoaders())
	})
	ai.RegisterLazyOAuthFlows()

	checks := []struct {
		name string
		load func() (*ai.OAuthAuth, error)
		want *ai.OAuthAuth
	}{
		{"Anthropic", LoadAnthropicOAuth, AnthropicOAuth},
		{"OpenAI Codex", LoadOpenAICodexOAuth, OpenaiCodexOAuth},
		{"OpenAI ChatGPT", LoadOpenAIChatGPTOAuth, OpenaiChatGPTOAuth},
		{"GitHub Copilot", LoadGitHubCopilotOAuth, GithubCopilotOAuth},
		{"OpenRouter", LoadOpenRouterOAuth, OpenRouterOAuth},
		{"Kimi Coding", LoadKimiCodingOAuth, KimiCodingOAuth},
		{"Meta", LoadMetaOAuth, MetaOAuth},
		{"xAI", LoadXaiOAuth, XaiOAuth},
	}

	for _, tc := range checks {
		got, err := tc.load()
		if err != nil {
			t.Fatalf("%s loader returned error: %v", tc.name, err)
		}
		if got != tc.want {
			t.Fatalf("%s loader returned %p, want %p", tc.name, got, tc.want)
		}
	}

	rad, err := LoadRadiusOAuth(RadiusOAuthOptions{Name: "Rad", Gateway: "https://rad.example"})
	if err != nil {
		t.Fatalf("LoadRadiusOAuth failed: %v", err)
	}
	if rad == nil || rad.Name != "Rad" {
		t.Fatalf("unexpected LoadRadiusOAuth result: %+v", rad)
	}
}

func TestLoad_RegisterBundledOAuthFlowLoadersOverrideAndRestore(t *testing.T) {
	t.Cleanup(func() {
		RegisterBundledOAuthFlowLoaders(loadDefaultFlowLoaders())
	})

	customAnthropic := &ai.OAuthAuth{Name: "Custom Anthropic Override"}
	RegisterBundledOAuthFlowLoaders(&OAuthFlowLoaders{
		Anthropic: func() (*ai.OAuthAuth, error) {
			return customAnthropic, nil
		},
		Radius: func(options RadiusOAuthOptions) (*ai.OAuthAuth, error) {
			return &ai.OAuthAuth{Name: "Override: " + options.Name}, nil
		},
	})

	gotAnth, err := LoadAnthropicOAuth()
	if err != nil || gotAnth != customAnthropic {
		t.Fatalf("override LoadAnthropicOAuth() = (%v, %v), want %v", gotAnth, err, customAnthropic)
	}
	// Unspecified override slots fall back cleanly to static flows.
	gotXai, err := LoadXaiOAuth()
	if err != nil || gotXai != XaiOAuth {
		t.Fatalf("fallback LoadXaiOAuth() = (%v, %v), want %v", gotXai, err, XaiOAuth)
	}
	gotRad, err := LoadRadiusOAuth(RadiusOAuthOptions{Name: "GW", Gateway: "https://gw.example"})
	if err != nil || gotRad.Name != "Override: GW" {
		t.Fatalf("override LoadRadiusOAuth() = (%v, %v)", gotRad, err)
	}

	RegisterBundledOAuthFlowLoaders(nil)
	gotAfterNil, err := LoadAnthropicOAuth()
	if err != nil || gotAfterNil != AnthropicOAuth {
		t.Fatalf("after nil LoadAnthropicOAuth() = (%v, %v), want %v", gotAfterNil, err, AnthropicOAuth)
	}
}

func TestLoad_LoadWithHttpClientInjectsAndFallsBack(t *testing.T) {
	custom := &http.Client{}
	ctx := LoadWithHttpClient(context.Background(), custom)
	if got := tuHttpClient(ctx); got != custom {
		t.Fatalf("tuHttpClient(ctx) = %p, want %p", got, custom)
	}

	// Nil client falls back to default proxy client.
	fallbackCtx := LoadWithHttpClient(context.Background(), nil)
	if got := tuHttpClient(fallbackCtx); got == nil {
		t.Fatal("expected non-nil default client for nil override")
	}
}

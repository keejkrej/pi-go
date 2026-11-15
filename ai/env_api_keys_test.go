package ai_test

import (
	"slices"
	"testing"

	"github.com/keejkrej/pi-go/ai"
)

func TestEnvApiKeys(t *testing.T) {
	t.Run("does not treat generic GitHub tokens as GitHub Copilot credentials", func(t *testing.T) {
		t.Setenv("COPILOT_GITHUB_TOKEN", "")
		t.Setenv("GH_TOKEN", "gh-token")
		t.Setenv("GITHUB_TOKEN", "github-token")

		if keys := ai.FindEnvKeys(string(ai.KnownProviderGithubCopilot), nil); len(keys) != 0 {
			t.Fatalf("expected no keys, got %v", keys)
		}
		if key := ai.GetEnvApiKey(string(ai.KnownProviderGithubCopilot), nil); key != nil {
			t.Fatalf("expected nil key, got %q", *key)
		}
	})

	t.Run("resolves GitHub Copilot credentials from COPILOT_GITHUB_TOKEN", func(t *testing.T) {
		t.Setenv("COPILOT_GITHUB_TOKEN", "copilot-token")
		t.Setenv("GH_TOKEN", "gh-token")
		t.Setenv("GITHUB_TOKEN", "github-token")

		keys := ai.FindEnvKeys(string(ai.KnownProviderGithubCopilot), nil)
		if !slices.Equal(keys, []string{"COPILOT_GITHUB_TOKEN"}) {
			t.Fatalf("got %v, want [COPILOT_GITHUB_TOKEN]", keys)
		}
		key := ai.GetEnvApiKey(string(ai.KnownProviderGithubCopilot), nil)
		if key == nil || *key != "copilot-token" {
			t.Fatalf("got %v, want copilot-token", key)
		}
	})

	t.Run("resolves ZAI China Coding Plan credentials from ZAI_CODING_CN_API_KEY", func(t *testing.T) {
		t.Setenv("ZAI_CODING_CN_API_KEY", "zai-coding-cn-token")

		keys := ai.FindEnvKeys("zai-coding-cn", nil)
		if !slices.Equal(keys, []string{"ZAI_CODING_CN_API_KEY"}) {
			t.Fatalf("got %v, want [ZAI_CODING_CN_API_KEY]", keys)
		}
		key := ai.GetEnvApiKey("zai-coding-cn", nil)
		if key == nil || *key != "zai-coding-cn-token" {
			t.Fatalf("got %v, want zai-coding-cn-token", key)
		}
	})

	t.Run("reports ANTHROPIC_AUTH_TOKEN but preserves OAuth token API key lookup", func(t *testing.T) {
		t.Setenv("ANTHROPIC_AUTH_TOKEN", "auth-token")
		t.Setenv("ANTHROPIC_OAUTH_TOKEN", "oauth-token")
		t.Setenv("ANTHROPIC_API_KEY", "api-key")

		keys := ai.FindEnvKeys(string(ai.KnownProviderAnthropic), nil)
		want := []string{"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_OAUTH_TOKEN", "ANTHROPIC_API_KEY"}
		if !slices.Equal(keys, want) {
			t.Fatalf("got %v, want %v", keys, want)
		}
		key := ai.GetEnvApiKey(string(ai.KnownProviderAnthropic), nil)
		if key == nil || *key != "oauth-token" {
			t.Fatalf("got %v, want oauth-token", key)
		}
	})

	t.Run("does not return ANTHROPIC_AUTH_TOKEN as an API key", func(t *testing.T) {
		t.Setenv("ANTHROPIC_AUTH_TOKEN", "auth-token")
		t.Setenv("ANTHROPIC_OAUTH_TOKEN", "")
		t.Setenv("ANTHROPIC_API_KEY", "")

		keys := ai.FindEnvKeys(string(ai.KnownProviderAnthropic), nil)
		if !slices.Equal(keys, []string{"ANTHROPIC_AUTH_TOKEN"}) {
			t.Fatalf("got %v, want [ANTHROPIC_AUTH_TOKEN]", keys)
		}
		if key := ai.GetEnvApiKey(string(ai.KnownProviderAnthropic), nil); key != nil {
			t.Fatalf("expected nil key, got %q", *key)
		}
	})

	t.Run("preserves ANTHROPIC_OAUTH_TOKEN as an API key", func(t *testing.T) {
		t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
		t.Setenv("ANTHROPIC_OAUTH_TOKEN", "oauth-token")
		t.Setenv("ANTHROPIC_API_KEY", "")

		keys := ai.FindEnvKeys(string(ai.KnownProviderAnthropic), nil)
		if !slices.Equal(keys, []string{"ANTHROPIC_OAUTH_TOKEN"}) {
			t.Fatalf("got %v, want [ANTHROPIC_OAUTH_TOKEN]", keys)
		}
		key := ai.GetEnvApiKey(string(ai.KnownProviderAnthropic), nil)
		if key == nil || *key != "oauth-token" {
			t.Fatalf("got %v, want oauth-token", key)
		}
	})

	t.Run("falls back to ANTHROPIC_API_KEY for API key lookup", func(t *testing.T) {
		t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
		t.Setenv("ANTHROPIC_OAUTH_TOKEN", "")
		t.Setenv("ANTHROPIC_API_KEY", "api-key")

		key := ai.GetEnvApiKey(string(ai.KnownProviderAnthropic), nil)
		if key == nil || *key != "api-key" {
			t.Fatalf("got %v, want api-key", key)
		}
	})
}

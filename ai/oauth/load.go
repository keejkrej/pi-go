// Ported from packages/ai/src/auth/oauth/load.ts (pi v1.5.2).

package oauth

import (
	"context"
	"net/http"
	"sync"

	"github.com/keejkrej/pi-go/ai"
)

// OAuthFlowLoaders supplies overrides for bundled OAuth flow implementations.
type OAuthFlowLoaders struct {
	Anthropic     func() (*ai.OAuthAuth, error)
	OpenaiCodex   func() (*ai.OAuthAuth, error)
	OpenaiChatGPT func() (*ai.OAuthAuth, error)
	GithubCopilot func() (*ai.OAuthAuth, error)
	Openrouter    func() (*ai.OAuthAuth, error)
	KimiCoding    func() (*ai.OAuthAuth, error)
	Meta          func() (*ai.OAuthAuth, error)
	Xai           func() (*ai.OAuthAuth, error)
	Radius        func(options RadiusOAuthOptions) (*ai.OAuthAuth, error)
}

var (
	loadMu                sync.RWMutex
	loadRegisteredLoaders *OAuthFlowLoaders
)

func init() {
	ai.SetLazyOAuthFlowsHook(func() {
		RegisterBundledOAuthFlowLoaders(loadDefaultFlowLoaders())
	})
}

func loadDefaultFlowLoaders() *OAuthFlowLoaders {
	return &OAuthFlowLoaders{
		Anthropic:     func() (*ai.OAuthAuth, error) { return AnthropicOAuth, nil },
		OpenaiCodex:   func() (*ai.OAuthAuth, error) { return OpenaiCodexOAuth, nil },
		OpenaiChatGPT: func() (*ai.OAuthAuth, error) { return OpenaiChatGPTOAuth, nil },
		GithubCopilot: func() (*ai.OAuthAuth, error) { return GithubCopilotOAuth, nil },
		Openrouter:    func() (*ai.OAuthAuth, error) { return OpenRouterOAuth, nil },
		KimiCoding:    func() (*ai.OAuthAuth, error) { return KimiCodingOAuth, nil },
		Meta:          func() (*ai.OAuthAuth, error) { return MetaOAuth, nil },
		Xai:           func() (*ai.OAuthAuth, error) { return XaiOAuth, nil },
		Radius:        func(options RadiusOAuthOptions) (*ai.OAuthAuth, error) { return CreateRadiusOAuth(options), nil },
	}
}

// RegisterBundledOAuthFlowLoaders registers statically bundled OAuth flows.
func RegisterBundledOAuthFlowLoaders(loaders *OAuthFlowLoaders) {
	loadMu.Lock()
	defer loadMu.Unlock()
	loadRegisteredLoaders = loaders
}

func loadSnapshot() *OAuthFlowLoaders {
	loadMu.RLock()
	defer loadMu.RUnlock()
	return loadRegisteredLoaders
}

// LoadWithHttpClient returns a derived context whose OAuth HTTP requests use client instead of ai.NodeHttpProxyDefaultClient.
func LoadWithHttpClient(ctx context.Context, client *http.Client) context.Context {
	return tuWithHttpClient(ctx, client)
}

// LoadAnthropicOAuth returns the Anthropic OAuth flow.
func LoadAnthropicOAuth() (*ai.OAuthAuth, error) {
	if loaders := loadSnapshot(); loaders != nil && loaders.Anthropic != nil {
		return loaders.Anthropic()
	}
	return AnthropicOAuth, nil
}

// LoadOpenAICodexOAuth returns the OpenAI Codex OAuth flow.
func LoadOpenAICodexOAuth() (*ai.OAuthAuth, error) {
	if loaders := loadSnapshot(); loaders != nil && loaders.OpenaiCodex != nil {
		return loaders.OpenaiCodex()
	}
	return OpenaiCodexOAuth, nil
}

// LoadOpenAIChatGPTOAuth returns the OpenAI ChatGPT OAuth flow.
func LoadOpenAIChatGPTOAuth() (*ai.OAuthAuth, error) {
	if loaders := loadSnapshot(); loaders != nil && loaders.OpenaiChatGPT != nil {
		return loaders.OpenaiChatGPT()
	}
	return OpenaiChatGPTOAuth, nil
}

// LoadGitHubCopilotOAuth returns the GitHub Copilot OAuth flow.
func LoadGitHubCopilotOAuth() (*ai.OAuthAuth, error) {
	if loaders := loadSnapshot(); loaders != nil && loaders.GithubCopilot != nil {
		return loaders.GithubCopilot()
	}
	return GithubCopilotOAuth, nil
}

// LoadOpenRouterOAuth returns the OpenRouter OAuth flow.
func LoadOpenRouterOAuth() (*ai.OAuthAuth, error) {
	if loaders := loadSnapshot(); loaders != nil && loaders.Openrouter != nil {
		return loaders.Openrouter()
	}
	return OpenRouterOAuth, nil
}

// LoadKimiCodingOAuth returns the Kimi Code OAuth flow.
func LoadKimiCodingOAuth() (*ai.OAuthAuth, error) {
	if loaders := loadSnapshot(); loaders != nil && loaders.KimiCoding != nil {
		return loaders.KimiCoding()
	}
	return KimiCodingOAuth, nil
}

// LoadMetaOAuth returns the Meta OAuth flow.
func LoadMetaOAuth() (*ai.OAuthAuth, error) {
	if loaders := loadSnapshot(); loaders != nil && loaders.Meta != nil {
		return loaders.Meta()
	}
	return MetaOAuth, nil
}

// LoadXaiOAuth returns the xAI OAuth flow.
func LoadXaiOAuth() (*ai.OAuthAuth, error) {
	if loaders := loadSnapshot(); loaders != nil && loaders.Xai != nil {
		return loaders.Xai()
	}
	return XaiOAuth, nil
}

// LoadRadiusOAuth returns a Radius gateway OAuth flow for the given options.
func LoadRadiusOAuth(options RadiusOAuthOptions) (*ai.OAuthAuth, error) {
	if loaders := loadSnapshot(); loaders != nil && loaders.Radius != nil {
		return loaders.Radius(options)
	}
	return CreateRadiusOAuth(options), nil
}

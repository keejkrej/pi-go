// Ported from packages/ai/src/bun-oauth.ts (pi v1.5.2).

package compat

import (
	"github.com/keejkrej/pi-go/ai"
	aioauth "github.com/keejkrej/pi-go/ai/oauth"
)

// RegisterBunOAuthFlows registers bundled OAuth flows eagerly.
func RegisterBunOAuthFlows() {
	aioauth.RegisterBundledOAuthFlowLoaders(&aioauth.OAuthFlowLoaders{
		Anthropic:     func() (*ai.OAuthAuth, error) { return aioauth.AnthropicOAuth, nil },
		OpenaiCodex:   func() (*ai.OAuthAuth, error) { return aioauth.OpenaiCodexOAuth, nil },
		OpenaiChatGPT: func() (*ai.OAuthAuth, error) { return aioauth.OpenaiChatGPTOAuth, nil },
		GithubCopilot: func() (*ai.OAuthAuth, error) { return aioauth.GithubCopilotOAuth, nil },
		Openrouter:    func() (*ai.OAuthAuth, error) { return aioauth.OpenRouterOAuth, nil },
		KimiCoding:    func() (*ai.OAuthAuth, error) { return aioauth.KimiCodingOAuth, nil },
		Meta:          func() (*ai.OAuthAuth, error) { return aioauth.MetaOAuth, nil },
		Xai:           func() (*ai.OAuthAuth, error) { return aioauth.XaiOAuth, nil },
		Radius: func(options aioauth.RadiusOAuthOptions) (*ai.OAuthAuth, error) {
			return aioauth.CreateRadiusOAuth(options), nil
		},
	})
}

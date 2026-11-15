// Ported from packages/ai/src/providers/cloudflare-ai-gateway.ts (pi v1.5.2).

package providers

import (
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/ai/catalog"
)

// CloudflareAIGatewayProvider constructs the built-in Cloudflare AI Gateway provider.
func CloudflareAIGatewayProvider() *ai.Provider {
	// The api map is pinned to all three APIs: models.dev's gateway catalog drops and
	// restores `workers-ai/*` (openai-completions) entries over time, and inference from
	// `models` alone would otherwise reject the openai-completions entry whenever the
	// generated catalog happens to contain none.
	return allMustCreateProvider(&ai.CreateProviderOptions{
		Id:     "cloudflare-ai-gateway",
		Name:   allPtr("Cloudflare AI Gateway"),
		Auth:   ai.ProviderAuth{ApiKey: CloudflareAIGatewayAuth()},
		Models: allCatalogModels(catalog.CloudflareAiGatewayModels, nil, nil),
		ApiByModel: map[string]ai.ProviderStreams{
			"anthropic-messages": CloudflareStreams(api.AnthropicMessagesApi()),
			"openai-completions": CloudflareStreams(api.OpenAICompletionsApi()),
			"openai-responses":   CloudflareStreams(api.OpenAIResponsesApi()),
		},
	})
}

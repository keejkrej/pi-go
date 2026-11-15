// Ported from packages/ai/src/providers/fireworks.ts (pi v1.5.2).

package providers

import (
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/ai/catalog"
)

// FireworksProvider constructs the built-in Fireworks provider.
func FireworksProvider() *ai.Provider {
	return allMustCreateProvider(&ai.CreateProviderOptions{
		Id:      "fireworks",
		Name:    allPtr("Fireworks"),
		BaseUrl: allPtr("https://api.fireworks.ai/inference"),
		Auth:    ai.ProviderAuth{ApiKey: ai.EnvApiKeyAuth("Fireworks API key", []string{"FIREWORKS_API_KEY"})},
		Models:  allCatalogModels(catalog.FireworksModels, nil, nil),
		ApiByModel: map[string]ai.ProviderStreams{
			"anthropic-messages": api.AnthropicMessagesApi(),
			"openai-completions": api.OpenAICompletionsApi(),
		},
	})
}

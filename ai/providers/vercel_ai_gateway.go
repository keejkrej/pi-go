// Ported from packages/ai/src/providers/vercel-ai-gateway.ts (pi v1.5.2).

package providers

import (
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/ai/catalog"
)

// VercelAIGatewayProvider constructs the built-in Vercel AI Gateway provider.
func VercelAIGatewayProvider() *ai.Provider {
	return allMustCreateProvider(&ai.CreateProviderOptions{
		Id:      "vercel-ai-gateway",
		Name:    allPtr("Vercel AI Gateway"),
		BaseUrl: allPtr("https://ai-gateway.vercel.sh"),
		Auth:    ai.ProviderAuth{ApiKey: ai.EnvApiKeyAuth("Vercel AI Gateway API key", []string{"AI_GATEWAY_API_KEY"})},
		Models:  allCatalogModels(catalog.VercelAiGatewayModels, nil, catalog.VercelAiGatewayClassifierModels),
		Api:     api.AnthropicMessagesApi(),
		// AI Gateway serves TypeSafe's System One protocol at /typesafe/v1/systemone.
		Classifiers: map[string]ai.ProviderClassifier{
			"typesafe-system-one": api.TypesafeSystemOneApi(),
		},
	})
}

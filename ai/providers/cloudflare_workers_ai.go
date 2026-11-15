// Ported from packages/ai/src/providers/cloudflare-workers-ai.ts (pi v1.5.2).

package providers

import (
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/ai/catalog"
)

// CloudflareWorkersAIProvider constructs the built-in Cloudflare Workers AI provider.
func CloudflareWorkersAIProvider() *ai.Provider {
	return allMustCreateProvider(&ai.CreateProviderOptions{
		Id:     "cloudflare-workers-ai",
		Name:   allPtr("Cloudflare Workers AI"),
		Auth:   ai.ProviderAuth{ApiKey: CloudflareWorkersAIAuth()},
		Models: allCatalogModels(catalog.CloudflareWorkersAiModels, nil, catalog.CloudflareWorkersAiClassifierModels),
		Api:    CloudflareStreams(api.OpenAICompletionsApi()),
		Classifiers: map[string]ai.ProviderClassifier{
			"cloudflare-workers-ai-system-one": CloudflareClassifier(api.CloudflareWorkersAISystemOneApi()),
		},
	})
}

// Ported from packages/ai/src/providers/xai.ts (pi v1.5.2).

package providers

import (
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/ai/catalog"
	aioauth "github.com/keejkrej/pi-go/ai/oauth"
)

// XaiProvider constructs the built-in xAI provider.
func XaiProvider() *ai.Provider {
	return allMustCreateProvider(&ai.CreateProviderOptions{
		Id:      "xai",
		Name:    allPtr("xAI"),
		BaseUrl: allPtr("https://api.x.ai/v1"),
		Auth: ai.ProviderAuth{
			ApiKey: ai.EnvApiKeyAuth("xAI API key", []string{"XAI_API_KEY"}),
			OAuth: ai.LazyOAuth(ai.LazyOAuthInput{
				Name:           "xAI (Grok/X subscription)",
				IsSubscription: true,
				LoginLabel:     allPtr("Sign in with SuperGrok or X Premium"),
				Load:           aioauth.LoadXaiOAuth,
			}),
		},
		Models: allCatalogModels(catalog.XaiModels, nil, nil),
		Api:    api.OpenAIResponsesApi(),
	})
}

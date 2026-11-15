// Ported from packages/ai/src/providers/meta.ts (pi v1.5.2).

package providers

import (
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/ai/catalog"
	aioauth "github.com/keejkrej/pi-go/ai/oauth"
)

// MetaProvider constructs the built-in Meta provider.
func MetaProvider() *ai.Provider {
	return allMustCreateProvider(&ai.CreateProviderOptions{
		Id:      "meta",
		Name:    allPtr("Meta"),
		BaseUrl: allPtr("https://api.meta.ai/v1"),
		Auth: ai.ProviderAuth{
			ApiKey: ai.EnvApiKeyAuth("Meta Model API key", []string{"META_API_KEY"}),
			OAuth: ai.LazyOAuth(ai.LazyOAuthInput{
				Name:           "Meta (Muse subscription)",
				IsSubscription: true,
				LoginLabel:     allPtr("Sign in with Meta"),
				Load:           aioauth.LoadMetaOAuth,
			}),
		},
		Models: allCatalogModels(catalog.MetaModels, nil, nil),
		Api:    api.OpenAIResponsesApi(),
	})
}

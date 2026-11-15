// Ported from packages/ai/src/providers/kimi-coding.ts (pi v1.5.2).

package providers

import (
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/ai/catalog"
	aioauth "github.com/keejkrej/pi-go/ai/oauth"
)

// KimiCodingProvider constructs the built-in Kimi For Coding provider.
func KimiCodingProvider() *ai.Provider {
	return allMustCreateProvider(&ai.CreateProviderOptions{
		Id:      "kimi-coding",
		Name:    allPtr("Kimi For Coding"),
		BaseUrl: allPtr("https://api.kimi.com/coding/v1"),
		Auth: ai.ProviderAuth{
			ApiKey: ai.EnvApiKeyAuth("Kimi API key", []string{"KIMI_API_KEY"}),
			OAuth: ai.LazyOAuth(ai.LazyOAuthInput{
				Name:           "Kimi Code (subscription)",
				IsSubscription: true,
				LoginLabel:     allPtr("Sign in with Kimi Code"),
				Load:           aioauth.LoadKimiCodingOAuth,
			}),
		},
		Models: allCatalogModels(catalog.KimiCodingModels, nil, nil),
		Api:    api.OpenAICompletionsApi(),
	})
}

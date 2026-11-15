// Ported from packages/ai/src/providers/openai-codex.ts (pi v1.5.2).

package providers

import (
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/ai/catalog"
	aioauth "github.com/keejkrej/pi-go/ai/oauth"
)

// OpenaiCodexProvider constructs the built-in OpenAI Codex provider.
func OpenaiCodexProvider() *ai.Provider {
	return allMustCreateProvider(&ai.CreateProviderOptions{
		Id:      "openai-codex",
		Name:    allPtr("OpenAI Codex"),
		BaseUrl: allPtr("https://chatgpt.com/backend-api"),
		Auth: ai.ProviderAuth{
			OAuth: ai.LazyOAuth(ai.LazyOAuthInput{
				Name:           "OpenAI (ChatGPT Plus/Pro)",
				IsSubscription: true,
				Load:           aioauth.LoadOpenAICodexOAuth,
			}),
		},
		Models: allCatalogModels(catalog.OpenaiCodexModels, nil, nil),
		Api:    api.OpenAICodexResponsesApi(),
	})
}

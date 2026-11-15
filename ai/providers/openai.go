// Ported from packages/ai/src/providers/openai.ts (pi v1.5.2).

package providers

import (
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/ai/catalog"
	aioauth "github.com/keejkrej/pi-go/ai/oauth"
)

// OpenaiProvider constructs the built-in OpenAI provider.
func OpenaiProvider() *ai.Provider {
	return allMustCreateProvider(&ai.CreateProviderOptions{
		Id:      "openai",
		Name:    allPtr("OpenAI"),
		BaseUrl: allPtr("https://api.openai.com/v1"),
		Auth: ai.ProviderAuth{
			ApiKey: ai.EnvApiKeyAuth("OpenAI API key", []string{"OPENAI_API_KEY"}),
			OAuth: ai.LazyOAuth(ai.LazyOAuthInput{
				Name:           "OpenAI (ChatGPT subscription)",
				IsSubscription: true,
				LoginLabel:     allPtr("Sign in with ChatGPT"),
				Load:           aioauth.LoadOpenAIChatGPTOAuth,
			}),
		},
		Models: allCatalogModels(catalog.OpenaiModels, nil, catalog.OpenaiClassifierModels),
		// Sign in with ChatGPT tokens only reach the Responses API; the Decisions API rejects them.
		FilterAllModels: func(models []ai.AnyModel, credential ai.Credential) []ai.AnyModel {
			if credential != nil && credential.CredentialType() == ai.AuthTypeOAuth {
				out := make([]ai.AnyModel, 0, len(models))
				for _, m := range models {
					if !ai.IsModelType(m, ai.ModelTypeClassifier) {
						out = append(out, m)
					}
				}
				return out
			}
			return models
		},
		Api: api.OpenAIResponsesApi(),
		Classifiers: map[string]ai.ProviderClassifier{
			"openai-decisions": api.OpenAIDecisionsApi(),
		},
	})
}

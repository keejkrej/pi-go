// Ported from packages/ai/src/providers/opencode.ts (pi v1.5.2).

package providers

import (
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/ai/catalog"
)

// OpencodeProvider constructs the built-in OpenCode Zen provider.
func OpencodeProvider() *ai.Provider {
	return allMustCreateProvider(&ai.CreateProviderOptions{
		Id:     "opencode",
		Name:   allPtr("OpenCode Zen"),
		Auth:   ai.ProviderAuth{ApiKey: ai.EnvApiKeyAuth("OpenCode API key", []string{"OPENCODE_API_KEY"})},
		Models: allCatalogModels(catalog.OpencodeModels, nil, catalog.OpencodeClassifierModels),
		ApiByModel: map[string]ai.ProviderStreams{
			"anthropic-messages":   WithOpenCodeSessionHeader(api.AnthropicMessagesApi()),
			"google-generative-ai": WithOpenCodeSessionHeader(api.GoogleGenerativeAiApi()),
			"openai-completions":   WithOpenCodeSessionHeader(api.OpenAICompletionsApi()),
			"openai-responses":     WithOpenCodeSessionHeader(api.OpenAIResponsesApi()),
		},
		// OpenCode Zen serves TypeSafe's System One protocol at /inference/systemone/v1/systemone.
		Classifiers: map[string]ai.ProviderClassifier{
			"typesafe-system-one": api.TypesafeSystemOneApi(),
		},
	})
}

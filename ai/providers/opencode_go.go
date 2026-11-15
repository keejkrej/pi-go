// Ported from packages/ai/src/providers/opencode-go.ts (pi v1.5.2).

package providers

import (
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/ai/catalog"
)

// OpencodeGoProvider constructs the built-in OpenCode Go provider.
func OpencodeGoProvider() *ai.Provider {
	return allMustCreateProvider(&ai.CreateProviderOptions{
		Id:     "opencode-go",
		Name:   allPtr("OpenCode Go"),
		Auth:   ai.ProviderAuth{ApiKey: ai.EnvApiKeyAuth("OpenCode API key", []string{"OPENCODE_API_KEY"})},
		Models: allCatalogModels(catalog.OpencodeGoModels, nil, nil),
		ApiByModel: map[string]ai.ProviderStreams{
			"anthropic-messages":   WithOpenCodeSessionHeader(api.AnthropicMessagesApi()),
			"google-generative-ai": WithOpenCodeSessionHeader(api.GoogleGenerativeAiApi()),
			"openai-completions":   WithOpenCodeSessionHeader(api.OpenAICompletionsApi()),
			"openai-responses":     WithOpenCodeSessionHeader(api.OpenAIResponsesApi()),
		},
	})
}

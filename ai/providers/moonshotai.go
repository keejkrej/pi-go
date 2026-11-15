// Ported from packages/ai/src/providers/moonshotai.ts (pi v1.5.2).

package providers

import (
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/ai/catalog"
)

// MoonshotaiProvider constructs the built-in Moonshot AI provider.
func MoonshotaiProvider() *ai.Provider {
	return allSimpleApiKeyProvider(
		"moonshotai",
		"Moonshot AI",
		"https://api.moonshot.ai/v1",
		"Moonshot AI API key",
		[]string{"MOONSHOT_API_KEY"},
		catalog.MoonshotaiModels,
		api.OpenAICompletionsApi(),
	)
}

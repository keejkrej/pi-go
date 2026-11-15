// Ported from packages/ai/src/providers/together.ts (pi v1.5.2).

package providers

import (
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/ai/catalog"
)

// TogetherProvider constructs the built-in Together provider.
func TogetherProvider() *ai.Provider {
	return allSimpleApiKeyProvider(
		"together",
		"Together",
		"https://api.together.ai/v1",
		"Together API key",
		[]string{"TOGETHER_API_KEY"},
		catalog.TogetherModels,
		api.OpenAICompletionsApi(),
	)
}

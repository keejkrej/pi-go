// Ported from packages/ai/src/providers/zai.ts (pi v1.5.2).

package providers

import (
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/ai/catalog"
)

// ZaiProvider constructs the built-in Z.AI provider.
func ZaiProvider() *ai.Provider {
	return allSimpleApiKeyProvider(
		"zai",
		"Z.AI",
		"https://api.z.ai/api/coding/paas/v4",
		"Z.AI API key",
		[]string{"ZAI_API_KEY"},
		catalog.ZaiModels,
		api.OpenAICompletionsApi(),
	)
}

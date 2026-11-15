// Ported from packages/ai/src/providers/ant-ling.ts (pi v1.5.2).

package providers

import (
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/ai/catalog"
)

// AntLingProvider constructs the built-in Ant Ling provider.
func AntLingProvider() *ai.Provider {
	return allSimpleApiKeyProvider(
		"ant-ling",
		"Ant Ling",
		"https://api.ant-ling.com/v1",
		"Ant Ling API key",
		[]string{"ANT_LING_API_KEY"},
		catalog.AntLingModels,
		api.OpenAICompletionsApi(),
	)
}

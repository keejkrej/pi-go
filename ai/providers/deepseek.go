// Ported from packages/ai/src/providers/deepseek.ts (pi v1.5.2).

package providers

import (
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/ai/catalog"
)

// DeepseekProvider constructs the built-in DeepSeek provider.
func DeepseekProvider() *ai.Provider {
	return allSimpleApiKeyProvider(
		"deepseek",
		"DeepSeek",
		"https://api.deepseek.com",
		"DeepSeek API key",
		[]string{"DEEPSEEK_API_KEY"},
		catalog.DeepseekModels,
		api.OpenAICompletionsApi(),
	)
}

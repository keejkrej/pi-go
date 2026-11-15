// Ported from packages/ai/src/providers/moonshotai-cn.ts (pi v1.5.2).

package providers

import (
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/ai/catalog"
)

// MoonshotaiCnProvider constructs the built-in Moonshot AI CN provider.
func MoonshotaiCnProvider() *ai.Provider {
	return allSimpleApiKeyProvider(
		"moonshotai-cn",
		"Moonshot AI CN",
		"https://api.moonshot.cn/v1",
		"Moonshot AI API key",
		[]string{"MOONSHOT_API_KEY"},
		catalog.MoonshotaiCnModels,
		api.OpenAICompletionsApi(),
	)
}

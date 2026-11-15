// Ported from packages/ai/src/providers/zai-coding-cn.ts (pi v1.5.2).

package providers

import (
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/ai/catalog"
)

// ZaiCodingCnProvider constructs the built-in Z.AI Coding CN provider.
func ZaiCodingCnProvider() *ai.Provider {
	return allSimpleApiKeyProvider(
		"zai-coding-cn",
		"Z.AI Coding CN",
		"https://open.bigmodel.cn/api/coding/paas/v4",
		"Z.AI Coding CN API key",
		[]string{"ZAI_CODING_CN_API_KEY"},
		catalog.ZaiCodingCnModels,
		api.OpenAICompletionsApi(),
	)
}

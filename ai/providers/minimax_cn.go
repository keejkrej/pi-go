// Ported from packages/ai/src/providers/minimax-cn.ts (pi v1.5.2).

package providers

import (
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/ai/catalog"
)

// MinimaxCnProvider constructs the built-in MiniMax CN provider.
func MinimaxCnProvider() *ai.Provider {
	return allSimpleApiKeyProvider(
		"minimax-cn",
		"MiniMax CN",
		"https://api.minimaxi.com/anthropic",
		"MiniMax CN API key",
		[]string{"MINIMAX_CN_API_KEY"},
		catalog.MinimaxCnModels,
		api.AnthropicMessagesApi(),
	)
}

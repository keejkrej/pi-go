// Ported from packages/ai/src/providers/minimax.ts (pi v1.5.2).

package providers

import (
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/ai/catalog"
)

// MinimaxProvider constructs the built-in MiniMax provider.
func MinimaxProvider() *ai.Provider {
	return allSimpleApiKeyProvider(
		"minimax",
		"MiniMax",
		"https://api.minimax.io/anthropic",
		"MiniMax API key",
		[]string{"MINIMAX_API_KEY"},
		catalog.MinimaxModels,
		api.AnthropicMessagesApi(),
	)
}

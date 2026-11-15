// Ported from packages/ai/src/providers/xiaomi.ts (pi v1.5.2).

package providers

import (
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/ai/catalog"
)

// XiaomiProvider constructs the built-in Xiaomi provider.
func XiaomiProvider() *ai.Provider {
	return allSimpleApiKeyProvider(
		"xiaomi",
		"Xiaomi",
		"https://api.xiaomimimo.com/v1",
		"Xiaomi API key",
		[]string{"XIAOMI_API_KEY"},
		catalog.XiaomiModels,
		api.OpenAICompletionsApi(),
	)
}

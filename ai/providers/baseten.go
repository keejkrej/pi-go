// Ported from packages/ai/src/providers/baseten.ts (pi v1.5.2).

package providers

import (
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/ai/catalog"
)

// BasetenProvider constructs the built-in Baseten provider.
func BasetenProvider() *ai.Provider {
	return allSimpleApiKeyProvider(
		"baseten",
		"Baseten",
		"https://inference.baseten.co/v1",
		"Baseten API key",
		[]string{"BASETEN_API_KEY"},
		catalog.BasetenModels,
		api.OpenAICompletionsApi(),
	)
}

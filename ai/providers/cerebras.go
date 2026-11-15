// Ported from packages/ai/src/providers/cerebras.ts (pi v1.5.2).

package providers

import (
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/ai/catalog"
)

// CerebrasProvider constructs the built-in Cerebras provider.
func CerebrasProvider() *ai.Provider {
	return allSimpleApiKeyProvider(
		"cerebras",
		"Cerebras",
		"https://api.cerebras.ai/v1",
		"Cerebras API key",
		[]string{"CEREBRAS_API_KEY"},
		catalog.CerebrasModels,
		api.OpenAICompletionsApi(),
	)
}

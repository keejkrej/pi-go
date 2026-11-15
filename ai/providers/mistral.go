// Ported from packages/ai/src/providers/mistral.ts (pi v1.5.2).

package providers

import (
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/ai/catalog"
)

// MistralProvider constructs the built-in Mistral provider.
func MistralProvider() *ai.Provider {
	return allSimpleApiKeyProvider(
		"mistral",
		"Mistral",
		"https://api.mistral.ai",
		"Mistral API key",
		[]string{"MISTRAL_API_KEY"},
		catalog.MistralModels,
		api.MistralConversationsApi(),
	)
}

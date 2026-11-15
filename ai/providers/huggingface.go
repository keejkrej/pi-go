// Ported from packages/ai/src/providers/huggingface.ts (pi v1.5.2).

package providers

import (
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/ai/catalog"
)

// HuggingfaceProvider constructs the built-in Hugging Face provider.
func HuggingfaceProvider() *ai.Provider {
	return allSimpleApiKeyProvider(
		"huggingface",
		"Hugging Face",
		"https://router.huggingface.co/v1",
		"Hugging Face token",
		[]string{"HF_TOKEN"},
		catalog.HuggingfaceModels,
		api.OpenAICompletionsApi(),
	)
}

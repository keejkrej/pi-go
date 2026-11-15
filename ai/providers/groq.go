// Ported from packages/ai/src/providers/groq.ts (pi v1.5.2).

package providers

import (
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/ai/catalog"
)

// GroqProvider constructs the built-in Groq provider.
func GroqProvider() *ai.Provider {
	return allSimpleApiKeyProvider(
		"groq",
		"Groq",
		"https://api.groq.com/openai/v1",
		"Groq API key",
		[]string{"GROQ_API_KEY"},
		catalog.GroqModels,
		api.OpenAICompletionsApi(),
	)
}

// Ported from packages/ai/src/providers/google.ts (pi v1.5.2).

package providers

import (
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/ai/catalog"
)

// GoogleProvider constructs the built-in Google (Gemini) provider.
func GoogleProvider() *ai.Provider {
	return allSimpleApiKeyProvider(
		"google",
		"Google",
		"https://generativelanguage.googleapis.com/v1beta",
		"Gemini API key",
		[]string{"GEMINI_API_KEY"},
		catalog.GoogleModels,
		api.GoogleGenerativeAiApi(),
	)
}

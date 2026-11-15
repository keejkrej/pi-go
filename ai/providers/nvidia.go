// Ported from packages/ai/src/providers/nvidia.ts (pi v1.5.2).

package providers

import (
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/ai/catalog"
)

// NvidiaProvider constructs the built-in NVIDIA provider.
func NvidiaProvider() *ai.Provider {
	return allSimpleApiKeyProvider(
		"nvidia",
		"NVIDIA",
		"https://integrate.api.nvidia.com/v1",
		"NVIDIA API key",
		[]string{"NVIDIA_API_KEY"},
		catalog.NvidiaModels,
		api.OpenAICompletionsApi(),
	)
}

// Ported from packages/ai/src/providers/qwen-token-plan.ts (pi v1.5.2).

package providers

import (
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/ai/catalog"
)

// QwenTokenPlanProvider constructs the built-in Qwen Token Plan Team provider.
func QwenTokenPlanProvider() *ai.Provider {
	return allSimpleApiKeyProvider(
		"qwen-token-plan",
		"Qwen Token Plan Team",
		"https://token-plan.maas.qwencloudapi.com/compatible-mode/v1",
		"Qwen Token Plan Team API key",
		[]string{"QWEN_TOKEN_PLAN_TEAM_API_KEY", "QWEN_TOKEN_PLAN_API_KEY"},
		catalog.QwenTokenPlanModels,
		api.OpenAICompletionsApi(),
	)
}

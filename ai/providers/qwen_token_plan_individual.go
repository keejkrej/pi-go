// Ported from packages/ai/src/providers/qwen-token-plan-individual.ts (pi v1.5.2).

package providers

import (
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/ai/catalog"
)

// QwenTokenPlanIndividualProvider constructs the built-in Qwen Token Plan Individual provider.
func QwenTokenPlanIndividualProvider() *ai.Provider {
	return allSimpleApiKeyProvider(
		"qwen-token-plan-individual",
		"Qwen Token Plan Individual",
		"https://token-plan.maas.qwencloudapi.com/compatible-mode/v1",
		"Qwen Token Plan Individual API key",
		[]string{"QWEN_TOKEN_PLAN_INDIVIDUAL_API_KEY", "QWEN_TOKEN_PLAN_API_KEY"},
		catalog.QwenTokenPlanIndividualModels,
		api.OpenAICompletionsApi(),
	)
}

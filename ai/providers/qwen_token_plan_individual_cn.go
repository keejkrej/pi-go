// Ported from packages/ai/src/providers/qwen-token-plan-individual-cn.ts (pi v1.5.2).

package providers

import (
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/ai/catalog"
)

// QwenTokenPlanIndividualCnProvider constructs the built-in Qwen Token Plan Individual CN provider.
func QwenTokenPlanIndividualCnProvider() *ai.Provider {
	return allSimpleApiKeyProvider(
		"qwen-token-plan-individual-cn",
		"Qwen Token Plan Individual CN",
		"https://token-plan.maas.qianwenaiapi.com/compatible-mode/v1",
		"Qwen Token Plan Individual CN API key",
		[]string{"QWEN_TOKEN_PLAN_INDIVIDUAL_CN_API_KEY", "QWEN_TOKEN_PLAN_CN_API_KEY"},
		catalog.QwenTokenPlanIndividualCnModels,
		api.OpenAICompletionsApi(),
	)
}

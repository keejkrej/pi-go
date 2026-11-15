// Ported from packages/ai/src/providers/qwen-token-plan-cn.ts (pi v1.5.2).

package providers

import (
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/ai/catalog"
)

// QwenTokenPlanCnProvider constructs the built-in Qwen Token Plan Team CN provider.
func QwenTokenPlanCnProvider() *ai.Provider {
	return allSimpleApiKeyProvider(
		"qwen-token-plan-cn",
		"Qwen Token Plan Team CN",
		"https://token-plan.maas.qianwenaiapi.com/compatible-mode/v1",
		"Qwen Token Plan Team CN API key",
		[]string{"QWEN_TOKEN_PLAN_TEAM_CN_API_KEY", "QWEN_TOKEN_PLAN_CN_API_KEY"},
		catalog.QwenTokenPlanCnModels,
		api.OpenAICompletionsApi(),
	)
}

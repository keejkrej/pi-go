// Ported from packages/ai/src/providers/xiaomi-token-plan-cn.ts (pi v1.5.2).

package providers

import (
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/ai/catalog"
)

// XiaomiTokenPlanCnProvider constructs the built-in Xiaomi Token Plan CN provider.
func XiaomiTokenPlanCnProvider() *ai.Provider {
	return allSimpleApiKeyProvider(
		"xiaomi-token-plan-cn",
		"Xiaomi Token Plan CN",
		"https://token-plan-cn.xiaomimimo.com/v1",
		"Xiaomi Token Plan CN API key",
		[]string{"XIAOMI_TOKEN_PLAN_CN_API_KEY"},
		catalog.XiaomiTokenPlanCnModels,
		api.OpenAICompletionsApi(),
	)
}

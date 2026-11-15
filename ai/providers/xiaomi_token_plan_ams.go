// Ported from packages/ai/src/providers/xiaomi-token-plan-ams.ts (pi v1.5.2).

package providers

import (
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/ai/catalog"
)

// XiaomiTokenPlanAmsProvider constructs the built-in Xiaomi Token Plan AMS provider.
func XiaomiTokenPlanAmsProvider() *ai.Provider {
	return allSimpleApiKeyProvider(
		"xiaomi-token-plan-ams",
		"Xiaomi Token Plan AMS",
		"https://token-plan-ams.xiaomimimo.com/v1",
		"Xiaomi Token Plan AMS API key",
		[]string{"XIAOMI_TOKEN_PLAN_AMS_API_KEY"},
		catalog.XiaomiTokenPlanAmsModels,
		api.OpenAICompletionsApi(),
	)
}

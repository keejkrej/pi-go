// Ported from packages/ai/src/providers/xiaomi-token-plan-sgp.ts (pi v1.5.2).

package providers

import (
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/ai/catalog"
)

// XiaomiTokenPlanSgpProvider constructs the built-in Xiaomi Token Plan SGP provider.
func XiaomiTokenPlanSgpProvider() *ai.Provider {
	return allSimpleApiKeyProvider(
		"xiaomi-token-plan-sgp",
		"Xiaomi Token Plan SGP",
		"https://token-plan-sgp.xiaomimimo.com/v1",
		"Xiaomi Token Plan SGP API key",
		[]string{"XIAOMI_TOKEN_PLAN_SGP_API_KEY"},
		catalog.XiaomiTokenPlanSgpModels,
		api.OpenAICompletionsApi(),
	)
}

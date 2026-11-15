// Ported from packages/ai/src/providers/xiaomi-token-plan-sgp.models.ts (pi v1.0.0).

package catalog

import "github.com/keejkrej/pi-go/ai"

var xtpsmChat, xtpsmImage, xtpsmClassifier = mgLoadProviderCatalog(ai.KnownProviderXiaomiTokenPlanSgp, "xiaomi-token-plan-sgp.json")

// XiaomiTokenPlanSgpModels is the chat model catalog for xiaomi-token-plan-sgp.
var XiaomiTokenPlanSgpModels ai.ChatModelCatalog = xtpsmChat

// XiaomiTokenPlanSgpImageModels is the image model catalog for xiaomi-token-plan-sgp.
var XiaomiTokenPlanSgpImageModels ai.ImageModelCatalog = xtpsmImage

// XiaomiTokenPlanSgpClassifierModels is the classifier model catalog for xiaomi-token-plan-sgp.
var XiaomiTokenPlanSgpClassifierModels ai.ClassifierModelCatalog = xtpsmClassifier

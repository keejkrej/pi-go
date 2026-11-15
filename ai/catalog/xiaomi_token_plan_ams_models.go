// Ported from packages/ai/src/providers/xiaomi-token-plan-ams.models.ts (pi v1.0.0).

package catalog

import "github.com/keejkrej/pi-go/ai"

var xtpamChat, xtpamImage, xtpamClassifier = mgLoadProviderCatalog(ai.KnownProviderXiaomiTokenPlanAms, "xiaomi-token-plan-ams.json")

// XiaomiTokenPlanAmsModels is the chat model catalog for xiaomi-token-plan-ams.
var XiaomiTokenPlanAmsModels ai.ChatModelCatalog = xtpamChat

// XiaomiTokenPlanAmsImageModels is the image model catalog for xiaomi-token-plan-ams.
var XiaomiTokenPlanAmsImageModels ai.ImageModelCatalog = xtpamImage

// XiaomiTokenPlanAmsClassifierModels is the classifier model catalog for xiaomi-token-plan-ams.
var XiaomiTokenPlanAmsClassifierModels ai.ClassifierModelCatalog = xtpamClassifier

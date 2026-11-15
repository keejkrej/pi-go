// Ported from packages/ai/src/providers/xiaomi-token-plan-cn.models.ts (pi v1.0.0).

package catalog

import "github.com/keejkrej/pi-go/ai"

var xtpcmChat, xtpcmImage, xtpcmClassifier = mgLoadProviderCatalog(ai.KnownProviderXiaomiTokenPlanCn, "xiaomi-token-plan-cn.json")

// XiaomiTokenPlanCnModels is the chat model catalog for xiaomi-token-plan-cn.
var XiaomiTokenPlanCnModels ai.ChatModelCatalog = xtpcmChat

// XiaomiTokenPlanCnImageModels is the image model catalog for xiaomi-token-plan-cn.
var XiaomiTokenPlanCnImageModels ai.ImageModelCatalog = xtpcmImage

// XiaomiTokenPlanCnClassifierModels is the classifier model catalog for xiaomi-token-plan-cn.
var XiaomiTokenPlanCnClassifierModels ai.ClassifierModelCatalog = xtpcmClassifier

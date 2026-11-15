// Ported from packages/ai/src/providers/qwen-token-plan-cn.models.ts (pi v1.0.0).

package catalog

import "github.com/keejkrej/pi-go/ai"

var qtpcmChat, qtpcmImage, qtpcmClassifier = mgLoadProviderCatalog(ai.KnownProviderQwenTokenPlanCn, "qwen-token-plan-cn.json")

// QwenTokenPlanCnModels is the chat model catalog for qwen-token-plan-cn.
var QwenTokenPlanCnModels ai.ChatModelCatalog = qtpcmChat

// QwenTokenPlanCnImageModels is the image model catalog for qwen-token-plan-cn.
var QwenTokenPlanCnImageModels ai.ImageModelCatalog = qtpcmImage

// QwenTokenPlanCnClassifierModels is the classifier model catalog for qwen-token-plan-cn.
var QwenTokenPlanCnClassifierModels ai.ClassifierModelCatalog = qtpcmClassifier

// Ported from packages/ai/src/providers/qwen-token-plan.models.ts (pi v1.0.0).

package catalog

import "github.com/keejkrej/pi-go/ai"

var qtpmChat, qtpmImage, qtpmClassifier = mgLoadProviderCatalog(ai.KnownProviderQwenTokenPlan, "qwen-token-plan.json")

// QwenTokenPlanModels is the chat model catalog for qwen-token-plan.
var QwenTokenPlanModels ai.ChatModelCatalog = qtpmChat

// QwenTokenPlanImageModels is the image model catalog for qwen-token-plan.
var QwenTokenPlanImageModels ai.ImageModelCatalog = qtpmImage

// QwenTokenPlanClassifierModels is the classifier model catalog for qwen-token-plan.
var QwenTokenPlanClassifierModels ai.ClassifierModelCatalog = qtpmClassifier

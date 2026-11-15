// Ported from packages/ai/src/providers/qwen-token-plan-individual-cn.models.ts (pi v1.5.2).

package catalog

import "github.com/keejkrej/pi-go/ai"

var qtpicmChat, qtpicmImage, qtpicmClassifier = mgLoadProviderCatalog(ai.KnownProviderQwenTokenPlanIndividualCn, "qwen-token-plan-individual-cn.json")

// QwenTokenPlanIndividualCnModels is the chat model catalog for qwen-token-plan-individual-cn.
var QwenTokenPlanIndividualCnModels ai.ChatModelCatalog = qtpicmChat

// QwenTokenPlanIndividualCnImageModels is the image model catalog for qwen-token-plan-individual-cn.
var QwenTokenPlanIndividualCnImageModels ai.ImageModelCatalog = qtpicmImage

// QwenTokenPlanIndividualCnClassifierModels is the classifier model catalog for qwen-token-plan-individual-cn.
var QwenTokenPlanIndividualCnClassifierModels ai.ClassifierModelCatalog = qtpicmClassifier

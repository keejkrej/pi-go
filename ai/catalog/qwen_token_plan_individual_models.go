// Ported from packages/ai/src/providers/qwen-token-plan-individual.models.ts (pi v1.0.0).

package catalog

import "github.com/keejkrej/pi-go/ai"

var qtpimChat, qtpimImage, qtpimClassifier = mgLoadProviderCatalog(ai.KnownProviderQwenTokenPlanIndividual, "qwen-token-plan-individual.json")

// QwenTokenPlanIndividualModels is the chat model catalog for qwen-token-plan-individual.
var QwenTokenPlanIndividualModels ai.ChatModelCatalog = qtpimChat

// QwenTokenPlanIndividualImageModels is the image model catalog for qwen-token-plan-individual.
var QwenTokenPlanIndividualImageModels ai.ImageModelCatalog = qtpimImage

// QwenTokenPlanIndividualClassifierModels is the classifier model catalog for qwen-token-plan-individual.
var QwenTokenPlanIndividualClassifierModels ai.ClassifierModelCatalog = qtpimClassifier

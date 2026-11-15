// Ported from packages/ai/src/providers/moonshotai-cn.models.ts (pi v1.0.0).

package catalog

import "github.com/keejkrej/pi-go/ai"

var mcmoChat, mcmoImage, mcmoClassifier = mgLoadProviderCatalog(ai.KnownProviderMoonshotaiCn, "moonshotai-cn.json")

// MoonshotaiCnModels is the chat model catalog for moonshotai-cn.
var MoonshotaiCnModels ai.ChatModelCatalog = mcmoChat

// MoonshotaiCnImageModels is the image model catalog for moonshotai-cn.
var MoonshotaiCnImageModels ai.ImageModelCatalog = mcmoImage

// MoonshotaiCnClassifierModels is the classifier model catalog for moonshotai-cn.
var MoonshotaiCnClassifierModels ai.ClassifierModelCatalog = mcmoClassifier

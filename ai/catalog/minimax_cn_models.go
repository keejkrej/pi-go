// Ported from packages/ai/src/providers/minimax-cn.models.ts (pi v1.0.0).

package catalog

import "github.com/keejkrej/pi-go/ai"

var mcmChat, mcmImage, mcmClassifier = mgLoadProviderCatalog(ai.KnownProviderMinimaxCn, "minimax-cn.json")

// MinimaxCnModels is the chat model catalog for minimax-cn.
var MinimaxCnModels ai.ChatModelCatalog = mcmChat

// MinimaxCnImageModels is the image model catalog for minimax-cn.
var MinimaxCnImageModels ai.ImageModelCatalog = mcmImage

// MinimaxCnClassifierModels is the classifier model catalog for minimax-cn.
var MinimaxCnClassifierModels ai.ClassifierModelCatalog = mcmClassifier

// Ported from packages/ai/src/providers/minimax.models.ts (pi v1.0.0).

package catalog

import "github.com/keejkrej/pi-go/ai"

var mmoChat, mmoImage, mmoClassifier = mgLoadProviderCatalog(ai.KnownProviderMinimax, "minimax.json")

// MinimaxModels is the chat model catalog for minimax.
var MinimaxModels ai.ChatModelCatalog = mmoChat

// MinimaxImageModels is the image model catalog for minimax.
var MinimaxImageModels ai.ImageModelCatalog = mmoImage

// MinimaxClassifierModels is the classifier model catalog for minimax.
var MinimaxClassifierModels ai.ClassifierModelCatalog = mmoClassifier

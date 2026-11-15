// Ported from packages/ai/src/providers/cerebras.models.ts (pi v1.0.0).

package catalog

import "github.com/keejkrej/pi-go/ai"

var cmChat, cmImage, cmClassifier = mgLoadProviderCatalog(ai.KnownProviderCerebras, "cerebras.json")

// CerebrasModels is the chat model catalog for cerebras.
var CerebrasModels ai.ChatModelCatalog = cmChat

// CerebrasImageModels is the image model catalog for cerebras.
var CerebrasImageModels ai.ImageModelCatalog = cmImage

// CerebrasClassifierModels is the classifier model catalog for cerebras.
var CerebrasClassifierModels ai.ClassifierModelCatalog = cmClassifier

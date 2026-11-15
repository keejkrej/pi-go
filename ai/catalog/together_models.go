// Ported from packages/ai/src/providers/together.models.ts (pi v1.0.0).

package catalog

import "github.com/keejkrej/pi-go/ai"

var tmChat, tmImage, tmClassifier = mgLoadProviderCatalog(ai.KnownProviderTogether, "together.json")

// TogetherModels is the chat model catalog for together.
var TogetherModels ai.ChatModelCatalog = tmChat

// TogetherImageModels is the image model catalog for together.
var TogetherImageModels ai.ImageModelCatalog = tmImage

// TogetherClassifierModels is the classifier model catalog for together.
var TogetherClassifierModels ai.ClassifierModelCatalog = tmClassifier

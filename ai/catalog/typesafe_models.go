// Ported from packages/ai/src/providers/typesafe.models.ts (pi v1.0.0).

package catalog

import "github.com/keejkrej/pi-go/ai"

var tmoChat, tmoImage, tmoClassifier = mgLoadProviderCatalog(ai.KnownProviderTypesafe, "typesafe.json")

// TypesafeModels is the chat model catalog for typesafe.
var TypesafeModels ai.ChatModelCatalog = tmoChat

// TypesafeImageModels is the image model catalog for typesafe.
var TypesafeImageModels ai.ImageModelCatalog = tmoImage

// TypesafeClassifierModels is the classifier model catalog for typesafe.
var TypesafeClassifierModels ai.ClassifierModelCatalog = tmoClassifier

// Ported from packages/ai/src/providers/baseten.models.ts (pi v1.0.0).

package catalog

import "github.com/keejkrej/pi-go/ai"

var bmChat, bmImage, bmClassifier = mgLoadProviderCatalog(ai.KnownProviderBaseten, "baseten.json")

// BasetenModels is the chat model catalog for baseten.
var BasetenModels ai.ChatModelCatalog = bmChat

// BasetenImageModels is the image model catalog for baseten.
var BasetenImageModels ai.ImageModelCatalog = bmImage

// BasetenClassifierModels is the classifier model catalog for baseten.
var BasetenClassifierModels ai.ClassifierModelCatalog = bmClassifier

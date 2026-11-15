// Ported from packages/ai/src/providers/radius.models.ts (pi v1.0.0).

package catalog

import "github.com/keejkrej/pi-go/ai"

var rmChat, rmImage, rmClassifier = mgLoadProviderCatalog(ai.KnownProviderRadius, "radius.json")

// RadiusModels is the chat model catalog for radius.
var RadiusModels ai.ChatModelCatalog = rmChat

// RadiusImageModels is the image model catalog for radius.
var RadiusImageModels ai.ImageModelCatalog = rmImage

// RadiusClassifierModels is the classifier model catalog for radius.
var RadiusClassifierModels ai.ClassifierModelCatalog = rmClassifier

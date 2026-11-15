// Ported from packages/ai/src/providers/fireworks.models.ts (pi v1.0.0).

package catalog

import "github.com/keejkrej/pi-go/ai"

var fmChat, fmImage, fmClassifier = mgLoadProviderCatalog(ai.KnownProviderFireworks, "fireworks.json")

// FireworksModels is the chat model catalog for fireworks.
var FireworksModels ai.ChatModelCatalog = fmChat

// FireworksImageModels is the image model catalog for fireworks.
var FireworksImageModels ai.ImageModelCatalog = fmImage

// FireworksClassifierModels is the classifier model catalog for fireworks.
var FireworksClassifierModels ai.ClassifierModelCatalog = fmClassifier

// Ported from packages/ai/src/providers/google.models.ts (pi v1.0.0).

package catalog

import "github.com/keejkrej/pi-go/ai"

var gmChat, gmImage, gmClassifier = mgLoadProviderCatalog(ai.KnownProviderGoogle, "google.json")

// GoogleModels is the chat model catalog for google.
var GoogleModels ai.ChatModelCatalog = gmChat

// GoogleImageModels is the image model catalog for google.
var GoogleImageModels ai.ImageModelCatalog = gmImage

// GoogleClassifierModels is the classifier model catalog for google.
var GoogleClassifierModels ai.ClassifierModelCatalog = gmClassifier

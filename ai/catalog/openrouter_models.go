// Ported from packages/ai/src/providers/openrouter.models.ts (pi v1.0.0).

package catalog

import "github.com/keejkrej/pi-go/ai"

var omodChat, omodImage, omodClassifier = mgLoadProviderCatalog(ai.KnownProviderOpenrouter, "openrouter.json")

// OpenrouterModels is the chat model catalog for openrouter.
var OpenrouterModels ai.ChatModelCatalog = omodChat

// OpenrouterImageModels is the image model catalog for openrouter.
var OpenrouterImageModels ai.ImageModelCatalog = omodImage

// OpenrouterClassifierModels is the classifier model catalog for openrouter.
var OpenrouterClassifierModels ai.ClassifierModelCatalog = omodClassifier

// Ported from packages/ai/src/providers/mistral.models.ts (pi v1.0.0).

package catalog

import "github.com/keejkrej/pi-go/ai"

var mmodChat, mmodImage, mmodClassifier = mgLoadProviderCatalog(ai.KnownProviderMistral, "mistral.json")

// MistralModels is the chat model catalog for mistral.
var MistralModels ai.ChatModelCatalog = mmodChat

// MistralImageModels is the image model catalog for mistral.
var MistralImageModels ai.ImageModelCatalog = mmodImage

// MistralClassifierModels is the classifier model catalog for mistral.
var MistralClassifierModels ai.ClassifierModelCatalog = mmodClassifier

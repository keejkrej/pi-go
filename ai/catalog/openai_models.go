// Ported from packages/ai/src/providers/openai.models.ts (pi v1.0.0).

package catalog

import "github.com/keejkrej/pi-go/ai"

var omChat, omImage, omClassifier = mgLoadProviderCatalog(ai.KnownProviderOpenai, "openai.json")

// OpenaiModels is the chat model catalog for openai.
var OpenaiModels ai.ChatModelCatalog = omChat

// OpenaiImageModels is the image model catalog for openai.
var OpenaiImageModels ai.ImageModelCatalog = omImage

// OpenaiClassifierModels is the classifier model catalog for openai.
var OpenaiClassifierModels ai.ClassifierModelCatalog = omClassifier

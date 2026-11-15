// Ported from packages/ai/src/providers/huggingface.models.ts (pi v1.0.0).

package catalog

import "github.com/keejkrej/pi-go/ai"

var hmChat, hmImage, hmClassifier = mgLoadProviderCatalog(ai.KnownProviderHuggingface, "huggingface.json")

// HuggingfaceModels is the chat model catalog for huggingface.
var HuggingfaceModels ai.ChatModelCatalog = hmChat

// HuggingfaceImageModels is the image model catalog for huggingface.
var HuggingfaceImageModels ai.ImageModelCatalog = hmImage

// HuggingfaceClassifierModels is the classifier model catalog for huggingface.
var HuggingfaceClassifierModels ai.ClassifierModelCatalog = hmClassifier

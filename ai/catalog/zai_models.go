// Ported from packages/ai/src/providers/zai.models.ts (pi v1.0.0).

package catalog

import "github.com/keejkrej/pi-go/ai"

var zmChat, zmImage, zmClassifier = mgLoadProviderCatalog(ai.KnownProviderZai, "zai.json")

// ZaiModels is the chat model catalog for zai.
var ZaiModels ai.ChatModelCatalog = zmChat

// ZaiImageModels is the image model catalog for zai.
var ZaiImageModels ai.ImageModelCatalog = zmImage

// ZaiClassifierModels is the classifier model catalog for zai.
var ZaiClassifierModels ai.ClassifierModelCatalog = zmClassifier

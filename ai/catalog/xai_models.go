// Ported from packages/ai/src/providers/xai.models.ts (pi v1.0.0).

package catalog

import "github.com/keejkrej/pi-go/ai"

var xmChat, xmImage, xmClassifier = mgLoadProviderCatalog(ai.KnownProviderXai, "xai.json")

// XaiModels is the chat model catalog for xai.
var XaiModels ai.ChatModelCatalog = xmChat

// XaiImageModels is the image model catalog for xai.
var XaiImageModels ai.ImageModelCatalog = xmImage

// XaiClassifierModels is the classifier model catalog for xai.
var XaiClassifierModels ai.ClassifierModelCatalog = xmClassifier

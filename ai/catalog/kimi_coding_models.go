// Ported from packages/ai/src/providers/kimi-coding.models.ts (pi v1.0.0).

package catalog

import "github.com/keejkrej/pi-go/ai"

var kcmChat, kcmImage, kcmClassifier = mgLoadProviderCatalog(ai.KnownProviderKimiCoding, "kimi-coding.json")

// KimiCodingModels is the chat model catalog for kimi-coding.
var KimiCodingModels ai.ChatModelCatalog = kcmChat

// KimiCodingImageModels is the image model catalog for kimi-coding.
var KimiCodingImageModels ai.ImageModelCatalog = kcmImage

// KimiCodingClassifierModels is the classifier model catalog for kimi-coding.
var KimiCodingClassifierModels ai.ClassifierModelCatalog = kcmClassifier

// Ported from packages/ai/src/providers/meta.models.ts (pi v1.0.0).

package catalog

import "github.com/keejkrej/pi-go/ai"

var mmChat, mmImage, mmClassifier = mgLoadProviderCatalog(ai.KnownProviderMeta, "meta.json")

// MetaModels is the chat model catalog for meta.
var MetaModels ai.ChatModelCatalog = mmChat

// MetaImageModels is the image model catalog for meta.
var MetaImageModels ai.ImageModelCatalog = mmImage

// MetaClassifierModels is the classifier model catalog for meta.
var MetaClassifierModels ai.ClassifierModelCatalog = mmClassifier

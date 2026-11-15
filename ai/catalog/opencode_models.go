// Ported from packages/ai/src/providers/opencode.models.ts (pi v1.0.0).

package catalog

import "github.com/keejkrej/pi-go/ai"

var omoChat, omoImage, omoClassifier = mgLoadProviderCatalog(ai.KnownProviderOpencode, "opencode.json")

// OpencodeModels is the chat model catalog for opencode.
var OpencodeModels ai.ChatModelCatalog = omoChat

// OpencodeImageModels is the image model catalog for opencode.
var OpencodeImageModels ai.ImageModelCatalog = omoImage

// OpencodeClassifierModels is the classifier model catalog for opencode.
var OpencodeClassifierModels ai.ClassifierModelCatalog = omoClassifier

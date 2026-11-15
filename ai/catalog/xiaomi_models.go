// Ported from packages/ai/src/providers/xiaomi.models.ts (pi v1.0.0).

package catalog

import "github.com/keejkrej/pi-go/ai"

var xmoChat, xmoImage, xmoClassifier = mgLoadProviderCatalog(ai.KnownProviderXiaomi, "xiaomi.json")

// XiaomiModels is the chat model catalog for xiaomi.
var XiaomiModels ai.ChatModelCatalog = xmoChat

// XiaomiImageModels is the image model catalog for xiaomi.
var XiaomiImageModels ai.ImageModelCatalog = xmoImage

// XiaomiClassifierModels is the classifier model catalog for xiaomi.
var XiaomiClassifierModels ai.ClassifierModelCatalog = xmoClassifier

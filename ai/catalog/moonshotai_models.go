// Ported from packages/ai/src/providers/moonshotai.models.ts (pi v1.0.0).

package catalog

import "github.com/keejkrej/pi-go/ai"

var mmodeChat, mmodeImage, mmodeClassifier = mgLoadProviderCatalog(ai.KnownProviderMoonshotai, "moonshotai.json")

// MoonshotaiModels is the chat model catalog for moonshotai.
var MoonshotaiModels ai.ChatModelCatalog = mmodeChat

// MoonshotaiImageModels is the image model catalog for moonshotai.
var MoonshotaiImageModels ai.ImageModelCatalog = mmodeImage

// MoonshotaiClassifierModels is the classifier model catalog for moonshotai.
var MoonshotaiClassifierModels ai.ClassifierModelCatalog = mmodeClassifier

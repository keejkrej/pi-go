// Ported from packages/ai/src/providers/groq.models.ts (pi v1.0.0).

package catalog

import "github.com/keejkrej/pi-go/ai"

var gmoChat, gmoImage, gmoClassifier = mgLoadProviderCatalog(ai.KnownProviderGroq, "groq.json")

// GroqModels is the chat model catalog for groq.
var GroqModels ai.ChatModelCatalog = gmoChat

// GroqImageModels is the image model catalog for groq.
var GroqImageModels ai.ImageModelCatalog = gmoImage

// GroqClassifierModels is the classifier model catalog for groq.
var GroqClassifierModels ai.ClassifierModelCatalog = gmoClassifier

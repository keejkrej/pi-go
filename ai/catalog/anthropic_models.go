// Ported from packages/ai/src/providers/anthropic.models.ts (pi v1.0.0).

package catalog

import "github.com/keejkrej/pi-go/ai"

var amChat, amImage, amClassifier = mgLoadProviderCatalog(ai.KnownProviderAnthropic, "anthropic.json")

// AnthropicModels is the chat model catalog for anthropic.
var AnthropicModels ai.ChatModelCatalog = amChat

// AnthropicImageModels is the image model catalog for anthropic.
var AnthropicImageModels ai.ImageModelCatalog = amImage

// AnthropicClassifierModels is the classifier model catalog for anthropic.
var AnthropicClassifierModels ai.ClassifierModelCatalog = amClassifier

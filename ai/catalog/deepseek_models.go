// Ported from packages/ai/src/providers/deepseek.models.ts (pi v1.0.0).

package catalog

import "github.com/keejkrej/pi-go/ai"

var dmChat, dmImage, dmClassifier = mgLoadProviderCatalog(ai.KnownProviderDeepseek, "deepseek.json")

// DeepseekModels is the chat model catalog for deepseek.
var DeepseekModels ai.ChatModelCatalog = dmChat

// DeepseekImageModels is the image model catalog for deepseek.
var DeepseekImageModels ai.ImageModelCatalog = dmImage

// DeepseekClassifierModels is the classifier model catalog for deepseek.
var DeepseekClassifierModels ai.ClassifierModelCatalog = dmClassifier

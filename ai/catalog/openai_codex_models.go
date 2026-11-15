// Ported from packages/ai/src/providers/openai-codex.models.ts (pi v1.0.0).

package catalog

import "github.com/keejkrej/pi-go/ai"

var ocmChat, ocmImage, ocmClassifier = mgLoadProviderCatalog(ai.KnownProviderOpenaiCodex, "openai-codex.json")

// OpenaiCodexModels is the chat model catalog for openai-codex.
var OpenaiCodexModels ai.ChatModelCatalog = ocmChat

// OpenaiCodexImageModels is the image model catalog for openai-codex.
var OpenaiCodexImageModels ai.ImageModelCatalog = ocmImage

// OpenaiCodexClassifierModels is the classifier model catalog for openai-codex.
var OpenaiCodexClassifierModels ai.ClassifierModelCatalog = ocmClassifier

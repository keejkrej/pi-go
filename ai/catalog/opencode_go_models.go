// Ported from packages/ai/src/providers/opencode-go.models.ts (pi v1.0.0).

package catalog

import "github.com/keejkrej/pi-go/ai"

var ogmChat, ogmImage, ogmClassifier = mgLoadProviderCatalog(ai.KnownProviderOpencodeGo, "opencode-go.json")

// OpencodeGoModels is the chat model catalog for opencode-go.
var OpencodeGoModels ai.ChatModelCatalog = ogmChat

// OpencodeGoImageModels is the image model catalog for opencode-go.
var OpencodeGoImageModels ai.ImageModelCatalog = ogmImage

// OpencodeGoClassifierModels is the classifier model catalog for opencode-go.
var OpencodeGoClassifierModels ai.ClassifierModelCatalog = ogmClassifier

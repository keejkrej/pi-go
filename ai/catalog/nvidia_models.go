// Ported from packages/ai/src/providers/nvidia.models.ts (pi v1.0.0).

package catalog

import "github.com/keejkrej/pi-go/ai"

var nmChat, nmImage, nmClassifier = mgLoadProviderCatalog(ai.KnownProviderNvidia, "nvidia.json")

// NvidiaModels is the chat model catalog for nvidia.
var NvidiaModels ai.ChatModelCatalog = nmChat

// NvidiaImageModels is the image model catalog for nvidia.
var NvidiaImageModels ai.ImageModelCatalog = nmImage

// NvidiaClassifierModels is the classifier model catalog for nvidia.
var NvidiaClassifierModels ai.ClassifierModelCatalog = nmClassifier

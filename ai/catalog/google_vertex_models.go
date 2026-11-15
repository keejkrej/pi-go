// Ported from packages/ai/src/providers/google-vertex.models.ts (pi v1.0.0).

package catalog

import "github.com/keejkrej/pi-go/ai"

var gvmChat, gvmImage, gvmClassifier = mgLoadProviderCatalog(ai.KnownProviderGoogleVertex, "google-vertex.json")

// GoogleVertexModels is the chat model catalog for google-vertex.
var GoogleVertexModels ai.ChatModelCatalog = gvmChat

// GoogleVertexImageModels is the image model catalog for google-vertex.
var GoogleVertexImageModels ai.ImageModelCatalog = gvmImage

// GoogleVertexClassifierModels is the classifier model catalog for google-vertex.
var GoogleVertexClassifierModels ai.ClassifierModelCatalog = gvmClassifier

// Ported from packages/ai/src/providers/ant-ling.models.ts (pi v1.0.0).

package catalog

import "github.com/keejkrej/pi-go/ai"

var almChat, almImage, almClassifier = mgLoadProviderCatalog(ai.KnownProviderAntLing, "ant-ling.json")

// AntLingModels is the chat model catalog for ant-ling.
var AntLingModels ai.ChatModelCatalog = almChat

// AntLingImageModels is the image model catalog for ant-ling.
var AntLingImageModels ai.ImageModelCatalog = almImage

// AntLingClassifierModels is the classifier model catalog for ant-ling.
var AntLingClassifierModels ai.ClassifierModelCatalog = almClassifier

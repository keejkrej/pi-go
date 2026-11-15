// Ported from packages/ai/src/providers/azure.models.ts (pi v1.5.2).

package catalog

import "github.com/keejkrej/pi-go/ai"

var azmChat, azmImage, azmClassifier = mgLoadProviderCatalog(ai.KnownProviderAzure, "azure.json")

// AzureModels is the chat model catalog for azure.
var AzureModels ai.ChatModelCatalog = azmChat

// AzureImageModels is the image model catalog for azure.
var AzureImageModels ai.ImageModelCatalog = azmImage

// AzureClassifierModels is the classifier model catalog for azure.
var AzureClassifierModels ai.ClassifierModelCatalog = azmClassifier

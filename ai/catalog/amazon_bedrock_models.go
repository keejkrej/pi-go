// Ported from packages/ai/src/providers/amazon-bedrock.models.ts (pi v1.0.0).

package catalog

import "github.com/keejkrej/pi-go/ai"

var abmChat, abmImage, abmClassifier = mgLoadProviderCatalog(ai.KnownProviderAmazonBedrock, "amazon-bedrock.json")

// AmazonBedrockModels is the chat model catalog for amazon-bedrock.
var AmazonBedrockModels ai.ChatModelCatalog = abmChat

// AmazonBedrockImageModels is the image model catalog for amazon-bedrock.
var AmazonBedrockImageModels ai.ImageModelCatalog = abmImage

// AmazonBedrockClassifierModels is the classifier model catalog for amazon-bedrock.
var AmazonBedrockClassifierModels ai.ClassifierModelCatalog = abmClassifier

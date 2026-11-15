// Ported from packages/ai/src/providers/vercel-ai-gateway.models.ts (pi v1.0.0).

package catalog

import "github.com/keejkrej/pi-go/ai"

var vagmChat, vagmImage, vagmClassifier = mgLoadProviderCatalog(ai.KnownProviderVercelAiGateway, "vercel-ai-gateway.json")

// VercelAiGatewayModels is the chat model catalog for vercel-ai-gateway.
var VercelAiGatewayModels ai.ChatModelCatalog = vagmChat

// VercelAiGatewayImageModels is the image model catalog for vercel-ai-gateway.
var VercelAiGatewayImageModels ai.ImageModelCatalog = vagmImage

// VercelAiGatewayClassifierModels is the classifier model catalog for vercel-ai-gateway.
var VercelAiGatewayClassifierModels ai.ClassifierModelCatalog = vagmClassifier

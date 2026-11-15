// Ported from packages/ai/src/providers/cloudflare-ai-gateway.models.ts (pi v1.0.0).

package catalog

import "github.com/keejkrej/pi-go/ai"

var cagmChat, cagmImage, cagmClassifier = mgLoadProviderCatalog(ai.KnownProviderCloudflareAiGateway, "cloudflare-ai-gateway.json")

// CloudflareAiGatewayModels is the chat model catalog for cloudflare-ai-gateway.
var CloudflareAiGatewayModels ai.ChatModelCatalog = cagmChat

// CloudflareAiGatewayImageModels is the image model catalog for cloudflare-ai-gateway.
var CloudflareAiGatewayImageModels ai.ImageModelCatalog = cagmImage

// CloudflareAiGatewayClassifierModels is the classifier model catalog for cloudflare-ai-gateway.
var CloudflareAiGatewayClassifierModels ai.ClassifierModelCatalog = cagmClassifier

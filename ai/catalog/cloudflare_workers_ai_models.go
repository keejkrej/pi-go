// Ported from packages/ai/src/providers/cloudflare-workers-ai.models.ts (pi v1.0.0).

package catalog

import "github.com/keejkrej/pi-go/ai"

var cwamChat, cwamImage, cwamClassifier = mgLoadProviderCatalog(ai.KnownProviderCloudflareWorkersAi, "cloudflare-workers-ai.json")

// CloudflareWorkersAiModels is the chat model catalog for cloudflare-workers-ai.
var CloudflareWorkersAiModels ai.ChatModelCatalog = cwamChat

// CloudflareWorkersAiImageModels is the image model catalog for cloudflare-workers-ai.
var CloudflareWorkersAiImageModels ai.ImageModelCatalog = cwamImage

// CloudflareWorkersAiClassifierModels is the classifier model catalog for cloudflare-workers-ai.
var CloudflareWorkersAiClassifierModels ai.ClassifierModelCatalog = cwamClassifier

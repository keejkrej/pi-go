// Ported from packages/ai/src/api/openrouter-images.lazy.ts (pi v1.5.2).

package api

import "github.com/keejkrej/pi-go/ai"

// OpenrouterImagesApi returns the ProviderImages implementation for OpenRouter image generation.
func OpenrouterImagesApi() ai.ProviderImages {
	return ai.ProviderImages{
		GenerateImages: GenerateImages,
	}
}

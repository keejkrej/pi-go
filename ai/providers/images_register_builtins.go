// Ported from packages/ai/src/providers/images/register-builtins.ts (pi v1.5.2).

package providers

import (
	"context"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
)

// GenerateImagesOpenRouter delegates image generation to ai/api's openrouter-images handler.
func GenerateImagesOpenRouter(
	ctx context.Context,
	model *ai.ImageModel,
	c *ai.ImagesContext,
	options *ai.ImagesOptions,
) (*ai.AssistantImages, error) {
	return api.GenerateImages(ctx, model, c, options)
}

// RegisterBuiltInImagesApiProviders registers built-in image generation APIs in the images registry.
func RegisterBuiltInImagesApiProviders() {
	ai.RegisterImagesApiProvider(ai.ImagesApiProvider{
		Api:            "openrouter-images",
		GenerateImages: GenerateImagesOpenRouter,
	}, nil)
}

func init() {
	RegisterBuiltInImagesApiProviders()
}

// Ported from packages/ai/src/images.ts (pi v1.5.2).

package compat

import (
	"context"
	"fmt"

	"github.com/keejkrej/pi-go/ai"
	_ "github.com/keejkrej/pi-go/ai/providers"
)

// GenerateImages runs global image generation dispatched on model.Api through the
// images API registry. Auth must be passed explicitly via options; prefer Models.GenerateImages,
// which resolves provider auth.
func GenerateImages(
	ctx context.Context,
	model *ai.ImageModel,
	c *ai.ImagesContext,
	options ai.ProviderImagesOptions,
) (*ai.AssistantImages, error) {
	if model == nil {
		return nil, fmt.Errorf("No API provider registered for api: <nil>")
	}
	provider, ok := ai.GetImagesApiProvider(ai.ImageApi(model.Api))
	if !ok || provider == nil {
		return nil, fmt.Errorf("No API provider registered for api: %s", model.Api)
	}
	var imgOpts *ai.ImagesOptions
	if options != nil {
		imgOpts = options.BaseImagesOptions()
	}
	return provider.GenerateImages(ctx, model, c, imgOpts)
}

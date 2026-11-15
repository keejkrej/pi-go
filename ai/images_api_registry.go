// Ported from packages/ai/src/images-api-registry.ts (pi v1.5.2).

package ai

import (
	"context"
	"fmt"
	"sync"
)

// ImagesApiProvider pairs an image API identifier with its ImagesFunction implementation.
type ImagesApiProvider struct {
	Api            ImageApi
	GenerateImages ImagesFunction
}

type iarRegisteredImagesApiProvider struct {
	provider ImagesApiProvider
	sourceId *string
}

var (
	iarMu       sync.RWMutex
	iarRegistry = make(map[string]iarRegisteredImagesApiProvider)
)

func iarWrapGenerateImages(api ImageApi, generateImages ImagesFunction) ImagesFunction {
	return func(ctx context.Context, model *ImageModel, context *ImagesContext, options *ImagesOptions) (*AssistantImages, error) {
		if model == nil {
			return nil, fmt.Errorf("Mismatched api: <nil> expected %s", api)
		}
		if ImageApi(model.Api) != api {
			return nil, fmt.Errorf("Mismatched api: %s expected %s", model.Api, api)
		}
		return generateImages(ctx, model, context, options)
	}
}

// RegisterImagesApiProvider registers an image generation implementation for provider.Api.
func RegisterImagesApiProvider(provider ImagesApiProvider, sourceId *string) {
	iarMu.Lock()
	defer iarMu.Unlock()
	iarRegistry[string(provider.Api)] = iarRegisteredImagesApiProvider{
		provider: ImagesApiProvider{
			Api:            provider.Api,
			GenerateImages: iarWrapGenerateImages(provider.Api, provider.GenerateImages),
		},
		sourceId: sourceId,
	}
}

// GetImagesApiProvider looks up the registered image generation provider for api.
func GetImagesApiProvider(api ImageApi) (*ImagesApiProvider, bool) {
	iarMu.RLock()
	defer iarMu.RUnlock()
	entry, ok := iarRegistry[string(api)]
	if !ok {
		return nil, false
	}
	p := entry.provider
	return &p, true
}

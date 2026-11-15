// Ported from packages/ai/src/image-models.ts (pi v1.5.2).

package compat

import (
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/catalog"
	"github.com/keejkrej/pi-go/internal/omap"
)

// BuiltinImageProvider identifies a built-in provider with at least one image model in the generated catalog.
type BuiltinImageProvider = ai.ProviderId

var imByProvider = imBuildImageModelsByProvider()

func imBuildImageModelsByProvider() *omap.Map[ai.ProviderId, *omap.Map[string, *ai.ImageModel]] {
	byProvider := omap.NewMap[ai.ProviderId, *omap.Map[string, *ai.ImageModel]]()
	if catalog.ImageModels == nil {
		return byProvider
	}
	for provider, models := range catalog.ImageModels.All() {
		if models == nil || models.Len() == 0 {
			continue
		}
		imageModels := omap.NewMap[string, *ai.ImageModel]()
		for _, model := range models.Values() {
			if model != nil {
				imageModels.Set(model.Id, model)
			}
		}
		if imageModels.Len() > 0 {
			byProvider.Set(provider, imageModels)
		}
	}
	return byProvider
}

// GetImageModel is a deprecated static catalog read. Prefer providers.GetBuiltinImageModel
// or ai.Models.GetModelOfType(ai.ModelTypeImage, ...).
func GetImageModel(provider BuiltinImageProvider, modelId string) *ai.ImageModel {
	models, ok := imByProvider.Get(provider)
	if !ok || models == nil {
		return nil
	}
	m, _ := models.Get(modelId)
	return m
}

// GetImageProviders is a deprecated static catalog read returning providers with image models.
func GetImageProviders() []BuiltinImageProvider {
	return imByProvider.Keys()
}

// GetImageModels is a deprecated static catalog read. Prefer providers.GetBuiltinImageModels
// or ai.Models.GetModelsOfType(ai.ModelTypeImage, ...).
func GetImageModels(provider BuiltinImageProvider) []*ai.ImageModel {
	models, ok := imByProvider.Get(provider)
	if !ok || models == nil {
		return []*ai.ImageModel{}
	}
	return models.Values()
}

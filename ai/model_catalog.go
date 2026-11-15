// Ported from packages/ai/src/model-catalog.ts (pi v1.5.2).

package ai

import (
	"fmt"

	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

// ChatModelCatalog maps chat model IDs to chat models in catalog order.
type ChatModelCatalog = *omap.Map[string, *Model]

// ImageModelCatalog maps image model IDs to image models in catalog order.
type ImageModelCatalog = *omap.Map[string, *ImageModel]

// ClassifierModelCatalog maps classifier model IDs to classifier models in catalog order.
type ClassifierModelCatalog = *omap.Map[string, *ClassifierModel]

// ModelGroups maps API group names to key -> model maps, matching data/*.json.
type ModelGroups = *omap.Map[string, *omap.Map[string, AnyModel]]

// UnmarshalModelGroups decodes a provider catalog JSON file into ordered ModelGroups.
func UnmarshalModelGroups(data []byte) (ModelGroups, error) {
	raw, err := jsonx.Parse(data)
	if err != nil {
		return nil, err
	}
	root, ok := raw.(*jsonx.Object)
	if !ok || root == nil {
		return nil, fmt.Errorf("expected model groups object")
	}
	groups := omap.NewMap[string, *omap.Map[string, AnyModel]]()
	for apiKey, groupRaw := range root.All() {
		groupObj, ok := groupRaw.(*jsonx.Object)
		if !ok || groupObj == nil {
			return nil, fmt.Errorf("expected model group %q object", apiKey)
		}
		models := omap.NewMap[string, AnyModel]()
		for entryKey, modelRaw := range groupObj.All() {
			model, err := DecodeAnyModel(modelRaw)
			if err != nil {
				return nil, fmt.Errorf("group %q entry %q: %w", apiKey, entryKey, err)
			}
			models.Set(entryKey, model)
		}
		groups.Set(apiKey, models)
	}
	return groups, nil
}

// FlattenChatModelCatalog extracts chat models from groups keyed by model ID in group/entry order.
func FlattenChatModelCatalog(_ ProviderId, groups ModelGroups) ChatModelCatalog {
	out := omap.NewMap[string, *Model]()
	if groups == nil {
		return out
	}
	for _, group := range groups.All() {
		if group == nil {
			continue
		}
		for _, anyModel := range group.All() {
			if m, ok := anyModel.(*Model); ok && m != nil && IsModelType(m, ModelTypeChat) {
				out.Set(m.Id, m)
			}
		}
	}
	return out
}

// FlattenImageModelCatalog extracts image models from groups keyed by model ID in group/entry order.
func FlattenImageModelCatalog(_ ProviderId, groups ModelGroups) ImageModelCatalog {
	out := omap.NewMap[string, *ImageModel]()
	if groups == nil {
		return out
	}
	for _, group := range groups.All() {
		if group == nil {
			continue
		}
		for _, anyModel := range group.All() {
			if m, ok := anyModel.(*ImageModel); ok && m != nil && IsModelType(m, ModelTypeImage) {
				out.Set(m.Id, m)
			}
		}
	}
	return out
}

// FlattenClassifierModelCatalog extracts classifier models from groups keyed by model ID in group/entry order.
func FlattenClassifierModelCatalog(_ ProviderId, groups ModelGroups) ClassifierModelCatalog {
	out := omap.NewMap[string, *ClassifierModel]()
	if groups == nil {
		return out
	}
	for _, group := range groups.All() {
		if group == nil {
			continue
		}
		for _, anyModel := range group.All() {
			if m, ok := anyModel.(*ClassifierModel); ok && m != nil && IsModelType(m, ModelTypeClassifier) {
				out.Set(m.Id, m)
			}
		}
	}
	return out
}

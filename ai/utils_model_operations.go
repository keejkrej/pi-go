// Ported from packages/ai/src/utils/model-operations.ts (pi v1.5.2).

package ai

import (
	"fmt"
	"slices"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/omap"
)

// GetModelType returns the type of a model. Models without Type are chat models.
func GetModelType(model AnyModel) ModelType {
	switch m := model.(type) {
	case *Model:
		if m.Type == nil || *m.Type == "" {
			return ModelTypeChat
		}
		return *m.Type
	case *ImageModel:
		if m.Type == "" {
			return ModelTypeImage
		}
		return m.Type
	case *ClassifierModel:
		if m.Type == "" {
			return ModelTypeClassifier
		}
		return m.Type
	case *UnknownAnyModel:
		if m.Raw != nil {
			if rawType, ok := m.Raw.Get("type"); ok {
				if s, ok := rawType.(string); ok {
					return ModelType(s)
				}
			}
		}
		return ModelTypeChat
	default:
		return ModelTypeChat
	}
}

// IsModelType checks whether model has the given ModelType (including legacy chat models without type).
func IsModelType(model AnyModel, typ ModelType) bool {
	if model == nil {
		return false
	}
	return GetModelType(model) == typ
}

func umoProviderAndId(model AnyModel) (ProviderId, string) {
	switch m := model.(type) {
	case *Model:
		return m.Provider, m.Id
	case *ImageModel:
		return m.Provider, m.Id
	case *ClassifierModel:
		return m.Provider, m.Id
	case *UnknownAnyModel:
		var p ProviderId
		var id string
		if m.Raw != nil {
			if v, ok := m.Raw.Get("provider"); ok {
				if s, ok := v.(string); ok {
					p = ProviderId(s)
				}
			}
			if v, ok := m.Raw.Get("id"); ok {
				if s, ok := v.(string); ok {
					id = s
				}
			}
		}
		return p, id
	default:
		return "", ""
	}
}

// AssertChatModel validates that model is a chat model and narrows it to *Model.
func AssertChatModel(model AnyModel) (*Model, error) {
	if m, ok := model.(*Model); ok && m != nil && IsModelType(m, ModelTypeChat) {
		return m, nil
	}
	p, id := umoProviderAndId(model)
	return nil, NewModelsError(ModelsErrorCodeProvider, fmt.Sprintf("Model %s/%s is not a chat model", p, id))
}

// AssertImageModel validates that model is an image model and narrows it to *ImageModel.
func AssertImageModel(model AnyModel) (*ImageModel, error) {
	if m, ok := model.(*ImageModel); ok && m != nil && IsModelType(m, ModelTypeImage) {
		return m, nil
	}
	p, id := umoProviderAndId(model)
	return nil, NewModelsError(ModelsErrorCodeProvider, fmt.Sprintf("Model %s/%s is not an image model", p, id))
}

// AssertClassifierModel validates that model is a classifier model and narrows it to *ClassifierModel.
func AssertClassifierModel(model AnyModel) (*ClassifierModel, error) {
	if m, ok := model.(*ClassifierModel); ok && m != nil && IsModelType(m, ModelTypeClassifier) {
		return m, nil
	}
	p, id := umoProviderAndId(model)
	return nil, NewModelsError(ModelsErrorCodeProvider, fmt.Sprintf("Model %s/%s is not a classifier model", p, id))
}

// AssertClassifierInputSupported rejects classifier images for models whose catalog entry does not accept image input.
func AssertClassifierInputSupported(model *ClassifierModel, ctx *ClassifierContext) error {
	if model == nil || ctx == nil {
		return nil
	}
	if len(ctx.Images) > 0 && !slices.Contains(model.Input, ModelModalityImage) {
		return NewModelsError(ModelsErrorCodeProvider, fmt.Sprintf("Model %s/%s does not accept image input", model.Provider, model.Id))
	}
	return nil
}

// ImageErrorResult builds an AssistantImages error/aborted result for model.
func ImageErrorResult(model *ImageModel, err error, aborted ...bool) *AssistantImages {
	stopReason := ImagesStopReasonError
	if len(aborted) > 0 && aborted[0] {
		stopReason = ImagesStopReasonAborted
	}
	var errMsg *string
	if err != nil {
		s := err.Error()
		errMsg = &s
	}
	return &AssistantImages{
		Api:          ImageApi(model.Api),
		Provider:     model.Provider,
		Model:        model.Id,
		Output:       ContentList{},
		StopReason:   stopReason,
		ErrorMessage: errMsg,
		Timestamp:    js.DateNow(),
	}
}

// ClassifierErrorResult builds a ClassifierResult error/aborted result for model.
func ClassifierErrorResult(model *ClassifierModel, err error, aborted ...bool) *ClassifierResult {
	stopReason := ClassifierStopReasonError
	if len(aborted) > 0 && aborted[0] {
		stopReason = ClassifierStopReasonAborted
	}
	var errMsg *string
	if err != nil {
		s := err.Error()
		errMsg = &s
	}
	return &ClassifierResult{
		Api:          ClassifierApi(model.Api),
		Provider:     model.Provider,
		Model:        model.Id,
		Answers:      omap.NewMap[string, ClassifierAnswer](),
		StopReason:   stopReason,
		ErrorMessage: errMsg,
		Timestamp:    js.DateNow(),
	}
}

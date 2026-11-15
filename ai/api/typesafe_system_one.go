// Ported from packages/ai/src/api/typesafe-system-one.ts (pi v1.5.2).

package api

import (
	"context"
	"errors"
	"strings"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

type tsoPayload struct {
	Model     string                 `json:"model"`
	State     any                    `json:"state"`
	Questions *omap.Map[string, any] `json:"questions"`
	Assets    *omap.Map[string, any] `json:"assets,omitzero"`
	Images    []string               `json:"images,omitzero"`
}

type tsoTransport struct{}

func (tsoTransport) Api() ai.ClassifierApi {
	return ai.ClassifierApi(ai.KnownClassifierApiTypesafeSystemOne)
}

func (tsoTransport) Label() string {
	return "System One API"
}

func (tsoTransport) DetectCompat(model *ai.ClassifierModel) ResolvedSystemOneCompat {
	if model != nil && (model.Provider == ai.KnownProviderOpenrouter || strings.Contains(model.BaseUrl, "openrouter.ai")) {
		return ResolvedSystemOneCompat{
			ImageInput:           ai.SystemOneImageInputStateImageUrls,
			SendImageOptInHeader: false,
		}
	}
	return ResolvedSystemOneCompat{
		ImageInput:           ai.SystemOneImageInputAssets,
		SendImageOptInHeader: true,
	}
}

func (tsoTransport) Url(model *ai.ClassifierModel) (string, error) {
	if model == nil {
		return "", errors.New("missing model")
	}
	return clshTrimTrailingSlashes(model.BaseUrl) + "/systemone", nil
}

func (tsoTransport) Payload(model *ai.ClassifierModel, request *SystemOneWireRequest) (any, error) {
	return tsoPayload{
		Model:     model.Id,
		State:     request.State,
		Questions: request.Questions,
		Assets:    request.Assets,
		Images:    request.Images,
	}, nil
}

func (tsoTransport) Output(body any) (*jsonx.Object, error) {
	obj, ok := body.(*jsonx.Object)
	if !ok || obj == nil {
		return nil, errors.New("System One API returned an unexpected response")
	}
	return obj, nil
}

// ClassifyTypesafeSystemOne runs TypeSafe System One classification with public `bool` values
// mapped to wire-level `noul`.
func ClassifyTypesafeSystemOne(
	ctx context.Context,
	model *ai.ClassifierModel,
	c *ai.ClassifierContext,
	options *ai.ClassifierOptions,
) (*ai.ClassifierResult, error) {
	return ClassifySystemOne(ctx, tsoTransport{}, model, c, options)
}

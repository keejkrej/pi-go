// Ported from packages/ai/src/api/cloudflare-workers-ai-system-one.ts (pi v1.5.2).

package api

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

const cwasoLabel = "Cloudflare Workers AI"

func cwasoErrorMessage(errorsVal any) string {
	if arr, ok := errorsVal.([]any); ok {
		var messages []string
		for _, item := range arr {
			if obj, ok := item.(*jsonx.Object); ok && obj != nil {
				if msg, ok := obj.GetString("message"); ok {
					messages = append(messages, msg)
				}
			}
		}
		if len(messages) > 0 {
			return fmt.Sprintf("%s error: %s", cwasoLabel, strings.Join(messages, "; "))
		}
	}
	return fmt.Sprintf("%s request failed", cwasoLabel)
}

type cwasoPayload struct {
	Model string                `json:"model"`
	Input *SystemOneWireRequest `json:"input"`
}

type cwasoTransport struct{}

func (cwasoTransport) Api() ai.ClassifierApi {
	return ai.ClassifierApi(ai.KnownClassifierApiCloudflareWorkersAiSystemOne)
}

func (cwasoTransport) Label() string {
	return cwasoLabel
}

func (cwasoTransport) DetectCompat(model *ai.ClassifierModel) ResolvedSystemOneCompat {
	imageInput := ai.SystemOneImageInputAssets
	if model != nil && strings.HasPrefix(model.Id, "@cf/cloudflare/clef") {
		imageInput = ai.SystemOneImageInputImages
	}
	return ResolvedSystemOneCompat{
		ImageInput:           imageInput,
		SendImageOptInHeader: false,
	}
}

func (cwasoTransport) Url(model *ai.ClassifierModel) (string, error) {
	if model == nil {
		return "", errors.New("missing model")
	}
	return clshTrimTrailingSlashes(model.BaseUrl) + "/run", nil
}

func (cwasoTransport) Payload(model *ai.ClassifierModel, request *SystemOneWireRequest) (any, error) {
	return cwasoPayload{
		Model: model.Id,
		Input: request,
	}, nil
}

func (cwasoTransport) Output(body any) (*jsonx.Object, error) {
	obj, ok := body.(*jsonx.Object)
	if !ok || obj == nil {
		return nil, fmt.Errorf("%s returned an unexpected response", cwasoLabel)
	}
	if successVal, hasSuccess := obj.Get("success"); hasSuccess {
		if b, isBool := successVal.(bool); isBool && !b {
			errVal, _ := obj.Get("errors")
			return nil, errors.New(cwasoErrorMessage(errVal))
		}
	}
	rawResult, _ := obj.Get("result")
	result, ok := rawResult.(*jsonx.Object)
	if !ok || result == nil {
		return nil, fmt.Errorf("%s returned an unexpected response", cwasoLabel)
	}
	if result.Has("answers") {
		return result, nil
	}
	rawState, _ := result.Get("state")
	stateStr, _ := rawState.(string)
	if stateStr != "Completed" {
		stateDesc := "undefined"
		if rawState != nil {
			stateDesc = fmt.Sprint(rawState)
		}
		return nil, fmt.Errorf("%s run did not complete (state: %s)", cwasoLabel, stateDesc)
	}
	rawInner, _ := result.Get("result")
	inner, ok := rawInner.(*jsonx.Object)
	if !ok || inner == nil {
		return nil, fmt.Errorf("%s returned an unexpected response", cwasoLabel)
	}
	return inner, nil
}

// ClassifyCloudflareWorkersAiSystemOne runs Cloudflare Workers AI System One classification
// with public `bool` values mapped to wire-level `noul`.
func ClassifyCloudflareWorkersAiSystemOne(
	ctx context.Context,
	model *ai.ClassifierModel,
	c *ai.ClassifierContext,
	options *ai.ClassifierOptions,
) (*ai.ClassifierResult, error) {
	return ClassifySystemOne(ctx, cwasoTransport{}, model, c, options)
}

// Ported from packages/ai/src/api/system-one-shared.ts (pi v1.5.2).

package api

import (
	"context"
	"fmt"
	"slices"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

type sosImageAsset struct {
	Type       string `json:"type"`
	Format     string `json:"format"`
	DataBase64 string `json:"data_base64"`
}

type sosImageUrlValue struct {
	Url string `json:"url"`
}

type sosImageUrlPart struct {
	Type     string           `json:"type"`
	ImageUrl sosImageUrlValue `json:"image_url"`
}

// SystemOneWireRequest is the System One request body without any transport-specific envelope.
type SystemOneWireRequest struct {
	State     any                    `json:"state"`
	Questions *omap.Map[string, any] `json:"questions"`
	Assets    *omap.Map[string, any] `json:"assets,omitzero"`
	Images    []string               `json:"images,omitzero"`
}

// SystemOneImageInput controls how System One services receive images.
type SystemOneImageInput = ai.SystemOneImageInput

// ResolvedSystemOneCompat holds fully resolved System One compatibility settings.
type ResolvedSystemOneCompat struct {
	ImageInput           SystemOneImageInput `json:"imageInput"`
	SendImageOptInHeader bool                `json:"sendImageOptInHeader"`
}

// SystemOneTransport defines transport differences between services serving System One models.
type SystemOneTransport interface {
	Api() ai.ClassifierApi
	Label() string
	DetectCompat(model *ai.ClassifierModel) ResolvedSystemOneCompat
	Url(model *ai.ClassifierModel) (string, error)
	Payload(model *ai.ClassifierModel, request *SystemOneWireRequest) (any, error)
	Output(body any) (*jsonx.Object, error)
}

var (
	sosAssetFormats = map[string]string{
		"image/png":  "png",
		"image/jpeg": "jpeg",
	}
	sosDataUrlTypes = map[string]bool{
		"image/png":  true,
		"image/jpeg": true,
		"image/webp": true,
	}
)

const sosMaxDataUrlImages = 4

func sosProbabilities(label string, value any, id string) (*omap.Map[string, float64], error) {
	if !IsRecord(value) {
		return nil, fmt.Errorf("%s returned invalid probabilities for %s", label, id)
	}
	obj, _ := value.(*jsonx.Object)
	if obj == nil {
		return nil, fmt.Errorf("%s returned invalid probabilities for %s", label, id)
	}
	out := omap.NewMap[string, float64]()
	for _, key := range obj.Keys() {
		rawProb, _ := obj.Get(key)
		prob, err := RequiredNumber(label, rawProb, fmt.Sprintf("probability for %s.%s", id, key))
		if err != nil {
			return nil, err
		}
		out.Set(key, prob)
	}
	return out, nil
}

func sosParseAnswers(label string, value any, context *ai.ClassifierContext) (*omap.Map[string, ai.ClassifierAnswer], error) {
	if !IsRecord(value) {
		return nil, fmt.Errorf("%s returned an unexpected response", label)
	}
	obj, _ := value.(*jsonx.Object)
	if obj == nil {
		return nil, fmt.Errorf("%s returned an unexpected response", label)
	}
	answers := omap.NewMap[string, ai.ClassifierAnswer]()
	if context == nil || context.Questions == nil {
		return answers, nil
	}
	for id, question := range context.Questions.All() {
		rawAnswer, ok := obj.Get(id)
		if !ok || !IsRecord(rawAnswer) {
			return nil, fmt.Errorf("%s did not return an answer for %s", label, id)
		}
		ansObj, _ := rawAnswer.(*jsonx.Object)
		if ansObj == nil {
			return nil, fmt.Errorf("%s did not return an answer for %s", label, id)
		}
		ansType, _ := ansObj.GetString("type")
		switch question.(type) {
		case *ai.ClassifierChoiceQuestion:
			choice, hasChoice := ansObj.GetString("choice")
			if ansType != "choice" || !hasChoice {
				return nil, fmt.Errorf("%s did not return a choice answer for %s", label, id)
			}
			rawProbs, _ := ansObj.Get("probabilities")
			probs, err := sosProbabilities(label, rawProbs, id)
			if err != nil {
				return nil, err
			}
			rawConf, _ := ansObj.Get("confidence")
			conf, err := RequiredNumber(label, rawConf, fmt.Sprintf("confidence for %s", id))
			if err != nil {
				return nil, err
			}
			answers.Set(id, &ai.ClassifierChoiceAnswer{
				Type:          "choice",
				Choice:        choice,
				Probabilities: probs,
				Confidence:    conf,
			})
		case *ai.ClassifierScoreQuestion:
			if ansType != "score" {
				return nil, fmt.Errorf("%s did not return a score answer for %s", label, id)
			}
			rawScore, _ := ansObj.Get("score")
			score, err := RequiredNumber(label, rawScore, fmt.Sprintf("score for %s", id))
			if err != nil {
				return nil, err
			}
			rawConf, _ := ansObj.Get("confidence")
			conf, err := RequiredNumber(label, rawConf, fmt.Sprintf("confidence for %s", id))
			if err != nil {
				return nil, err
			}
			answers.Set(id, &ai.ClassifierScoreAnswer{
				Type:       "score",
				Score:      score,
				Confidence: conf,
			})
		default:
			if ansType != "noul" {
				return nil, fmt.Errorf("%s did not return a bool answer for %s", label, id)
			}
			rawNoul, _ := ansObj.Get("noul")
			prob, err := RequiredNumber(label, rawNoul, fmt.Sprintf("probability for %s", id))
			if err != nil {
				return nil, err
			}
			answers.Set(id, &ai.ClassifierBoolAnswer{
				Type:        "bool",
				Probability: prob,
			})
		}
	}
	return answers, nil
}

func sosAddImages(
	label string,
	model *ai.ClassifierModel,
	input SystemOneImageInput,
	images []*ai.ImageContent,
	request *SystemOneWireRequest,
) error {
	if input == ai.SystemOneImageInputImages {
		if len(images) > sosMaxDataUrlImages {
			return fmt.Errorf("%s accepts at most %d images for %s, got %d", label, sosMaxDataUrlImages, model.Id, len(images))
		}
		urls := make([]string, 0, len(images))
		for _, image := range images {
			if image == nil || !sosDataUrlTypes[image.MimeType] {
				mime := ""
				if image != nil {
					mime = image.MimeType
				}
				return fmt.Errorf("%s accepts PNG, JPEG, and WebP images for %s, got %s", label, model.Id, mime)
			}
			urls = append(urls, fmt.Sprintf("data:%s;base64,%s", image.MimeType, image.Data))
		}
		request.Images = urls
		return nil
	}

	if input == ai.SystemOneImageInputStateImageUrls {
		stateSlice := make([]any, 0, len(images)+1)
		for _, image := range images {
			if image == nil {
				continue
			}
			stateSlice = append(stateSlice, sosImageUrlPart{
				Type:     "image_url",
				ImageUrl: sosImageUrlValue{Url: fmt.Sprintf("data:%s;base64,%s", image.MimeType, image.Data)},
			})
		}
		stateSlice = append(stateSlice, request.State)
		request.State = stateSlice
		return nil
	}

	assets := omap.NewMap[string, any]()
	stateSlice := make([]any, 0, len(images)+1)
	for index, image := range images {
		mime := ""
		data := ""
		if image != nil {
			mime = image.MimeType
			data = image.Data
		}
		format, ok := sosAssetFormats[mime]
		if !ok {
			return fmt.Errorf("%s accepts PNG and JPEG images for %s, got %s", label, model.Id, mime)
		}
		name := fmt.Sprintf("<image-%d>", index+1)
		assets.Set(name, sosImageAsset{
			Type:       "image",
			Format:     format,
			DataBase64: data,
		})
		stateSlice = append(stateSlice, name)
	}
	request.Assets = assets
	stateSlice = append(stateSlice, request.State)
	request.State = stateSlice
	return nil
}

func sosWireQuestion(question ai.ClassifierQuestion) any {
	if bq, ok := question.(*ai.ClassifierBoolQuestion); ok && bq != nil {
		return &ai.ClassifierBoolQuestion{
			Type:         "noul",
			Instructions: bq.Instructions,
			Criteria:     bq.Criteria,
		}
	}
	return question
}

func sosWireRequest(context *ai.ClassifierContext) *SystemOneWireRequest {
	questions := omap.NewMap[string, any]()
	if context != nil && context.Questions != nil {
		for id, question := range context.Questions.All() {
			questions.Set(id, sosWireQuestion(question))
		}
	}
	var state any
	if context != nil && context.State != nil {
		state = context.State
	} else {
		state = jsonx.NewObject()
	}
	return &SystemOneWireRequest{
		State:     state,
		Questions: questions,
	}
}

func sosResolveCompat(model *ai.ClassifierModel, detected ResolvedSystemOneCompat) ResolvedSystemOneCompat {
	resolved := detected
	if model == nil || model.Compat.V == nil {
		return resolved
	}
	switch c := model.Compat.V.(type) {
	case *ai.SystemOneCompat:
		if c != nil {
			if c.ImageInput != nil {
				resolved.ImageInput = *c.ImageInput
			}
			if c.SendImageOptInHeader != nil {
				resolved.SendImageOptInHeader = *c.SendImageOptInHeader
			}
		}
	case *ai.ProviderCompat:
		if c != nil {
			if c.ImageInput != nil {
				resolved.ImageInput = *c.ImageInput
			}
			if c.SendImageOptInHeader != nil {
				resolved.SendImageOptInHeader = *c.SendImageOptInHeader
			}
		}
	}
	return resolved
}

// ClassifySystemOne executes one System One classification over the supplied transport.
func ClassifySystemOne(
	ctx context.Context,
	transport SystemOneTransport,
	model *ai.ClassifierModel,
	context *ai.ClassifierContext,
	options *ai.ClassifierOptions,
) (*ai.ClassifierResult, error) {
	var apiName ai.ClassifierApi
	var provider ai.ProviderId
	var modelId string
	if model != nil {
		apiName = ai.ClassifierApi(model.Api)
		provider = model.Provider
		modelId = model.Id
	}

	output := &ai.ClassifierResult{
		Api:        apiName,
		Provider:   provider,
		Model:      modelId,
		Answers:    omap.NewMap[string, ai.ClassifierAnswer](),
		StopReason: ai.ClassifierStopReasonStop,
		Timestamp:  js.DateNow(),
	}

	fail := func(err error) (*ai.ClassifierResult, error) {
		reason := ai.ClassifierStopReasonError
		if ctx != nil && ctx.Err() != nil {
			reason = ai.ClassifierStopReasonAborted
		}
		output.StopReason = reason
		errMsg := ai.FormatProviderError(ai.NormalizeProviderError(err), transport.Label()+" error")
		output.ErrorMessage = &errMsg
		return output, nil
	}

	if transport == nil {
		return fail(fmt.Errorf("Unsupported classifier transport: <nil>"))
	}
	if model == nil || ai.ClassifierApi(model.Api) != transport.Api() {
		return fail(fmt.Errorf("Unsupported classifier API: %s", apiName))
	}

	req := sosWireRequest(context)
	var requestOptions *ai.ClassifierOptions
	if options != nil {
		cp := *options
		requestOptions = &cp
	} else {
		requestOptions = &ai.ClassifierOptions{}
	}

	if context != nil && len(context.Images) > 0 {
		if !slices.Contains(model.Input, ai.ModelModalityImage) {
			return fail(fmt.Errorf("%s does not support image input for %s", transport.Label(), model.Id))
		}
		resolvedCompat := sosResolveCompat(model, transport.DetectCompat(model))
		if err := sosAddImages(transport.Label(), model, resolvedCompat.ImageInput, context.Images, req); err != nil {
			return fail(err)
		}
		if resolvedCompat.SendImageOptInHeader {
			requestOptions.Headers = clshToProviderHeaders(ai.ProviderHeadersToOrderedRecord(
				clshStaticProviderHeaders("TypeSafe-Danger-Zone-Images", "true"),
				requestOptions.Headers,
			))
		}
	}

	rawUrl, err := transport.Url(model)
	if err != nil {
		return fail(err)
	}
	payload, err := transport.Payload(model, req)
	if err != nil {
		return fail(err)
	}
	body, err := PostClassifierRequest(ctx, transport.Label(), rawUrl, model, payload, requestOptions, nil)
	if err != nil {
		return fail(err)
	}
	resultObj, err := transport.Output(body)
	if err != nil {
		return fail(err)
	}
	// Set before parsing answers: a request with malformed answers was still billed.
	rawUsage, _ := resultObj.Get("usage")
	if usage := ParseClassifierUsage(rawUsage, model); usage != nil {
		output.Usage = usage
	}
	rawAnswers, _ := resultObj.Get("answers")
	answers, err := sosParseAnswers(transport.Label(), rawAnswers, context)
	if err != nil {
		return fail(err)
	}
	output.Answers = answers
	return output, nil
}

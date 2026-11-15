// Ported from packages/ai/test/openrouter-images.test.ts (pi v1.5.2).

package compat_test

import (
	"context"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/compat"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

type toiRoundTripFunc func(*http.Request) (*http.Response, error)

func (f toiRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func toiNewMockClient(lastParams **jsonx.Object, sawCancelledCtx *bool) *http.Client {
	return &http.Client{
		Transport: toiRoundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Context() != nil && r.Context().Err() != nil {
				if sawCancelledCtx != nil {
					*sawCancelledCtx = true
				}
				return nil, r.Context().Err()
			}
			if r.Body != nil {
				raw, err := io.ReadAll(r.Body)
				if err == nil && len(raw) > 0 {
					if parsed, perr := jsonx.Parse(raw); perr == nil {
						if obj, ok := parsed.(*jsonx.Object); ok {
							*lastParams = obj
						}
					}
				}
			}
			respJSON := `{
				"id": "img-1",
				"usage": {
					"prompt_tokens": 12,
					"completion_tokens": 34,
					"prompt_tokens_details": { "cached_tokens": 0 }
				},
				"choices": [
					{
						"message": {
							"content": "Here is your image.",
							"images": [{ "image_url": "data:image/png;base64,ZmFrZS1wbmc=" }]
						}
					}
				]
			}`
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(respJSON)),
			}, nil
		}),
	}
}

func toiToStringSlice(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func TestOpenRouterImages_TextAndImageOutput(t *testing.T) {
	headers := omap.NewMap[string, string]()
	headers.Set("HTTP-Referer", "https://example.com")
	model := &ai.ImageModel{
		BaseModel: ai.BaseModel{
			Id:       "google/gemini-3.1-flash-image-preview",
			Name:     "Gemini 3.1 Flash Image Preview",
			Api:      string(ai.KnownImageApiOpenrouterImages),
			Provider: "openrouter",
			BaseUrl:  "https://openrouter.ai/api/v1",
			Input:    []ai.ModelModality{ai.ModelModalityText, ai.ModelModalityImage},
			Cost:     ai.ModelCost{ModelCostRates: ai.ModelCostRates{Input: 0.015, Output: 0.03}},
			Headers:  headers,
		},
		Type:   ai.ModelTypeImage,
		Output: []ai.ModelModality{ai.ModelModalityText, ai.ModelModalityImage},
	}
	imgCtx := &ai.ImagesContext{
		Input: ai.ContentList{&ai.TextContent{Type: "text", Text: "Generate a dog"}},
	}

	var lastParams *jsonx.Object
	apiKey := "test"
	output, err := compat.GenerateImages(context.Background(), model, imgCtx, &ai.ImagesOptions{
		ProviderRequestOptions: ai.ProviderRequestOptions[ai.ImageModel]{
			ApiKey:     &apiKey,
			HTTPClient: toiNewMockClient(&lastParams, nil),
		},
	})
	if err != nil {
		t.Fatalf("GenerateImages returned error: %v", err)
	}
	if output.StopReason != ai.ImagesStopReasonStop {
		t.Fatalf("stopReason = %q, want stop (err=%v)", output.StopReason, output.ErrorMessage)
	}
	if output.ResponseId == nil || *output.ResponseId != "img-1" {
		t.Fatalf("responseId = %v, want img-1", output.ResponseId)
	}
	if len(output.Output) != 2 {
		t.Fatalf("output len = %d, want 2", len(output.Output))
	}
	txt, ok := output.Output[0].(*ai.TextContent)
	if !ok || txt.Text != "Here is your image." {
		t.Fatalf("output[0] = %#v, want text 'Here is your image.'", output.Output[0])
	}
	img, ok := output.Output[1].(*ai.ImageContent)
	if !ok || img.MimeType != "image/png" || img.Data != "ZmFrZS1wbmc=" {
		t.Fatalf("output[1] = %#v, want image/png base64", output.Output[1])
	}

	if lastParams == nil {
		t.Fatal("expected captured HTTP request params")
	}
	if streamFlag, _ := lastParams.Get("stream"); streamFlag != false {
		t.Fatalf("params.stream = %v, want false", streamFlag)
	}
	modalitiesRaw, _ := lastParams.Get("modalities")
	if got := toiToStringSlice(modalitiesRaw); !slices.Equal(got, []string{"image", "text"}) {
		t.Fatalf("params.modalities = %v, want [image text]", got)
	}
	msgsRaw, _ := lastParams.Get("messages")
	msgs, _ := msgsRaw.([]any)
	msg0, _ := msgs[0].(*jsonx.Object)
	contentRaw, _ := msg0.Get("content")
	contentArr, _ := contentRaw.([]any)
	part0, _ := contentArr[0].(*jsonx.Object)
	if typ, _ := part0.Get("type"); typ != "text" {
		t.Fatalf("part0.type = %v, want text", typ)
	}
	if text, _ := part0.Get("text"); text != "Generate a dog" {
		t.Fatalf("part0.text = %v, want Generate a dog", text)
	}
}

func TestOpenRouterImages_AbortContext(t *testing.T) {
	model := &ai.ImageModel{
		BaseModel: ai.BaseModel{
			Id:       "black-forest-labs/flux.2-pro",
			Name:     "FLUX.2 Pro",
			Api:      string(ai.KnownImageApiOpenrouterImages),
			Provider: "openrouter",
			BaseUrl:  "https://openrouter.ai/api/v1",
			Input:    []ai.ModelModality{ai.ModelModalityText, ai.ModelModalityImage},
			Cost:     ai.ModelCost{ModelCostRates: ai.ModelCostRates{Input: 0.015, Output: 0.03}},
		},
		Type:   ai.ModelTypeImage,
		Output: []ai.ModelModality{ai.ModelModalityImage},
	}
	imgCtx := &ai.ImagesContext{
		Input: ai.ContentList{&ai.TextContent{Type: "text", Text: "Generate a dog"}},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var lastParams *jsonx.Object
	var sawCancelledCtx bool
	apiKey := "test"
	output, err := compat.GenerateImages(ctx, model, imgCtx, &ai.ImagesOptions{
		ProviderRequestOptions: ai.ProviderRequestOptions[ai.ImageModel]{
			ApiKey:     &apiKey,
			HTTPClient: toiNewMockClient(&lastParams, &sawCancelledCtx),
		},
	})
	if err != nil {
		t.Fatalf("GenerateImages returned error: %v", err)
	}
	if output.StopReason != ai.ImagesStopReasonAborted {
		t.Fatalf("stopReason = %q, want aborted", output.StopReason)
	}
	if output.ErrorMessage == nil || *output.ErrorMessage == "" {
		t.Fatalf("expected non-empty errorMessage on aborted result")
	}
	_ = sawCancelledCtx
}

func TestOpenRouterImages_ImageOnlyModalities(t *testing.T) {
	model := &ai.ImageModel{
		BaseModel: ai.BaseModel{
			Id:       "black-forest-labs/flux.2-pro",
			Name:     "FLUX.2 Pro",
			Api:      string(ai.KnownImageApiOpenrouterImages),
			Provider: "openrouter",
			BaseUrl:  "https://openrouter.ai/api/v1",
			Input:    []ai.ModelModality{ai.ModelModalityText, ai.ModelModalityImage},
			Cost:     ai.ModelCost{ModelCostRates: ai.ModelCostRates{Input: 0.015, Output: 0.03}},
		},
		Type:   ai.ModelTypeImage,
		Output: []ai.ModelModality{ai.ModelModalityImage},
	}
	imgCtx := &ai.ImagesContext{
		Input: ai.ContentList{&ai.TextContent{Type: "text", Text: "Generate a dog"}},
	}

	var lastParams *jsonx.Object
	apiKey := "test"
	output, err := compat.GenerateImages(context.Background(), model, imgCtx, &ai.ImagesOptions{
		ProviderRequestOptions: ai.ProviderRequestOptions[ai.ImageModel]{
			ApiKey:     &apiKey,
			HTTPClient: toiNewMockClient(&lastParams, nil),
		},
	})
	if err != nil {
		t.Fatalf("GenerateImages returned error: %v", err)
	}
	hasImage := false
	for _, item := range output.Output {
		if _, ok := item.(*ai.ImageContent); ok {
			hasImage = true
			break
		}
	}
	if !hasImage {
		t.Fatal("expected image item in output")
	}
	modalitiesRaw, _ := lastParams.Get("modalities")
	if got := toiToStringSlice(modalitiesRaw); !slices.Equal(got, []string{"image"}) {
		t.Fatalf("image-only modalities = %v, want [image]", got)
	}
}

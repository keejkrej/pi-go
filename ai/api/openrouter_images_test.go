// Ported from packages/ai/test/openrouter-images.test.ts (pi v1.5.2).

package api_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func toitModel(baseUrl string, output []ai.ModelModality) *ai.ImageModel {
	return &ai.ImageModel{
		BaseModel: ai.BaseModel{
			Id:       "google/gemini-2.5-flash-image",
			Name:     "Gemini Image",
			Api:      "openrouter-images",
			Provider: "openrouter",
			BaseUrl:  baseUrl,
			Input:    []ai.ModelModality{ai.ModelModalityText, ai.ModelModalityImage},
			Cost:     ai.ModelCost{Input: 1.0, Output: 2.0},
		},
		Type:   ai.ModelTypeImage,
		Output: output,
	}
}

func TestOpenrouterImagesGenerate(t *testing.T) {
	var seenBody map[string]any
	var seenPath string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = jsonx.Unmarshal(raw, &seenBody)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "gen-123",
			"usage": {
				"prompt_tokens": 100,
				"completion_tokens": 50,
				"cost": 0.045,
				"cost_details": {"upstream_inference_cost": 0.04}
			},
			"choices": [{
				"message": {
					"content": "Here is your cat:",
					"images": [
						{"type": "image_url", "image_url": {"url": "data:image/png;base64,iVBORw0KGgo="}},
						{"type": "image_url", "image_url": "data:image/jpeg;base64,/9j/4AAQ="}
					]
				}
			}]
		}`))
	}))
	defer srv.Close()

	apiKey := "or-key"
	imgApi := api.OpenrouterImagesApi()
	model := toitModel(srv.URL+"/v1/", []ai.ModelModality{ai.ModelModalityImage, ai.ModelModalityText})
	res, err := imgApi.GenerateImages(context.Background(), model, &ai.ImagesContext{
		Input: ai.ContentList{
			&ai.TextContent{Type: "text", Text: "Draw a cat"},
			&ai.ImageContent{Type: "image", MimeType: "image/png", Data: "REF"},
		},
	}, &ai.ImagesOptions{
		ProviderRequestOptions: ai.ProviderRequestOptions[ai.ImageModel]{
			ApiKey: &apiKey,
		},
	})
	if err != nil {
		t.Fatalf("GenerateImages error: %v", err)
	}
	if res.StopReason != ai.ImagesStopReasonStop {
		t.Fatalf("unexpected stopReason %q: %v", res.StopReason, res.ErrorMessage)
	}
	if seenPath != "/v1/chat/completions" {
		t.Fatalf("expected /v1/chat/completions, got %q", seenPath)
	}
	if len(res.Output) != 3 {
		t.Fatalf("expected 1 text + 2 image outputs, got %d (%+v)", len(res.Output), res.Output)
	}
	if txt, ok := res.Output[0].(*ai.TextContent); !ok || txt.Text != "Here is your cat:" {
		t.Fatalf("unexpected first block: %#v", res.Output[0])
	}
	if img1, ok := res.Output[1].(*ai.ImageContent); !ok || img1.MimeType != "image/png" || img1.Data != "iVBORw0KGgo=" {
		t.Fatalf("unexpected second block: %#v", res.Output[1])
	}
	if img2, ok := res.Output[2].(*ai.ImageContent); !ok || img2.MimeType != "image/jpeg" || img2.Data != "/9j/4AAQ=" {
		t.Fatalf("unexpected third block: %#v", res.Output[2])
	}
	if res.ResponseId == nil || *res.ResponseId != "gen-123" {
		t.Fatalf("unexpected responseId: %v", res.ResponseId)
	}
	if res.Usage == nil || res.Usage.TotalTokens != 150 {
		t.Fatalf("unexpected usage: %+v", res.Usage)
	}

	// Missing API key yields ImagesStopReasonError without panic.
	resNoKey, err := imgApi.GenerateImages(context.Background(), model, &ai.ImagesContext{
		Input: ai.ContentList{&ai.TextContent{Type: "text", Text: "hi"}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resNoKey.StopReason != ai.ImagesStopReasonError || resNoKey.ErrorMessage == nil || !strings.Contains(*resNoKey.ErrorMessage, "No API key") {
		t.Fatalf("expected missing api key error, got %+v", resNoKey)
	}
}

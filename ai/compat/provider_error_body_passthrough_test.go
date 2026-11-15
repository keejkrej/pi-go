// Ported from packages/ai/test/provider-error-body-passthrough.test.ts (pi v1.5.2).

package compat_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/compat"
)

type tpebpRoundTripFunc func(*http.Request) (*http.Response, error)

func (f tpebpRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestProviderErrorBodyPassthrough_OpenRouterImagesSurfacesHTTPBody(t *testing.T) {
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

	client := &http.Client{
		Transport: tpebpRoundTripFunc(func(_ *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusForbidden,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"error":"blocked by gateway WAF"}`)),
			}, nil
		}),
	}

	apiKey := "test"
	output, err := compat.GenerateImages(context.Background(), model, imgCtx, &ai.ImagesOptions{
		ProviderRequestOptions: ai.ProviderRequestOptions[ai.ImageModel]{
			ApiKey:     &apiKey,
			HTTPClient: client,
		},
	})
	if err != nil {
		t.Fatalf("GenerateImages returned error: %v", err)
	}
	if output.StopReason != ai.ImagesStopReasonError {
		t.Fatalf("stopReason = %q, want error", output.StopReason)
	}
	if output.ErrorMessage == nil {
		t.Fatal("expected non-nil errorMessage")
	}
	if !strings.Contains(*output.ErrorMessage, "403") {
		t.Fatalf("errorMessage = %q, want status 403", *output.ErrorMessage)
	}
	if !strings.Contains(*output.ErrorMessage, "blocked by gateway WAF") {
		t.Fatalf("errorMessage = %q, want body reason", *output.ErrorMessage)
	}
	if *output.ErrorMessage == "403 status code (no body)" {
		t.Fatalf("errorMessage must not be opaque SDK fallback: %q", *output.ErrorMessage)
	}
}

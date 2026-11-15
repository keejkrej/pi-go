// Ported from packages/ai/test/sampling-options.test.ts (pi v1.5.2).

package compat_test

import (
	"context"
	"errors"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/ai/compat"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

var tsoErrPayloadCaptured = errors.New("payload captured")

func tsoPtr[T any](v T) *T { return &v }

func tsoMakeContext() *ai.Context {
	return &ai.Context{
		Messages: ai.MessageList{
			&ai.UserMessage{
				Role:      "user",
				Content:   ai.UserMessageContent{Text: tsoPtr("Hello")},
				Timestamp: js.DateNow(),
			},
		},
	}
}

func tsoObj(kv ...any) *jsonx.Object {
	o := jsonx.NewObject()
	for i := 0; i+1 < len(kv); i += 2 {
		o.Set(kv[i].(string), kv[i+1])
	}
	return o
}

func tsoMakeModel(apiName string, samplingParams *jsonx.Object, modify func(*ai.Model)) *ai.Model {
	m := &ai.Model{
		BaseModel: ai.BaseModel{
			Id:       "custom-model",
			Name:     "Custom Model",
			Api:      apiName,
			Provider: "custom-provider",
			BaseUrl:  "http://127.0.0.1:9/v1",
			Input:    []ai.ModelModality{ai.ModelModalityText},
		},
		Reasoning:      false,
		ContextWindow:  128000,
		MaxTokens:      16384,
		SamplingParams: samplingParams,
	}
	if modify != nil {
		modify(m)
	}
	return m
}

func tsoCaptureStream(t *testing.T, model *ai.Model, opts ai.ProviderStreamOptions) *jsonx.Object {
	t.Helper()
	var captured *jsonx.Object
	if opts == nil {
		opts = &ai.StreamOptions{}
	}
	base := opts.BaseStreamOptions()
	apiKey := "fake-key"
	base.ApiKey = &apiKey
	base.OnPayload = func(payload any, _ *ai.Model) (any, error) {
		if obj, ok := payload.(*jsonx.Object); ok {
			captured = obj
		}
		return nil, tsoErrPayloadCaptured
	}
	_, _ = compat.Complete(context.Background(), model, tsoMakeContext(), opts)
	if captured == nil {
		t.Fatalf("expected payload captured for api=%s", model.Api)
	}
	return captured
}

func tsoCaptureSimple(t *testing.T, model *ai.Model, opts *ai.SimpleStreamOptions) *jsonx.Object {
	t.Helper()
	var cp ai.SimpleStreamOptions
	if opts != nil {
		cp = *opts
	}
	apiKey := "fake-key"
	cp.ApiKey = &apiKey
	var captured *jsonx.Object
	cp.OnPayload = func(payload any, _ *ai.Model) (any, error) {
		if obj, ok := payload.(*jsonx.Object); ok {
			captured = obj
		}
		return nil, tsoErrPayloadCaptured
	}
	_, _ = compat.CompleteSimple(context.Background(), model, tsoMakeContext(), &cp)
	if captured == nil {
		t.Fatalf("expected simple payload captured for api=%s", model.Api)
	}
	return captured
}

func tsoDirectOptions(apiName string, samplingParams *jsonx.Object, effort *ai.ThinkingLevel, summary *string) ai.ProviderStreamOptions {
	base := ai.StreamOptions{SamplingParams: samplingParams}
	switch apiName {
	case "openai-completions":
		return &api.OpenAICompletionsOptions{
			StreamOptions:   base,
			ReasoningEffort: effort,
		}
	case "openai-responses":
		return &api.OpenAIResponsesOptions{
			StreamOptions:    base,
			ReasoningEffort:  effort,
			ReasoningSummary: summary,
		}
	case "azure-openai-responses":
		return &api.AzureOpenAIResponsesOptions{
			AzureEndpointOptions: api.AzureEndpointOptions{StreamOptions: base},
			ReasoningEffort:      effort,
			ReasoningSummary:     summary,
		}
	default:
		return &base
	}
}

func TestSamplingOptions(t *testing.T) {
	t.Run("merges request sampling params into the request body", func(t *testing.T) {
		p := tsoCaptureStream(t, tsoMakeModel("openai-completions", nil, nil), &ai.StreamOptions{
			SamplingParams: tsoObj("top_p", 0.95, "top_k", float64(0), "min_p", float64(0)),
		})
		if v, _ := p.Get("top_p"); v != 0.95 {
			t.Fatalf("top_p = %v", v)
		}
		if v, _ := p.Get("top_k"); v != float64(0) && v != 0 {
			t.Fatalf("top_k = %v", v)
		}
		if v, _ := p.Get("min_p"); v != float64(0) && v != 0 {
			t.Fatalf("min_p = %v", v)
		}
	})

	t.Run("omits sampling params when neither options nor model set them", func(t *testing.T) {
		p := tsoCaptureStream(t, tsoMakeModel("openai-completions", nil, nil), nil)
		if _, has := p.Get("temperature"); has {
			t.Fatal("unexpected temperature")
		}
		if _, has := p.Get("top_p"); has {
			t.Fatal("unexpected top_p")
		}
	})

	for _, apiName := range []string{"openai-completions", "openai-responses", "azure-openai-responses"} {
		t.Run("applies model-level sampling params with request precedence for "+apiName, func(t *testing.T) {
			m := tsoMakeModel(apiName, tsoObj("top_p", 0.95, "min_p", 0.05), nil)
			p := tsoCaptureStream(t, m, &ai.StreamOptions{
				SamplingParams: tsoObj("top_p", 0.5),
			})
			if v, _ := p.Get("top_p"); v != 0.5 {
				t.Fatalf("top_p = %v, want 0.5", v)
			}
			if v, _ := p.Get("min_p"); v != 0.05 {
				t.Fatalf("min_p = %v, want 0.05", v)
			}
		})
	}

	t.Run("passes request sampling params through streamSimple", func(t *testing.T) {
		p := tsoCaptureSimple(t, tsoMakeModel("openai-completions", nil, nil), &ai.SimpleStreamOptions{
			StreamOptions: ai.StreamOptions{SamplingParams: tsoObj("top_p", 0.5)},
		})
		if v, _ := p.Get("top_p"); v != 0.5 {
			t.Fatalf("top_p = %v, want 0.5", v)
		}
	})

	t.Run("applies sampling params for the effective thinking level over model defaults", func(t *testing.T) {
		m := tsoMakeModel("openai-completions", tsoObj("temperature", float64(1), "top_p", 0.95), func(m *ai.Model) {
			m.Reasoning = true
			m.ThinkingLevelMap = &ai.ThinkingLevelMap{
				Low:    jsonx.Null[string](),
				Medium: jsonx.Null[string](),
			}
			m.SamplingParamsByThinkingLevel = &ai.SamplingParamsByThinkingLevel{
				High: tsoObj("temperature", 0.8, "top_k", float64(64)),
			}
		})
		low := ai.ThinkingLevelLow
		p := tsoCaptureSimple(t, m, &ai.SimpleStreamOptions{Reasoning: &low})
		if v, _ := p.Get("temperature"); v != 0.8 {
			t.Fatalf("temperature = %v, want 0.8", v)
		}
		if v, _ := p.Get("top_p"); v != 0.95 {
			t.Fatalf("top_p = %v, want 0.95", v)
		}
		if v, _ := p.Get("top_k"); v != float64(64) && v != 64 {
			t.Fatalf("top_k = %v, want 64", v)
		}
	})

	t.Run("applies off sampling params when reasoning is disabled", func(t *testing.T) {
		m := tsoMakeModel("openai-completions", nil, func(m *ai.Model) {
			m.SamplingParamsByThinkingLevel = &ai.SamplingParamsByThinkingLevel{
				Off: tsoObj("temperature", 0.7),
			}
		})
		p := tsoCaptureSimple(t, m, nil)
		if v, _ := p.Get("temperature"); v != 0.7 {
			t.Fatalf("temperature = %v, want 0.7", v)
		}
	})

	t.Run("merges stream-option keys over thinking-level keys", func(t *testing.T) {
		m := tsoMakeModel("openai-completions", nil, func(m *ai.Model) {
			m.Reasoning = true
			m.SamplingParamsByThinkingLevel = &ai.SamplingParamsByThinkingLevel{
				Low: tsoObj("temperature", 0.6, "top_p", 0.95),
			}
		})
		low := ai.ThinkingLevelLow
		p := tsoCaptureSimple(t, m, &ai.SimpleStreamOptions{
			Reasoning:     &low,
			StreamOptions: ai.StreamOptions{SamplingParams: tsoObj("top_p", 0.5)},
		})
		if v, _ := p.Get("temperature"); v != 0.6 {
			t.Fatalf("temperature = %v, want 0.6", v)
		}
		if v, _ := p.Get("top_p"); v != 0.5 {
			t.Fatalf("top_p = %v, want 0.5", v)
		}
	})

	for _, apiName := range []string{"openai-completions", "openai-responses", "azure-openai-responses"} {
		t.Run("applies thinking-level params between model and request params for "+apiName, func(t *testing.T) {
			m := tsoMakeModel(apiName, tsoObj("temperature", float64(1), "top_p", 0.95), func(m *ai.Model) {
				m.Reasoning = true
				m.SamplingParamsByThinkingLevel = &ai.SamplingParamsByThinkingLevel{
					Low: tsoObj("temperature", 0.6, "top_k", float64(64)),
				}
			})
			low := ai.ThinkingLevelLow
			p := tsoCaptureStream(t, m, tsoDirectOptions(apiName, tsoObj("top_p", 0.5), &low, nil))
			if v, _ := p.Get("temperature"); v != 0.6 {
				t.Fatalf("temperature = %v, want 0.6", v)
			}
			if v, _ := p.Get("top_p"); v != 0.5 {
				t.Fatalf("top_p = %v, want 0.5", v)
			}
			if v, _ := p.Get("top_k"); v != float64(64) && v != 64 {
				t.Fatalf("top_k = %v, want 64", v)
			}
		})
	}

	for _, apiName := range []string{"openai-responses", "azure-openai-responses"} {
		t.Run("uses medium sampling params for summary-only "+apiName+" requests", func(t *testing.T) {
			m := tsoMakeModel(apiName, nil, func(m *ai.Model) {
				m.Reasoning = true
				m.SamplingParamsByThinkingLevel = &ai.SamplingParamsByThinkingLevel{
					Off:    tsoObj("temperature", 0.7),
					Medium: tsoObj("temperature", 0.8),
				}
			})
			autoSummary := "auto"
			p := tsoCaptureStream(t, m, tsoDirectOptions(apiName, nil, nil, &autoSummary))
			rRaw, _ := p.Get("reasoning")
			rObj, _ := rRaw.(*jsonx.Object)
			if rObj == nil {
				t.Fatalf("expected reasoning object, got %T", rRaw)
			}
			if eff, _ := rObj.Get("effort"); eff != "medium" {
				t.Fatalf("reasoning.effort = %v, want medium", eff)
			}
			if v, _ := p.Get("temperature"); v != 0.8 {
				t.Fatalf("temperature = %v, want 0.8", v)
			}
		})
	}

	t.Run("overrides named request fields", func(t *testing.T) {
		zero := 0.0
		p := tsoCaptureStream(t, tsoMakeModel("openai-completions", nil, nil), &ai.StreamOptions{
			Temperature:    &zero,
			SamplingParams: tsoObj("temperature", float64(1)),
		})
		if v, _ := p.Get("temperature"); v != float64(1) && v != 1 {
			t.Fatalf("temperature = %v, want 1", v)
		}
	})

	t.Run("is ignored by non-OpenAI-compatible APIs", func(t *testing.T) {
		p := tsoCaptureStream(t, tsoMakeModel("anthropic-messages", nil, nil), &ai.StreamOptions{
			SamplingParams: tsoObj("top_p", 0.9, "top_k", float64(40)),
		})
		if _, has := p.Get("top_p"); has {
			t.Fatal("unexpected top_p for anthropic-messages")
		}
		if _, has := p.Get("top_k"); has {
			t.Fatal("unexpected top_k for anthropic-messages")
		}
	})
}

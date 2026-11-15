// Ported from packages/ai/test/openai-completions-provider-stream-event.test.ts (pi v1.5.2).

package compat_test

import (
	"context"
	"io"
	"math"
	"net/http"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/compat"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

type tocpseRoundTripFunc func(*http.Request) (*http.Response, error)

func (f tocpseRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func tocpsePtr[T any](v T) *T { return &v }

func tocpseSSEClient(chunks []string) *http.Client {
	var b strings.Builder
	for _, c := range chunks {
		b.WriteString("data: ")
		b.WriteString(c)
		b.WriteString("\n\n")
	}
	b.WriteString("data: [DONE]\n\n")
	body := b.String()
	return &http.Client{
		Transport: tocpseRoundTripFunc(func(_ *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header: http.Header{
					"Content-Type": []string{"text/event-stream"},
					"x-request-id": []string{"req-1"},
				},
				Body: io.NopCloser(strings.NewReader(body)),
			}, nil
		}),
	}
}

func tocpseOpenRouterModel(provider string) *ai.Model {
	if provider == "" {
		provider = "openrouter"
	}
	return &ai.Model{
		BaseModel: ai.BaseModel{
			Id:       "openrouter/auto",
			Name:     "OpenRouter Auto",
			Api:      string(ai.KnownApiOpenaiCompletions),
			Provider: ai.ProviderId(provider),
			BaseUrl:  "https://openrouter.ai/api/v1",
			Input:    []ai.ModelModality{ai.ModelModalityText},
		},
		Reasoning:     false,
		ContextWindow: 200000,
		MaxTokens:     8192,
	}
}

func tocpseToObject(t *testing.T, v any) *jsonx.Object {
	t.Helper()
	if obj, ok := v.(*jsonx.Object); ok {
		return obj
	}
	raw, err := jsonx.Marshal(v)
	if err != nil {
		t.Fatalf("jsonx.Marshal event: %v", err)
	}
	parsed, err := jsonx.Parse(raw)
	if err != nil {
		t.Fatalf("jsonx.Parse event: %v", err)
	}
	obj, _ := parsed.(*jsonx.Object)
	if obj == nil {
		t.Fatalf("expected object event, got %T", parsed)
	}
	return obj
}

func TestOpenAICompletionsProviderStreamEvent(t *testing.T) {
	t.Run("exposes provider chunks including OpenRouter metadata (#9784)", func(t *testing.T) {
		firstChunk := `{"id":"chatcmpl-1","model":"anthropic/claude-sonnet-4.6","choices":[{"index":0,"delta":{"content":"hello"}}]}`
		finalChunk := `{"id":"chatcmpl-1","model":"anthropic/claude-sonnet-4.6","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12,"cost":0.0012,"is_byok":false},"openrouter_metadata":{"strategy":"direct","region":"iad"}}`

		var events []any
		apiKey := "test"
		msg, err := compat.CompleteSimple(
			context.Background(),
			tocpseOpenRouterModel("openrouter"),
			&ai.Context{
				Messages: ai.MessageList{
					&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tocpsePtr("hi")}, Timestamp: js.DateNow()},
				},
			},
			&ai.SimpleStreamOptions{
				StreamOptions: ai.StreamOptions{
					ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
						ApiKey:     &apiKey,
						HTTPClient: tocpseSSEClient([]string{firstChunk, finalChunk}),
					},
					OnProviderStreamEvent: func(data any, _ *ai.Model) error {
						events = append(events, data)
						return nil
					},
				},
			},
		)
		if err != nil {
			t.Fatalf("CompleteSimple error: %v", err)
		}
		if len(msg.Content) != 1 {
			t.Fatalf("content len = %d, want 1", len(msg.Content))
		}
		txt, _ := msg.Content[0].(*ai.TextContent)
		if txt == nil || txt.Text != "hello" {
			t.Fatalf("content[0] = %#v, want hello", msg.Content[0])
		}
		if len(events) != 2 {
			t.Fatalf("events len = %d, want 2", len(events))
		}
		e0 := tocpseToObject(t, events[0])
		if id, _ := e0.Get("id"); id != "chatcmpl-1" {
			t.Fatalf("event[0].id = %v", id)
		}
		if m, _ := e0.Get("model"); m != "anthropic/claude-sonnet-4.6" {
			t.Fatalf("event[0].model = %v", m)
		}
		e1 := tocpseToObject(t, events[1])
		metaRaw, _ := e1.Get("openrouter_metadata")
		meta, _ := metaRaw.(*jsonx.Object)
		if meta == nil {
			t.Fatalf("expected openrouter_metadata object, got %T", metaRaw)
		}
		if s, _ := meta.Get("strategy"); s != "direct" {
			t.Fatalf("strategy = %v", s)
		}
		if r, _ := meta.Get("region"); r != "iad" {
			t.Fatalf("region = %v", r)
		}
	})

	t.Run("uses OpenRouter reported total without double-counting non-BYOK upstream cost (#9980)", func(t *testing.T) {
		chunk := `{"id":"chatcmpl-cost","model":"openrouter/auto","choices":[{"index":0,"delta":{"content":"done"},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":20,"cost":0.004,"is_byok":false,"cost_details":{"upstream_inference_cost":0.003}}}`

		apiKey := "test"
		msg, err := compat.CompleteSimple(
			context.Background(),
			tocpseOpenRouterModel("openrouter-work"),
			&ai.Context{
				Messages: ai.MessageList{
					&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tocpsePtr("hi")}, Timestamp: js.DateNow()},
				},
			},
			&ai.SimpleStreamOptions{
				StreamOptions: ai.StreamOptions{
					ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
						ApiKey:     &apiKey,
						HTTPClient: tocpseSSEClient([]string{chunk}),
					},
				},
			},
		)
		if err != nil {
			t.Fatalf("CompleteSimple error: %v", err)
		}
		cost := msg.Usage.Cost
		if cost.Input != 0 || cost.Output != 0 || cost.CacheRead != 0 || cost.CacheWrite != 0 {
			t.Fatalf("unexpected itemized cost: %+v", cost)
		}
		if math.Abs(cost.Total-0.004) > 1e-12 {
			t.Fatalf("cost.Total = %v, want 0.004", cost.Total)
		}
		if cost.TotalFromProvider == nil || !*cost.TotalFromProvider {
			t.Fatalf("cost.TotalFromProvider = %v, want true", cost.TotalFromProvider)
		}
	})

	t.Run("includes upstream inference cost for OpenRouter BYOK requests", func(t *testing.T) {
		chunk := `{"id":"chatcmpl-byok","model":"openrouter/auto","choices":[{"index":0,"delta":{"content":"done"},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":20,"cost":0.0002,"is_byok":true,"cost_details":{"upstream_inference_cost":0.003}}}`

		apiKey := "test"
		msg, err := compat.CompleteSimple(
			context.Background(),
			tocpseOpenRouterModel("openrouter"),
			&ai.Context{
				Messages: ai.MessageList{
					&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tocpsePtr("hi")}, Timestamp: js.DateNow()},
				},
			},
			&ai.SimpleStreamOptions{
				StreamOptions: ai.StreamOptions{
					ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
						ApiKey:     &apiKey,
						HTTPClient: tocpseSSEClient([]string{chunk}),
					},
				},
			},
		)
		if err != nil {
			t.Fatalf("CompleteSimple error: %v", err)
		}
		if math.Abs(msg.Usage.Cost.Total-0.0032) > 1e-10 {
			t.Fatalf("cost.Total = %v, want ~0.0032", msg.Usage.Cost.Total)
		}
		if msg.Usage.Cost.TotalFromProvider == nil || !*msg.Usage.Cost.TotalFromProvider {
			t.Fatalf("cost.TotalFromProvider = %v, want true", msg.Usage.Cost.TotalFromProvider)
		}
	})
}

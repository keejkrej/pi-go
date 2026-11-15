// Ported from packages/ai/test/openai-completions-response-model.test.ts (pi v1.5.2).

package compat_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/compat"
	"github.com/keejkrej/pi-go/internal/js"
)

type tocrmRoundTripFunc func(*http.Request) (*http.Response, error)

func (f tocrmRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func tocrmSSEClient(chunks []string) *http.Client {
	var b strings.Builder
	for _, c := range chunks {
		b.WriteString("data: ")
		b.WriteString(c)
		b.WriteString("\n\n")
	}
	b.WriteString("data: [DONE]\n\n")
	body := b.String()
	return &http.Client{
		Transport: tocrmRoundTripFunc(func(_ *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(body)),
			}, nil
		}),
	}
}

func tocrmPtr[T any](v T) *T { return &v }

func tocrmOpenRouterAuto() *ai.Model {
	return &ai.Model{
		BaseModel: ai.BaseModel{
			Id:       "openrouter/auto",
			Name:     "OpenRouter Auto",
			Api:      string(ai.KnownApiOpenaiCompletions),
			Provider: "openrouter",
			BaseUrl:  "https://openrouter.ai/api/v1",
			Input:    []ai.ModelModality{ai.ModelModalityText},
		},
		Reasoning:     false,
		ContextWindow: 200000,
		MaxTokens:     8192,
	}
}

func tocrmRun(t *testing.T, chunks []string) *ai.AssistantMessage {
	t.Helper()
	apiKey := "test"
	msg, err := compat.Complete(
		context.Background(),
		tocrmOpenRouterAuto(),
		&ai.Context{
			Messages: ai.MessageList{
				&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tocrmPtr("hi")}, Timestamp: js.DateNow()},
			},
		},
		&ai.StreamOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
				ApiKey:     &apiKey,
				HTTPClient: tocrmSSEClient(chunks),
			},
		},
	)
	if err != nil {
		t.Fatalf("Complete returned error: %v", err)
	}
	return msg
}

func TestOpenAICompletionsResponseModel(t *testing.T) {
	t.Run("surfaces routed chunk.model on responseModel without changing model", func(t *testing.T) {
		msg := tocrmRun(t, []string{
			`{"id":"chatcmpl-1","model":"anthropic/claude-opus-4.8","choices":[{"index":0,"delta":{"content":"hi"}}]}`,
			`{"id":"chatcmpl-1","model":"anthropic/claude-opus-4.8","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{"reasoning_tokens":0}}}`,
		})
		if msg.Model != "openrouter/auto" {
			t.Fatalf("model = %q, want openrouter/auto", msg.Model)
		}
		if msg.ResponseModel == nil || *msg.ResponseModel != "anthropic/claude-opus-4.8" {
			t.Fatalf("responseModel = %v, want anthropic/claude-opus-4.8", msg.ResponseModel)
		}
		if msg.Provider != "openrouter" {
			t.Fatalf("provider = %q, want openrouter", msg.Provider)
		}
		if msg.StopReason != ai.StopReasonStop {
			t.Fatalf("stopReason = %q, want stop", msg.StopReason)
		}
	})

	t.Run("leaves responseModel nil when chunks echo the requested id", func(t *testing.T) {
		msg := tocrmRun(t, []string{
			`{"id":"chatcmpl-2","model":"openrouter/auto","choices":[{"index":0,"delta":{"content":"hi"}}]}`,
			`{"id":"chatcmpl-2","model":"openrouter/auto","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{"reasoning_tokens":0}}}`,
		})
		if msg.Model != "openrouter/auto" {
			t.Fatalf("model = %q", msg.Model)
		}
		if msg.ResponseModel != nil {
			t.Fatalf("responseModel = %q, want nil", *msg.ResponseModel)
		}
	})

	t.Run("ignores empty or missing chunk.model", func(t *testing.T) {
		msg := tocrmRun(t, []string{
			`{"id":"chatcmpl-3","choices":[{"index":0,"delta":{"content":"hi"}}]}`,
			`{"id":"chatcmpl-3","model":"","choices":[{"index":0,"delta":{"content":"!"}}]}`,
			`{"id":"chatcmpl-3","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{"reasoning_tokens":0}}}`,
		})
		if msg.Model != "openrouter/auto" {
			t.Fatalf("model = %q", msg.Model)
		}
		if msg.ResponseModel != nil {
			t.Fatalf("responseModel = %q, want nil", *msg.ResponseModel)
		}
	})
}

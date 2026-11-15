// Ported from packages/ai/test/openai-completions-empty-tools.test.ts (pi v1.5.2).

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
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

func tocetPtr[T any](v T) *T { return &v }

type tocetRoundTripFunc func(*http.Request) (*http.Response, error)

func (f tocetRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type tocetCapture struct {
	params  *jsonx.Object
	url     string
	headers http.Header
}

func tocetMockClient(capOut *tocetCapture) *http.Client {
	return &http.Client{
		Transport: tocetRoundTripFunc(func(r *http.Request) (*http.Response, error) {
			capOut.url = r.URL.String()
			capOut.headers = r.Header.Clone()
			if r.Body != nil {
				raw, err := io.ReadAll(r.Body)
				if err == nil && len(raw) > 0 {
					if parsed, perr := jsonx.Parse(raw); perr == nil {
						if obj, ok := parsed.(*jsonx.Object); ok {
							capOut.params = obj
						}
					}
				}
			}
			var body string
			if strings.Contains(r.URL.Path, "/responses") {
				body = `event: response.completed
data: {"type":"response.completed","response":{"id":"resp-1","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1,"input_tokens_details":{"cached_tokens":0},"output_tokens_details":{"reasoning_tokens":0}}}}

`
			} else {
				body = `data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{"reasoning_tokens":0}}}

data: [DONE]

`
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(body)),
			}, nil
		}),
	}
}

func tocetGpt4oMiniCompletions(compatCfg *ai.OpenAICompletionsCompat) *ai.Model {
	base := compat.GetModel("openai", "gpt-4o-mini")
	if base == nil {
		return nil
	}
	m := *base
	m.Api = string(ai.KnownApiOpenaiCompletions)
	if compatCfg != nil {
		m.Compat = ai.ModelCompatValue{V: compatCfg}
	} else {
		m.Compat = ai.ModelCompatValue{}
	}
	return &m
}

func tocetInt(v any) (int, bool) {
	switch x := v.(type) {
	case float64:
		return int(x), true
	case int:
		return x, true
	case int64:
		return int(x), true
	default:
		return 0, false
	}
}

func TestOpenAICompletionsEmptyToolsAndCloudflare(t *testing.T) {
	t.Run("omits tools field when context.tools is an empty array", func(t *testing.T) {
		m := tocetGpt4oMiniCompletions(nil)
		if m == nil {
			t.Fatal("missing openai/gpt-4o-mini")
		}
		var capData tocetCapture
		apiKey := "test"
		_, err := compat.CompleteSimple(context.Background(), m, &ai.Context{
			Messages: ai.MessageList{
				&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tocetPtr("hi")}, Timestamp: js.DateNow()},
			},
			Tools: []*ai.Tool{},
		}, &ai.SimpleStreamOptions{
			StreamOptions: ai.StreamOptions{
				ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
					ApiKey:     &apiKey,
					HTTPClient: tocetMockClient(&capData),
				},
			},
		})
		if err != nil {
			t.Fatalf("CompleteSimple error: %v", err)
		}
		if capData.params == nil {
			t.Fatal("expected captured params")
		}
		if _, has := capData.params.Get("tools"); has {
			t.Fatal("expected tools to be omitted")
		}
	})

	t.Run("omits tools field when context.tools is nil", func(t *testing.T) {
		m := tocetGpt4oMiniCompletions(nil)
		var capData tocetCapture
		apiKey := "test"
		_, err := compat.CompleteSimple(context.Background(), m, &ai.Context{
			Messages: ai.MessageList{
				&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tocetPtr("hi")}, Timestamp: js.DateNow()},
			},
		}, &ai.SimpleStreamOptions{
			StreamOptions: ai.StreamOptions{
				ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
					ApiKey:     &apiKey,
					HTTPClient: tocetMockClient(&capData),
				},
			},
		})
		if err != nil {
			t.Fatalf("CompleteSimple error: %v", err)
		}
		if _, has := capData.params.Get("tools"); has {
			t.Fatal("expected tools to be omitted")
		}
	})

	t.Run("sends default maxTokens", func(t *testing.T) {
		m := tocetGpt4oMiniCompletions(nil)
		var capData tocetCapture
		apiKey := "test"
		_, err := compat.CompleteSimple(context.Background(), m, &ai.Context{
			Messages: ai.MessageList{
				&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tocetPtr("hi")}, Timestamp: js.DateNow()},
			},
		}, &ai.SimpleStreamOptions{
			StreamOptions: ai.StreamOptions{
				ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
					ApiKey:     &apiKey,
					HTTPClient: tocetMockClient(&capData),
				},
			},
		})
		if err != nil {
			t.Fatalf("CompleteSimple error: %v", err)
		}
		if _, has := capData.params.Get("max_tokens"); has {
			t.Fatal("expected max_tokens to be omitted")
		}
		v, _ := capData.params.Get("max_completion_tokens")
		if n, _ := tocetInt(v); n != m.MaxTokens {
			t.Fatalf("max_completion_tokens = %v, want %d", v, m.MaxTokens)
		}
	})

	t.Run("sends explicit maxTokens", func(t *testing.T) {
		m := tocetGpt4oMiniCompletions(nil)
		var capData tocetCapture
		apiKey := "test"
		maxTok := 1234
		_, err := compat.CompleteSimple(context.Background(), m, &ai.Context{
			Messages: ai.MessageList{
				&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tocetPtr("hi")}, Timestamp: js.DateNow()},
			},
		}, &ai.SimpleStreamOptions{
			StreamOptions: ai.StreamOptions{
				ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
					ApiKey:     &apiKey,
					HTTPClient: tocetMockClient(&capData),
				},
				MaxTokens: &maxTok,
			},
		})
		if err != nil {
			t.Fatalf("CompleteSimple error: %v", err)
		}
		if _, has := capData.params.Get("max_tokens"); has {
			t.Fatal("expected max_tokens omitted")
		}
		v, _ := capData.params.Get("max_completion_tokens")
		if n, _ := tocetInt(v); n != 1234 {
			t.Fatalf("max_completion_tokens = %v, want 1234", v)
		}
	})

	t.Run("clamps default maxTokens to remaining context", func(t *testing.T) {
		m := tocetGpt4oMiniCompletions(nil)
		m.ContextWindow = 10000
		m.MaxTokens = 8000
		var capData tocetCapture
		apiKey := "test"
		_, err := compat.CompleteSimple(context.Background(), m, &ai.Context{
			Messages: ai.MessageList{
				&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tocetPtr(strings.Repeat("x", 8000))}, Timestamp: js.DateNow()},
			},
		}, &ai.SimpleStreamOptions{
			StreamOptions: ai.StreamOptions{
				ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
					ApiKey:     &apiKey,
					HTTPClient: tocetMockClient(&capData),
				},
			},
		})
		if err != nil {
			t.Fatalf("CompleteSimple error: %v", err)
		}
		if _, has := capData.params.Get("max_tokens"); has {
			t.Fatal("expected max_tokens omitted")
		}
		v, _ := capData.params.Get("max_completion_tokens")
		if n, _ := tocetInt(v); n != 3618 {
			t.Fatalf("max_completion_tokens = %v, want 3618", v)
		}
	})

	t.Run("clamps explicit maxTokens to remaining context", func(t *testing.T) {
		m := tocetGpt4oMiniCompletions(nil)
		m.ContextWindow = 10000
		m.MaxTokens = 8000
		var capData tocetCapture
		apiKey := "test"
		maxTok := 7000
		_, err := compat.CompleteSimple(context.Background(), m, &ai.Context{
			Messages: ai.MessageList{
				&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tocetPtr(strings.Repeat("x", 8000))}, Timestamp: js.DateNow()},
			},
		}, &ai.SimpleStreamOptions{
			StreamOptions: ai.StreamOptions{
				ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
					ApiKey:     &apiKey,
					HTTPClient: tocetMockClient(&capData),
				},
				MaxTokens: &maxTok,
			},
		})
		if err != nil {
			t.Fatalf("CompleteSimple error: %v", err)
		}
		if _, has := capData.params.Get("max_tokens"); has {
			t.Fatal("expected max_tokens omitted")
		}
		v, _ := capData.params.Get("max_completion_tokens")
		if n, _ := tocetInt(v); n != 3618 {
			t.Fatalf("max_completion_tokens = %v, want 3618", v)
		}
	})

	t.Run("uses conservative OpenAI-compatible fields for Cloudflare AI Gateway /compat models", func(t *testing.T) {
		t.Setenv("CLOUDFLARE_API_KEY", "cf-token")
		t.Setenv("CLOUDFLARE_ACCOUNT_ID", "account-id")
		t.Setenv("CLOUDFLARE_GATEWAY_ID", "gateway-id")

		m := compat.GetModel("cloudflare-ai-gateway", "workers-ai/@cf/moonshotai/kimi-k2.6")
		if m == nil {
			t.Fatal("missing cloudflare-ai-gateway/workers-ai/@cf/moonshotai/kimi-k2.6")
		}
		var capData tocetCapture
		maxTok := 1234
		high := ai.ThinkingLevelHigh
		_, err := compat.CompleteSimple(context.Background(), m, &ai.Context{
			SystemPrompt: tocetPtr("You are helpful."),
			Messages: ai.MessageList{
				&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tocetPtr("hi")}, Timestamp: js.DateNow()},
			},
		}, &ai.SimpleStreamOptions{
			StreamOptions: ai.StreamOptions{
				ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
					HTTPClient: tocetMockClient(&capData),
				},
				MaxTokens: &maxTok,
			},
			Reasoning: &high,
		})
		if err != nil {
			t.Fatalf("CompleteSimple error: %v", err)
		}
		msgsRaw, _ := capData.params.Get("messages")
		msgs, _ := msgsRaw.([]any)
		if len(msgs) == 0 {
			t.Fatal("expected messages")
		}
		msg0, _ := msgs[0].(*jsonx.Object)
		if r, _ := msg0.Get("role"); r != "system" {
			t.Fatalf("messages[0].role = %v, want system", r)
		}
		if mt, _ := capData.params.Get("max_tokens"); func() int { n, _ := tocetInt(mt); return n }() != 1234 {
			t.Fatalf("max_tokens = %v, want 1234", mt)
		}
		if _, has := capData.params.Get("max_completion_tokens"); has {
			t.Fatal("unexpected max_completion_tokens")
		}
		if _, has := capData.params.Get("reasoning_effort"); has {
			t.Fatal("unexpected reasoning_effort")
		}
		if _, has := capData.params.Get("store"); has {
			t.Fatal("unexpected store")
		}
		wantPrefix := "https://gateway.ai.cloudflare.com/v1/account-id/gateway-id/compat"
		if !strings.HasPrefix(capData.url, wantPrefix) {
			t.Fatalf("url = %q, want prefix %q", capData.url, wantPrefix)
		}
		if auth := capData.headers.Get("Authorization"); auth != "" {
			t.Fatalf("expected empty Authorization header, got %q", auth)
		}
		if cfAuth := capData.headers.Get("cf-aig-authorization"); cfAuth != "Bearer cf-token" {
			t.Fatalf("cf-aig-authorization = %q, want Bearer cf-token", cfAuth)
		}
	})

	t.Run("resolves Cloudflare AI Gateway base URL through provider auth", func(t *testing.T) {
		t.Setenv("CLOUDFLARE_API_KEY", "cf-token")
		t.Setenv("CLOUDFLARE_ACCOUNT_ID", "account-id")
		t.Setenv("CLOUDFLARE_GATEWAY_ID", "gateway-id")

		m := compat.GetModel("cloudflare-ai-gateway", "workers-ai/@cf/moonshotai/kimi-k2.6")
		if m == nil {
			t.Fatal("missing model")
		}
		var capData tocetCapture
		_, err := compat.CompleteSimple(context.Background(), m, &ai.Context{
			Messages: ai.MessageList{
				&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tocetPtr("hi")}, Timestamp: js.DateNow()},
			},
		}, &ai.SimpleStreamOptions{
			StreamOptions: ai.StreamOptions{
				ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
					HTTPClient: tocetMockClient(&capData),
				},
			},
		})
		if err != nil {
			t.Fatalf("CompleteSimple error: %v", err)
		}
		wantPrefix := "https://gateway.ai.cloudflare.com/v1/account-id/gateway-id/compat"
		if !strings.HasPrefix(capData.url, wantPrefix) {
			t.Fatalf("url = %q, want prefix %q", capData.url, wantPrefix)
		}
	})

	t.Run("preserves inline upstream Authorization for Cloudflare AI Gateway BYOK requests", func(t *testing.T) {
		t.Setenv("CLOUDFLARE_API_KEY", "cf-token")
		t.Setenv("CLOUDFLARE_ACCOUNT_ID", "account-id")
		t.Setenv("CLOUDFLARE_GATEWAY_ID", "gateway-id")

		m := compat.GetModel("cloudflare-ai-gateway", "gpt-5.1")
		if m == nil {
			t.Fatal("missing cloudflare-ai-gateway/gpt-5.1")
		}
		var capData tocetCapture
		hdrs := omap.NewMap[string, *string]()
		hdrs.Set("Authorization", tocetPtr("Bearer upstream-token"))
		_, err := compat.CompleteSimple(context.Background(), m, &ai.Context{
			Messages: ai.MessageList{
				&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tocetPtr("hi")}, Timestamp: js.DateNow()},
			},
		}, &ai.SimpleStreamOptions{
			StreamOptions: ai.StreamOptions{
				ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
					HTTPClient: tocetMockClient(&capData),
					Headers:    hdrs,
				},
			},
		})
		if err != nil {
			t.Fatalf("CompleteSimple error: %v", err)
		}
		if got := capData.headers.Get("Authorization"); got != "Bearer upstream-token" {
			t.Fatalf("Authorization = %q, want Bearer upstream-token", got)
		}
		if cf := capData.headers.Get("cf-aig-authorization"); cf != "Bearer cf-token" {
			t.Fatalf("cf-aig-authorization = %q, want Bearer cf-token", cf)
		}
	})

	t.Run("sends session affinity headers for Workers AI through Cloudflare AI Gateway", func(t *testing.T) {
		t.Setenv("CLOUDFLARE_API_KEY", "cf-token")
		t.Setenv("CLOUDFLARE_ACCOUNT_ID", "account-id")
		t.Setenv("CLOUDFLARE_GATEWAY_ID", "gateway-id")

		m := compat.GetModel("cloudflare-ai-gateway", "workers-ai/@cf/moonshotai/kimi-k2.6")
		if m == nil {
			t.Fatal("missing model")
		}
		var capData tocetCapture
		sid := "session-1"
		_, err := compat.CompleteSimple(context.Background(), m, &ai.Context{
			Messages: ai.MessageList{
				&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tocetPtr("hi")}, Timestamp: js.DateNow()},
			},
		}, &ai.SimpleStreamOptions{
			StreamOptions: ai.StreamOptions{
				ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
					HTTPClient: tocetMockClient(&capData),
				},
				SessionId: &sid,
			},
		})
		if err != nil {
			t.Fatalf("CompleteSimple error: %v", err)
		}
		for _, hdr := range []string{"session_id", "x-client-request-id", "x-session-affinity"} {
			if got := capData.headers.Get(hdr); got != "session-1" {
				t.Fatalf("%s = %q, want session-1", hdr, got)
			}
		}
	})

	t.Run("tool history without tools (#11843)", func(t *testing.T) {
		toolHistoryCtx := &ai.Context{
			Messages: ai.MessageList{
				&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tocetPtr("use the tool")}, Timestamp: js.DateNow()},
				&ai.AssistantMessage{
					Role: "assistant",
					Content: ai.ContentList{
						&ai.ToolCall{Type: "toolCall", Id: "t1", Name: "noop", Arguments: jsonx.NewObject()},
					},
					StopReason: ai.StopReasonToolUse,
					Api:        ai.KnownApiOpenaiCompletions,
					Provider:   "openai",
					Model:      "gpt-4o-mini",
					Timestamp:  js.DateNow(),
				},
				&ai.ToolResultMessage{
					Role:       "toolResult",
					ToolCallId: "t1",
					ToolName:   "noop",
					Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: "done"}},
					IsError:    false,
					Timestamp:  js.DateNow(),
				},
			},
			Tools: []*ai.Tool{},
		}

		t.Run("omits tools by default", func(t *testing.T) {
			m := tocetGpt4oMiniCompletions(nil)
			var capData tocetCapture
			apiKey := "test"
			_, err := compat.CompleteSimple(context.Background(), m, toolHistoryCtx, &ai.SimpleStreamOptions{
				StreamOptions: ai.StreamOptions{
					ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
						ApiKey:     &apiKey,
						HTTPClient: tocetMockClient(&capData),
					},
				},
			})
			if err != nil {
				t.Fatalf("CompleteSimple error: %v", err)
			}
			if _, has := capData.params.Get("tools"); has {
				t.Fatal("expected tools to be omitted")
			}
		})

		t.Run("sends tools: [] when compat.requiresToolsWithToolHistory is set", func(t *testing.T) {
			req := true
			m := tocetGpt4oMiniCompletions(&ai.OpenAICompletionsCompat{
				RequiresToolsWithToolHistory: &req,
			})
			var capData tocetCapture
			apiKey := "test"
			_, err := compat.CompleteSimple(context.Background(), m, toolHistoryCtx, &ai.SimpleStreamOptions{
				StreamOptions: ai.StreamOptions{
					ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
						ApiKey:     &apiKey,
						HTTPClient: tocetMockClient(&capData),
					},
				},
			})
			if err != nil {
				t.Fatalf("CompleteSimple error: %v", err)
			}
			toolsRaw, has := capData.params.Get("tools")
			if !has {
				t.Fatal("expected tools: [] when requiresToolsWithToolHistory is true")
			}
			toolsArr, ok := toolsRaw.([]any)
			if !ok || len(toolsArr) != 0 {
				t.Fatalf("tools = %#v, want empty []any", toolsRaw)
			}
		})
	})
}

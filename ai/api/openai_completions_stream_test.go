package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

type tocsRoundTripperFunc func(*http.Request) (*http.Response, error)

func (f tocsRoundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func tocsBoolPtr(b bool) *bool    { return &b }
func tocsStrPtr(s string) *string { return &s }

func tocsSseFromChunks(chunks ...string) *http.Response {
	var sb strings.Builder
	for _, c := range chunks {
		sb.WriteString("data: ")
		sb.WriteString(c)
		sb.WriteString("\n\n")
	}
	sb.WriteString("data: [DONE]\n\n")
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(sb.String())),
	}
}

func tocsModel(provider ai.ProviderId, baseURL string, id string) *ai.Model {
	return &ai.Model{
		Id:            id,
		Name:          id,
		Api:           string(ai.KnownApiOpenaiCompletions),
		Provider:      provider,
		BaseUrl:       baseURL,
		Reasoning:     true,
		Input:         []ai.ModelModality{ai.ModelModalityText, ai.ModelModalityImage},
		ContextWindow: 128000,
		MaxTokens:     4096,
	}
}

func TestOpenAICompletionsMessageConversion(t *testing.T) {
	t.Run("RequiresNonNullAssistantContent and RequiresAssistantAfterToolResult", func(t *testing.T) {
		args := jsonx.NewObject()
		args.Set("q", "1")
		ctx := ai.NormalizeContext(&ai.Context{
			Messages: ai.MessageList{
				&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tocsStrPtr("go")}, Timestamp: 1},
				&ai.AssistantMessage{
					Role: "assistant", Api: ai.KnownApiOpenaiCompletions, Provider: "proxy", Model: "m1",
					Content:    ai.ContentList{&ai.ToolCall{Type: "toolCall", Id: "call_1", Name: "lookup", Arguments: args}},
					StopReason: ai.StopReasonToolUse, Timestamp: 2,
				},
				&ai.ToolResultMessage{
					Role: "toolResult", ToolCallId: "call_1", ToolName: "lookup",
					Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "ok"}}, Timestamp: 3,
				},
				&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tocsStrPtr("next")}, Timestamp: 4},
			},
		})

		mDefault := tocsModel("proxy", "https://proxy.example.com/v1", "m1")
		defaultMsgs, err := ConvertMessagesOpenaiCompletions(mDefault, ctx, nil, nil)
		if err != nil {
			t.Fatalf("ConvertMessagesOpenaiCompletions error: %v", err)
		}
		if val, _ := defaultMsgs[1].Get("content"); val != nil {
			t.Fatalf("expected default content=null on tool-call-only assistant message, got %v", val)
		}

		mCompat := tocsModel("proxy", "https://proxy.example.com/v1", "m1")
		mCompat.Compat.V = &ai.OpenAICompletionsCompat{
			RequiresNonNullAssistantContent:  tocsBoolPtr(true),
			RequiresAssistantAfterToolResult: tocsBoolPtr(true),
		}
		compatMsgs, err := ConvertMessagesOpenaiCompletions(mCompat, ctx, nil, nil)
		if err != nil {
			t.Fatalf("ConvertMessagesOpenaiCompletions compat error: %v", err)
		}
		if content, _ := compatMsgs[1].Get("content"); content != "" {
			t.Fatalf("expected content=\"\" with RequiresNonNullAssistantContent, got %#v", content)
		}
		if len(compatMsgs) != 5 {
			t.Fatalf("expected 5 messages with synthetic assistant after toolResult, got %d", len(compatMsgs))
		}
		role3, _ := compatMsgs[3].GetString("role")
		content3, _ := compatMsgs[3].GetString("content")
		if role3 != "assistant" || content3 != "I have processed the tool results." {
			t.Fatalf("expected inserted {role:assistant, content:\"I have processed the tool results.\"} before user msg, got role=%q content=%q", role3, content3)
		}
	})

	t.Run("batches tool-result images, omits empty user text alongside image, and fills (no tool output)", func(t *testing.T) {
		ctx := ai.NormalizeContext(&ai.Context{
			Messages: ai.MessageList{
				&ai.UserMessage{
					Role: "user",
					Content: ai.UserMessageContent{
						Blocks: ai.ContentList{
							&ai.TextContent{Type: "text", Text: ""},
							&ai.ImageContent{Type: "image", MimeType: "image/png", Data: "AAAA"},
						},
					},
					Timestamp: 1,
				},
				&ai.AssistantMessage{
					Role: "assistant", Api: ai.KnownApiOpenaiCompletions, Provider: "openai", Model: "gpt-4o",
					Content: ai.ContentList{
						&ai.ToolCall{Type: "toolCall", Id: "c1", Name: "img1", Arguments: jsonx.NewObject()},
						&ai.ToolCall{Type: "toolCall", Id: "c2", Name: "txt", Arguments: jsonx.NewObject()},
					},
					StopReason: ai.StopReasonToolUse, Timestamp: 2,
				},
				&ai.ToolResultMessage{
					Role: "toolResult", ToolCallId: "c1", ToolName: "img1",
					Content: ai.ContentList{&ai.ImageContent{Type: "image", MimeType: "image/jpeg", Data: "BBBB"}}, Timestamp: 3,
				},
				&ai.ToolResultMessage{
					Role: "toolResult", ToolCallId: "c2", ToolName: "txt",
					Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "   "}}, Timestamp: 4,
				},
			},
		})
		m := tocsModel("openai", "https://api.openai.com/v1", "gpt-4o")
		converted, err := ConvertMessagesOpenaiCompletions(m, ctx, nil, nil)
		if err != nil {
			t.Fatalf("ConvertMessagesOpenaiCompletions failed: %v", err)
		}
		rawFirst, _ := jsonx.Stringify(converted[0])
		if strings.Contains(rawFirst, `"text":""`) {
			t.Fatalf("expected empty text part omitted alongside user image: %s", rawFirst)
		}
		rawLast, _ := jsonx.Stringify(converted[len(converted)-1])
		if role, _ := converted[len(converted)-1].GetString("role"); role != "user" || !strings.Contains(rawLast, "data:image/jpeg;base64,BBBB") {
			t.Fatalf("expected batched trailing user image message, got %s", rawLast)
		}
		rawTool2, _ := jsonx.Stringify(converted[3])
		if !strings.Contains(rawTool2, "(no tool output)") {
			t.Fatalf("expected (no tool output) for whitespace-only tool result, got %s", rawTool2)
		}
	})

	t.Run("RequiresThinkingAsText replays same-model thinking over httptest SSE server", func(t *testing.T) {
		var capturedReq map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &capturedReq)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"id\":\"1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
		}))
		defer srv.Close()

		m := tocsModel("custom", srv.URL, "qwen-think")
		m.Compat.V = &ai.OpenAICompletionsCompat{RequiresThinkingAsText: tocsBoolPtr(true)}
		ctx := ai.NormalizeContext(&ai.Context{
			Messages: ai.MessageList{
				&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tocsStrPtr("q1")}, Timestamp: 1},
				&ai.AssistantMessage{
					Role: "assistant", Api: ai.KnownApiOpenaiCompletions, Provider: "custom", Model: "qwen-think",
					Content: ai.ContentList{
						&ai.ThinkingContent{Type: "thinking", Thinking: "step 1"},
						&ai.TextContent{Type: "text", Text: "answer 1"},
					},
					StopReason: ai.StopReasonStop, Timestamp: 2,
				},
				&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tocsStrPtr("q2")}, Timestamp: 3},
			},
		})
		res, err := StreamOpenaiCompletions(context.Background(), m, ctx, &OpenAICompletionsOptions{
			StreamOptions: ai.StreamOptions{ApiKey: tocsStrPtr("k")},
		}).Result(context.Background())
		if err != nil || res.StopReason != ai.StopReasonStop {
			t.Fatalf("unexpected error/stop: %v / %s", err, res.StopReason)
		}
		msgs := capturedReq["messages"].([]any)
		asstRaw, _ := json.Marshal(msgs[1])
		if !strings.Contains(string(asstRaw), `"type":"text"`) || !strings.Contains(string(asstRaw), "step 1") || !strings.Contains(string(asstRaw), "answer 1") {
			t.Fatalf("expected thinking+text replayed as text blocks, got %s", string(asstRaw))
		}
	})
}

func TestOpenAICompletionsStreamingBehaviors(t *testing.T) {
	t.Run("sets RawStopReason and maps content_filter and non-standard finish_reason to error", func(t *testing.T) {
		m := tocsModel("openai", "https://api.openai.com/v1", "gpt-4o-mini")
		userCtx := ai.NormalizeContext(&ai.Context{
			Messages: ai.MessageList{&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tocsStrPtr("hi")}, Timestamp: 1}},
		})

		// 1. normal stop records RawStopReason="stop"
		c1 := &http.Client{
			Transport: tocsRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
				return tocsSseFromChunks(`{"id":"1","model":"gpt-4o-mini","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":"stop"}]}`), nil
			}),
		}
		res1, err := StreamOpenaiCompletions(context.Background(), m, userCtx, &OpenAICompletionsOptions{
			StreamOptions: ai.StreamOptions{ApiKey: tocsStrPtr("k"), HTTPClient: c1},
		}).Result(context.Background())
		if err != nil || res1.StopReason != ai.StopReasonStop || res1.RawStopReason == nil || *res1.RawStopReason != "stop" {
			t.Fatalf("unexpected stop/rawStopReason: stop=%s raw=%v err=%v", res1.StopReason, res1.RawStopReason, err)
		}

		// 2. content_filter and network_error -> error
		for _, reason := range []string{"content_filter", "network_error"} {
			rCopy := reason
			cErr := &http.Client{
				Transport: tocsRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
					return tocsSseFromChunks(`{"id":"1","choices":[{"index":0,"delta":{"content":"x"},"finish_reason":"` + rCopy + `"}]}`), nil
				}),
			}
			res, err := StreamOpenaiCompletions(context.Background(), m, userCtx, &OpenAICompletionsOptions{
				StreamOptions: ai.StreamOptions{ApiKey: tocsStrPtr("k"), HTTPClient: cErr},
			}).Result(context.Background())
			if err != nil || res.StopReason != ai.StopReasonError || res.ErrorMessage == nil || !strings.Contains(*res.ErrorMessage, "Provider finish_reason: "+rCopy) {
				t.Fatalf("expected error for finish_reason=%s, got stop=%s msg=%v err=%v", rCopy, res.StopReason, res.ErrorMessage, err)
			}
		}

		// 3. missing finish_reason fails unless SupportsFinishReason=false
		cNoFinish := &http.Client{
			Transport: tocsRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
				return tocsSseFromChunks(`{"id":"1","choices":[{"index":0,"delta":{"content":"partial"},"finish_reason":null}]}`), nil
			}),
		}
		resMissing, _ := StreamOpenaiCompletions(context.Background(), m, userCtx, &OpenAICompletionsOptions{
			StreamOptions: ai.StreamOptions{ApiKey: tocsStrPtr("k"), HTTPClient: cNoFinish},
		}).Result(context.Background())
		if resMissing.StopReason != ai.StopReasonError {
			t.Fatalf("expected StopReasonError when finish_reason omitted, got %s", resMissing.StopReason)
		}

		mNoFinish := tocsModel("custom", "https://custom.example.com/v1", "llama")
		mNoFinish.Compat.V = &ai.OpenAICompletionsCompat{SupportsFinishReason: tocsBoolPtr(false)}
		resAllowed, _ := StreamOpenaiCompletions(context.Background(), mNoFinish, userCtx, &OpenAICompletionsOptions{
			StreamOptions: ai.StreamOptions{ApiKey: tocsStrPtr("k"), HTTPClient: cNoFinish},
		}).Result(context.Background())
		if resAllowed.StopReason != ai.StopReasonStop {
			t.Fatalf("expected StopReasonStop when SupportsFinishReason=false, got %s (%v)", resAllowed.StopReason, resAllowed.ErrorMessage)
		}
	})

	t.Run("captures ResponseModel when different, OnProviderStreamEvent, OpenRouter BYOK costs, and non-double-counted reasoning_tokens", func(t *testing.T) {
		m := tocsModel("openrouter", "https://openrouter.ai/api/v1", "openai/gpt-5")
		userCtx := ai.NormalizeContext(&ai.Context{
			Messages: ai.MessageList{&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tocsStrPtr("hi")}, Timestamp: 1}},
		})

		var seenModels []string
		cNonByok := &http.Client{
			Transport: tocsRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
				return tocsSseFromChunks(
					`{"id":"1","model":"openai/gpt-5-2026-01-01","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`,
					`{"id":"1","model":"openai/gpt-5-2026-01-01","choices":[],"usage":{"prompt_tokens":100,"completion_tokens":50,"total_tokens":150,"cost":0.004,"prompt_tokens_details":{"cached_tokens":20,"cache_write_tokens":10},"completion_tokens_details":{"reasoning_tokens":15}}}`,
				), nil
			}),
		}
		res1, _ := StreamOpenaiCompletions(context.Background(), m, userCtx, &OpenAICompletionsOptions{
			StreamOptions: ai.StreamOptions{
				ApiKey:     tocsStrPtr("k"),
				HTTPClient: cNonByok,
				OnProviderStreamEvent: func(data any, evModel *ai.Model) error {
					if obj, ok := data.(*jsonx.Object); ok {
						id, _ := obj.GetString("id")
						seenModels = append(seenModels, id+":"+evModel.Id)
					}
					return nil
				},
			},
		}).Result(context.Background())
		if res1.ResponseModel == nil || *res1.ResponseModel != "openai/gpt-5-2026-01-01" {
			t.Fatalf("expected ResponseModel captured, got %v", res1.ResponseModel)
		}
		if len(seenModels) != 2 {
			t.Fatalf("expected 2 OnProviderStreamEvent callbacks, got %v", seenModels)
		}
		if res1.Usage.Cost.Total != 0.004 || res1.Usage.Cost.TotalFromProvider == nil || !*res1.Usage.Cost.TotalFromProvider || res1.Usage.Cost.Input != 0 || res1.Usage.Cost.Output != 0 {
			t.Fatalf("unexpected OpenRouter non-BYOK cost: %+v", res1.Usage.Cost)
		}
		if res1.Usage.Output != 50 || res1.Usage.Reasoning == nil || *res1.Usage.Reasoning != 15 || res1.Usage.CacheWrite != 10 {
			t.Fatalf("unexpected usage token split: %+v", res1.Usage)
		}

		// BYOK cost + upstream_inference_cost + same model omits ResponseModel
		cByok := &http.Client{
			Transport: tocsRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
				return tocsSseFromChunks(
					`{"id":"2","model":"openai/gpt-5","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`,
					`{"id":"2","model":"openai/gpt-5","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,"cost":0.001,"is_byok":true,"cost_details":{"upstream_inference_cost":0.003},"reasoning_tokens":7}}`,
				), nil
			}),
		}
		res2, _ := StreamOpenaiCompletions(context.Background(), m, userCtx, &OpenAICompletionsOptions{
			StreamOptions: ai.StreamOptions{ApiKey: tocsStrPtr("k"), HTTPClient: cByok},
		}).Result(context.Background())
		if res2.ResponseModel != nil {
			t.Fatalf("expected ResponseModel omitted when same as model.Id, got %v", *res2.ResponseModel)
		}
		if res2.Usage.Cost.Total < 0.00399 || res2.Usage.Cost.Total > 0.00401 {
			t.Fatalf("expected OpenRouter BYOK cost=0.004, got %v", res2.Usage.Cost.Total)
		}
		if res2.Usage.Reasoning == nil || *res2.Usage.Reasoning != 7 {
			t.Fatalf("expected top-level usage.reasoning_tokens=7, got %v", res2.Usage.Reasoning)
		}
	})

	t.Run("coalesces tool calls across mutating IDs, preserves reasoning_details & thoughtSignature, handles null chunk, empty custom, and opencode-go", func(t *testing.T) {
		m := tocsModel("openrouter", "https://openrouter.ai/api/v1", "google/gemini-2.5")
		userCtx := ai.NormalizeContext(&ai.Context{
			Messages: ai.MessageList{&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tocsStrPtr("go")}, Timestamp: 1}},
		})
		c := &http.Client{
			Transport: tocsRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
				return tocsSseFromChunks(
					`null`,
					`{"id":"1","choices":[{"index":0,"delta":{"reasoning_details":[{"type":"reasoning.text","id":"r1","text":"a"},{"type":"reasoning.text","id":"r1","text":"b"},{"type":"reasoning.encrypted","id":"r2","data":"ENC"},{"type":"reasoning.summary","id":"r3","summary":"sum"}]}}]}`,
					`{"id":"1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","custom":{},"function":{"name":"lookup","arguments":"{\"q\":"}}]}}]}`,
					`{"id":"1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_b","function":{"arguments":"\"hi\"}"}}]}}]}`,
					`{"id":"1","choices":[{"index":0,"delta":{"tool_calls":[{"id":"call_unindexed","type":"function","function":{"name":"extra","arguments":"{}"}}]}}]}`,
					`{"id":"1","choices":[{"index":0,"delta":{"content":"done"},"finish_reason":"tool_calls"}]}`,
				), nil
			}),
		}
		res, err := StreamOpenaiCompletions(context.Background(), m, userCtx, &OpenAICompletionsOptions{
			StreamOptions: ai.StreamOptions{ApiKey: tocsStrPtr("k"), HTTPClient: c},
		}).Result(context.Background())
		if err != nil || res.StopReason != ai.StopReasonToolUse {
			t.Fatalf("unexpected stop/err: %s / %v (%v)", res.StopReason, err, res.ErrorMessage)
		}
		var thinking *ai.ThinkingContent
		var tc0, tc1 *ai.ToolCall
		for _, blk := range res.Content {
			switch b := blk.(type) {
			case *ai.ThinkingContent:
				thinking = b
			case *ai.ToolCall:
				if b.Name == "lookup" {
					tc0 = b
				} else if b.Name == "extra" {
					tc1 = b
				}
			}
		}
		if thinking == nil || thinking.ThinkingSignature == nil || !strings.Contains(*thinking.ThinkingSignature, `"ab"`) || !strings.Contains(*thinking.ThinkingSignature, `"ENC"`) {
			t.Fatalf("expected merged reasoning_details in ThinkingSignature, got %+v", thinking)
		}
		if tc0 == nil || tc0.Id != "call_a" {
			t.Fatalf("expected tool call coalesced under original id call_a, got %+v", tc0)
		}
		if q, _ := tc0.Arguments.GetString("q"); q != "hi" {
			t.Fatalf("expected parsed tool argument q=hi, got %v", tc0.Arguments)
		}
		if tc1 == nil || tc1.Id != "call_unindexed" {
			t.Fatalf("expected unindexed second tool call preserved, got %+v", tc1)
		}

		// opencode-go normalizes delta.reasoning; single delta reasoning_content+content orders thinking first
		mGo := tocsModel("opencode-go", "https://opencode.ai/zen/go/v1", "glm-5")
		cGo := &http.Client{
			Transport: tocsRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
				return tocsSseFromChunks(
					`{"id":"1","choices":[{"index":0,"delta":{"reasoning":"go-thought","content":"go-text"},"finish_reason":"stop"}]}`,
				), nil
			}),
		}
		resGo, _ := StreamOpenaiCompletions(context.Background(), mGo, userCtx, &OpenAICompletionsOptions{
			StreamOptions: ai.StreamOptions{ApiKey: tocsStrPtr("k"), HTTPClient: cGo},
		}).Result(context.Background())
		if len(resGo.Content) < 2 {
			t.Fatalf("expected thinking + text blocks for opencode-go, got %+v", resGo.Content)
		}
		if tb, ok := resGo.Content[0].(*ai.ThinkingContent); !ok || tb.Thinking != "go-thought" {
			t.Fatalf("expected thinking ordered first with go-thought, got %+v", resGo.Content[0])
		}
	})

	t.Run("retries on 429 with short delay and aborts when retry-after exceeds maxRetryDelayMs", func(t *testing.T) {
		m := tocsModel("openai", "https://api.openai.com/v1", "gpt-4o-mini")
		userCtx := ai.NormalizeContext(&ai.Context{
			Messages: ai.MessageList{&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tocsStrPtr("hi")}, Timestamp: 1}},
		})

		// 1. Default maxRetries=0 -> 1 attempt
		var attempts1 atomic.Int32
		c1 := &http.Client{
			Transport: tocsRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
				attempts1.Add(1)
				return &http.Response{
					StatusCode: 429,
					Status:     "429 Too Many Requests",
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"rate limited"}}`)),
				}, nil
			}),
		}
		res1, _ := StreamOpenaiCompletions(context.Background(), m, userCtx, &OpenAICompletionsOptions{
			StreamOptions: ai.StreamOptions{ApiKey: tocsStrPtr("k"), HTTPClient: c1},
		}).Result(context.Background())
		if res1.StopReason != ai.StopReasonError || attempts1.Load() != 1 {
			t.Fatalf("expected single attempt error on default maxRetries=0, got attempts=%d stop=%s", attempts1.Load(), res1.StopReason)
		}

		// 2. Retries with retry-after-ms=1
		var attempts2 atomic.Int32
		c2 := &http.Client{
			Transport: tocsRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
				if attempts2.Add(1) == 1 {
					return &http.Response{
						StatusCode: 500,
						Status:     "500 Internal Server Error",
						Header:     http.Header{"Retry-After-Ms": []string{"1"}, "Content-Type": []string{"application/json"}},
						Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"transient"}}`)),
					}, nil
				}
				return tocsSseFromChunks(`{"id":"ok","choices":[{"index":0,"delta":{"content":"recovered"},"finish_reason":"stop"}]}`), nil
			}),
		}
		maxRetries := 1
		res2, _ := StreamOpenaiCompletions(context.Background(), m, userCtx, &OpenAICompletionsOptions{
			StreamOptions: ai.StreamOptions{ApiKey: tocsStrPtr("k"), MaxRetries: &maxRetries, HTTPClient: c2},
		}).Result(context.Background())
		if res2.StopReason != ai.StopReasonStop || attempts2.Load() != 2 {
			t.Fatalf("expected recovery on attempt 2, got attempts=%d stop=%s err=%v", attempts2.Load(), res2.StopReason, res2.ErrorMessage)
		}

		// 3. Exceeding MaxRetryDelayMs fails immediately
		var attempts3 atomic.Int32
		c3 := &http.Client{
			Transport: tocsRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
				attempts3.Add(1)
				return &http.Response{
					StatusCode: 429,
					Status:     "429 Too Many Requests",
					Header:     http.Header{"Retry-After": []string{"277403"}, "Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"too long"}}`)),
				}, nil
			}),
		}
		maxDelay := int64(1000)
		retries3 := 2
		res3, _ := StreamOpenaiCompletions(context.Background(), m, userCtx, &OpenAICompletionsOptions{
			StreamOptions: ai.StreamOptions{ApiKey: tocsStrPtr("k"), MaxRetries: &retries3, MaxRetryDelayMs: &maxDelay, HTTPClient: c3},
		}).Result(context.Background())
		if res3.StopReason != ai.StopReasonError || attempts3.Load() != 1 || res3.ErrorMessage == nil || !strings.Contains(*res3.ErrorMessage, "Server requested 277403s retry delay (max: 1s)") {
			t.Fatalf("expected immediate retry-delay error, got attempts=%d stop=%s msg=%v", attempts3.Load(), res3.StopReason, res3.ErrorMessage)
		}
	})
}

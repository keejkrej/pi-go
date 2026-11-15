// Ported from packages/ai/test/mistral-http-transport.test.ts (pi v1.5.2).

package api_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

func tmhtStrPtr(s string) *string { return &s }

func tmhtIntPtr(v int) *int { return &v }

func tmhtInt64Ptr(v int64) *int64 { return &v }

func tmhtModel(baseUrl, id string) *ai.Model {
	if id == "" {
		id = "mistral-large-latest"
	}
	return &ai.Model{
		BaseModel: ai.BaseModel{
			Id:       id,
			Name:     id,
			Api:      string(ai.KnownApiMistralConversations),
			Provider: "mistral",
			BaseUrl:  baseUrl,
			Input:    []ai.ModelModality{ai.ModelModalityText, ai.ModelModalityImage},
			Cost: ai.ModelCost{
				ModelCostRates: ai.ModelCostRates{Input: 2.0, Output: 6.0},
			},
		},
		Reasoning:     true,
		ContextWindow: 128000,
		MaxTokens:     16384,
	}
}

func tmhtTerminalEvent(finishReason string) string {
	return fmt.Sprintf(`{"id":"mistral-response-id","model":"mistral-large-latest","choices":[{"index":0,"finish_reason":%q,"delta":{}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`, finishReason)
}

func tmhtSSEBody(events ...string) string {
	var b strings.Builder
	for _, ev := range events {
		b.WriteString("data: ")
		b.WriteString(ev)
		b.WriteString("\r\n\r\n")
	}
	b.WriteString("data: [DONE]\r\n\r\n")
	return b.String()
}

type tmhtRoundTripperFunc func(*http.Request) (*http.Response, error)

func (f tmhtRoundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestMistralHttpTransportPayloadSerialization(t *testing.T) {
	var seenURL, seenBody string
	var seenHeaders http.Header
	var callbackPayload *jsonx.Object
	var callbackResponse *ai.ProviderResponse

	client := &http.Client{
		Transport: tmhtRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
			seenURL = r.URL.String()
			seenHeaders = r.Header.Clone()
			raw, _ := io.ReadAll(r.Body)
			seenBody = string(raw)
			h := make(http.Header)
			h.Set("Content-Type", "text/event-stream")
			h.Set("X-Request-Id", "request-1")
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     h,
				Body:       io.NopCloser(strings.NewReader(tmhtSSEBody(tmhtTerminalEvent("stop")))),
			}, nil
		}),
	}

	props := omap.NewMap[string, *typebox.Schema]()
	props.Set("query", typebox.String())
	sysPrompt := "Be precise"
	transcript := ai.NormalizeContext(&ai.Context{
		SystemPrompt: &sysPrompt,
		Messages: ai.MessageList{
			&ai.UserMessage{
				Role: "user",
				Content: ai.UserMessageContent{
					Blocks: ai.ContentList{
						&ai.TextContent{Type: "text", Text: "describe"},
						&ai.ImageContent{Type: "image", Data: "aGVsbG8=", MimeType: "image/png"},
					},
				},
				Timestamp: 1,
			},
		},
		Tools: []*ai.Tool{
			{
				Name:        "lookup",
				Description: "Look something up",
				Parameters:  typebox.Object(props),
			},
		},
	})

	optHeaders := omap.NewMap[string, *string]()
	optHeaders.Set("x-custom", tmhtStrPtr("value"))
	promptMode := "reasoning"
	effort := api.MistralReasoningEffortHigh

	stream := api.StreamMistralConversations(context.Background(), tmhtModel("https://api.mistral.ai", "mistral-large-latest"), transcript, &api.MistralOptions{
		StreamOptions: ai.StreamOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
				ApiKey:     tmhtStrPtr("secret"),
				HTTPClient: client,
				Headers:    optHeaders,
				OnPayload: func(payload any, m *ai.Model) (any, error) {
					obj := payload.(*jsonx.Object).Clone()
					callbackPayload = obj
					out := obj.Clone()
					out.Set("topP", 0.9)
					out.Set("randomSeed", 42)
					out.Set("presencePenalty", 0.1)
					out.Set("frequencyPenalty", 0.2)
					out.Set("parallelToolCalls", true)
					out.Set("safePrompt", true)
					out.Set("responseFormat", jsonx.ObjectOf(
						"type", "json_schema",
						"jsonSchema", jsonx.ObjectOf(
							"name", "result",
							"schemaDefinition", jsonx.ObjectOf(
								"type", "object",
								"properties", jsonx.ObjectOf("maxTokens", jsonx.ObjectOf("type", "number")),
							),
						),
					))
					return out, nil
				},
				OnResponse: func(resp *ai.ProviderResponse, m *ai.Model) error {
					callbackResponse = resp
					return nil
				},
			},
			MaxTokens: tmhtIntPtr(123),
			SessionId: tmhtStrPtr("session-1"),
		},
		PromptMode:      &promptMode,
		ReasoningEffort: &effort,
		ToolChoice:      jsonx.ObjectOf("type", "function", "function", jsonx.ObjectOf("name", "lookup")),
	})

	msg, err := stream.Result(context.Background())
	if err != nil {
		t.Fatalf("stream.Result error: %v", err)
	}
	if msg.StopReason != ai.StopReasonStop {
		t.Fatalf("expected stop, got %q (%v)", msg.StopReason, msg.ErrorMessage)
	}
	if seenURL != "https://api.mistral.ai/v1/chat/completions" {
		t.Fatalf("unexpected request URL: %q", seenURL)
	}
	if seenHeaders.Get("Authorization") != "Bearer secret" ||
		seenHeaders.Get("Accept") != "text/event-stream" ||
		seenHeaders.Get("X-Affinity") != "session-1" ||
		seenHeaders.Get("X-Custom") != "value" ||
		seenHeaders.Get("User-Agent") != ai.FormatUserAgent(nil) {
		t.Fatalf("unexpected request headers: %+v", seenHeaders)
	}
	if callbackPayload == nil {
		t.Fatal("expected callbackPayload to be captured")
	}
	if mt, _ := callbackPayload.GetNumber("maxTokens"); mt != 123 {
		t.Fatalf("expected callbackPayload maxTokens=123, got %v", mt)
	}
	if pm, _ := callbackPayload.GetString("promptMode"); pm != "reasoning" {
		t.Fatalf("expected callbackPayload promptMode=reasoning, got %q", pm)
	}
	if pk, _ := callbackPayload.GetString("promptCacheKey"); pk != "session-1" {
		t.Fatalf("expected callbackPayload promptCacheKey=session-1, got %q", pk)
	}
	if callbackResponse == nil || callbackResponse.Status != 200 || callbackResponse.Headers["x-request-id"] != "request-1" {
		t.Fatalf("unexpected callbackResponse: %+v", callbackResponse)
	}

	var wire *jsonx.Object
	if err := jsonx.Unmarshal([]byte(seenBody), &wire); err != nil {
		t.Fatalf("unmarshal wire body: %v", err)
	}
	if v, _ := wire.GetNumber("max_tokens"); v != 123 {
		t.Fatalf("expected snake_case max_tokens=123, got %v", v)
	}
	if v, _ := wire.GetString("prompt_mode"); v != "reasoning" {
		t.Fatalf("expected prompt_mode=reasoning, got %q", v)
	}
	if v, _ := wire.GetString("reasoning_effort"); v != "high" {
		t.Fatalf("expected reasoning_effort=high, got %q", v)
	}
	if v, _ := wire.GetString("prompt_cache_key"); v != "session-1" {
		t.Fatalf("expected prompt_cache_key=session-1, got %q", v)
	}
	if v, _ := wire.GetNumber("random_seed"); v != 42 {
		t.Fatalf("expected random_seed=42, got %v", v)
	}
	if wire.Has("maxTokens") || wire.Has("promptMode") || wire.Has("promptCacheKey") {
		t.Fatalf("wire body leaked camelCase keys: %s", seenBody)
	}
	rf, _ := wire.GetObject("response_format")
	jsObj, _ := rf.GetObject("json_schema")
	schemaObj, _ := jsObj.GetObject("schema")
	if schemaObj == nil || !schemaObj.Has("properties") {
		t.Fatalf("expected nested response_format.json_schema.schema preserved: %s", seenBody)
	}
}

func TestMistralHttpTransportReplayAndSSE(t *testing.T) {
	var seenBody string
	events := []string{
		`{"id":"response-1","model":"mistral-large-latest","choices":[{"index":0,"finish_reason":null,"delta":{"content":[{"type":"thinking","thinking":[{"type":"text","text":"reason"}]}]}}]}`,
		`{"id":"response-1","model":"mistral-large-latest","choices":[{"index":0,"finish_reason":null,"delta":{"content":[{"type":"text","text":"answer"}]}}]}`,
		`{"id":"response-1","model":"mistral-large-latest","choices":[{"index":0,"finish_reason":null,"delta":{"tool_calls":[{"id":"abc123456","index":0,"function":{"name":"lookup","arguments":"{\"query\":"}}]}}]}`,
		`{"id":"response-1","model":"mistral-large-latest","choices":[{"index":0,"finish_reason":"tool_calls","delta":{"tool_calls":[{"index":0,"function":{"name":"","arguments":"\"pi\"}"}}]}}],"usage":{"prompt_tokens":10,"completion_tokens":4,"total_tokens":14,"prompt_tokens_details":{"cached_tokens":3}}}`,
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		seenBody = string(raw)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, tmhtSSEBody(events...))
	}))
	defer srv.Close()

	model := tmhtModel(srv.URL, "mistral-large-latest")
	transcript := ai.NormalizeContext(&ai.Context{
		Messages: ai.MessageList{
			&ai.AssistantMessage{
				Role:     "assistant",
				Api:      ai.KnownApiMistralConversations,
				Provider: "mistral",
				Model:    model.Id,
				Content: ai.ContentList{
					&ai.ThinkingContent{Type: "thinking", Thinking: "reason"},
					&ai.TextContent{Type: "text", Text: "answer"},
					&ai.ToolCall{Type: "toolCall", Id: "abc123456", Name: "lookup", Arguments: jsonx.ObjectOf("query", "pi")},
				},
				StopReason: ai.StopReasonToolUse,
				Timestamp:  1,
			},
			&ai.ToolResultMessage{
				Role:       "toolResult",
				ToolCallId: "abc123456",
				ToolName:   "lookup",
				Content: ai.ContentList{
					&ai.TextContent{Type: "text", Text: "found"},
					&ai.ImageContent{Type: "image", Data: "aGVsbG8=", MimeType: "image/png"},
				},
				IsError:   false,
				Timestamp: 2,
			},
		},
	})

	msg, err := api.StreamMistralConversations(context.Background(), model, transcript, &api.MistralOptions{
		StreamOptions: ai.StreamOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{ApiKey: tmhtStrPtr("test")},
		},
	}).Result(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(seenBody, `"prefix":false`) || !strings.Contains(seenBody, `"role":"tool"`) {
		t.Fatalf("unexpected replay wire body: %s", seenBody)
	}
	if msg.StopReason != ai.StopReasonToolUse || msg.RawStopReason == nil || *msg.RawStopReason != "tool_calls" {
		t.Fatalf("unexpected stopReason/rawStopReason: %+v", msg)
	}
	if msg.ResponseId == nil || *msg.ResponseId != "response-1" || len(msg.Content) != 3 {
		t.Fatalf("unexpected parsed message: %+v", msg)
	}
	if msg.Usage.Input != 7 || msg.Usage.Output != 4 || msg.Usage.CacheRead != 3 || msg.Usage.TotalTokens != 14 {
		t.Fatalf("unexpected cached token usage: %+v", msg.Usage)
	}
}

type tmhtByteReader struct {
	data []byte
	pos  int
}

func (b *tmhtByteReader) Read(p []byte) (int, error) {
	if b.pos >= len(b.data) {
		return 0, io.EOF
	}
	p[0] = b.data[b.pos]
	b.pos++
	return 1, nil
}

func TestMistralHttpTransportEmptyDeltasHooksUTF8AndHeaders(t *testing.T) {
	// Empty content deltas must not split thinking (#9674) + OnProviderStreamEvent hook + bytewise UTF-8.
	deltas := []string{
		`{"id":"r1","model":"zai-glm-5-3","choices":[{"index":0,"finish_reason":null,"delta":{"content":""}}]}`,
		`{"id":"r1","model":"zai-glm-5-3","choices":[{"index":0,"finish_reason":null,"delta":{"content":[{"type":"thinking","thinking":[{"type":"text","text":"first part,"}]}]}}]}`,
		`{"id":"r1","model":"zai-glm-5-3","choices":[{"index":0,"finish_reason":null,"delta":{"content":""}}]}`,
		`{"id":"r1","model":"zai-glm-5-3","choices":[{"index":0,"finish_reason":null,"delta":{"content":[{"type":"text","text":""}]}}]}`,
		`{"id":"r1","model":"zai-glm-5-3","choices":[{"index":0,"finish_reason":"stop","delta":{"content":[{"type":"thinking","thinking":[{"type":"text","text":" second part."}]},{"type":"text","text":"héllo 🌍"}]}}]}`,
	}
	rawSSE := tmhtSSEBody(deltas...)
	var seenHeaders http.Header

	client := &http.Client{
		Transport: tmhtRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
			seenHeaders = r.Header.Clone()
			h := make(http.Header)
			h.Set("Content-Type", "text/event-stream")
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     h,
				Body:       io.NopCloser(&tmhtByteReader{data: []byte(rawSSE)}),
			}, nil
		}),
	}

	modelHeaders := omap.NewMap[string, string]()
	modelHeaders.Set("Authorization", "Bearer model-key")
	modelHeaders.Set("X-Affinity", "model-affinity")
	model := tmhtModel("https://api.mistral.ai", "zai-glm-5-3")
	model.Headers = modelHeaders

	optHeaders := omap.NewMap[string, *string]()
	optHeaders.Set("authorization", nil)
	optHeaders.Set("x-affinity", nil)
	optHeaders.Set("User-Agent", tmhtStrPtr("custom-agent"))

	var hookPayloads []any
	var hookMu sync.Mutex
	transcript := ai.NormalizeContext(&ai.Context{
		Messages: ai.MessageList{
			&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tmhtStrPtr("hello")}, Timestamp: 1},
		},
	})

	msg, err := api.StreamMistralConversations(context.Background(), model, transcript, &api.MistralOptions{
		StreamOptions: ai.StreamOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
				ApiKey:     tmhtStrPtr("request-key"),
				HTTPClient: client,
				Headers:    optHeaders,
			},
			SessionId: tmhtStrPtr("automatic-affinity"),
			OnProviderStreamEvent: func(data any, m *ai.Model) error {
				hookMu.Lock()
				hookPayloads = append(hookPayloads, data)
				hookMu.Unlock()
				return nil
			},
		},
	}).Result(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(hookPayloads) != len(deltas) {
		t.Fatalf("expected %d provider stream events, got %d", len(deltas), len(hookPayloads))
	}
	if seenHeaders.Get("Authorization") != "" || seenHeaders.Get("X-Affinity") != "" || seenHeaders.Get("User-Agent") != "custom-agent" {
		t.Fatalf("unexpected suppressed/overridden headers: %+v", seenHeaders)
	}
	if len(msg.Content) != 2 {
		t.Fatalf("expected 2 merged blocks, got %d (%+v)", len(msg.Content), msg.Content)
	}
	if th, ok := msg.Content[0].(*ai.ThinkingContent); !ok || th.Thinking != "first part, second part." {
		t.Fatalf("unexpected thinking block: %#v", msg.Content[0])
	}
	if tx, ok := msg.Content[1].(*ai.TextContent); !ok || tx.Text != "héllo 🌍" {
		t.Fatalf("unexpected UTF-8 text block: %#v", msg.Content[1])
	}
}

func TestMistralHttpTransportTimeoutsAbortAndErrors(t *testing.T) {
	transcript := ai.NormalizeContext(&ai.Context{
		Messages: ai.MessageList{
			&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tmhtStrPtr("hello")}, Timestamp: 1},
		},
	})

	// 1. Response header wait timeout.
	timeoutClient := &http.Client{
		Transport: tmhtRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
			<-r.Context().Done()
			return nil, r.Context().Err()
		}),
	}
	resTimeout, _ := api.StreamMistralConversations(context.Background(), tmhtModel("https://api.mistral.ai", ""), transcript, &api.MistralOptions{
		StreamOptions: ai.StreamOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
				ApiKey:     tmhtStrPtr("test"),
				HTTPClient: timeoutClient,
				TimeoutMs:  tmhtInt64Ptr(10),
			},
		},
	}).Result(context.Background())
	if resTimeout.StopReason != ai.StopReasonError || resTimeout.ErrorMessage == nil || *resTimeout.ErrorMessage != "Mistral response headers timed out after 10ms" {
		t.Fatalf("unexpected timeout error: %+v", resTimeout)
	}

	// 2. Active stream exceeding TimeoutMs must NOT abort (#10609).
	srvSlow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		for range 4 {
			_, _ = io.WriteString(w, `data: {"choices":[{"index":0,"delta":{"content":[{"type":"thinking","thinking":[{"type":"text","text":"x"}]}]}}]}`+"\n\n")
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(10 * time.Millisecond)
		}
		_, _ = io.WriteString(w, tmhtSSEBody(tmhtTerminalEvent("stop")))
	}))
	defer srvSlow.Close()

	resSlow, _ := api.StreamMistralConversations(context.Background(), tmhtModel(srvSlow.URL, ""), transcript, &api.MistralOptions{
		StreamOptions: ai.StreamOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
				ApiKey:    tmhtStrPtr("test"),
				TimeoutMs: tmhtInt64Ptr(15),
			},
		},
	}).Result(context.Background())
	if resSlow.StopReason != ai.StopReasonStop {
		t.Fatalf("expected active stream beyond timeoutMs to finish with stop, got %+v", resSlow)
	}
	if len(resSlow.Content) != 1 || resSlow.Content[0].(*ai.ThinkingContent).Thinking != "xxxx" {
		t.Fatalf("unexpected streamed content: %+v", resSlow.Content)
	}

	// 3. Aborted stream.
	srvHang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	defer srvHang.Close()
	abortCtx, cancel := context.WithCancel(context.Background())
	sAbort := api.StreamMistralConversations(abortCtx, tmhtModel(srvHang.URL, ""), transcript, &api.MistralOptions{
		StreamOptions: ai.StreamOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{ApiKey: tmhtStrPtr("test")},
		},
	})
	cancel()
	resAbort, _ := sAbort.Result(context.Background())
	if resAbort.StopReason != ai.StopReasonAborted {
		t.Fatalf("expected aborted, got %+v", resAbort)
	}

	// 4. HTTP 403 preserves status and response body.
	srvErr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"message":"blocked by gateway"}`)
	}))
	defer srvErr.Close()
	resErr, _ := api.StreamMistralConversations(context.Background(), tmhtModel(srvErr.URL, ""), transcript, &api.MistralOptions{
		StreamOptions: ai.StreamOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{ApiKey: tmhtStrPtr("test")},
		},
	}).Result(context.Background())
	if resErr.StopReason != ai.StopReasonError || resErr.ErrorMessage == nil || *resErr.ErrorMessage != `Mistral API error (403): {"message":"blocked by gateway"}` {
		t.Fatalf("unexpected 403 error message: %+v", resErr)
	}
}

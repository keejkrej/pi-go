// Ported from packages/ai/test/anthropic-{mid-conversation-effort,strict-tool-fallback,message-threads} and github-copilot-anthropic.test.ts (pi v1.5.2).

package api_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

func tafcStrPtr(s string) *string { return &s }

func tafcBoolPtr(b bool) *bool { return &b }

func tafcIntPtr(v int) *int { return &v }

func tafcSchema() *typebox.Schema {
	props := omap.NewMap[string, *typebox.Schema]()
	props.Set("value", typebox.String())
	props.Set("opt", typebox.String())
	return typebox.Object(props)
}

func tafcStrictTool(name string, strict ai.ConstrainedSamplingStrict) *ai.Tool {
	t := &ai.Tool{
		Name:        name,
		Description: "Look up",
		Parameters:  tafcSchema(),
	}
	if strict != "" {
		t.ConstrainedSampling = ai.ToolConstrainedSamplingValue{
			V: &ai.ConstrainedSamplingJsonSchema{
				Type:   "json_schema",
				Strict: strict,
			},
		}
	}
	return t
}

func TestAnthropicMidConversationEffortAndMessageThreads(t *testing.T) {
	// 1. Mid-conversation effort reconstructs marker prefix and appends current effort message.
	lvlLow := ai.ThinkingLevel("low")
	modelEffort := &ai.Model{
		BaseModel: ai.BaseModel{
			Id:       "claude-fable-5-1",
			Name:     "Claude Fable 5.1",
			Api:      string(ai.KnownApiAnthropicMessages),
			Provider: "anthropic",
			BaseUrl:  "http://127.0.0.1:9",
			Input:    []ai.ModelModality{ai.ModelModalityText},
		},
		Reasoning:     true,
		ContextWindow: 200000,
		MaxTokens:     32000,
		Compat: ai.ModelCompatValue{
			V: &ai.AnthropicMessagesCompat{
				ForceAdaptiveThinking:  tafcBoolPtr(true),
				SupportsMidConvoEffort: tafcBoolPtr(true),
			},
		},
	}

	var capPayload *jsonx.Object
	effortHigh := api.AnthropicEffortHigh
	thinkingOn := true
	retNone := ai.CacheRetentionNone
	_ = lvlLow
	transcript := ai.NormalizeContext(&ai.Context{
		Messages: ai.MessageList{
			&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tafcStrPtr("one")}, Timestamp: 1},
			&ai.AssistantMessage{
				Role:                  "assistant",
				Api:                   ai.KnownApiAnthropicMessages,
				Provider:              "anthropic",
				Model:                 modelEffort.Id,
				ProviderThinkingLevel: tafcStrPtr("low"),
				Content: ai.ContentList{
					&ai.ThinkingContent{Type: "thinking", Thinking: "r", ThinkingSignature: tafcStrPtr("sig")},
					&ai.TextContent{Type: "text", Text: "answer"},
				},
				StopReason: ai.StopReasonStop,
				Timestamp:  2,
			},
			&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tafcStrPtr("two")}, Timestamp: 3},
		},
	})
	_, _ = api.StreamAnthropicMessages(context.Background(), modelEffort, transcript, &api.AnthropicOptions{
		StreamOptions: ai.StreamOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
				ApiKey: tafcStrPtr("test-key"),
				OnPayload: func(p any, m *ai.Model) (any, error) {
					capPayload = p.(*jsonx.Object).Clone()
					return nil, errors.New("captured")
				},
			},
			CacheRetention: &retNone,
		},
		ThinkingEnabled: &thinkingOn,
		Effort:          &effortHigh,
	}).Result(context.Background())

	if capPayload == nil {
		t.Fatal("expected payload capture")
	}
	msgs, _ := capPayload.GetArray("messages")
	var systemEffortCount int
	for _, m := range msgs {
		mo := m.(*jsonx.Object)
		if role, _ := mo.GetString("role"); role == "system" {
			systemEffortCount++
		}
	}
	if systemEffortCount < 2 {
		t.Fatalf("expected historical and current system effort marker messages, got %d in %v", systemEffortCount, msgs)
	}

	// 2. Message threads: first turn sends full state; second turn with thread_id sends incremental continuation.
	var requests []string
	var betas []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		betas = append(betas, r.Header.Get("anthropic-beta"))
		raw, _ := io.ReadAll(r.Body)
		requests = append(requests, string(raw))
		w.Header().Set("Content-Type", "text/event-stream")
		id := "msg_t1"
		if len(requests) > 1 {
			id = "msg_t2"
		}
		_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\""+id+"\",\"model\":\"claude-fable-5-1\",\"usage\":{\"input_tokens\":5,\"output_tokens\":0}}}\n\n"+
			"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"+
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n"+
			"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n"+
			"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\n\n"+
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer srv.Close()

	threadModel := &ai.Model{
		BaseModel: ai.BaseModel{
			Id:       "claude-fable-5-1",
			Name:     "Claude Fable 5.1",
			Api:      string(ai.KnownApiAnthropicMessages),
			Provider: "anthropic",
			BaseUrl:  srv.URL,
			Input:    []ai.ModelModality{ai.ModelModalityText},
		},
		ContextWindow: 200000,
		MaxTokens:     4096,
		Compat: ai.ModelCompatValue{
			V: &ai.AnthropicMessagesCompat{
				SupportsMessageThreads: tafcBoolPtr(true),
			},
		},
	}

	threadSession := tafcStrPtr("thread-test-1")
	t1Ctx := ai.NormalizeContext(&ai.Context{
		Messages: ai.MessageList{
			&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tafcStrPtr("turn 1")}, Timestamp: 1},
		},
	})
	m1, err := api.StreamAnthropicMessages(context.Background(), threadModel, t1Ctx, &api.AnthropicOptions{
		StreamOptions: ai.StreamOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{ApiKey: tafcStrPtr("k")},
			SessionId:              threadSession,
		},
	}).Result(context.Background())
	if err != nil || m1.ResponseId == nil || *m1.ResponseId != "msg_t1" {
		t.Fatalf("turn 1 failed: %v, %+v", err, m1)
	}
	if !strings.Contains(betas[0], "message-threads-2026-08-12") {
		t.Fatalf("expected message-threads beta on turn 1, got %q", betas[0])
	}

	t2Ctx := ai.NormalizeContext(&ai.Context{
		Messages: ai.MessageList{
			&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tafcStrPtr("turn 1")}, Timestamp: 1},
			m1,
			&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tafcStrPtr("turn 2")}, Timestamp: 2},
		},
	})
	_, err = api.StreamAnthropicMessages(context.Background(), threadModel, t2Ctx, &api.AnthropicOptions{
		StreamOptions: ai.StreamOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{ApiKey: tafcStrPtr("k")},
			SessionId:              threadSession,
		},
	}).Result(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(requests[1], `"previous_message_id":"msg_t1"`) {
		t.Fatalf("expected thread continuation with previous_message_id=msg_t1, got %s", requests[1])
	}
}

func TestAnthropicStrictToolFallbackAndGitHubCopilotHeaders(t *testing.T) {
	// 1. Strict tool fallback retries preferred strict tools as non-strict while keeping required strict.
	var bodies []string
	var callNum atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(raw))
		if callNum.Add(1) == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"type":"error","error":{"type":"invalid_request_error","message":"The compiled grammar is too large, which would cause performance issues. Simplify your tool schemas or reduce the number of strict tools."}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_ok\",\"model\":\"claude-haiku-4-5\",\"usage\":{\"input_tokens\":10,\"output_tokens\":0}}}\n\n"+
			"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n"+
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer srv.Close()

	strictModel := &ai.Model{
		BaseModel: ai.BaseModel{
			Id:       "claude-haiku-4-5",
			Name:     "Test Claude",
			Api:      string(ai.KnownApiAnthropicMessages),
			Provider: "anthropic",
			BaseUrl:  srv.URL,
			Input:    []ai.ModelModality{ai.ModelModalityText},
		},
		ContextWindow: 200000,
		MaxTokens:     100,
		Compat:        ai.ModelCompatValue{V: &ai.AnthropicMessagesCompat{SupportsStrictTools: tafcBoolPtr(true)}},
	}

	preferred := tafcStrictTool("preferred", ai.ConstrainedSamplingStrictPrefer)
	required := tafcStrictTool("required", ai.ConstrainedSamplingStrictRequire)
	ordinary := tafcStrictTool("ordinary", "")
	transcript := ai.NormalizeContext(&ai.Context{
		Messages: ai.MessageList{
			&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tafcStrPtr("Hello")}, Timestamp: 1},
		},
		Tools: []*ai.Tool{preferred, required, ordinary},
	})

	retries := 0
	var onRespCount int
	res, err := api.StreamAnthropicMessages(context.Background(), strictModel, transcript, &api.AnthropicOptions{
		StreamOptions: ai.StreamOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
				ApiKey:     tafcStrPtr("k"),
				MaxRetries: tafcIntPtr(retries),
				OnResponse: func(resp *ai.ProviderResponse, m *ai.Model) error {
					onRespCount++
					return nil
				},
			},
			SessionId: tafcStrPtr("strict-sess-1"),
		},
	}).Result(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.StopReason != ai.StopReasonStop || len(bodies) != 2 || onRespCount != 1 {
		t.Fatalf("expected 2 attempts and stop, got stop=%q bodies=%d onResp=%d", res.StopReason, len(bodies), onRespCount)
	}
	var b1, b2 *jsonx.Object
	_ = jsonx.Unmarshal([]byte(bodies[0]), &b1)
	_ = jsonx.Unmarshal([]byte(bodies[1]), &b2)
	tools1, _ := b1.GetArray("tools")
	tools2, _ := b2.GetArray("tools")
	if strict1, _ := tools1[0].(*jsonx.Object).GetBool("strict"); !strict1 {
		t.Fatalf("attempt 1 should set strict=true on preferred tool, got %v", tools1[0])
	}
	if tools2[0].(*jsonx.Object).Has("strict") {
		t.Fatalf("attempt 2 fallback should drop strict on preferred tool, got %v", tools2[0])
	}
	if reqStrict2, _ := tools2[1].(*jsonx.Object).GetBool("strict"); !reqStrict2 {
		t.Fatalf("attempt 2 fallback must preserve strict on required tool, got %v", tools2[1])
	}
	var sawDiag bool
	for _, d := range res.Diagnostics {
		if d.Type == "anthropic_strict_tools_fallback" {
			sawDiag = true
		}
	}
	if !sawDiag {
		t.Fatalf("expected anthropic_strict_tools_fallback diagnostic, got %+v", res.Diagnostics)
	}

	// 2. GitHub Copilot Anthropic dynamic headers (Bearer auth, X-Initiator agent vs user, Copilot-Vision-Request).
	var seenCopilotAuth, seenInitiator, seenVision, seenXApiKey string
	srvCopilot := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenCopilotAuth = r.Header.Get("Authorization")
		seenInitiator = r.Header.Get("X-Initiator")
		seenVision = r.Header.Get("Copilot-Vision-Request")
		seenXApiKey = r.Header.Get("x-api-key")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_c\",\"usage\":{\"input_tokens\":5,\"output_tokens\":0}}}\n\n"+
			"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n"+
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer srvCopilot.Close()

	copHeaders := omap.NewMap[string, string]()
	copHeaders.Set("User-Agent", "GitHubCopilotChat/0.35.0")
	copHeaders.Set("Copilot-Integration-Id", "vscode-chat")
	copModel := &ai.Model{
		BaseModel: ai.BaseModel{
			Id:       "claude-sonnet-4.6",
			Name:     "claude-sonnet-4.6",
			Api:      string(ai.KnownApiAnthropicMessages),
			Provider: "github-copilot",
			BaseUrl:  srvCopilot.URL,
			Input:    []ai.ModelModality{ai.ModelModalityText, ai.ModelModalityImage},
			Headers:  copHeaders,
		},
		ContextWindow: 200000,
		MaxTokens:     8192,
	}
	imgCtx := ai.NormalizeContext(&ai.Context{
		Messages: ai.MessageList{
			&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tafcStrPtr("q")}, Timestamp: 1},
			&ai.AssistantMessage{
				Role: "assistant", Api: ai.Api(copModel.Api), Provider: copModel.Provider, Model: copModel.Id,
				Content:    ai.ContentList{&ai.ToolCall{Type: "toolCall", Id: "c1", Name: "t", Arguments: jsonx.NewObject()}},
				StopReason: ai.StopReasonToolUse, Timestamp: 2,
			},
			&ai.ToolResultMessage{
				Role: "toolResult", ToolCallId: "c1", ToolName: "t",
				Content: ai.ContentList{&ai.ImageContent{Type: "image", Data: "AAAA", MimeType: "image/png"}}, Timestamp: 3,
			},
		},
	})
	_, err = api.StreamAnthropicMessages(context.Background(), copModel, imgCtx, &api.AnthropicOptions{
		StreamOptions: ai.StreamOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{ApiKey: tafcStrPtr("tid_copilot_tok")},
		},
	}).Result(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if seenCopilotAuth != "Bearer tid_copilot_tok" || seenXApiKey != "" || seenInitiator != "agent" || seenVision != "true" {
		t.Fatalf("unexpected Copilot headers: auth=%q x-api-key=%q initiator=%q vision=%q",
			seenCopilotAuth, seenXApiKey, seenInitiator, seenVision)
	}
}

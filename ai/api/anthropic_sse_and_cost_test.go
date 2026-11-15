// Ported from packages/ai/test/anthropic-{sse-parsing,cache-write-1h-cost,federation}.test.ts (pi v1.5.2).

package api_test

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

func tascStrPtr(s string) *string { return &s }

func tascBoolPtr(b bool) *bool { return &b }

func tascModel(baseUrl, id, provider string, cost ai.ModelCostRates, compat ai.ModelCompat) *ai.Model {
	if provider == "" {
		provider = "anthropic"
	}
	return &ai.Model{
		BaseModel: ai.BaseModel{
			Id:       id,
			Name:     id,
			Api:      string(ai.KnownApiAnthropicMessages),
			Provider: ai.ProviderId(provider),
			BaseUrl:  baseUrl,
			Input:    []ai.ModelModality{ai.ModelModalityText},
			Cost:     ai.ModelCost{ModelCostRates: cost},
		},
		ContextWindow: 200000,
		MaxTokens:     8192,
		Compat:        ai.ModelCompatValue{V: compat},
	}
}

func tascSSE(events ...struct{ event, data string }) string {
	var b strings.Builder
	for _, ev := range events {
		b.WriteString("event: ")
		b.WriteString(ev.event)
		b.WriteString("\ndata: ")
		b.WriteString(ev.data)
		b.WriteString("\n\n")
	}
	return b.String()
}

func tascContext() *ai.TranscriptContext {
	return ai.NormalizeContext(&ai.Context{
		Messages: ai.MessageList{
			&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tascStrPtr("hi")}, Timestamp: 1},
		},
	})
}

func TestAnthropicSSEThinkingTextToolUseAndCost(t *testing.T) {
	var seenHeaders http.Header
	var seenBody string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenHeaders = r.Header.Clone()
		raw, _ := io.ReadAll(r.Body)
		seenBody = string(raw)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, tascSSE(
			struct{ event, data string }{"message_start", `{"type":"message_start","message":{"id":"msg_test","model":"claude-opus-4-8","usage":{"input_tokens":100,"output_tokens":0,"cache_read_input_tokens":50,"cache_creation_input_tokens":1000000,"cache_creation":{"ephemeral_5m_input_tokens":600000,"ephemeral_1h_input_tokens":400000}}}}`},
			struct{ event, data string }{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`},
			struct{ event, data string }{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"ponder"}}`},
			struct{ event, data string }{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig_1"}}`},
			struct{ event, data string }{"content_block_stop", `{"type":"content_block_stop","index":0}`},
			struct{ event, data string }{"content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"redacted_thinking","data":"ZW5j"}}`},
			struct{ event, data string }{"content_block_stop", `{"type":"content_block_stop","index":1}`},
			struct{ event, data string }{"content_block_start", `{"type":"content_block_start","index":2,"content_block":{"type":"text","text":""}}`},
			struct{ event, data string }{"content_block_delta", `{"type":"content_block_delta","index":2,"delta":{"type":"text_delta","text":"Hello"}}`},
			struct{ event, data string }{"content_block_stop", `{"type":"content_block_stop","index":2}`},
			struct{ event, data string }{"content_block_start", `{"type":"content_block_start","index":3,"content_block":{"type":"tool_use","id":"tu_1","name":"lookup","input":{}}}`},
			struct{ event, data string }{"content_block_delta", `{"type":"content_block_delta","index":3,"delta":{"type":"input_json_delta","partial_json":"{\"q\":\"pi\"}"}}`},
			struct{ event, data string }{"content_block_stop", `{"type":"content_block_stop","index":3}`},
			struct{ event, data string }{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"input_tokens":100,"output_tokens":20,"cache_read_input_tokens":50,"cache_creation_input_tokens":1000000}}`},
			struct{ event, data string }{"message_stop", `{"type":"message_stop"}`},
		))
	}))
	defer srv.Close()

	// claude-opus-4-8 pricing: input 5, cacheWrite(5m) 6.25 -> 1h write = 2x input = 10.
	// 600k * 6.25 + 400k * 10 = 3.75 + 4.0 = 7.75
	model := tascModel(srv.URL, "claude-opus-4-8", "anthropic", ai.ModelCostRates{
		Input: 5, Output: 25, CacheRead: 0.5, CacheWrite: 6.25,
	}, nil)

	var hookCount int
	props := omap.NewMap[string, *typebox.Schema]()
	props.Set("q", typebox.String())
	ctx := ai.NormalizeContext(&ai.Context{
		Messages: ai.MessageList{
			&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tascStrPtr("hi")}, Timestamp: 1},
		},
		Tools: []*ai.Tool{{Name: "lookup", Description: "Look up", Parameters: typebox.Object(props)}},
	})

	msg, err := api.StreamAnthropicMessages(context.Background(), model, ctx, &api.AnthropicOptions{
		StreamOptions: ai.StreamOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{ApiKey: tascStrPtr("sk-ant-api-key")},
			OnProviderStreamEvent: func(data any, m *ai.Model) error {
				hookCount++
				return nil
			},
		},
	}).Result(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if seenHeaders.Get("x-api-key") != "sk-ant-api-key" || seenHeaders.Get("anthropic-version") != "2023-06-01" {
		t.Fatalf("unexpected anthropic headers: %+v", seenHeaders)
	}
	if !strings.Contains(seenBody, `"stream":true`) {
		t.Fatalf("expected stream:true in body: %s", seenBody)
	}
	if msg.StopReason != ai.StopReasonToolUse || msg.ResponseId == nil || *msg.ResponseId != "msg_test" {
		t.Fatalf("unexpected stop/responseId: %+v", msg)
	}
	if hookCount < 14 || len(msg.Content) != 4 {
		t.Fatalf("expected >=14 SSE hook calls and 4 blocks (thinking+redacted+text+toolUse), got hooks=%d blocks=%d", hookCount, len(msg.Content))
	}
	th := msg.Content[0].(*ai.ThinkingContent)
	if th.Thinking != "ponder" || th.ThinkingSignature == nil || *th.ThinkingSignature != "sig_1" {
		t.Fatalf("unexpected thinking block: %+v", th)
	}
	red := msg.Content[1].(*ai.ThinkingContent)
	if red.Redacted == nil || !*red.Redacted || red.Thinking != "[Reasoning redacted]" || red.ThinkingSignature == nil || *red.ThinkingSignature != "ZW5j" {
		t.Fatalf("unexpected redacted thinking block: %+v", red)
	}
	tc := msg.Content[3].(*ai.ToolCall)
	if q, _ := tc.Arguments.GetString("q"); tc.Name != "lookup" || q != "pi" {
		t.Fatalf("unexpected toolCall: %+v", tc)
	}
	if msg.Usage.CacheWrite != 1_000_000 || msg.Usage.CacheWrite1h == nil || *msg.Usage.CacheWrite1h != 400_000 || math.Abs(msg.Usage.Cost.CacheWrite-7.75) > 1e-9 {
		t.Fatalf("expected 1h cache write pricing (7.75), got usage=%+v", msg.Usage)
	}
}

func TestAnthropicMessageDeltaOnlyCacheCreationAndFallbackCost(t *testing.T) {
	// Vercel AI Gateway / message_delta-only cache creation (#9210) + allowedFallbackModels cost resolution.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, tascSSE(
			struct{ event, data string }{"message_start", `{"type":"message_start","message":{"id":"msg_fb","model":"claude-haiku-fallback","usage":{"input_tokens":0,"output_tokens":0}}}`},
			struct{ event, data string }{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":100000,"output_tokens":50000,"cache_creation_input_tokens":500000,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":500000}}}`},
			struct{ event, data string }{"message_stop", `{"type":"message_stop"}`},
		))
	}))
	defer srv.Close()

	model := tascModel(srv.URL, "claude-opus-4-8", "anthropic", ai.ModelCostRates{
		Input: 5, Output: 25, CacheWrite: 6.25,
	}, &ai.AnthropicMessagesCompat{
		AllowedFallbackModels: []ai.AnthropicAllowedFallbackModel{
			{
				Provider: "anthropic",
				Model:    "claude-haiku-fallback",
				Cost: ai.ModelCost{
					ModelCostRates: ai.ModelCostRates{Input: 1, Output: 5, CacheWrite: 1.25},
				},
			},
		},
	})

	msg, err := api.StreamAnthropicMessages(context.Background(), model, tascContext(), &api.AnthropicOptions{
		StreamOptions: ai.StreamOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{ApiKey: tascStrPtr("sk-ant-test")},
		},
	}).Result(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if msg.ResponseModel == nil || *msg.ResponseModel != "claude-haiku-fallback" {
		t.Fatalf("expected responseModel claude-haiku-fallback, got %v", msg.ResponseModel)
	}
	// Using fallback rates (Input=1, 1h cacheWrite = 2*1 = 2 per Mtok -> 500k = 1.0; input 100k = 0.1; output 50k * 5 = 0.25).
	if msg.Usage.CacheWrite1h == nil || *msg.Usage.CacheWrite1h != 500_000 || math.Abs(msg.Usage.Cost.CacheWrite-1.0) > 1e-9 || math.Abs(msg.Usage.Cost.Input-0.1) > 1e-9 {
		t.Fatalf("expected fallback 1h cache pricing, got usage=%+v", msg.Usage)
	}
}

func TestAnthropicWorkloadIdentityFederationAndErrors(t *testing.T) {
	tokenFile := filepath.Join(t.TempDir(), "identity.jwt")
	// Fake JWT (unsigned test token) with far-future exp so cache doesn't mandate immediate refresh until exchanged.
	if err := os.WriteFile(tokenFile, []byte("header.e30.sig"), 0o600); err != nil {
		t.Fatal(err)
	}

	var tokenGrantType, tokenRuleId, seenMsgAuth, seenMsgBetas string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/oauth/token":
			raw, _ := io.ReadAll(r.Body)
			var obj *jsonx.Object
			_ = jsonx.Unmarshal(raw, &obj)
			tokenGrantType, _ = obj.GetString("grant_type")
			tokenRuleId, _ = obj.GetString("federation_rule_id")
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"access_token":"fed-access-tok","expires_in":3600}`)
		case "/v1/messages":
			seenMsgAuth = r.Header.Get("Authorization")
			seenMsgBetas = r.Header.Get("anthropic-beta")
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, tascSSE(
				struct{ event, data string }{"message_start", `{"type":"message_start","message":{"id":"msg_fed","usage":{"input_tokens":1,"output_tokens":0}}}`},
				struct{ event, data string }{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`},
				struct{ event, data string }{"message_stop", `{"type":"message_stop"}`},
			))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	fedEnv := ai.ProviderEnv{
		"ANTHROPIC_FEDERATION_RULE_ID":  "fdrl_test",
		"ANTHROPIC_ORGANIZATION_ID":     "org-test",
		"ANTHROPIC_SERVICE_ACCOUNT_ID":  "svac_test",
		"ANTHROPIC_IDENTITY_TOKEN_FILE": tokenFile,
	}
	model := tascModel(srv.URL, "claude-test", "anthropic", ai.ModelCostRates{}, nil)
	msg, err := api.StreamAnthropicMessages(context.Background(), model, tascContext(), &api.AnthropicOptions{
		StreamOptions: ai.StreamOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{Env: fedEnv},
		},
	}).Result(context.Background())
	if err != nil || msg.StopReason != ai.StopReasonStop {
		t.Fatalf("federation stream failed: %v, %+v", err, msg)
	}
	if tokenGrantType != "urn:ietf:params:oauth:grant-type:jwt-bearer" || tokenRuleId != "fdrl_test" {
		t.Fatalf("unexpected OAuth token request: grant=%q rule=%q", tokenGrantType, tokenRuleId)
	}
	if seenMsgAuth != "Bearer fed-access-tok" || strings.Contains(seenMsgBetas, "oauth-2025-04-20") {
		t.Fatalf("unexpected federated /v1/messages auth/betas: auth=%q betas=%q", seenMsgAuth, seenMsgBetas)
	}

	// Non-anthropic providers must NOT federate (#10177).
	kimi := tascModel(srv.URL, "claude-test", "kimi-coding", ai.ModelCostRates{}, nil)
	kimiMsg, _ := api.StreamAnthropicMessages(context.Background(), kimi, tascContext(), &api.AnthropicOptions{
		StreamOptions: ai.StreamOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{Env: fedEnv},
		},
	}).Result(context.Background())
	if kimiMsg.StopReason != ai.StopReasonError || kimiMsg.ErrorMessage == nil || !strings.Contains(strings.ToLower(fmt.Sprintf("%s", *kimiMsg.ErrorMessage)), "api key") {
		t.Fatalf("expected non-anthropic provider not to federate, got %+v", kimiMsg)
	}

	// 4. Stream ending after message_start/content_block without message_delta stop_reason errors as missing stop reason.
	srvNoStop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_nostop\",\"model\":\"claude-sonnet-4-5\",\"usage\":{\"input_tokens\":3,\"output_tokens\":0}}}\n\n"+
			"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"+
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"partial\"}}\n\n"+
			"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n"+
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer srvNoStop.Close()

	noStopMsg, err := api.StreamAnthropicMessages(context.Background(), tascModel(srvNoStop.URL, "claude-sonnet-4-5", "", ai.ModelCostRates{}, nil), ai.NormalizeContext(&ai.Context{
		Messages: ai.MessageList{&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tascStrPtr("hi")}, Timestamp: 1}},
	}), &api.AnthropicOptions{
		StreamOptions: ai.StreamOptions{ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{ApiKey: tascStrPtr("k")}},
	}).Result(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if noStopMsg.StopReason != ai.StopReasonError || noStopMsg.ErrorMessage == nil ||
		*noStopMsg.ErrorMessage != "Anthropic stream ended without a stop reason" {
		t.Fatalf("expected missing stop reason error, got stop=%q errMsg=%v", noStopMsg.StopReason, noStopMsg.ErrorMessage)
	}
}

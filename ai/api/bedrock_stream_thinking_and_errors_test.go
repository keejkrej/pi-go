// Ported from packages/ai/test/bedrock-{raw-stop-reason,cache-write-1h-cost,thinking-payload,redacted-reasoning,error-metadata,strict-tool-fallback}.test.ts (pi v1.5.2).

package api_test

import (
	"context"
	"io"
	"math"
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

func TestBedrockStreamEventsRedactedReasoningCache1hCostAndRawStop(t *testing.T) {
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")

	// Construct real AWS EventStream binary payload containing:
	// 0: reasoningContent text + signature
	// 1: redactedContent (buffered & emitted cleanly)
	// 2: text block
	// 3: toolUse delta
	// messageStop + metadata with 1h + 5m cacheDetails (#9457)
	var body []byte
	body = append(body, tbdEncodeEventFrame("messageStart", `{"role":"assistant"}`)...)
	body = append(body, tbdEncodeEventFrame("contentBlockStart", `{"contentBlockIndex":0}`)...)
	body = append(body, tbdEncodeEventFrame("contentBlockDelta", `{"contentBlockIndex":0,"delta":{"reasoningContent":{"text":"ponder"}}}`)...)
	body = append(body, tbdEncodeEventFrame("contentBlockDelta", `{"contentBlockIndex":0,"delta":{"reasoningContent":{"signature":"sig_bcs"}}}`)...)
	body = append(body, tbdEncodeEventFrame("contentBlockStop", `{"contentBlockIndex":0}`)...)
	body = append(body, tbdEncodeEventFrame("contentBlockStart", `{"contentBlockIndex":1}`)...)
	body = append(body, tbdEncodeEventFrame("contentBlockDelta", `{"contentBlockIndex":1,"delta":{"reasoningContent":{"redactedContent":"cmVk"}}}`)...)
	body = append(body, tbdEncodeEventFrame("contentBlockDelta", `{"contentBlockIndex":1,"delta":{"reasoningContent":{"redactedContent":"YWN0"}}}`)...)
	body = append(body, tbdEncodeEventFrame("contentBlockStop", `{"contentBlockIndex":1}`)...)
	body = append(body, tbdEncodeEventFrame("contentBlockStart", `{"contentBlockIndex":2}`)...)
	body = append(body, tbdEncodeEventFrame("contentBlockDelta", `{"contentBlockIndex":2,"delta":{"text":"hello"}}`)...)
	body = append(body, tbdEncodeEventFrame("contentBlockStop", `{"contentBlockIndex":2}`)...)
	body = append(body, tbdEncodeEventFrame("contentBlockStart", `{"contentBlockIndex":3,"start":{"toolUse":{"toolUseId":"tu_1","name":"lookup"}}}`)...)
	body = append(body, tbdEncodeEventFrame("contentBlockDelta", `{"contentBlockIndex":3,"delta":{"toolUse":{"input":"{\"q\":\"pi\"}"}}}`)...)
	body = append(body, tbdEncodeEventFrame("contentBlockStop", `{"contentBlockIndex":3}`)...)
	body = append(body, tbdEncodeEventFrame("messageStop", `{"stopReason":"tool_use","additionalModelResponseFields":{"source":"test"}}`)...)
	body = append(body, tbdEncodeEventFrame("metadata", `{"usage":{"inputTokens":100,"outputTokens":5,"totalTokens":1000105,"cacheWriteInputTokens":1000000,"cacheDetails":[{"ttl":"1h","inputTokens":150000},{"ttl":"5m","inputTokens":600000},{"ttl":"1h","inputTokens":250000}]}}`)...)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	var hookEvents int
	props := omap.NewMap[string, *typebox.Schema]()
	props.Set("q", typebox.String())
	transcript := ai.NormalizeContext(&ai.Context{
		Messages: ai.MessageList{
			&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tbdStrPtr("hi")}, Timestamp: 1},
		},
		Tools: []*ai.Tool{{Name: "lookup", Description: "Look up", Parameters: typebox.Object(props)}},
	})

	model := tbdModel("us.anthropic.claude-opus-4-8", "", srv.URL, nil)
	retNone := ai.CacheRetentionNone
	msg, err := api.StreamBedrockConverseStream(context.Background(), model, transcript, &api.BedrockOptions{
		StreamOptions: ai.StreamOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
				Env: ai.ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1", "AWS_REGION": "us-east-1"},
			},
			CacheRetention: &retNone,
			OnProviderStreamEvent: func(data any, m *ai.Model) error {
				hookEvents++
				return nil
			},
		},
	}).Result(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if hookEvents < 15 || msg.StopReason != ai.StopReasonToolUse || msg.RawStopReason == nil || *msg.RawStopReason != "tool_use" {
		t.Fatalf("unexpected stop/hooks: hooks=%d msg=%+v", hookEvents, msg)
	}
	if len(msg.Content) != 4 {
		t.Fatalf("expected 4 content blocks, got %d (%+v)", len(msg.Content), msg.Content)
	}
	red := msg.Content[1].(*ai.ThinkingContent)
	if red.Redacted == nil || !*red.Redacted || red.Thinking != "[Reasoning redacted]" || red.ThinkingSignature == nil {
		t.Fatalf("expected redacted thinking block, got %+v", red)
	}
	// 1h cache details: 150k + 250k = 400k @ 2x input (10/Mtok = 4.0); 5m: 600k @ 6.25/Mtok = 3.75 => 7.75
	if msg.Usage.CacheWrite != 1_000_000 || msg.Usage.CacheWrite1h == nil || *msg.Usage.CacheWrite1h != 400_000 || math.Abs(msg.Usage.Cost.CacheWrite-7.75) > 1e-9 {
		t.Fatalf("expected Bedrock 1h cache write pricing (7.75), got usage=%+v", msg.Usage)
	}
}

func TestBedrockThinkingPayloadAndInferenceProfiles(t *testing.T) {
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")

	var lastBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		lastBody = string(raw)
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		_, _ = w.Write(tbdEncodeEventFrame("messageStart", `{"role":"assistant"}`))
		_, _ = w.Write(tbdEncodeEventFrame("messageStop", `{"stopReason":"end_turn"}`))
	}))
	defer srv.Close()

	captureWirePayload := func(model *ai.Model, r ai.ThinkingLevel) *jsonx.Object {
		lastBody = ""
		res, err := api.StreamSimpleBedrockConverseStream(context.Background(), model, tbdContext(), &ai.SimpleStreamOptions{
			StreamOptions: ai.StreamOptions{
				ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
					Env: ai.ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1", "AWS_REGION": "us-east-1"},
				},
			},
			Reasoning: &r,
		}).Result(context.Background())
		if err != nil || res.StopReason != ai.StopReasonStop {
			t.Fatalf("unexpected result for %s: err=%v res=%+v", model.Id, err, res)
		}
		var wire *jsonx.Object
		if err := jsonx.Unmarshal([]byte(lastBody), &wire); err != nil {
			t.Fatalf("unmarshal wire body for %s (%s): %v", model.Id, lastBody, err)
		}
		return wire
	}

	// 1. GPT-6 sends additionalModelRequestFields.reasoning.effort
	pGpt := captureWirePayload(tbdModel("global.openai.gpt-6-sol", "", srv.URL, nil), ai.ThinkingLevelHigh)
	addGpt, _ := pGpt.GetObject("additionalModelRequestFields")
	rGpt, _ := addGpt.GetObject("reasoning")
	if eff, _ := rGpt.GetString("effort"); eff != "high" {
		t.Fatalf("expected GPT reasoning.effort=high, got %v", addGpt)
	}

	// 2. GPT-OSS sends flat reasoning_effort clamped to high
	pOss := captureWirePayload(tbdModel("openai.gpt-oss-120b-1:0", "", srv.URL, nil), ai.ThinkingLevelXhigh)
	addOss, _ := pOss.GetObject("additionalModelRequestFields")
	if eff, _ := addOss.GetString("reasoning_effort"); eff != "high" {
		t.Fatalf("expected GPT-OSS reasoning_effort=high (clamped from xhigh), got %v", addOss)
	}

	// 3. Application inference profile ARN identified by model.Name enables adaptive thinking (#9331).
	arnId := "arn:aws:bedrock:us-east-1:123456789012:application-inference-profile/my-profile"
	pArn := captureWirePayload(tbdModel(arnId, "Claude Opus 4.6", srv.URL, nil), ai.ThinkingLevelMedium)
	addArn, _ := pArn.GetObject("additionalModelRequestFields")
	if !addArn.Has("thinking") {
		t.Fatalf("expected adaptive thinking payload on Claude Opus 4.6 profile, got %v", addArn)
	}
}

func TestBedrockStreamExceptionAndStrictToolFallback(t *testing.T) {
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")

	// 1. Stream exception frame yields error + bedrock_response_failure diagnostic.
	excBody := append(
		tbdEncodeEventFrame("messageStart", `{"role":"assistant"}`),
		tbdEncodeExceptionFrame("internalServerException", `{"message":"bedrock stream failed"}`)...,
	)
	srvExc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		_, _ = w.Write(excBody)
	}))
	defer srvExc.Close()

	resExc, _ := api.StreamBedrockConverseStream(context.Background(), tbdModel("us.anthropic.claude-opus-4-8", "", srvExc.URL, nil), tbdContext(), &api.BedrockOptions{
		StreamOptions: ai.StreamOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
				Env: ai.ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1", "AWS_REGION": "us-east-1"},
			},
		},
	}).Result(context.Background())
	if resExc.StopReason != ai.StopReasonError || resExc.ErrorMessage == nil || !strings.Contains(*resExc.ErrorMessage, "bedrock stream failed") {
		t.Fatalf("expected stream exception error, got %+v", resExc)
	}

	// 2. Strict tool fallback retries when Bedrock returns union-limit ValidationException.
	var calls atomic.Int32
	srvStrict := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("x-amzn-errortype", "ValidationException")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"message":"The model returned the following errors: Schemas contains too many parameters with union types (20 parameters with type arrays or anyOf). This causes exponential compilation cost. Reduce the number of nullable or union-typed parameters (limit: 16 parameters with unions)."}`))
			return
		}
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		_, _ = w.Write(tbdEncodeEventFrame("messageStart", `{"role":"assistant"}`))
		_, _ = w.Write(tbdEncodeEventFrame("messageStop", `{"stopReason":"end_turn"}`))
	}))
	defer srvStrict.Close()

	props := omap.NewMap[string, *typebox.Schema]()
	props.Set("v", typebox.String())
	strictModel := tbdModel("us.anthropic.claude-opus-5-5", "", srvStrict.URL, &ai.BedrockCompat{
		SupportsStrictMode: tbdBoolPtr(true),
	})
	prefTool := &ai.Tool{
		Name: "preferred", Description: "d", Parameters: typebox.Object(props),
		ConstrainedSampling: ai.ToolConstrainedSamplingValue{
			V: &ai.ConstrainedSamplingJsonSchema{Type: "json_schema", Strict: ai.ConstrainedSamplingStrictPrefer},
		},
	}
	reqTool := &ai.Tool{
		Name: "required", Description: "d", Parameters: typebox.Object(props),
		ConstrainedSampling: ai.ToolConstrainedSamplingValue{
			V: &ai.ConstrainedSamplingJsonSchema{Type: "json_schema", Strict: ai.ConstrainedSamplingStrictRequire},
		},
	}
	toolCtx := ai.NormalizeContext(&ai.Context{
		Messages: ai.MessageList{
			&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tbdStrPtr("hi")}, Timestamp: 1},
		},
		Tools: []*ai.Tool{prefTool, reqTool},
	})

	var payloadAttemptCount atomic.Int32
	resStrict, err := api.StreamBedrockConverseStream(context.Background(), strictModel, toolCtx, &api.BedrockOptions{
		StreamOptions: ai.StreamOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
				Env: ai.ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1", "AWS_REGION": "us-east-1"},
				OnPayload: func(p any, m *ai.Model) (any, error) {
					payloadAttemptCount.Add(1)
					return p, nil
				},
			},
			SessionId: tbdStrPtr("bcs-strict-sess"),
		},
	}).Result(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if resStrict.StopReason != ai.StopReasonStop || calls.Load() != 2 || payloadAttemptCount.Load() != 2 {
		t.Fatalf("expected 2 attempts and stop on strict fallback, calls=%d payloads=%d msg=%+v", calls.Load(), payloadAttemptCount.Load(), resStrict)
	}
	var sawDiag bool
	for _, d := range resStrict.Diagnostics {
		if d.Type == "bedrock_strict_tools_fallback" {
			sawDiag = true
		}
	}
	if !sawDiag {
		t.Fatalf("expected bedrock_strict_tools_fallback diagnostic, got %+v", resStrict.Diagnostics)
	}
}

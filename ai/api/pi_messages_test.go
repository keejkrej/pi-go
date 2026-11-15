// Ported from packages/ai/test/pi-messages.test.ts (pi v1.5.2).

package api_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func tpmModel(baseUrl string) *ai.Model {
	return &ai.Model{
		BaseModel: ai.BaseModel{
			Id:       "claude-sonnet",
			Name:     "claude-sonnet",
			Api:      string(ai.KnownApiPiMessages),
			Provider: "radius",
			BaseUrl:  baseUrl,
			Input:    []ai.ModelModality{ai.ModelModalityText},
		},
		MaxTokens:     4096,
		ContextWindow: 128000,
	}
}

func tpmDrainEvents(ctx context.Context, stream *ai.AssistantMessageEventStream) []ai.AssistantMessageEvent {
	var out []ai.AssistantMessageEvent
	for {
		ev, ok, err := stream.Next(ctx)
		if err != nil || !ok {
			break
		}
		out = append(out, ev)
	}
	return out
}

func TestPiMessagesStreamingAndDiagnostics(t *testing.T) {
	var seenQuery string
	var seenAuth string
	var seenBody *jsonx.Object

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenQuery = r.URL.RawQuery
		seenAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		_ = jsonx.Unmarshal(raw, &seenBody)

		w.Header().Set("Content-Type", "text/event-stream")
		sseLines := []string{
			`data: {"type":"start"}`,
			`data: {"type":"thinking_start","contentIndex":0}`,
			`data: {"type":"thinking_delta","contentIndex":0,"delta":"let me think"}`,
			`data: {"type":"thinking_end","contentIndex":0,"content":"let me think","contentSignature":"sig-t","redacted":false}`,
			`data: {"type":"text_start","contentIndex":1}`,
			`data: {"type":"text_delta","contentIndex":1,"delta":"hi"}`,
			`data: {"type":"text_end","contentIndex":1,"content":"hi there","contentSignature":"sig-x"}`,
			`data: {"type":"toolcall_start","contentIndex":2,"id":"call_1","toolName":"bash"}`,
			`data: {"type":"toolcall_delta","contentIndex":2,"delta":"{\"cmd\":\"l"}`,
			`data: {"type":"toolcall_end","contentIndex":2,"toolCall":{"type":"toolCall","id":"call_1","name":"bash","arguments":{"cmd":"ls"},"namespace":"core"}}`,
			`data: {"type":"done","reason":"toolUse","usage":{"input":10,"output":5,"cacheRead":0,"cacheWrite":0,"totalTokens":15,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"responseId":"resp-99","responseModel":"claude-actual","providerThinkingLevel":"high","rewrite":{"policyId":"pol-1","policyVersion":2,"changed":true,"tokenCountChange":12,"messageCountChange":0,"systemPromptChanged":true}}`,
		}
		for _, line := range sseLines {
			_, _ = io.WriteString(w, line+"\n\n")
		}
	}))
	defer srv.Close()

	apiKey := "pm-key"
	debug := true
	maxTok := 512
	var hookEventCount int
	var onRespStatus int

	ctx := context.Background()
	userText := "run ls"
	transcriptCtx := ai.NormalizeContext(&ai.Context{
		Messages: ai.MessageList{
			&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: &userText}},
		},
	})

	stream := api.StreamPiMessages(ctx, tpmModel(srv.URL+"/"), transcriptCtx, &api.PiMessagesOptions{
		StreamOptions: ai.StreamOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
				ApiKey: &apiKey,
				Env:    ai.ProviderEnv{"PI_CACHE_RETENTION": "long"},
				OnResponse: func(resp *ai.ProviderResponse, m *ai.Model) error {
					onRespStatus = resp.Status
					return nil
				},
			},
			MaxTokens: &maxTok,
			OnProviderStreamEvent: func(data any, m *ai.Model) error {
				hookEventCount++
				return nil
			},
		},
		Debug: &debug,
	})
	events := tpmDrainEvents(ctx, stream)
	msg, err := stream.Result(ctx)
	if err != nil {
		t.Fatalf("stream.Result error: %v", err)
	}
	if seenQuery != "debug=1" || seenAuth != "Bearer pm-key" || onRespStatus != 200 || hookEventCount != 11 {
		t.Fatalf("unexpected transport state: query=%q auth=%q resp=%d hookCount=%d", seenQuery, seenAuth, onRespStatus, hookEventCount)
	}
	if optsObj, ok := seenBody.GetObject("options"); !ok {
		t.Fatalf("expected options object, got %v", seenBody)
	} else if cr, _ := optsObj.GetString("cacheRetention"); cr != "long" {
		t.Fatalf("expected PI_CACHE_RETENTION=long mapped to cacheRetention, got %+v", optsObj)
	}
	if msg.StopReason != ai.StopReasonToolUse || msg.ResponseId == nil || *msg.ResponseId != "resp-99" {
		t.Fatalf("unexpected terminal assistant message: %+v", msg)
	}
	if len(msg.Content) != 3 {
		t.Fatalf("expected 3 content blocks, got %d (%d events)", len(msg.Content), len(events))
	}
	if tc, ok := msg.Content[2].(*ai.ToolCall); !ok || tc.Namespace == nil || *tc.Namespace != "core" {
		t.Fatalf("expected toolCall with namespace 'core', got %#v", msg.Content[2])
	}
	if len(msg.Diagnostics) != 1 || msg.Diagnostics[0].Type != "pi_messages_rewrite" {
		t.Fatalf("expected pi_messages_rewrite diagnostic, got %+v", msg.Diagnostics)
	}
}

func TestPiMessagesHttpErrorAndInvalidContentIndex(t *testing.T) {
	srvErr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"bad input","code":"invalid_request"}}`))
	}))
	defer srvErr.Close()

	apiKey := "pm-key"
	onRespCalled := false
	transcriptCtx := ai.NormalizeContext(&ai.Context{})
	stream := api.StreamPiMessages(context.Background(), tpmModel(srvErr.URL), transcriptCtx, &api.PiMessagesOptions{
		StreamOptions: ai.StreamOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
				ApiKey: &apiKey,
				OnResponse: func(resp *ai.ProviderResponse, m *ai.Model) error {
					onRespCalled = true
					return nil
				},
			},
		},
	})
	res, err := stream.Result(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !onRespCalled {
		t.Fatal("expected OnResponse to run before error throw")
	}
	if res.StopReason != ai.StopReasonError || res.ErrorMessage == nil || !strings.Contains(*res.ErrorMessage, "400 Bad Request: bad input (invalid_request)") {
		t.Fatalf("unexpected error message: %+v", res)
	}
	if len(res.Diagnostics) != 1 || res.Diagnostics[0].Type != "pi_messages_response_failure" {
		t.Fatalf("expected failure diagnostic, got %+v", res.Diagnostics)
	}

	// Sparse / invalid contentIndex fails cleanly with error event.
	srvBadIdx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"type":"text_start","contentIndex":5}`+"\n\n")
	}))
	defer srvBadIdx.Close()
	stream2 := api.StreamPiMessages(context.Background(), tpmModel(srvBadIdx.URL), transcriptCtx, &api.PiMessagesOptions{
		StreamOptions: ai.StreamOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{ApiKey: &apiKey},
		},
	})
	res2, _ := stream2.Result(context.Background())
	if res2.StopReason != ai.StopReasonError || res2.ErrorMessage == nil || !strings.Contains(*res2.ErrorMessage, "Invalid contentIndex") {
		t.Fatalf("expected invalid contentIndex error, got %+v", res2)
	}

	// Huge float contentIndex (e.g. 1e20) must reject cleanly instead of overflowing int cast.
	srvOverflowIdx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"type":"text_start","contentIndex":1e20}`+"\n\n")
	}))
	defer srvOverflowIdx.Close()
	res3, _ := api.StreamPiMessages(context.Background(), tpmModel(srvOverflowIdx.URL), transcriptCtx, &api.PiMessagesOptions{
		StreamOptions: ai.StreamOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{ApiKey: &apiKey},
		},
	}).Result(context.Background())
	if res3.StopReason != ai.StopReasonError || res3.ErrorMessage == nil || !strings.Contains(*res3.ErrorMessage, "Invalid contentIndex") {
		t.Fatalf("expected invalid contentIndex error on huge float, got %+v", res3)
	}
}

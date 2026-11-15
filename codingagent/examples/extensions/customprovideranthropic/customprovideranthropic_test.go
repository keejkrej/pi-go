package customprovideranthropic

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
)

func TestCustomProviderAnthropicTS(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" || r.Method != http.MethodPost {
			http.Error(w, "bad path/method", http.StatusNotFound)
			return
		}
		if r.Header.Get("x-api-key") != "test-key" {
			http.Error(w, "missing key", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"type":"message_start","message":{"usage":{"input_tokens":5,"output_tokens":0}}}`,
			``,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			``,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi from SSE!"}}`,
			``,
			`data: {"type":"content_block_stop","index":0}`,
			``,
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":7}}`,
			``,
		}, "\n")))
	}))
	defer srv.Close()

	ctx := context.Background()
	res := core.LoadExtensions(ctx, []string{"."}, ".")
	if len(res.Errors) != 0 {
		t.Fatalf("LoadExtensions(.) errors: %+v", res.Errors)
	}
	runner := core.NewExtensionRunner(res.Extensions, res.Runtime, ".", nil, nil)
	defer runner.Shutdown()

	if len(res.Runtime.PendingProviderRegistrations) != 1 {
		t.Fatalf("expected 1 pending provider, got %d", len(res.Runtime.PendingProviderRegistrations))
	}
	reg := res.Runtime.PendingProviderRegistrations[0]
	if reg.Name != "custom-anthropic" || reg.Config.StreamSimple == nil || reg.Config.OAuth == nil {
		t.Fatalf("unexpected provider registration: %+v", reg)
	}

	model := &ai.Model{
		Id:       "claude-sonnet-4-5",
		Api:      "custom-anthropic-api",
		Provider: "custom-anthropic",
		BaseUrl:  srv.URL,
	}
	tx := &ai.TranscriptContext{
		Messages: []ai.Message{
			&ai.UserMessage{
				Role:    "user",
				Content: ai.UserMessageContent{Blocks: ai.ContentList{&ai.TextContent{Type: "text", Text: "hello"}}},
			},
		},
	}
	apiKey := "test-key"
	stream := reg.Config.StreamSimple(ctx, model, tx, &ai.SimpleStreamOptions{StreamOptions: ai.StreamOptions{ApiKey: &apiKey}})
	var deltas []string
	for ev := range stream.All() {
		if td, ok := ev.(*ai.AssistantMessageEventTextDelta); ok {
			deltas = append(deltas, td.Delta)
		}
	}
	finalMsg, err := stream.Result(ctx)
	if err != nil || finalMsg == nil {
		t.Fatalf("stream result failed: msg=%+v err=%v", finalMsg, err)
	}
	if strings.Join(deltas, "") != "Hi from SSE!" {
		t.Fatalf("unexpected streamed text: %q", strings.Join(deltas, ""))
	}
	if finalMsg.StopReason != ai.StopReasonStop || finalMsg.Usage.TotalTokens != 12 {
		t.Fatalf("unexpected final message stop/usage: stop=%s usage=%+v", finalMsg.StopReason, finalMsg.Usage)
	}
}

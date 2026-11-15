// Ported from packages/ai/test/mistral-raw-stop-reason.test.ts (pi v1.5.2).

package api_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
)

func tmrspStrPtr(s string) *string { return &s }

func tmrspModel(baseUrl string) *ai.Model {
	return &ai.Model{
		BaseModel: ai.BaseModel{
			Id:       "devstral-medium-latest",
			Name:     "devstral-medium-latest",
			Api:      string(ai.KnownApiMistralConversations),
			Provider: "mistral",
			BaseUrl:  baseUrl,
			Input:    []ai.ModelModality{ai.ModelModalityText},
		},
		ContextWindow: 128000,
		MaxTokens:     16384,
	}
}

func tmrspServeFinishReason(t *testing.T, finishReason string) *ai.AssistantMessage {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		payload := fmt.Sprintf(
			`{"id":"mistral-response-id","model":"devstral-medium-latest","choices":[{"index":0,"finish_reason":%q,"delta":{}}],"usage":{"prompt_tokens":1,"completion_tokens":0,"total_tokens":1}}`,
			finishReason,
		)
		_, _ = io.WriteString(w, "data: "+payload+"\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()

	transcript := ai.NormalizeContext(&ai.Context{
		Messages: ai.MessageList{
			&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tmrspStrPtr("hello")}, Timestamp: 1},
		},
	})
	msg, err := api.StreamMistralConversations(context.Background(), tmrspModel(srv.URL), transcript, &api.MistralOptions{
		StreamOptions: ai.StreamOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{ApiKey: tmrspStrPtr("test")},
		},
	}).Result(context.Background())
	if err != nil {
		t.Fatalf("Result error: %v", err)
	}
	return msg
}

func TestMistralRawStopReasons(t *testing.T) {
	msgStop := tmrspServeFinishReason(t, "stop")
	if msgStop.StopReason != ai.StopReasonStop || msgStop.RawStopReason == nil || *msgStop.RawStopReason != "stop" || msgStop.ErrorMessage != nil {
		t.Fatalf("unexpected stop result: %+v", msgStop)
	}

	msgErr := tmrspServeFinishReason(t, "error")
	if msgErr.StopReason != ai.StopReasonError || msgErr.RawStopReason == nil || *msgErr.RawStopReason != "error" ||
		msgErr.ErrorMessage == nil || *msgErr.ErrorMessage != "Provider stopped with: error (server error)" {
		t.Fatalf("unexpected error finish_reason result: %+v", msgErr)
	}
	if !ai.IsRetryableAssistantError(msgErr) {
		t.Fatal("expected error finish_reason to be retryable (#10487)")
	}

	msgUnknown := tmrspServeFinishReason(t, "unmapped_error")
	if msgUnknown.StopReason != ai.StopReasonError || msgUnknown.RawStopReason == nil || *msgUnknown.RawStopReason != "unmapped_error" ||
		msgUnknown.ErrorMessage == nil || *msgUnknown.ErrorMessage != "Provider stopped with: unmapped_error" {
		t.Fatalf("unexpected unmapped_error result: %+v", msgUnknown)
	}
	if ai.IsRetryableAssistantError(msgUnknown) {
		t.Fatal("expected unmapped_error finish_reason not to be retryable")
	}
}

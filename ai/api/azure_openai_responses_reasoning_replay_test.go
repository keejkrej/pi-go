package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
)

type tarRoundTripperFunc func(*http.Request) (*http.Response, error)

func (f tarRoundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func tarStrPtr(s string) *string { return &s }

func tarModel() *ai.Model {
	return &ai.Model{
		Id:            "gpt-5.2",
		Name:          "GPT-5.2 Azure",
		Api:           string(ai.KnownApiAzureOpenaiResponses),
		Provider:      "azure-openai-responses",
		BaseUrl:       "https://my-resource.openai.azure.com/openai/v1",
		Reasoning:     true,
		Input:         []ai.ModelModality{ai.ModelModalityText},
		ContextWindow: 128000,
		MaxTokens:     4096,
	}
}

func tarSseResponse(events ...string) *http.Response {
	var sb strings.Builder
	for _, ev := range events {
		sb.WriteString("data: ")
		sb.WriteString(ev)
		sb.WriteString("\n\n")
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(sb.String())),
	}
}

func TestAzureOpenAIResponsesReasoningReplay(t *testing.T) {
	t.Run("preserves encrypted_content from output_item.done and backfills when only response.completed has it", func(t *testing.T) {
		var turnBodies []map[string]any
		turn := 0
		client := &http.Client{
			Transport: tarRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
				raw, _ := io.ReadAll(req.Body)
				var body map[string]any
				_ = json.Unmarshal(raw, &body)
				turnBodies = append(turnBodies, body)
				turn++
				if turn == 1 {
					return tarSseResponse(
						`{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs_1","summary":[]}}`,
						`{"type":"response.reasoning_summary_text.delta","output_index":0,"summary_index":0,"delta":"thinking"}`,
						`{"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"thinking"}]}}`,
						`{"type":"response.output_item.added","output_index":1,"item":{"type":"message","id":"msg_1","role":"assistant","content":[]}}`,
						`{"type":"response.output_text.delta","output_index":1,"content_index":0,"delta":"done"}`,
						`{"type":"response.output_item.done","output_index":1,"item":{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"done"}]}}`,
						`{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[{"type":"reasoning","id":"rs_1","encrypted_content":"enc_from_completed","summary":[{"type":"summary_text","text":"thinking"}]}],"usage":{"input_tokens":5,"output_tokens":3,"total_tokens":8}}}`,
					), nil
				}
				return tarSseResponse(
					`{"type":"response.completed","response":{"id":"resp_2","status":"completed","usage":{"input_tokens":10,"output_tokens":1,"total_tokens":11}}}`,
				), nil
			}),
		}

		model := tarModel()
		msg1Text := "first question"
		ctx1 := ai.NormalizeContext(&ai.Context{
			Messages: ai.MessageList{
				&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: &msg1Text}, Timestamp: 1},
			},
		})
		res1, err1 := StreamAzureOpenaiResponses(context.Background(), model, ctx1, &AzureOpenAIResponsesOptions{
			AzureEndpointOptions: AzureEndpointOptions{
				StreamOptions: ai.StreamOptions{ApiKey: tarStrPtr("k"), HTTPClient: client, Env: ai.ProviderEnv{}},
			},
		}).Result(context.Background())
		if err1 != nil {
			t.Fatalf("turn 1 result error: %v", err1)
		}

		if res1.StopReason != ai.StopReasonStop {
			t.Fatalf("turn 1 failed: %v", res1.ErrorMessage)
		}
		tb, ok := res1.Content[0].(*ai.ThinkingContent)
		if !ok || tb.ThinkingSignature == nil || !strings.Contains(*tb.ThinkingSignature, "enc_from_completed") {
			t.Fatalf("expected ThinkingSignature backfilled with enc_from_completed, got %+v", res1.Content[0])
		}

		msg2Text := "follow up"
		ctx2 := ai.NormalizeContext(&ai.Context{
			Messages: ai.MessageList{
				ctx1.Messages[0],
				res1,
				&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: &msg2Text}, Timestamp: 2},
			},
		})
		res2, err2 := StreamAzureOpenaiResponses(context.Background(), model, ctx2, &AzureOpenAIResponsesOptions{
			AzureEndpointOptions: AzureEndpointOptions{
				StreamOptions: ai.StreamOptions{ApiKey: tarStrPtr("k"), HTTPClient: client, Env: ai.ProviderEnv{}},
			},
		}).Result(context.Background())
		if err2 != nil {
			t.Fatalf("turn 2 result error: %v", err2)
		}
		if res2.StopReason != ai.StopReasonStop {
			t.Fatalf("turn 2 failed: %v", res2.ErrorMessage)
		}

		rawInput, _ := json.Marshal(turnBodies[1]["input"])
		if !strings.Contains(string(rawInput), `"encrypted_content":"enc_from_completed"`) {
			t.Fatalf("expected encrypted_content replayed in turn 2 input, got %s", string(rawInput))
		}
	})
}

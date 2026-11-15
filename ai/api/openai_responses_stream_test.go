package api

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

type torsRoundTripperFunc func(*http.Request) (*http.Response, error)

func (f torsRoundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func torsStrPtr(s string) *string { return &s }

func torsSseResp(events ...string) *http.Response {
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

func torsModel(provider ai.ProviderId, input ...ai.ModelModality) *ai.Model {
	mods := input
	if len(mods) == 0 {
		mods = []ai.ModelModality{ai.ModelModalityText, ai.ModelModalityImage}
	}
	return &ai.Model{
		Id:            "gpt-5.2",
		Name:          "GPT-5.2",
		Api:           string(ai.KnownApiOpenaiResponses),
		Provider:      provider,
		BaseUrl:       "https://api.openai.com/v1",
		Reasoning:     true,
		Input:         mods,
		ContextWindow: 128000,
		MaxTokens:     16384,
		Cost:          ai.ModelCost{Input: 2, Output: 8, CacheRead: 0.5},
	}
}

func TestOpenAIResponsesConversionAndNamespace(t *testing.T) {
	t.Run("converts empty tool results, tool-result images, foreign toolCall IDs, message signatures, and tool namespaces", func(t *testing.T) {
		m := torsModel("openai")
		longMsgID := "msg_" + strings.Repeat("x", 90)
		sigV1 := `{"v":1,"id":"` + longMsgID + `","phase":"commentary"}`
		ns := "crm"

		ctx := ai.NormalizeContext(&ai.Context{
			Messages: ai.MessageList{
				&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: torsStrPtr("start")}, Timestamp: 1},
				&ai.AssistantMessage{
					Role: "assistant", Api: ai.KnownApiOpenaiResponses, Provider: "openai", Model: "gpt-5.2",
					Content: ai.ContentList{
						&ai.TextContent{Type: "text", Text: "checking", TextSignature: &sigV1},
						&ai.ToolCall{Type: "toolCall", Id: "call_1|fc_1", Name: "lookup", Namespace: &ns, Arguments: jsonx.NewObject()},
					},
					StopReason: ai.StopReasonToolUse, Timestamp: 2,
				},
				&ai.ToolResultMessage{
					Role: "toolResult", ToolCallId: "call_1|fc_1", ToolName: "lookup",
					Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "   "}}, Timestamp: 3,
				},
				// Foreign assistant message from another provider
				&ai.AssistantMessage{
					Role: "assistant", Api: ai.KnownApiPiMessages, Provider: "anthropic", Model: "claude",
					Content: ai.ContentList{
						&ai.ToolCall{Type: "toolCall", Id: "call_foreign|fc_foreign", Name: "img_tool", Arguments: jsonx.NewObject()},
					},
					StopReason: ai.StopReasonToolUse, Timestamp: 4,
				},
				&ai.ToolResultMessage{
					Role: "toolResult", ToolCallId: "call_foreign|fc_foreign", ToolName: "img_tool",
					Content: ai.ContentList{
						&ai.TextContent{Type: "text", Text: "chart:"},
						&ai.ImageContent{Type: "image", MimeType: "image/png", Data: "AAAA"},
					},
					Timestamp: 5,
				},
			},
		})

		items, err := ConvertResponsesMessages(m, ctx, map[string]struct{}{"openai": {}}, nil)
		if err != nil {
			t.Fatalf("ConvertResponsesMessages error: %v", err)
		}
		rawItems, _ := jsonx.Stringify(items)

		// Long message ID (>64) should be dropped or normalized while preserving phase commentary
		if strings.Contains(rawItems, longMsgID) {
			t.Fatalf("expected oversized >64-char message ID stripped from input items: %s", rawItems)
		}
		if !strings.Contains(rawItems, `"phase":"commentary"`) {
			t.Fatalf("expected phase=commentary preserved on assistant message item: %s", rawItems)
		}
		if !strings.Contains(rawItems, `"namespace":"crm"`) {
			t.Fatalf("expected namespace=crm preserved on same-model function_call: %s", rawItems)
		}
		if !strings.Contains(rawItems, `"output":"(no tool output)"`) {
			t.Fatalf("expected whitespace tool output normalized to (no tool output): %s", rawItems)
		}
		if strings.Contains(rawItems, `"fc_foreign"`) || !strings.Contains(rawItems, `"call_id":"call_foreign"`) {
			t.Fatalf("expected foreign fc_ item id dropped while preserving call_id=call_foreign: %s", rawItems)
		}
		if !strings.Contains(rawItems, "data:image/png;base64,AAAA") {
			t.Fatalf("expected tool-result image serialized as input_image: %s", rawItems)
		}

		// Text-only model downgrades tool-result image to placeholder text
		mTextOnly := torsModel("openai", ai.ModelModalityText)
		textItems, err := ConvertResponsesMessages(mTextOnly, ctx, map[string]struct{}{"openai": {}}, nil)
		if err != nil {
			t.Fatalf("ConvertResponsesMessages text-only error: %v", err)
		}
		rawTextItems, _ := jsonx.Stringify(textItems)
		if strings.Contains(rawTextItems, "data:image/png") {
			t.Fatalf("expected images excluded for text-only model: %s", rawTextItems)
		}
	})
}

func TestOpenAIResponsesStreamingTerminalAndUsage(t *testing.T) {
	t.Run("captures tool namespace, partial JSON cleanup, message phase/id, priority usage pricing, and terminal event errors", func(t *testing.T) {
		m := torsModel("openai")
		userCtx := ai.NormalizeContext(&ai.Context{
			Messages: ai.MessageList{&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: torsStrPtr("go")}, Timestamp: 1}},
		})

		// 1. Complete stream with namespace, partial/final tool args, message id/phase, priority tier pricing & usage
		cSuccess := &http.Client{
			Transport: torsRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
				return torsSseResp(
					`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_short","role":"assistant","phase":"final_answer","content":[]}}`,
					`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"done"}`,
					`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"msg_short","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"done"}]}}`,
					`{"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","call_id":"call_ns","id":"fc_ns","name":"search","namespace":"web","arguments":""}}`,
					`{"type":"response.function_call_arguments.delta","output_index":1,"delta":"{\"q\":\"broken"}`,
					`{"type":"response.function_call_arguments.done","output_index":1,"arguments":"{\"q\":\"fixed\"}"}`,
					`{"type":"response.output_item.done","output_index":1,"item":{"type":"function_call","call_id":"call_ns","id":"fc_ns","name":"search","namespace":"web","arguments":"{\"q\":\"fixed\"}"}}`,
					`{"type":"response.completed","response":{"id":"resp_ns","status":"completed","service_tier":"priority","usage":{"input_tokens":1000000,"output_tokens":1000000,"input_tokens_details":{"cached_tokens":200000,"cache_write_tokens":50000},"output_tokens_details":{"reasoning_tokens":100},"total_tokens":2000000}}}`,
				), nil
			}),
		}
		res, err := StreamOpenaiResponses(context.Background(), m, userCtx, &OpenAIResponsesOptions{
			StreamOptions: ai.StreamOptions{ApiKey: torsStrPtr("sk-1"), HTTPClient: cSuccess},
		}).Result(context.Background())
		if err != nil || res.StopReason != ai.StopReasonToolUse {
			t.Fatalf("unexpected result: stop=%s err=%v msg=%v", res.StopReason, err, res.ErrorMessage)
		}
		txt, _ := res.Content[0].(*ai.TextContent)
		if txt == nil || txt.TextSignature == nil || !strings.Contains(*txt.TextSignature, "msg_short") || !strings.Contains(*txt.TextSignature, "final_answer") {
			t.Fatalf("expected v1 TextSignature with id+phase, got %+v", txt)
		}
		tc, _ := res.Content[1].(*ai.ToolCall)
		if tc == nil || tc.Namespace == nil || *tc.Namespace != "web" {
			t.Fatalf("expected tool Namespace=web, got %+v", tc)
		}
		if q, _ := tc.Arguments.GetString("q"); q != "fixed" {
			t.Fatalf("expected final arguments q=fixed overriding partial delta, got %v", tc.Arguments)
		}
		// service_tier="priority" doubles OpenAI token cost (2x)
		if res.Usage.CacheWrite != 50000 || res.Usage.Reasoning == nil || *res.Usage.Reasoning != 100 || res.Usage.Cost.Total <= 0 {
			t.Fatalf("unexpected usage stats: %+v", res.Usage)
		}
		if res.Usage.Cost.Output < 15.99 || res.Usage.Cost.Output > 16.01 {
			t.Fatalf("expected 2x priority output cost=16.0 (base 8 * 2), got %v", res.Usage.Cost.Output)
		}

		// 2. Truncated stream without terminal response event -> error
		cNoTerminal := &http.Client{
			Transport: torsRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
				return torsSseResp(
					`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_1","role":"assistant","content":[]}}`,
					`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"partial"}`,
				), nil
			}),
		}
		resNoTerm, _ := StreamOpenaiResponses(context.Background(), m, userCtx, &OpenAIResponsesOptions{
			StreamOptions: ai.StreamOptions{ApiKey: torsStrPtr("sk-1"), HTTPClient: cNoTerminal},
		}).Result(context.Background())
		if resNoTerm.StopReason != ai.StopReasonError || resNoTerm.ErrorMessage == nil || !strings.Contains(*resNoTerm.ErrorMessage, "ended before a terminal response event") {
			t.Fatalf("expected missing terminal event error, got stop=%s msg=%v", resNoTerm.StopReason, resNoTerm.ErrorMessage)
		}

		// 3. response.incomplete max_output_tokens -> StopReasonLength & content_filter -> error
		cMaxLen := &http.Client{
			Transport: torsRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
				return torsSseResp(`{"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}}}`), nil
			}),
		}
		resLen, _ := StreamOpenaiResponses(context.Background(), m, userCtx, &OpenAIResponsesOptions{
			StreamOptions: ai.StreamOptions{ApiKey: torsStrPtr("sk-1"), HTTPClient: cMaxLen},
		}).Result(context.Background())
		if resLen.StopReason != ai.StopReasonLength {
			t.Fatalf("expected StopReasonLength on incomplete max_output_tokens, got %s", resLen.StopReason)
		}

		// 4. response.failed -> StopReasonError with code: message
		cFailed := &http.Client{
			Transport: torsRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
				return torsSseResp(`{"type":"response.failed","response":{"status":"failed","error":{"code":"server_error","message":"boom"}}}`), nil
			}),
		}
		resFail, _ := StreamOpenaiResponses(context.Background(), m, userCtx, &OpenAIResponsesOptions{
			StreamOptions: ai.StreamOptions{ApiKey: torsStrPtr("sk-1"), HTTPClient: cFailed},
		}).Result(context.Background())
		if resFail.StopReason != ai.StopReasonError || resFail.ErrorMessage == nil || !strings.Contains(*resFail.ErrorMessage, "server_error: boom") {
			t.Fatalf("expected server_error: boom, got stop=%s msg=%v", resFail.StopReason, resFail.ErrorMessage)
		}

		// 5. Unfinished tool call at response.completed -> error
		var seen []ai.AssistantMessageEvent
		_ = seen
		cUnfinishedTC := &http.Client{
			Transport: torsRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
				return torsSseResp(
					`{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","call_id":"call_u","id":"fc_u","name":"run","arguments":""}}`,
					`{"type":"response.completed","response":{"status":"completed"}}`,
				), nil
			}),
		}
		resUnfin, _ := StreamOpenaiResponses(context.Background(), m, userCtx, &OpenAIResponsesOptions{
			StreamOptions: ai.StreamOptions{ApiKey: torsStrPtr("sk-1"), HTTPClient: cUnfinishedTC},
		}).Result(context.Background())
		if resUnfin.StopReason != ai.StopReasonError || resUnfin.ErrorMessage == nil || !strings.Contains(*resUnfin.ErrorMessage, "unfinished tool call") {
			t.Fatalf("expected unfinished tool call error, got stop=%s msg=%v", resUnfin.StopReason, resUnfin.ErrorMessage)
		}
	})
}

// Tests for codingagent/modes/json_event.go.

package modes_test

import (
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func tjeSampleAssistant() *ai.AssistantMessage {
	return &ai.AssistantMessage{
		Role:     "assistant",
		Provider: "anthropic",
		Model:    "claude",
		Content: ai.ContentList{
			&ai.TextContent{Type: "text", Text: "hello"},
			&ai.ToolCall{
				Type:      "toolCall",
				Id:        "tc_1",
				Name:      "read",
				Arguments: jsonx.ObjectOf("path", "README.md"),
			},
		},
		Usage: ai.Usage{
			Input:       10,
			Output:      5,
			TotalTokens: 15,
		},
		StopReason: ai.StopReasonToolUse,
		Timestamp:  1000,
	}
}

func TestToJsonEventVariants(t *testing.T) {
	msg := tjeSampleAssistant()

	t.Run("passes non-message_update events through untouched", func(t *testing.T) {
		ev := &core.AgentSessionEventAgentStart{Type: "agent_start"}
		out, err := modes.ToJsonEvent(ev)
		if err != nil {
			t.Fatalf("ToJsonEvent unexpected error: %v", err)
		}
		if out != ev {
			t.Fatalf("expected identical event pointer")
		}
	})

	t.Run("strips partial from delta events and retains usage", func(t *testing.T) {
		cases := []struct {
			name      string
			in        ai.AssistantMessageEvent
			wantType  string
			wantExtra func(t *testing.T, raw string)
		}{
			{
				name:     "start",
				in:       &ai.AssistantMessageEventStart{Type: "start", Partial: msg},
				wantType: "start",
			},
			{
				name:     "text_start",
				in:       &ai.AssistantMessageEventTextStart{Type: "text_start", ContentIndex: 0, Partial: msg},
				wantType: "text_start",
			},
			{
				name:     "text_delta",
				in:       &ai.AssistantMessageEventTextDelta{Type: "text_delta", ContentIndex: 0, Delta: "hel", Partial: msg},
				wantType: "text_delta",
			},
			{
				name:     "text_end",
				in:       &ai.AssistantMessageEventTextEnd{Type: "text_end", ContentIndex: 0, Content: "hello", Partial: msg},
				wantType: "text_end",
			},
			{
				name:     "thinking_start",
				in:       &ai.AssistantMessageEventThinkingStart{Type: "thinking_start", ContentIndex: 0, Partial: msg},
				wantType: "thinking_start",
			},
			{
				name:     "thinking_delta",
				in:       &ai.AssistantMessageEventThinkingDelta{Type: "thinking_delta", ContentIndex: 0, Delta: "hmm", Partial: msg},
				wantType: "thinking_delta",
			},
			{
				name:     "thinking_end",
				in:       &ai.AssistantMessageEventThinkingEnd{Type: "thinking_end", ContentIndex: 0, Content: "thought", Partial: msg},
				wantType: "thinking_end",
			},
			{
				name:     "toolcall_start",
				in:       &ai.AssistantMessageEventToolcallStart{Type: "toolcall_start", ContentIndex: 1, Partial: msg},
				wantType: "toolcall_start",
				wantExtra: func(t *testing.T, raw string) {
					t.Helper()
					if !strings.Contains(raw, `"id":"tc_1"`) || !strings.Contains(raw, `"toolName":"read"`) {
						t.Fatalf("expected toolcall metadata in JSON, got %s", raw)
					}
				},
			},
			{
				name:     "toolcall_delta",
				in:       &ai.AssistantMessageEventToolcallDelta{Type: "toolcall_delta", ContentIndex: 1, Delta: `{"path"`, Partial: msg},
				wantType: "toolcall_delta",
			},
			{
				name:     "toolcall_end",
				in:       &ai.AssistantMessageEventToolcallEnd{Type: "toolcall_end", ContentIndex: 1, ToolCall: msg.Content[1].(*ai.ToolCall), Partial: msg},
				wantType: "toolcall_end",
			},
			{
				name:     "done",
				in:       &ai.AssistantMessageEventDone{Type: "done", Reason: ai.StopReasonStop, Message: msg},
				wantType: "done",
			},
			{
				name:     "error",
				in:       &ai.AssistantMessageEventError{Type: "error", Reason: ai.StopReasonError, Error: msg},
				wantType: "error",
			},
			{
				name:     "unknown strips partial",
				in:       &ai.UnknownAssistantMessageEvent{Raw: jsonx.ObjectOf("type", "custom", "partial", msg, "delta", "x")},
				wantType: "custom",
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				out, err := modes.ToJsonEvent(&core.AgentSessionEventMessageUpdate{
					Type:                  "message_update",
					Message:               msg,
					AssistantMessageEvent: tc.in,
				})
				if err != nil {
					t.Fatalf("ToJsonEvent(%s) failed: %v", tc.name, err)
				}
				update, ok := out.(*modes.JsonMessageUpdateEvent)
				if !ok {
					t.Fatalf("expected *modes.JsonMessageUpdateEvent, got %T", out)
				}
				if update.Usage.TotalTokens != 15 {
					t.Fatalf("expected usage totalTokens=15, got %d", update.Usage.TotalTokens)
				}
				b, err := jsonx.Marshal(update)
				if err != nil {
					t.Fatalf("jsonx.Marshal failed: %v", err)
				}
				raw := string(b)
				if strings.Contains(raw, `"partial"`) {
					t.Fatalf("unexpected partial key in %s", raw)
				}
				if strings.Contains(raw, `"message"`) && tc.wantType != "done" {
					t.Fatalf("unexpected message key in %s", raw)
				}
				if !strings.Contains(raw, `"type":"`+tc.wantType+`"`) {
					t.Fatalf("expected type %q in %s", tc.wantType, raw)
				}
				if tc.wantExtra != nil {
					tc.wantExtra(t, raw)
				}
			})
		}
	})

	t.Run("errors on non-assistant message_update and invalid toolcall_start index", func(t *testing.T) {
		_, err := modes.ToJsonEvent(&core.AgentSessionEventMessageUpdate{
			Type:                  "message_update",
			Message:               &ai.UserMessage{Role: "user", Timestamp: 1},
			AssistantMessageEvent: &ai.AssistantMessageEventStart{Type: "start", Partial: msg},
		})
		if err == nil {
			t.Fatalf("expected error for non-assistant message_update")
		}

		_, err = modes.ToJsonEvent(&core.AgentSessionEventMessageUpdate{
			Type:                  "message_update",
			Message:               msg,
			AssistantMessageEvent: &ai.AssistantMessageEventToolcallStart{Type: "toolcall_start", ContentIndex: 0, Partial: msg},
		})
		if err == nil {
			t.Fatalf("expected error when toolcall_start points to text block")
		}
	})
}

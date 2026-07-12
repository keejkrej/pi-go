package agentloop

import (
	"reflect"
	"testing"
)

func TestMessagesJSONRoundTrip(t *testing.T) {
	transcript := []AgentMessage{
		&UserMessage{
			Content: []Content{
				&TextContent{Text: "look at this"},
				&ImageContent{Data: "AAAA", MimeType: "image/png"},
			},
			Timestamp: 111,
		},
		&AssistantMessage{
			Content: []Content{
				&ThinkingContent{Thinking: "hmm", ThinkingSignature: "reasoning_content"},
				&TextContent{Text: "calling tool"},
				&ToolCall{ID: "call_1", Name: "grep", Arguments: map[string]any{"pattern": "x", "limit": float64(3)}},
			},
			Api: "openai-completions", Provider: "local", Model: "m1",
			ResponseID: "r1", StopReason: StopReasonToolUse,
			Usage: Usage{
				Input: 10, Output: 5, CacheRead: 2, TotalTokens: 17,
				Cost: UsageCost{Input: 0.1, Output: 0.2, Total: 0.3},
			},
			Timestamp: 222,
		},
		&ToolResultMessage{
			ToolCallID: "call_1", ToolName: "grep",
			Content:   []Content{&TextContent{Text: "3 matches"}},
			Details:   map[string]any{"count": float64(3)},
			IsError:   false,
			Timestamp: 333,
		},
		&AssistantMessage{
			StopReason: StopReasonError, ErrorMessage: "HTTP 500",
			Timestamp: 444,
		},
	}

	data, err := MarshalMessages(transcript)
	if err != nil {
		t.Fatalf("MarshalMessages: %v", err)
	}
	back, err := UnmarshalMessages(data)
	if err != nil {
		t.Fatalf("UnmarshalMessages: %v", err)
	}
	if !reflect.DeepEqual(transcript, back) {
		t.Fatalf("round trip mismatch:\n got %#v\nwant %#v", back, transcript)
	}
}

func TestUnmarshalMessage_StringContent(t *testing.T) {
	m, err := UnmarshalMessage([]byte(`{"role":"user","content":"plain text","timestamp":1}`))
	if err != nil {
		t.Fatalf("UnmarshalMessage: %v", err)
	}
	u, ok := m.(*UserMessage)
	if !ok {
		t.Fatalf("type = %T", m)
	}
	txt, ok := u.Content[0].(*TextContent)
	if !ok || txt.Text != "plain text" {
		t.Fatalf("content = %#v", u.Content)
	}
}

func TestUnmarshalMessage_UnknownRole(t *testing.T) {
	if _, err := UnmarshalMessage([]byte(`{"role":"alien"}`)); err == nil {
		t.Fatal("expected error for unknown role")
	}
}

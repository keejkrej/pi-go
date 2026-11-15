package core

import (
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func TestSerializeConversationFormatsRolesAndTruncatesToolResults(t *testing.T) {
	args := jsonx.NewObject()
	args.Set("path", "src/main.go")

	longResult := strings.Repeat("a", 2500)
	msgs := []ai.Message{
		&ai.UserMessage{
			Role:      "user",
			Content:   ai.UserMessageContent{Blocks: ai.ContentList{&ai.TextContent{Type: "text", Text: "Read main.go"}}},
			Timestamp: 1,
		},
		&ai.AssistantMessage{
			Role: "assistant",
			Content: ai.ContentList{
				&ai.TextContent{Type: "text", Text: "Checking file."},
				&ai.ToolCall{Type: "toolCall", Id: "call-1", Name: "read", Arguments: args},
			},
			Timestamp: 2,
		},
		&ai.ToolResultMessage{
			Role:       "toolResult",
			ToolCallId: "call-1",
			ToolName:   "read",
			Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: longResult}},
			Timestamp:  3,
		},
	}

	serialized := SerializeConversation(msgs)
	for _, want := range []string{
		"[User]: Read main.go",
		"[Assistant]: Checking file.",
		`[Assistant tool calls]: read(path="src/main.go")`,
		"[Tool result]: ",
		"[... 500 more characters truncated]",
	} {
		if !strings.Contains(serialized, want) {
			t.Fatalf("missing %q in serialized conversation:\n%s", want, serialized)
		}
	}
}

func TestSerializeConversationUtf16SurrogateBoundary(t *testing.T) {
	// Lead+trail emoji (2 UTF-16 code units) placed right across the 2000-unit boundary.
	text := strings.Repeat("x", 1999) + "😀" + "tail"
	msgs := []ai.Message{
		&ai.ToolResultMessage{
			Role:       "toolResult",
			ToolCallId: "c1",
			ToolName:   "read",
			Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: text}},
		},
	}
	serialized := SerializeConversation(msgs)
	if !strings.Contains(serialized, "[... 5 more characters truncated]") {
		t.Fatalf("expected 5 UTF-16 units truncated, got:\n%s", serialized)
	}
	if strings.Contains(serialized, "😀") {
		t.Fatalf("emoji crossing 2000-unit boundary should not be split/included:\n%s", serialized)
	}
}

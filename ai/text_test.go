package ai_test

import (
	"testing"

	"github.com/keejkrej/pi-go/ai"
)

func TestContentText(t *testing.T) {
	content := ai.ContentList{
		&ai.ThinkingContent{Type: "thinking", Thinking: "reasoning"},
		&ai.TextContent{Type: "text", Text: "first"},
		&ai.ToolCall{Type: "toolCall", Id: "1", Name: "read"},
		&ai.TextContent{Type: "text", Text: "second"},
	}

	if got := ai.ContentText(content); got != "first\nsecond" {
		t.Fatalf("ContentText(content) = %q, want %q", got, "first\nsecond")
	}
	if got := ai.ContentText(content, ""); got != "firstsecond" {
		t.Fatalf("ContentText(content, \"\") = %q, want %q", got, "firstsecond")
	}
	if got := ai.ContentText("hello"); got != "hello" {
		t.Fatalf("ContentText(\"hello\") = %q, want %q", got, "hello")
	}

	toolResultContent := ai.ContentList{
		&ai.TextContent{Type: "text", Text: "first"},
		&ai.ImageContent{Type: "image", Data: "...", MimeType: "image/png"},
		&ai.TextContent{Type: "text", Text: "second"},
	}
	if got := ai.ContentText(toolResultContent, ""); got != "firstsecond" {
		t.Fatalf("ContentText(toolResultContent, \"\") = %q, want %q", got, "firstsecond")
	}
}

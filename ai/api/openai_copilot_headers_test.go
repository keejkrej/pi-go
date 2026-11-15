// Unit tests for github_copilot_headers.go (pi v1.5.2).

package api

import (
	"reflect"
	"testing"

	"github.com/keejkrej/pi-go/ai"
)

func tcopUserText(text string) *ai.UserMessage {
	return &ai.UserMessage{
		Role:      "user",
		Content:   ai.UserMessageContent{Text: &text},
		Timestamp: 1,
	}
}

func TestCopilotDynamicHeaders(t *testing.T) {
	t.Run("InferCopilotInitiator returns user for empty or trailing user message", func(t *testing.T) {
		if got := InferCopilotInitiator(nil); got != "user" {
			t.Fatalf("empty: got %q, want user", got)
		}
		if got := InferCopilotInitiator([]ai.Message{tcopUserText("hi")}); got != "user" {
			t.Fatalf("trailing user: got %q, want user", got)
		}
	})

	t.Run("InferCopilotInitiator returns agent for trailing assistant or toolResult message", func(t *testing.T) {
		assistant := &ai.AssistantMessage{Role: "assistant", Content: ai.ContentList{}}
		if got := InferCopilotInitiator([]ai.Message{tcopUserText("hi"), assistant}); got != "agent" {
			t.Fatalf("trailing assistant: got %q, want agent", got)
		}
		toolRes := &ai.ToolResultMessage{Role: "toolResult", ToolCallId: "c1", ToolName: "bash"}
		if got := InferCopilotInitiator([]ai.Message{tcopUserText("hi"), assistant, toolRes}); got != "agent" {
			t.Fatalf("trailing toolResult: got %q, want agent", got)
		}
	})

	t.Run("HasCopilotVisionInput detects images in user and toolResult messages", func(t *testing.T) {
		textOnly := []ai.Message{tcopUserText("hi")}
		if HasCopilotVisionInput(textOnly) {
			t.Fatalf("expected false for text-only")
		}

		userImage := []ai.Message{
			&ai.UserMessage{
				Role: "user",
				Content: ai.UserMessageContent{
					Blocks: ai.ContentList{&ai.ImageContent{Type: "image", Data: "abc", MimeType: "image/png"}},
				},
			},
		}
		if !HasCopilotVisionInput(userImage) {
			t.Fatalf("expected true for user image block")
		}

		toolImage := []ai.Message{
			tcopUserText("hi"),
			&ai.ToolResultMessage{
				Role:    "toolResult",
				Content: ai.ContentList{&ai.ImageContent{Type: "image", Data: "abc", MimeType: "image/png"}},
			},
		}
		if !HasCopilotVisionInput(toolImage) {
			t.Fatalf("expected true for toolResult image block")
		}
	})

	t.Run("BuildCopilotDynamicHeaders sets initiator, intent, and optional vision header", func(t *testing.T) {
		gotNoVision := BuildCopilotDynamicHeaders(CopilotDynamicHeadersParams{
			Messages:  []ai.Message{tcopUserText("hi")},
			HasImages: false,
		})
		wantNoVision := map[string]string{
			"X-Initiator":   "user",
			"Openai-Intent": "conversation-edits",
		}
		if !reflect.DeepEqual(gotNoVision, wantNoVision) {
			t.Fatalf("got %v, want %v", gotNoVision, wantNoVision)
		}

		gotVision := BuildCopilotDynamicHeaders(CopilotDynamicHeadersParams{
			Messages:  []ai.Message{&ai.AssistantMessage{Role: "assistant"}},
			HasImages: true,
		})
		wantVision := map[string]string{
			"X-Initiator":            "agent",
			"Openai-Intent":          "conversation-edits",
			"Copilot-Vision-Request": "true",
		}
		if !reflect.DeepEqual(gotVision, wantVision) {
			t.Fatalf("got %v, want %v", gotVision, wantVision)
		}
	})
}

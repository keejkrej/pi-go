// Ported from packages/ai/test/transform-messages-cross-model-thinking.test.ts and
// packages/ai/test/transform-messages-copilot-openai-to-anthropic.test.ts (pi v1.5.2).

package api

import (
	"reflect"
	"regexp"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func ttmStrPtr(s string) *string { return &s }

func ttmArgsObj(kv ...[2]any) *jsonx.Object {
	o := jsonx.NewObject()
	for _, pair := range kv {
		o.Set(pair[0].(string), pair[1])
	}
	return o
}

func ttmKimiModel() *ai.Model {
	return &ai.Model{
		Id:            "moonshotai/Kimi-K3",
		Name:          "Kimi K3",
		Api:           string(ai.KnownApiOpenaiCompletions),
		Provider:      "sference",
		BaseUrl:       "https://api.sference.com/v1",
		Reasoning:     true,
		Input:         []ai.ModelModality{ai.ModelModalityText, ai.ModelModalityImage},
		ContextWindow: 1048576,
		MaxTokens:     32768,
	}
}

func ttmAssistant(modelID string, content ai.ContentList, stopReason ...ai.StopReason) *ai.AssistantMessage {
	sr := ai.StopReasonToolUse
	if len(stopReason) > 0 {
		sr = stopReason[0]
	}
	return &ai.AssistantMessage{
		Role:       "assistant",
		Content:    content,
		Api:        ai.KnownApiOpenaiCompletions,
		Provider:   "sference",
		Model:      modelID,
		StopReason: sr,
		Timestamp:  1,
	}
}

func ttmUserText(text string) *ai.UserMessage {
	return &ai.UserMessage{
		Role:      "user",
		Content:   ai.UserMessageContent{Text: &text},
		Timestamp: 1,
	}
}

var ttmAnthropicIDRegexp = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

func ttmAnthropicNormalizeToolCallID(id string, _ *ai.Model, _ *ai.AssistantMessage) string {
	cleaned := ttmAnthropicIDRegexp.ReplaceAllString(id, "_")
	if len(cleaned) > 64 {
		return cleaned[:64]
	}
	return cleaned
}

func ttmCopilotClaudeModel() *ai.Model {
	return &ai.Model{
		Id:            "claude-sonnet-4.6",
		Name:          "Claude Sonnet 4.6",
		Api:           string(ai.KnownApiAnthropicMessages),
		Provider:      "github-copilot",
		BaseUrl:       "https://api.individual.githubcopilot.com",
		Reasoning:     true,
		Input:         []ai.ModelModality{ai.ModelModalityText, ai.ModelModalityImage},
		ContextWindow: 128000,
		MaxTokens:     16000,
	}
}

func TestTransformMessagesCrossModelThinking(t *testing.T) {
	kimi := ttmKimiModel()

	t.Run("does not send another model's readable thinking as text", func(t *testing.T) {
		sig := "reasoning_content"
		messages := []ai.Message{
			ttmUserText("Why does curl work but node fails?"),
			ttmAssistant("zai-org/GLM-5.3", ai.ContentList{
				&ai.ThinkingContent{
					Type:              "thinking",
					Thinking:          "The user asks about ECONNRESET. Candidates: proxy, IPv6, TLS.",
					ThinkingSignature: &sig,
				},
				&ai.TextContent{Type: "text", Text: "Let me check the TLS versions."},
				&ai.ToolCall{
					Type:      "toolCall",
					Id:        "call_1",
					Name:      "bash",
					Arguments: ttmArgsObj([2]any{"command", "curl -v"}),
				},
			}),
			&ai.ToolResultMessage{
				Role:       "toolResult",
				ToolCallId: "call_1",
				ToolName:   "bash",
				Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: "TLSv1.2"}},
				IsError:    false,
				Timestamp:  2,
			},
		}

		result := TransformMessages(messages, kimi, nil)
		var assistantMsg *ai.AssistantMessage
		for _, m := range result {
			if a, ok := m.(*ai.AssistantMessage); ok {
				assistantMsg = a
				break
			}
		}
		if assistantMsg == nil {
			t.Fatalf("expected assistant message in transformed output")
		}
		if len(assistantMsg.Content) != 2 {
			t.Fatalf("expected 2 blocks (thinking dropped, not converted to text), got %d: %#v", len(assistantMsg.Content), assistantMsg.Content)
		}
		if _, ok := assistantMsg.Content[0].(*ai.TextContent); !ok {
			t.Fatalf("expected text block first, got %T", assistantMsg.Content[0])
		}
		if _, ok := assistantMsg.Content[1].(*ai.ToolCall); !ok {
			t.Fatalf("expected toolCall block second, got %T", assistantMsg.Content[1])
		}
	})

	t.Run("keeps thinking only from the target model when models alternate", func(t *testing.T) {
		thinking := func(text string) *ai.ThinkingContent {
			sig := "reasoning_content"
			return &ai.ThinkingContent{Type: "thinking", Thinking: text, ThinkingSignature: &sig}
		}
		messages := []ai.Message{
			ttmUserText("1"),
			ttmAssistant("moonshotai/Kimi-K3", ai.ContentList{thinking("kimi 1"), &ai.TextContent{Type: "text", Text: "a"}}, ai.StopReasonStop),
			ttmUserText("2"),
			ttmAssistant("zai-org/GLM-5.3", ai.ContentList{thinking("glm 1"), &ai.TextContent{Type: "text", Text: "b"}}, ai.StopReasonStop),
			ttmUserText("3"),
			ttmAssistant("moonshotai/Kimi-K3", ai.ContentList{thinking("kimi 2"), &ai.TextContent{Type: "text", Text: "c"}}, ai.StopReasonStop),
			ttmUserText("4"),
			ttmAssistant("zai-org/GLM-5.3", ai.ContentList{thinking("glm 2"), &ai.TextContent{Type: "text", Text: "d"}}, ai.StopReasonStop),
			ttmUserText("hi"),
		}

		result := TransformMessages(messages, kimi, nil)
		var contents []ai.ContentList
		for _, m := range result {
			if a, ok := m.(*ai.AssistantMessage); ok {
				contents = append(contents, a.Content)
			}
		}
		if len(contents) != 4 {
			t.Fatalf("expected 4 assistant contents, got %d", len(contents))
		}
		want := []ai.ContentList{
			{thinking("kimi 1"), &ai.TextContent{Type: "text", Text: "a"}},
			{&ai.TextContent{Type: "text", Text: "b"}},
			{thinking("kimi 2"), &ai.TextContent{Type: "text", Text: "c"}},
			{&ai.TextContent{Type: "text", Text: "d"}},
		}
		if !reflect.DeepEqual(contents, want) {
			t.Fatalf("contents mismatch:\ngot:  %#v\nwant: %#v", contents, want)
		}
	})
}

func TestTransformMessagesOpenAIToAnthropicMigration(t *testing.T) {
	model := ttmCopilotClaudeModel()
	makeOpenAIAssistant := func(content ai.ContentList) *ai.AssistantMessage {
		return &ai.AssistantMessage{
			Role:       "assistant",
			Content:    content,
			Api:        ai.KnownApiOpenaiResponses,
			Provider:   "github-copilot",
			Model:      "gpt-5",
			StopReason: ai.StopReasonToolUse,
			Timestamp:  2,
		}
	}

	t.Run("removes thoughtSignature from tool calls when migrating between models", func(t *testing.T) {
		sig := `{"type":"reasoning.encrypted","id":"call_123","data":"encrypted"}`
		messages := []ai.Message{
			ttmUserText("run a command"),
			makeOpenAIAssistant(ai.ContentList{
				&ai.ToolCall{
					Type:             "toolCall",
					Id:               "call_123",
					Name:             "bash",
					Arguments:        ttmArgsObj([2]any{"command", "ls"}),
					ThoughtSignature: &sig,
				},
			}),
			&ai.ToolResultMessage{
				Role:       "toolResult",
				ToolCallId: "call_123",
				ToolName:   "bash",
				Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: "output"}},
				IsError:    false,
				Timestamp:  3,
			},
		}

		result := TransformMessages(messages, model, ttmAnthropicNormalizeToolCallID)
		var tc *ai.ToolCall
		for _, m := range result {
			if a, ok := m.(*ai.AssistantMessage); ok {
				for _, b := range a.Content {
					if call, ok := b.(*ai.ToolCall); ok {
						tc = call
					}
				}
			}
		}
		if tc == nil {
			t.Fatalf("expected toolCall")
		}
		if tc.ThoughtSignature != nil {
			t.Fatalf("expected ThoughtSignature to be stripped on cross-model migration, got %q", *tc.ThoughtSignature)
		}
	})

	t.Run("adds synthetic tool results for trailing orphaned tool calls", func(t *testing.T) {
		messages := []ai.Message{
			ttmUserText("read the file"),
			makeOpenAIAssistant(ai.ContentList{
				&ai.ToolCall{
					Type:      "toolCall",
					Id:        "call_123|fc_123",
					Name:      "read",
					Arguments: ttmArgsObj([2]any{"path", "README.md"}),
				},
			}),
		}

		result := TransformMessages(messages, model, ttmAnthropicNormalizeToolCallID)
		last, ok := result[len(result)-1].(*ai.ToolResultMessage)
		if !ok {
			t.Fatalf("expected trailing ToolResultMessage, got %T", result[len(result)-1])
		}
		if last.ToolCallId != "call_123_fc_123" || last.ToolName != "read" || !last.IsError {
			t.Fatalf("unexpected synthetic tool result: %+v", last)
		}
		if len(last.Content) != 1 || last.Content[0].(*ai.TextContent).Text != "No result provided" {
			t.Fatalf("unexpected synthetic content: %#v", last.Content)
		}
	})

	t.Run("adds synthetic results only for trailing tool calls that are still missing results", func(t *testing.T) {
		messages := []ai.Message{
			ttmUserText("run commands"),
			makeOpenAIAssistant(ai.ContentList{
				&ai.ToolCall{
					Type:      "toolCall",
					Id:        "call_1|fc_1",
					Name:      "read",
					Arguments: ttmArgsObj([2]any{"path", "README.md"}),
				},
				&ai.ToolCall{
					Type:      "toolCall",
					Id:        "call_2|fc_2",
					Name:      "bash",
					Arguments: ttmArgsObj([2]any{"command", "pwd"}),
				},
			}),
			&ai.ToolResultMessage{
				Role:       "toolResult",
				ToolCallId: "call_1|fc_1",
				ToolName:   "read",
				Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: "done"}},
				IsError:    false,
				Timestamp:  3,
			},
		}

		result := TransformMessages(messages, model, ttmAnthropicNormalizeToolCallID)
		var synthetic []*ai.ToolResultMessage
		for _, m := range result {
			if tr, ok := m.(*ai.ToolResultMessage); ok && tr.IsError {
				synthetic = append(synthetic, tr)
			}
		}
		if len(synthetic) != 1 {
			t.Fatalf("expected 1 synthetic tool result, got %d", len(synthetic))
		}
		if synthetic[0].ToolCallId != "call_2_fc_2" || synthetic[0].ToolName != "bash" {
			t.Fatalf("unexpected synthetic result: %+v", synthetic[0])
		}
	})

	t.Run("strips unsupported images for text-only models and skips aborted/error assistant messages", func(t *testing.T) {
		textOnlyModel := &ai.Model{
			Id:       "text-only",
			Api:      string(ai.KnownApiOpenaiCompletions),
			Provider: "openai",
			Input:    []ai.ModelModality{ai.ModelModalityText},
		}
		abortedAssistant := &ai.AssistantMessage{
			Role:       "assistant",
			Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: "partial"}},
			Api:        ai.KnownApiOpenaiCompletions,
			Provider:   "openai",
			Model:      "text-only",
			StopReason: ai.StopReasonAborted,
			Timestamp:  2,
		}
		messages := []ai.Message{
			&ai.UserMessage{
				Role: "user",
				Content: ai.UserMessageContent{
					Blocks: ai.ContentList{&ai.ImageContent{Type: "image", Data: "ZmFrZQ==", MimeType: "image/png"}},
				},
				Timestamp: 1,
			},
			abortedAssistant,
		}
		out := TransformMessages(messages, textOnlyModel, nil)
		if len(out) != 1 {
			t.Fatalf("expected aborted assistant message to be skipped, got %d messages", len(out))
		}
		u := out[0].(*ai.UserMessage)
		if len(u.Content.Blocks) != 1 {
			t.Fatalf("expected 1 block in downgraded user message, got %d", len(u.Content.Blocks))
		}
		if txt, ok := u.Content.Blocks[0].(*ai.TextContent); !ok || txt.Text != "(image omitted: model does not support images)" {
			t.Fatalf("unexpected downgraded block: %#v", u.Content.Blocks[0])
		}
	})
}

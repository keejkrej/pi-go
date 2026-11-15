package ai_test

import (
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
)

func createEstimateUsage(totalTokens int) ai.Usage {
	return ai.Usage{
		Input:       totalTokens,
		TotalTokens: totalTokens,
	}
}

func createEstimateAssistant(timestamp int64, totalTokens int) *ai.AssistantMessage {
	return &ai.AssistantMessage{
		Role:       "assistant",
		Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: "kept"}},
		Api:        "openai-responses",
		Provider:   "openai",
		Model:      "test-model",
		Usage:      createEstimateUsage(totalTokens),
		StopReason: ai.StopReasonStop,
		Timestamp:  timestamp,
	}
}

func TestContextTokenEstimation(t *testing.T) {
	t.Run("reserves 3.5 characters per token for new text", func(t *testing.T) {
		ctx := ai.NormalizeContext(&ai.Context{
			Messages: ai.MessageList{
				createEstimateAssistant(100, 2000),
				&ai.UserMessage{
					Role:      "user",
					Content:   ai.UserMessageContent{Text: ptrString(strings.Repeat("x", 3500))},
					Timestamp: 200,
				},
			},
		})

		got := ai.EstimateContextTokens(ctx)
		if got.Tokens != 3000 || got.UsageTokens != 2000 || got.TrailingTokens != 1000 || got.LastUsageIndex == nil || *got.LastUsageIndex != 0 {
			t.Fatalf("got %+v, want {Tokens:3000 UsageTokens:2000 TrailingTokens:1000 LastUsageIndex:0}", got)
		}
	})

	t.Run("ignores stale assistant usage after a newer message is inserted before it", func(t *testing.T) {
		buf := make([]byte, 4000)
		for i := range buf {
			buf[i] = 'x'
		}
		ctx := ai.NormalizeContext(&ai.Context{
			SystemPrompt: ptrString("system"),
			Messages: ai.MessageList{
				&ai.UserMessage{
					Role:      "user",
					Content:   ai.UserMessageContent{Text: ptrString("summary")},
					Timestamp: 200,
				},
				createEstimateAssistant(100, 9500),
				&ai.UserMessage{
					Role:      "user",
					Content:   ai.UserMessageContent{Text: ptrString(string(buf))},
					Timestamp: 300,
				},
			},
		})

		got := ai.EstimateContextTokens(ctx)
		if got.Tokens != 1149 || got.UsageTokens != 0 || got.TrailingTokens != 1149 || got.LastUsageIndex != nil {
			t.Fatalf("got %+v, want {Tokens:1149 UsageTokens:0 TrailingTokens:1149 LastUsageIndex:nil}", got)
		}
	})

	t.Run("uses assistant usage again after a response to the inserted context", func(t *testing.T) {
		ctx := ai.NormalizeContext(&ai.Context{
			Messages: ai.MessageList{
				&ai.UserMessage{
					Role:      "user",
					Content:   ai.UserMessageContent{Text: ptrString("summary")},
					Timestamp: 200,
				},
				createEstimateAssistant(100, 9500),
				&ai.UserMessage{
					Role:      "user",
					Content:   ai.UserMessageContent{Text: ptrString("new prompt")},
					Timestamp: 300,
				},
				createEstimateAssistant(400, 2000),
				&ai.UserMessage{
					Role:      "user",
					Content:   ai.UserMessageContent{Text: ptrString("tail")},
					Timestamp: 500,
				},
			},
		})

		got := ai.EstimateContextTokens(ctx)
		if got.Tokens != 2002 || got.UsageTokens != 2000 || got.TrailingTokens != 2 || got.LastUsageIndex == nil || *got.LastUsageIndex != 3 {
			t.Fatalf("got %+v, want {Tokens:2002 UsageTokens:2000 TrailingTokens:2 LastUsageIndex:3}", got)
		}
	})
}

func ptrString(s string) *string { return &s }

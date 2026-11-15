// Ported from packages/ai/src/utils/estimate.ts (pi v1.5.2).

package ai

import (
	"math"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// ContextUsageEstimate summarizes estimated context tokens and the latest usage anchor.
type ContextUsageEstimate struct {
	Tokens         int  `json:"tokens"`
	UsageTokens    int  `json:"usageTokens"`
	TrailingTokens int  `json:"trailingTokens"`
	LastUsageIndex *int `json:"lastUsageIndex"`
}

const (
	ueCharsPerToken       = 3.5
	ueEstimatedImageChars = 4800
)

// CalculateContextTokens computes total context tokens from a Usage record.
func CalculateContextTokens(usage *Usage) int {
	if usage == nil {
		return 0
	}
	if usage.TotalTokens != 0 {
		return usage.TotalTokens
	}
	return usage.Input + usage.Output + usage.CacheRead + usage.CacheWrite
}

func ueSafeJsonStringify(value any) string {
	if value == nil {
		return "undefined"
	}
	s, err := jsonx.Stringify(value)
	if err != nil {
		return "[unserializable]"
	}
	if s == "" {
		return "undefined"
	}
	return s
}

func ueEstimateTextAndImageContentChars(content any) int {
	switch v := content.(type) {
	case nil:
		return 0
	case string:
		return js.Len(v)
	case *string:
		if v == nil {
			return 0
		}
		return js.Len(*v)
	case UserMessageContent:
		if v.Text != nil {
			return js.Len(*v.Text)
		}
		return ueEstimateBlocksChars(v.Blocks)
	case *UserMessageContent:
		if v == nil {
			return 0
		}
		if v.Text != nil {
			return js.Len(*v.Text)
		}
		return ueEstimateBlocksChars(v.Blocks)
	case SystemMessageContent:
		if v.Text != nil {
			return js.Len(*v.Text)
		}
		return ueEstimateBlocksChars(v.Blocks)
	case *SystemMessageContent:
		if v == nil {
			return 0
		}
		if v.Text != nil {
			return js.Len(*v.Text)
		}
		return ueEstimateBlocksChars(v.Blocks)
	case ContentList:
		return ueEstimateBlocksChars(v)
	case []Content:
		return ueEstimateBlocksChars(ContentList(v))
	default:
		return 0
	}
}

func ueEstimateBlocksChars(blocks ContentList) int {
	chars := 0
	for _, block := range blocks {
		if tc, ok := block.(*TextContent); ok && tc != nil {
			chars += js.Len(tc.Text)
		} else {
			chars += ueEstimatedImageChars
		}
	}
	return chars
}

// EstimateTextTokens estimates token count for plain text using the shared char/token heuristic.
func EstimateTextTokens(text string) int {
	return int(math.Ceil(float64(js.Len(text)) / ueCharsPerToken))
}

// EstimateTextAndImageContentTokens estimates token count for text and image content blocks.
func EstimateTextAndImageContentTokens(content any) int {
	return int(math.Ceil(float64(ueEstimateTextAndImageContentChars(content)) / ueCharsPerToken))
}

// EstimateMessageTokens estimates token count for a single transcript message.
func EstimateMessageTokens(message Message) int {
	switch m := message.(type) {
	case *SystemMessage:
		if m == nil {
			return 0
		}
		return EstimateTextTokens(GetSystemMessageText(m)) +
			ueEstimateToolsTokens(m.ToolsAdded) +
			ueEstimateToolsTokens(m.ToolsRemoved)
	case *UserMessage:
		if m == nil {
			return 0
		}
		return EstimateTextAndImageContentTokens(m.Content)
	case *ToolResultMessage:
		if m == nil {
			return 0
		}
		return EstimateTextAndImageContentTokens(m.Content)
	case *AssistantMessage:
		if m == nil {
			return 0
		}
		chars := 0
		for _, block := range m.Content {
			switch b := block.(type) {
			case *TextContent:
				if b != nil {
					chars += js.Len(b.Text)
				}
			case *ThinkingContent:
				if b != nil {
					chars += js.Len(b.Thinking)
				}
			case *ToolCall:
				if b != nil {
					chars += js.Len(b.Name) + js.Len(ueSafeJsonStringify(b.Arguments))
				}
			}
		}
		return int(math.Ceil(float64(chars) / ueCharsPerToken))
	default:
		return 0
	}
}

func ueMessageTimestamp(msg Message) (int64, bool) {
	switch m := msg.(type) {
	case *SystemMessage:
		if m != nil {
			return m.Timestamp, true
		}
	case *UserMessage:
		if m != nil {
			return m.Timestamp, true
		}
	case *AssistantMessage:
		if m != nil {
			return m.Timestamp, true
		}
	case *ToolResultMessage:
		if m != nil {
			return m.Timestamp, true
		}
	case *UnknownMessage:
		if m != nil && m.Raw != nil {
			if rawTs, ok := m.Raw.Get("timestamp"); ok {
				if f, ok := rawTs.(float64); ok {
					return int64(f), true
				}
			}
		}
	}
	return 0, false
}

func ueGetLastAssistantUsageInfo(messages []Message) (*Usage, int, bool) {
	hasLatest := false
	var latestPrefixTimestamp int64
	var usageInfo *Usage
	usageIdx := 0
	found := false

	for i, message := range messages {
		if assistant, ok := message.(*AssistantMessage); ok && assistant != nil {
			usageAppliesToPrefix := !hasLatest || assistant.Timestamp >= latestPrefixTimestamp
			if usageAppliesToPrefix &&
				assistant.StopReason != StopReasonAborted &&
				assistant.StopReason != StopReasonError &&
				CalculateContextTokens(&assistant.Usage) > 0 {
				u := assistant.Usage
				usageInfo = &u
				usageIdx = i
				found = true
			}
		}
		if ts, ok := ueMessageTimestamp(message); ok {
			if !hasLatest || ts > latestPrefixTimestamp {
				latestPrefixTimestamp = ts
				hasLatest = true
			}
		}
	}
	return usageInfo, usageIdx, found
}

// EstimateContextTokens estimates total context tokens for a *TranscriptContext, *Context, MessageList, or []Message.
func EstimateContextTokens(contextOrMessages any) ContextUsageEstimate {
	var messages []Message
	switch v := contextOrMessages.(type) {
	case *TranscriptContext:
		if v != nil {
			messages = v.Messages
		}
	case *Context:
		if v != nil {
			messages = v.Messages
		}
	case MessageList:
		messages = v
	case []Message:
		messages = v
	}

	if usage, idx, ok := ueGetLastAssistantUsageInfo(messages); ok {
		usageTokens := CalculateContextTokens(usage)
		trailingTokens := 0
		for i := idx + 1; i < len(messages); i++ {
			trailingTokens += EstimateMessageTokens(messages[i])
		}
		idxCopy := idx
		return ContextUsageEstimate{
			Tokens:         usageTokens + trailingTokens,
			UsageTokens:    usageTokens,
			TrailingTokens: trailingTokens,
			LastUsageIndex: &idxCopy,
		}
	}

	tokens := 0
	for _, msg := range messages {
		tokens += EstimateMessageTokens(msg)
	}
	return ContextUsageEstimate{
		Tokens:         tokens,
		UsageTokens:    0,
		TrailingTokens: tokens,
		LastUsageIndex: nil,
	}
}

func ueEstimateToolsTokens[T any](tools []T) int {
	if len(tools) == 0 {
		return 0
	}
	return EstimateTextTokens(ueSafeJsonStringify(tools))
}

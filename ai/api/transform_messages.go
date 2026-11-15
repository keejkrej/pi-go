// Ported from packages/ai/src/api/transform-messages.ts (pi v1.5.2).

package api

import (
	"slices"
	"strings"
	"time"

	"github.com/keejkrej/pi-go/ai"
)

const (
	tmNonVisionUserImagePlaceholder = "(image omitted: model does not support images)"
	tmNonVisionToolImagePlaceholder = "(tool image omitted: model does not support images)"
	tmErrorToolImagePlaceholder     = "(image attached after the tool results)"
)

// SplitErrorToolResult is an error tool result split into text-only result blocks and the
// images to send after the turn's tool results.
type SplitErrorToolResult struct {
	Content []*ai.TextContent
	Caption string
	Images  []*ai.ImageContent
}

// SplitErrorToolResultImages splits images out of an error tool result for APIs that reject
// images in error tool results (Anthropic, and Claude on Bedrock). Returns nil for results
// that need no split (#10794).
func SplitErrorToolResultImages(msg *ai.ToolResultMessage) *SplitErrorToolResult {
	if msg == nil || !msg.IsError {
		return nil
	}
	var images []*ai.ImageContent
	for _, block := range msg.Content {
		if img, ok := block.(*ai.ImageContent); ok && img != nil {
			images = append(images, img)
		}
	}
	if len(images) == 0 {
		return nil
	}
	return &SplitErrorToolResult{
		Content: tmReplaceImagesWithPlaceholder(msg.Content, tmErrorToolImagePlaceholder),
		Caption: "Images from the error result of tool call " + msg.ToolCallId + " (" + msg.ToolName + "):",
		Images:  images,
	}
}

func tmReplaceImagesWithPlaceholder(content ai.ContentList, placeholder string) []*ai.TextContent {
	result := make([]*ai.TextContent, 0, len(content))
	previousWasPlaceholder := false

	for _, block := range content {
		if _, ok := block.(*ai.ImageContent); ok {
			if !previousWasPlaceholder {
				result = append(result, &ai.TextContent{Type: "text", Text: placeholder})
			}
			previousWasPlaceholder = true
			continue
		}
		if tc, ok := block.(*ai.TextContent); ok && tc != nil {
			result = append(result, tc)
			previousWasPlaceholder = tc.Text == placeholder
		}
	}
	return result
}

func tmTextSliceToContentList(blocks []*ai.TextContent) ai.ContentList {
	out := make(ai.ContentList, len(blocks))
	for i, b := range blocks {
		out[i] = b
	}
	return out
}

func tmModelSupportsImages(model *ai.Model) bool {
	if model == nil {
		return false
	}
	return slices.Contains(model.Input, ai.ModelModalityImage)
}

func tmDowngradeUnsupportedImages(messages []ai.Message, model *ai.Model) []ai.Message {
	if tmModelSupportsImages(model) {
		return messages
	}
	out := make([]ai.Message, len(messages))
	for i, msg := range messages {
		switch m := msg.(type) {
		case *ai.UserMessage:
			if m != nil && m.Content.Blocks != nil {
				cp := *m
				cp.Content = ai.UserMessageContent{
					Blocks: tmTextSliceToContentList(tmReplaceImagesWithPlaceholder(m.Content.Blocks, tmNonVisionUserImagePlaceholder)),
				}
				out[i] = &cp
				continue
			}
		case *ai.ToolResultMessage:
			if m != nil {
				cp := *m
				cp.Content = tmTextSliceToContentList(tmReplaceImagesWithPlaceholder(m.Content, tmNonVisionToolImagePlaceholder))
				out[i] = &cp
				continue
			}
		}
		out[i] = msg
	}
	return out
}

func tmCloneAssistantWithoutLocks(src *ai.AssistantMessage, content ai.ContentList) *ai.AssistantMessage {
	return &ai.AssistantMessage{
		Role:                  src.Role,
		Content:               content,
		Api:                   src.Api,
		Provider:              src.Provider,
		Model:                 src.Model,
		ResponseModel:         src.ResponseModel,
		ResponseId:            src.ResponseId,
		ProviderThinkingLevel: src.ProviderThinkingLevel,
		ThinkingLevel:         src.ThinkingLevel,
		Diagnostics:           src.Diagnostics,
		Usage:                 src.Usage,
		StopReason:            src.StopReason,
		Deferred:              src.Deferred,
		ErrorMessage:          src.ErrorMessage,
		RawStopReason:         src.RawStopReason,
		EndTurn:               src.EndTurn,
		Timestamp:             src.Timestamp,
		DurationMs:            src.DurationMs,
	}
}

// TransformMessages normalizes messages for cross-provider replay: downgrades unsupported images,
// normalizes nil content, filters cross-model thinking blocks, normalizes tool call IDs, skips
// errored/aborted assistant messages, and synthesizes tool results for orphaned tool calls.
func TransformMessages(
	messages []ai.Message,
	model *ai.Model,
	normalizeToolCallId func(id string, model *ai.Model, source *ai.AssistantMessage) string,
) []ai.Message {
	toolCallIdMap := make(map[string]string)

	// Normalize nil content from untyped callers so downstream code can rely on non-nil slices.
	normalizedMessages := make([]ai.Message, len(messages))
	for i, msg := range messages {
		switch m := msg.(type) {
		case *ai.UserMessage:
			if m != nil && m.Content.Text == nil && m.Content.Blocks == nil {
				cp := *m
				cp.Content = ai.UserMessageContent{Blocks: ai.ContentList{}}
				normalizedMessages[i] = &cp
				continue
			}
		case *ai.AssistantMessage:
			if m != nil && m.Content == nil {
				normalizedMessages[i] = tmCloneAssistantWithoutLocks(m, ai.ContentList{})
				continue
			}
		case *ai.ToolResultMessage:
			if m != nil && m.Content == nil {
				cp := *m
				cp.Content = ai.ContentList{}
				normalizedMessages[i] = &cp
				continue
			}
		}
		normalizedMessages[i] = msg
	}

	imageAwareMessages := tmDowngradeUnsupportedImages(normalizedMessages, model)

	// First pass: transform messages (thinking blocks, text signatures, tool call IDs).
	transformed := make([]ai.Message, len(imageAwareMessages))
	for i, msg := range imageAwareMessages {
		switch m := msg.(type) {
		case *ai.SystemMessage, *ai.UserMessage:
			transformed[i] = msg
		case *ai.ToolResultMessage:
			if m != nil {
				if normalizedId, ok := toolCallIdMap[m.ToolCallId]; ok && normalizedId != "" && normalizedId != m.ToolCallId {
					cp := *m
					cp.ToolCallId = normalizedId
					transformed[i] = &cp
					continue
				}
			}
			transformed[i] = msg
		case *ai.AssistantMessage:
			if m == nil {
				transformed[i] = msg
				continue
			}
			isSameModel := model != nil &&
				m.Provider == model.Provider &&
				string(m.Api) == model.Api &&
				m.Model == model.Id

			transformedContent := make(ai.ContentList, 0, len(m.Content))
			for _, block := range m.Content {
				switch b := block.(type) {
				case *ai.ThinkingContent:
					if b == nil || !isSameModel {
						continue
					}
					if (b.Redacted != nil && *b.Redacted) || (b.ThinkingSignature != nil && *b.ThinkingSignature != "") {
						transformedContent = append(transformedContent, b)
						continue
					}
					if strings.TrimSpace(b.Thinking) == "" {
						continue
					}
					transformedContent = append(transformedContent, b)
				case *ai.TextContent:
					if b == nil {
						continue
					}
					if isSameModel {
						transformedContent = append(transformedContent, b)
					} else {
						transformedContent = append(transformedContent, &ai.TextContent{
							Type: "text",
							Text: b.Text,
						})
					}
				case *ai.ToolCall:
					if b == nil {
						continue
					}
					normalizedToolCall := b
					var copied *ai.ToolCall
					ensureCopy := func() *ai.ToolCall {
						if copied == nil {
							c := *b
							copied = &c
							normalizedToolCall = copied
						}
						return copied
					}
					if !isSameModel && b.ThoughtSignature != nil {
						ensureCopy().ThoughtSignature = nil
					}
					if !isSameModel && normalizeToolCallId != nil {
						normalizedId := normalizeToolCallId(b.Id, model, m)
						if normalizedId != b.Id {
							toolCallIdMap[b.Id] = normalizedId
							ensureCopy().Id = normalizedId
						}
					}
					transformedContent = append(transformedContent, normalizedToolCall)
				default:
					transformedContent = append(transformedContent, block)
				}
			}
			transformed[i] = tmCloneAssistantWithoutLocks(m, transformedContent)
		default:
			transformed[i] = msg
		}
	}

	// Second pass: insert synthetic empty tool results for orphaned tool calls.
	result := make([]ai.Message, 0, len(transformed))
	var pendingToolCalls []*ai.ToolCall
	existingToolResultIds := make(map[string]struct{})
	var heldSystemMessages []ai.Message

	closePendingToolCalls := func() {
		if len(pendingToolCalls) > 0 {
			for _, tc := range pendingToolCalls {
				if _, ok := existingToolResultIds[tc.Id]; !ok {
					result = append(result, &ai.ToolResultMessage{
						Role:       "toolResult",
						ToolCallId: tc.Id,
						ToolName:   tc.Name,
						Content: ai.ContentList{
							&ai.TextContent{Type: "text", Text: "No result provided"},
						},
						IsError:   true,
						Timestamp: time.Now().UnixMilli(),
					})
				}
			}
			pendingToolCalls = nil
			existingToolResultIds = make(map[string]struct{})
		}
		if len(heldSystemMessages) > 0 {
			result = append(result, heldSystemMessages...)
			heldSystemMessages = heldSystemMessages[:0]
		}
	}

	for _, msg := range transformed {
		switch m := msg.(type) {
		case *ai.AssistantMessage:
			closePendingToolCalls()
			if m == nil {
				continue
			}
			if m.StopReason == ai.StopReasonError || m.StopReason == ai.StopReasonAborted {
				continue
			}
			var toolCalls []*ai.ToolCall
			for _, b := range m.Content {
				if tc, ok := b.(*ai.ToolCall); ok && tc != nil {
					toolCalls = append(toolCalls, tc)
				}
			}
			if len(toolCalls) > 0 {
				pendingToolCalls = toolCalls
				existingToolResultIds = make(map[string]struct{})
			}
			result = append(result, msg)
		case *ai.ToolResultMessage:
			if m != nil {
				existingToolResultIds[m.ToolCallId] = struct{}{}
			}
			result = append(result, msg)
		case *ai.SystemMessage:
			if len(pendingToolCalls) > 0 {
				heldSystemMessages = append(heldSystemMessages, msg)
			} else {
				result = append(result, msg)
			}
		case *ai.UserMessage:
			closePendingToolCalls()
			result = append(result, msg)
		default:
			result = append(result, msg)
		}
	}

	closePendingToolCalls()
	return result
}

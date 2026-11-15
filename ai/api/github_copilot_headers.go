// Ported from packages/ai/src/api/github-copilot-headers.ts (pi v1.5.2).

package api

import "github.com/keejkrej/pi-go/ai"

// CopilotDynamicHeadersParams inputs for BuildCopilotDynamicHeaders.
type CopilotDynamicHeadersParams struct {
	Messages  []ai.Message
	HasImages bool
}

// InferCopilotInitiator returns "agent" if the last message is not from the user, or "user" otherwise.
// Copilot expects X-Initiator to indicate whether the request is user-initiated or agent-initiated
// (e.g. follow-up after assistant/tool messages).
func InferCopilotInitiator(messages []ai.Message) string {
	if len(messages) == 0 {
		return "user"
	}
	last := messages[len(messages)-1]
	if last != nil && last.GetRole() != "user" {
		return "agent"
	}
	return "user"
}

// HasCopilotVisionInput reports whether any user or toolResult message contains an image block.
// Copilot requires the Copilot-Vision-Request header when sending images.
func HasCopilotVisionInput(messages []ai.Message) bool {
	for _, msg := range messages {
		switch m := msg.(type) {
		case *ai.UserMessage:
			if m != nil && m.Content.Blocks != nil {
				for _, block := range m.Content.Blocks {
					if _, ok := block.(*ai.ImageContent); ok {
						return true
					}
				}
			}
		case *ai.ToolResultMessage:
			if m != nil {
				for _, block := range m.Content {
					if _, ok := block.(*ai.ImageContent); ok {
						return true
					}
				}
			}
		}
	}
	return false
}

// BuildCopilotDynamicHeaders returns dynamic per-request GitHub Copilot headers.
func BuildCopilotDynamicHeaders(params CopilotDynamicHeadersParams) map[string]string {
	headers := map[string]string{
		"X-Initiator":   InferCopilotInitiator(params.Messages),
		"Openai-Intent": "conversation-edits",
	}
	if params.HasImages {
		headers["Copilot-Vision-Request"] = "true"
	}
	return headers
}

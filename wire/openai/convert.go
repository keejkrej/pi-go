package openai

import (
	"encoding/json"
	"strings"

	ag "github.com/keejkrej/pi-go/agentloop"
)

// wireMessage is a single OpenAI Chat Completions message. Optional fields are
// pointers/omitempty so the JSON body matches the OpenAI schema.
type wireMessage struct {
	Role       string         `json:"role"`
	Content    any            `json:"content,omitempty"`
	ToolCalls  []wireToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type wireToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function wireToolCallFunc `json:"function"`
}

type wireToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type wireContentPart struct {
	Type     string        `json:"type"`
	Text     string        `json:"text,omitempty"`
	ImageURL *wireImageURL `json:"image_url,omitempty"`
}

type wireImageURL struct {
	URL string `json:"url"`
}

type wireTool struct {
	Type     string       `json:"type"`
	Function wireToolFunc `json:"function"`
}

type wireToolFunc struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

// preparedMessage is a normalized, model-agnostic view of an LLM message used
// during the transform pass (port of transform-messages.ts second pass).
type preparedMessage struct {
	role    string // "user" | "assistant" | "toolResult"
	user    *ag.UserMessage
	assist  *ag.AssistantMessage
	toolRes *ag.ToolResultMessage
}

// prepareMessages ports the second pass of transformMessages: it skips errored
// or aborted assistant messages and inserts synthetic tool results for any
// tool-call IDs that never received a result.
func prepareMessages(messages []ag.Message) []preparedMessage {
	var result []preparedMessage
	var pendingToolCalls []*ag.ToolCall
	existing := map[string]bool{}

	flush := func() {
		if len(pendingToolCalls) == 0 {
			return
		}
		for _, tc := range pendingToolCalls {
			if !existing[tc.ID] {
				result = append(result, preparedMessage{
					role: "toolResult",
					toolRes: &ag.ToolResultMessage{
						ToolCallID: tc.ID,
						ToolName:   tc.Name,
						Content:    []ag.Content{&ag.TextContent{Text: "No result provided"}},
						IsError:    true,
					},
				})
			}
		}
		pendingToolCalls = nil
		existing = map[string]bool{}
	}

	for _, msg := range messages {
		switch m := msg.(type) {
		case *ag.AssistantMessage:
			flush()
			// Skip errored/aborted assistant messages entirely.
			if m.StopReason == ag.StopReasonError || m.StopReason == ag.StopReasonAborted {
				continue
			}
			toolCalls := m.ToolCalls()
			if len(toolCalls) > 0 {
				pendingToolCalls = toolCalls
				existing = map[string]bool{}
			}
			result = append(result, preparedMessage{role: "assistant", assist: m})
		case *ag.ToolResultMessage:
			existing[m.ToolCallID] = true
			result = append(result, preparedMessage{role: "toolResult", toolRes: m})
		case *ag.UserMessage:
			flush()
			result = append(result, preparedMessage{role: "user", user: m})
		}
	}
	flush()
	return result
}

// convertMessages builds the OpenAI messages array from the wire context.
func convertMessages(c *ag.Context) []wireMessage {
	prepared := prepareMessages(c.Messages)
	var params []wireMessage

	if c.SystemPrompt != "" {
		params = append(params, wireMessage{Role: "system", Content: c.SystemPrompt})
	}

	for i := 0; i < len(prepared); i++ {
		pm := prepared[i]
		switch pm.role {
		case "user":
			params = append(params, convertUserMessage(pm.user))
		case "assistant":
			if am, ok := convertAssistantMessage(pm.assist); ok {
				params = append(params, am)
			}
		case "toolResult":
			// Collapse a run of consecutive tool results.
			j := i
			for ; j < len(prepared) && prepared[j].role == "toolResult"; j++ {
				params = append(params, convertToolResultMessage(prepared[j].toolRes))
			}
			i = j - 1
		}
	}

	return params
}

func convertUserMessage(m *ag.UserMessage) wireMessage {
	// Single text block => plain string content.
	if len(m.Content) == 1 {
		if t, ok := m.Content[0].(*ag.TextContent); ok {
			return wireMessage{Role: "user", Content: t.Text}
		}
	}
	var parts []wireContentPart
	for _, block := range m.Content {
		switch b := block.(type) {
		case *ag.TextContent:
			parts = append(parts, wireContentPart{Type: "text", Text: b.Text})
		case *ag.ImageContent:
			parts = append(parts, wireContentPart{
				Type:     "image_url",
				ImageURL: &wireImageURL{URL: "data:" + b.MimeType + ";base64," + b.Data},
			})
		}
	}
	if len(parts) == 0 {
		// Degenerate empty user message: send empty string.
		return wireMessage{Role: "user", Content: ""}
	}
	return wireMessage{Role: "user", Content: parts}
}

// convertAssistantMessage returns (msg, true) or (_, false) when the message has
// neither content nor tool calls and should be skipped.
func convertAssistantMessage(m *ag.AssistantMessage) (wireMessage, bool) {
	var text strings.Builder
	for _, block := range m.Content {
		if t, ok := block.(*ag.TextContent); ok {
			if strings.TrimSpace(t.Text) != "" {
				text.WriteString(t.Text)
			}
		}
		// Thinking blocks are omitted in the thin port.
	}

	out := wireMessage{Role: "assistant"}
	assistantText := text.String()
	if assistantText != "" {
		out.Content = assistantText
	}

	for _, block := range m.Content {
		if tc, ok := block.(*ag.ToolCall); ok {
			args := tc.Arguments
			if args == nil {
				args = map[string]any{}
			}
			raw, err := json.Marshal(args)
			if err != nil {
				raw = []byte("{}")
			}
			out.ToolCalls = append(out.ToolCalls, wireToolCall{
				ID:   tc.ID,
				Type: "function",
				Function: wireToolCallFunc{
					Name:      tc.Name,
					Arguments: string(raw),
				},
			})
		}
	}

	if assistantText == "" && len(out.ToolCalls) == 0 {
		return wireMessage{}, false
	}
	return out, true
}

func convertToolResultMessage(m *ag.ToolResultMessage) wireMessage {
	var texts []string
	hasImage := false
	for _, block := range m.Content {
		switch b := block.(type) {
		case *ag.TextContent:
			texts = append(texts, b.Text)
		case *ag.ImageContent:
			hasImage = true
		}
	}
	content := strings.Join(texts, "\n")
	if content == "" && hasImage {
		content = "(see attached image)"
	}
	return wireMessage{
		Role:       "tool",
		Content:    content,
		ToolCallID: m.ToolCallID,
	}
}

// convertTools maps agent tools to the OpenAI tools array.
func convertTools(tools []ag.AgentTool) []wireTool {
	out := make([]wireTool, 0, len(tools))
	for _, t := range tools {
		out = append(out, wireTool{
			Type: "function",
			Function: wireToolFunc{
				Name:        t.Name(),
				Description: t.Description(),
				Parameters:  t.Parameters(),
			},
		})
	}
	return out
}

// reasoningEffort resolves the reasoning_effort value for a model + level, or ""
// when reasoning is disabled / unsupported.
func reasoningEffort(model *ag.Model, level ag.ThinkingLevel) string {
	if !model.Reasoning || level == "" || level == ag.ThinkingOff {
		return ""
	}
	if model.ThinkingLevelMap != nil {
		if mapped, ok := model.ThinkingLevelMap[string(level)]; ok {
			return mapped
		}
	}
	return string(level)
}

// maxTokensField returns the request field name for the max-tokens limit.
func maxTokensField(model *ag.Model) string {
	if model.MaxTokensField == "max_tokens" {
		return "max_tokens"
	}
	return "max_completion_tokens"
}

// buildRequestBody assembles the JSON request body for a streaming completion.
func buildRequestBody(model *ag.Model, c *ag.Context, opts *ag.StreamOptions) map[string]any {
	body := map[string]any{
		"model":          model.ID,
		"messages":       convertMessages(c),
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
	}

	if opts != nil {
		if opts.MaxTokens > 0 {
			body[maxTokensField(model)] = opts.MaxTokens
		}
		if opts.Temperature != nil {
			body["temperature"] = *opts.Temperature
		}
		if effort := reasoningEffort(model, opts.Reasoning); effort != "" {
			body["reasoning_effort"] = effort
		}
	}

	if len(c.Tools) > 0 {
		body["tools"] = convertTools(c.Tools)
	}

	return body
}

// completionsURL joins the model base URL with the chat-completions path.
func completionsURL(baseURL string) string {
	trimmed := strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(trimmed, "/chat/completions") {
		return trimmed
	}
	return trimmed + "/chat/completions"
}

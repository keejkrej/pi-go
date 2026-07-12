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
	Name       string         `json:"name,omitempty"`
	// Extra carries dynamic top-level keys (e.g. replayed thinking under its
	// signature key such as "reasoning_content").
	Extra map[string]any `json:"-"`
}

// MarshalJSON merges Extra keys into the encoded message.
func (m wireMessage) MarshalJSON() ([]byte, error) {
	type plain wireMessage
	raw, err := json.Marshal(plain(m))
	if err != nil {
		return nil, err
	}
	if len(m.Extra) == 0 {
		return raw, nil
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}
	for k, v := range m.Extra {
		obj[k] = v
	}
	return json.Marshal(obj)
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
	Strict      *bool          `json:"strict,omitempty"`
}

// compatOf returns the model's Compat, or a zero value when unset.
var zeroCompat ag.Compat

func compatOf(model *ag.Model) *ag.Compat {
	if model != nil && model.Compat != nil {
		return model.Compat
	}
	return &zeroCompat
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
func convertMessages(model *ag.Model, c *ag.Context) []wireMessage {
	compat := compatOf(model)
	prepared := prepareMessages(c.Messages)
	var params []wireMessage

	if c.SystemPrompt != "" {
		role := "system"
		if model != nil && model.Reasoning && compat.SupportsDeveloperRole {
			role = "developer"
		}
		params = append(params, wireMessage{Role: role, Content: c.SystemPrompt})
	}

	modelAcceptsImages := false
	if model != nil {
		for _, in := range model.Input {
			if in == "image" {
				modelAcceptsImages = true
			}
		}
	}

	for i := 0; i < len(prepared); i++ {
		pm := prepared[i]
		switch pm.role {
		case "user":
			params = append(params, convertUserMessage(pm.user))
		case "assistant":
			if am, ok := convertAssistantMessage(compat, pm.assist); ok {
				params = append(params, am)
			}
		case "toolResult":
			// Collapse a run of consecutive tool results, collecting image
			// blocks to re-emit as a trailing user message (models can't see
			// images inside role:"tool" messages).
			var imageParts []wireContentPart
			j := i
			for ; j < len(prepared) && prepared[j].role == "toolResult"; j++ {
				m := prepared[j].toolRes
				params = append(params, convertToolResultMessage(compat, m, modelAcceptsImages))
				if modelAcceptsImages {
					for _, block := range m.Content {
						if img, ok := block.(*ag.ImageContent); ok {
							imageParts = append(imageParts, wireContentPart{
								Type:     "image_url",
								ImageURL: &wireImageURL{URL: "data:" + img.MimeType + ";base64," + img.Data},
							})
						}
					}
				}
			}
			i = j - 1
			if len(imageParts) > 0 {
				parts := append([]wireContentPart{{Type: "text", Text: "Attached image(s) from tool result:"}}, imageParts...)
				params = append(params, wireMessage{Role: "user", Content: parts})
			} else if compat.RequiresAssistantAfterToolResult &&
				i+1 < len(prepared) && prepared[i+1].role == "user" {
				params = append(params, wireMessage{Role: "assistant", Content: "I have processed the tool results."})
			}
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
func convertAssistantMessage(compat *ag.Compat, m *ag.AssistantMessage) (wireMessage, bool) {
	var text strings.Builder
	thinkingBySignature := map[string]string{}
	var signatureOrder []string
	for _, block := range m.Content {
		switch t := block.(type) {
		case *ag.TextContent:
			if strings.TrimSpace(t.Text) != "" {
				text.WriteString(t.Text)
			}
		case *ag.ThinkingContent:
			if t.Redacted || strings.TrimSpace(t.Thinking) == "" {
				continue
			}
			if compat.RequiresThinkingAsText {
				// Replay thinking as plain text ahead of the answer.
				text.WriteString(t.Thinking)
				continue
			}
			// Replay thinking under the delta field name it arrived on
			// (e.g. "reasoning_content"), mirroring pi's signature replay.
			sig := t.ThinkingSignature
			if sig == "" {
				continue
			}
			if _, seen := thinkingBySignature[sig]; !seen {
				signatureOrder = append(signatureOrder, sig)
			}
			thinkingBySignature[sig] += t.Thinking
		}
	}

	out := wireMessage{Role: "assistant"}
	assistantText := text.String()
	if assistantText != "" {
		out.Content = assistantText
	}
	for _, sig := range signatureOrder {
		if out.Extra == nil {
			out.Extra = map[string]any{}
		}
		out.Extra[sig] = thinkingBySignature[sig]
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

func convertToolResultMessage(compat *ag.Compat, m *ag.ToolResultMessage, modelAcceptsImages bool) wireMessage {
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
		if modelAcceptsImages {
			content = "(see following user message for attached image)"
		} else {
			content = "(image omitted: model does not support image input)"
		}
	}
	out := wireMessage{
		Role:       "tool",
		Content:    content,
		ToolCallID: m.ToolCallID,
	}
	if compat.RequiresToolResultName {
		out.Name = m.ToolName
	}
	return out
}

// convertTools maps agent tools to the OpenAI tools array.
func convertTools(compat *ag.Compat, tools []ag.AgentTool) []wireTool {
	var strict *bool
	if compat.SupportsStrictMode {
		f := false
		strict = &f
	}
	out := make([]wireTool, 0, len(tools))
	for _, t := range tools {
		out = append(out, wireTool{
			Type: "function",
			Function: wireToolFunc{
				Name:        t.Name(),
				Description: t.Description(),
				Parameters:  t.Parameters(),
				Strict:      strict,
			},
		})
	}
	return out
}

// resolveReasoning resolves whether reasoning is on for this request and the
// provider-specific effort value (mapped through Model.ThinkingLevelMap; an
// empty mapped value means the level is unsupported).
func resolveReasoning(model *ag.Model, level ag.ThinkingLevel) (on bool, effort string) {
	if !model.Reasoning || level == "" || level == ag.ThinkingOff {
		return false, ""
	}
	effort = string(level)
	if model.ThinkingLevelMap != nil {
		if mapped, ok := model.ThinkingLevelMap[string(level)]; ok {
			effort = mapped
		}
	}
	return true, effort
}

// applyThinking encodes the reasoning request onto the body according to the
// model's ThinkingFormat (port of the thinkingFormat dispatch in pi's
// openai-completions.ts).
func applyThinking(body map[string]any, model *ag.Model, level ag.ThinkingLevel) {
	compat := compatOf(model)
	on, effort := resolveReasoning(model, level)
	offValue := ""
	if model.ThinkingLevelMap != nil {
		offValue = model.ThinkingLevelMap["off"]
	}

	enabledType := "disabled"
	if on {
		enabledType = "enabled"
	}

	switch compat.ThinkingFormat {
	case ag.ThinkingFormatZai:
		body["thinking"] = map[string]any{"type": enabledType}
		if on && effort != "" {
			body["reasoning_effort"] = effort
		}
	case ag.ThinkingFormatQwen:
		body["enable_thinking"] = on
	case ag.ThinkingFormatQwenChatTemplate:
		body["chat_template_kwargs"] = map[string]any{
			"enable_thinking":   on,
			"preserve_thinking": true,
		}
	case ag.ThinkingFormatChatTemplate:
		if kwargs := buildChatTemplateKwargs(compat.ChatTemplateKwargs, on, effort); len(kwargs) > 0 {
			body["chat_template_kwargs"] = kwargs
		}
	case ag.ThinkingFormatDeepseek:
		body["thinking"] = map[string]any{"type": enabledType}
		if on && effort != "" {
			body["reasoning_effort"] = effort
		}
	case ag.ThinkingFormatOpenRouter:
		if on && effort != "" {
			body["reasoning"] = map[string]any{"effort": effort}
		}
	case ag.ThinkingFormatTogether:
		body["reasoning"] = map[string]any{"enabled": on}
		if on && effort != "" {
			body["reasoning_effort"] = effort
		}
	case ag.ThinkingFormatAntLing:
		if on && effort != "" {
			body["reasoning"] = map[string]any{"effort": effort}
		}
	case ag.ThinkingFormatStringThinking:
		if on && effort != "" {
			body["thinking"] = effort
		}
	default: // ThinkingFormatOpenAI
		if on && effort != "" {
			body["reasoning_effort"] = effort
		} else if !on && offValue != "" {
			body["reasoning_effort"] = offValue
		}
	}
}

// buildChatTemplateKwargs resolves ChatTemplateKwargs literals and
// ChatTemplateVar placeholders against the current reasoning state.
func buildChatTemplateKwargs(spec map[string]any, on bool, effort string) map[string]any {
	if len(spec) == 0 {
		return nil
	}
	out := map[string]any{}
	for k, v := range spec {
		variable, ok := v.(ag.ChatTemplateVar)
		if !ok {
			if p, isPtr := v.(*ag.ChatTemplateVar); isPtr {
				variable, ok = *p, true
			}
		}
		if !ok {
			out[k] = v // literal
			continue
		}
		if !on && variable.OmitWhenOff {
			continue
		}
		switch variable.Var {
		case "thinking.enabled":
			out[k] = on
		case "thinking.effort":
			if on && effort != "" {
				out[k] = effort
			} else {
				out[k] = nil
			}
		}
	}
	return out
}

// convertToolChoice encodes a tool-choice constraint for the request body.
func convertToolChoice(tc *ag.ToolChoice) any {
	if tc == nil || tc.Mode == "" || tc.Mode == "auto" {
		return nil
	}
	if tc.Mode == "function" {
		return map[string]any{
			"type":     "function",
			"function": map[string]any{"name": tc.Name},
		}
	}
	return tc.Mode // "none" | "required"
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
	compat := compatOf(model)
	body := map[string]any{
		"model":    model.ID,
		"messages": convertMessages(model, c),
		"stream":   true,
	}
	if !compat.NoUsageInStreaming {
		body["stream_options"] = map[string]any{"include_usage": true}
	}

	var reasoning ag.ThinkingLevel
	if opts != nil {
		if opts.MaxTokens > 0 {
			body[maxTokensField(model)] = opts.MaxTokens
		}
		if opts.Temperature != nil {
			body["temperature"] = *opts.Temperature
		}
		if tc := convertToolChoice(opts.ToolChoice); tc != nil {
			body["tool_choice"] = tc
		}
		reasoning = opts.Reasoning
	}
	applyThinking(body, model, reasoning)

	if len(c.Tools) > 0 {
		body["tools"] = convertTools(compat, c.Tools)
	}

	for k, v := range compat.ExtraBody {
		body[k] = v
	}

	if opts != nil && opts.OnPayload != nil {
		if replaced := opts.OnPayload(body, model); replaced != nil {
			body = replaced
		}
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

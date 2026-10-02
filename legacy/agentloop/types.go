// Package agentloop is the core engine of the pi agent harness Go port.
//
// It defines the data model (messages, content blocks, usage), the event
// unions emitted by stream functions and by the loop itself, the generic
// unbounded EventStream, the AgentTool interface, JSON-Schema argument
// validation, and the agent loop machinery that drives turns and tool
// execution.
package agentloop

import "time"

// StopReason describes why an assistant message stopped generating.
type StopReason string

// StopReason values.
const (
	StopReasonStop    StopReason = "stop"
	StopReasonLength  StopReason = "length"
	StopReasonToolUse StopReason = "toolUse"
	StopReasonError   StopReason = "error"
	StopReasonAborted StopReason = "aborted"
)

// ThinkingLevel is the requested reasoning effort.
type ThinkingLevel string

// ThinkingLevel values.
const (
	ThinkingOff     ThinkingLevel = "off"
	ThinkingMinimal ThinkingLevel = "minimal"
	ThinkingLow     ThinkingLevel = "low"
	ThinkingMedium  ThinkingLevel = "medium"
	ThinkingHigh    ThinkingLevel = "high"
	ThinkingXHigh   ThinkingLevel = "xhigh"
)

// Content is a sealed union of content blocks: *TextContent,
// *ThinkingContent, *ImageContent, *ToolCall.
type Content interface{ isContent() }

// TextContent is a plain-text content block.
type TextContent struct {
	Text          string
	TextSignature string // optional
}

// ThinkingContent is a reasoning content block.
type ThinkingContent struct {
	Thinking          string
	ThinkingSignature string // optional
	Redacted          bool
}

// ImageContent is a base64-encoded image content block.
type ImageContent struct {
	Data     string // base64
	MimeType string
}

// ToolCall is a tool invocation content block.
type ToolCall struct {
	ID               string
	Name             string
	Arguments        map[string]any
	ThoughtSignature string // optional
}

func (*TextContent) isContent()     {}
func (*ThinkingContent) isContent() {}
func (*ImageContent) isContent()    {}
func (*ToolCall) isContent()        {}

// UsageCost is the per-category and total monetary cost of a request.
type UsageCost struct{ Input, Output, CacheRead, CacheWrite, Total float64 }

// Usage is the token usage and cost of an assistant message.
type Usage struct {
	Input, Output, CacheRead, CacheWrite int
	CacheWrite1h                         int // optional
	Reasoning                            int // optional
	TotalTokens                          int
	Cost                                 UsageCost
}

// AgentMessage is any transcript message (LLM messages plus host custom
// messages).
type AgentMessage interface {
	Role() string
	isAgentMessage()
}

// Message is an LLM-visible message (user/assistant/toolResult). It is a
// subset of AgentMessage.
type Message interface {
	AgentMessage
	isMessage()
}

// UserMessage is a user-authored message.
type UserMessage struct {
	Content   []Content // text + image blocks
	Timestamp int64
}

// AssistantMessage is a model-authored message.
type AssistantMessage struct {
	Content       []Content // text, thinking, toolCall
	Api           string
	Provider      string
	Model         string
	ResponseModel string // optional
	ResponseID    string // optional
	Usage         Usage
	StopReason    StopReason
	ErrorMessage  string // optional
	Timestamp     int64
}

// ToolResultMessage is the result of a tool execution.
type ToolResultMessage struct {
	ToolCallID string
	ToolName   string
	Content    []Content // text + image
	Details    any
	IsError    bool
	Timestamp  int64
}

// Role returns "user".
func (*UserMessage) Role() string { return "user" }

// Role returns "assistant".
func (*AssistantMessage) Role() string { return "assistant" }

// Role returns "toolResult".
func (*ToolResultMessage) Role() string { return "toolResult" }

func (*UserMessage) isAgentMessage()       {}
func (*AssistantMessage) isAgentMessage()  {}
func (*ToolResultMessage) isAgentMessage() {}

func (*UserMessage) isMessage()       {}
func (*AssistantMessage) isMessage()  {}
func (*ToolResultMessage) isMessage() {}

// NewUserText builds a *UserMessage with a single text block and the current
// timestamp.
func NewUserText(text string) *UserMessage {
	return &UserMessage{
		Content:   []Content{&TextContent{Text: text}},
		Timestamp: time.Now().UnixMilli(),
	}
}

// ToolCalls returns the *ToolCall content blocks of an assistant message, in
// order.
func (m *AssistantMessage) ToolCalls() []*ToolCall {
	var out []*ToolCall
	for _, c := range m.Content {
		if tc, ok := c.(*ToolCall); ok {
			out = append(out, tc)
		}
	}
	return out
}

// ModelCost is the price per million tokens for each category.
type ModelCost struct{ Input, Output, CacheRead, CacheWrite float64 }

// ThinkingFormat selects how a reasoning request is encoded on the wire for
// OpenAI-compatible endpoints (port of OpenAICompletionsCompat.thinkingFormat).
type ThinkingFormat string

// ThinkingFormat values.
const (
	// ThinkingFormatOpenAI (default): top-level "reasoning_effort".
	ThinkingFormatOpenAI ThinkingFormat = "openai"
	// ThinkingFormatZai: "thinking": {"type": "enabled"|"disabled"} plus
	// "reasoning_effort" when a level maps to a non-empty value.
	ThinkingFormatZai ThinkingFormat = "zai"
	// ThinkingFormatQwen: top-level "enable_thinking": bool.
	ThinkingFormatQwen ThinkingFormat = "qwen"
	// ThinkingFormatQwenChatTemplate: "chat_template_kwargs":
	// {"enable_thinking": bool, "preserve_thinking": true}.
	ThinkingFormatQwenChatTemplate ThinkingFormat = "qwen-chat-template"
	// ThinkingFormatChatTemplate: configurable "chat_template_kwargs" built
	// from Compat.ChatTemplateKwargs (see ChatTemplateVar).
	ThinkingFormatChatTemplate ThinkingFormat = "chat-template"
	// ThinkingFormatDeepseek: "thinking": {"type": ...} plus "reasoning_effort".
	ThinkingFormatDeepseek ThinkingFormat = "deepseek"
	// ThinkingFormatOpenRouter: "reasoning": {"effort": ...}.
	ThinkingFormatOpenRouter ThinkingFormat = "openrouter"
	// ThinkingFormatTogether: "reasoning": {"enabled": bool} plus
	// "reasoning_effort".
	ThinkingFormatTogether ThinkingFormat = "together"
	// ThinkingFormatStringThinking: top-level "thinking": "<level>".
	ThinkingFormatStringThinking ThinkingFormat = "string-thinking"
	// ThinkingFormatAntLing: "reasoning": {"effort": ...} only when the level
	// maps to a non-empty value.
	ThinkingFormatAntLing ThinkingFormat = "ant-ling"
)

// ChatTemplateVar is a dynamic chat_template_kwargs value resolved per request
// (port of the {"$var": ...} form of ChatTemplateKwargValue).
type ChatTemplateVar struct {
	// Var is "thinking.enabled" (bool: reasoning requested) or
	// "thinking.effort" (mapped ThinkingLevelMap value or the raw level).
	Var string
	// OmitWhenOff drops the key entirely when reasoning is off.
	OmitWhenOff bool
}

// Compat holds per-model wire-compatibility switches for OpenAI-compatible
// endpoints. All fields are read by wire/openai; the zero value matches the
// previous behavior of the thin port. It is a trimmed port of
// OpenAICompletionsCompat in pi's types.ts.
type Compat struct {
	// ThinkingFormat selects the reasoning request encoding. Empty =>
	// ThinkingFormatOpenAI ("reasoning_effort").
	ThinkingFormat ThinkingFormat
	// ChatTemplateKwargs is emitted as "chat_template_kwargs" when
	// ThinkingFormat is ThinkingFormatChatTemplate. Values are literals
	// (string/number/bool/nil) or ChatTemplateVar.
	ChatTemplateKwargs map[string]any
	// SupportsUsageInStreaming=false omits stream_options.include_usage
	// (some proxies reject it). Nil pointer semantics avoided: this is an
	// opt-out flag, so the field is inverted relative to pi.
	NoUsageInStreaming bool
	// SupportsDeveloperRole sends the system prompt with role "developer"
	// when the model has Reasoning=true.
	SupportsDeveloperRole bool
	// SupportsStrictMode adds "strict": false on tool definitions (pi sends
	// it unless the provider rejects it; here it is opt-in to preserve the
	// thin port's wire shape).
	SupportsStrictMode bool
	// RequiresThinkingAsText replays assistant thinking blocks as plain text
	// instead of under their signature key.
	RequiresThinkingAsText bool
	// RequiresToolResultName adds "name" to role:"tool" messages.
	RequiresToolResultName bool
	// RequiresAssistantAfterToolResult inserts a synthetic assistant message
	// between a tool result and a following user message.
	RequiresAssistantAfterToolResult bool
	// ExtraBody is merged into the request body after all other fields
	// (occlusion uses this for endpoint-specific fields the port does not
	// model). Keys set here override generated fields.
	ExtraBody map[string]any
}

// Model describes a model and its wire configuration.
type Model struct {
	ID               string
	Name             string
	Api              string // e.g. "openai-completions"
	Provider         string
	BaseURL          string
	Reasoning        bool
	Input            []string // "text","image"
	Cost             ModelCost
	ContextWindow    int
	MaxTokens        int
	Headers          map[string]string // optional default headers
	ThinkingLevelMap map[string]string // optional: pi level -> provider value
	MaxTokensField   string            // optional: "max_tokens" or "max_completion_tokens" (default)
	Compat           *Compat           // optional wire-compatibility switches
}

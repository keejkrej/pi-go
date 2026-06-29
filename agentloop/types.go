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
}

package agentloop

import (
	"context"
	"time"
)

// AgentContext is the mutable state the loop operates on.
type AgentContext struct {
	SystemPrompt string
	Messages     []AgentMessage
	Tools        []AgentTool
}

// Context is the wire-level context handed to a StreamFn.
type Context struct {
	SystemPrompt string
	Messages     []Message
	Tools        []AgentTool // adapter reads Name/Description/Parameters only
}

// ToolChoice constrains which tool the model may call (port of pi's
// toolChoice option). The zero value means the provider default ("auto").
type ToolChoice struct {
	// Mode is "auto", "none", "required", or "function".
	Mode string
	// Name is the forced tool name when Mode == "function".
	Name string
}

// StreamOptions are the per-call options handed to a StreamFn.
type StreamOptions struct {
	Temperature *float64
	MaxTokens   int
	APIKey      string
	Reasoning   ThinkingLevel // "" or "off" => no reasoning

	// Headers are per-request headers applied after Model.Headers (an empty
	// string value suppresses a model default header).
	Headers map[string]string
	// ToolChoice constrains tool selection; nil => provider default.
	ToolChoice *ToolChoice
	// OnPayload, if set, may inspect and replace the outgoing request body
	// just before it is sent (the universal escape hatch, port of pi's
	// onPayload). Return nil to keep the body unchanged.
	OnPayload func(body map[string]any, model *Model) map[string]any
	// MaxRetries is the number of pre-stream HTTP retries (429/5xx/network
	// failures before any event is emitted). 0 => no retries.
	MaxRetries int
	// MaxRetryDelay caps server-requested (Retry-After) and backoff delays
	// between pre-stream retries. Zero means DefaultMaxRetryDelay.
	MaxRetryDelay time.Duration
}

// StreamFn produces an assistant response as an event stream. It must NOT
// return an error or panic for request/model/runtime failures; it encodes them
// in the returned stream (StartEvent then ErrorEvent, final AssistantMessage
// with StopReason error/aborted plus ErrorMessage). ctx cancellation => aborted.
type StreamFn func(ctx context.Context, model *Model, c *Context, opts *StreamOptions) *AssistantMessageEventStream

// BeforeToolCallResult is returned by the BeforeToolCall hook.
type BeforeToolCallResult struct {
	Block  bool
	Reason string
}

// AfterToolCallResult is returned by the AfterToolCall hook. Pointer/flag
// fields express "provided vs omitted" (omitted keeps the original).
type AfterToolCallResult struct {
	Content    []Content // non-nil and HasContent => replace content
	HasContent bool      // set true to replace content (allows replacing with empty)
	Details    any
	HasDetails bool  // set true to replace details
	IsError    *bool // non-nil => replace error flag
	Terminate  *bool // non-nil => replace terminate hint
}

// BeforeToolCallContext is the input to the BeforeToolCall hook.
type BeforeToolCallContext struct {
	AssistantMessage *AssistantMessage
	ToolCall         *ToolCall
	Args             any
	Context          *AgentContext
}

// AfterToolCallContext is the input to the AfterToolCall hook.
type AfterToolCallContext struct {
	AssistantMessage *AssistantMessage
	ToolCall         *ToolCall
	Args             any
	Result           ToolResult
	IsError          bool
	Context          *AgentContext
}

// ShouldStopAfterTurnContext is the input to the ShouldStopAfterTurn and
// PrepareNextTurn hooks.
type ShouldStopAfterTurnContext struct {
	Message     *AssistantMessage
	ToolResults []*ToolResultMessage
	Context     *AgentContext
	NewMessages []AgentMessage
}

// PrepareNextTurnContext is the input to the PrepareNextTurn hook.
type PrepareNextTurnContext = ShouldStopAfterTurnContext

// AgentLoopTurnUpdate is the optional update returned by PrepareNextTurn.
type AgentLoopTurnUpdate struct {
	Context       *AgentContext // optional replacement
	Model         *Model        // optional replacement
	ThinkingLevel ThinkingLevel // "" => unchanged; "off" => clears reasoning; else sets it
	HasThinking   bool          // set true to apply ThinkingLevel
}

// AgentLoopConfig configures an agent run.
type AgentLoopConfig struct {
	Model       *Model
	Temperature *float64
	MaxTokens   int
	Reasoning   ThinkingLevel
	APIKey      string

	// Headers/ToolChoice/OnPayload/MaxRetries/MaxRetryDelay are forwarded to
	// the StreamFn on every turn (see StreamOptions).
	Headers       map[string]string
	ToolChoice    *ToolChoice
	OnPayload     func(body map[string]any, model *Model) map[string]any
	MaxRetries    int
	MaxRetryDelay time.Duration

	// AutoRetry, when non-nil, restarts an assistant turn whose response
	// failed with a transient provider/transport error (see AutoRetryConfig).
	AutoRetry *AutoRetryConfig

	ConvertToLlm     func(messages []AgentMessage) ([]Message, error) // REQUIRED
	TransformContext func(ctx context.Context, messages []AgentMessage) ([]AgentMessage, error)
	GetApiKey        func(provider string) (string, error)

	ShouldStopAfterTurn func(c ShouldStopAfterTurnContext) (bool, error)
	PrepareNextTurn     func(c PrepareNextTurnContext) (*AgentLoopTurnUpdate, error)
	GetSteeringMessages func() ([]AgentMessage, error)
	GetFollowUpMessages func() ([]AgentMessage, error)

	ToolExecution  ExecutionMode // "" => parallel (default)
	BeforeToolCall func(c BeforeToolCallContext) (*BeforeToolCallResult, error)
	AfterToolCall  func(c AfterToolCallContext) (*AfterToolCallResult, error)
}

// clone returns a shallow copy of the config so per-turn updates do not mutate
// the caller's config.
func (c *AgentLoopConfig) clone() *AgentLoopConfig {
	cp := *c
	return &cp
}

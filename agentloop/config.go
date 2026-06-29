package agentloop

import "context"

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

// StreamOptions are the per-call options handed to a StreamFn.
type StreamOptions struct {
	Temperature *float64
	MaxTokens   int
	APIKey      string
	Reasoning   ThinkingLevel // "" or "off" => no reasoning
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

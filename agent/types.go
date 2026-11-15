// Ported from packages/agent/src/types.ts (pi v1.5.2).

package agent

import (
	"context"
	"fmt"
	"net/http"
	"slices"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/typebox"
	"github.com/keejkrej/pi-go/telemetry"
)

// ThinkingLevel represents the agent reasoning effort setting, including "off".
// It aliases ai.ModelThinkingLevel so values are directly compatible with model
// thinking level maps.
type ThinkingLevel = ai.ModelThinkingLevel

const (
	ThinkingLevelOff     ThinkingLevel = ai.ModelThinkingLevelOff
	ThinkingLevelMinimal ThinkingLevel = ai.ModelThinkingLevelMinimal
	ThinkingLevelLow     ThinkingLevel = ai.ModelThinkingLevelLow
	ThinkingLevelMedium  ThinkingLevel = ai.ModelThinkingLevelMedium
	ThinkingLevelHigh    ThinkingLevel = ai.ModelThinkingLevelHigh
	ThinkingLevelXhigh   ThinkingLevel = ai.ModelThinkingLevelXhigh
	ThinkingLevelMax     ThinkingLevel = ai.ModelThinkingLevelMax
)

// StreamFn streams an assistant response for a model and transcript context.
// The ctx parameter carries cancellation/deadline (replacing TS AbortSignal).
type StreamFn func(
	ctx context.Context,
	model *ai.Model,
	transcript *ai.TranscriptContext,
	options *ai.SimpleStreamOptions,
) (*ai.AssistantMessageEventStream, error)

// ToolExecutionMode controls whether tool calls execute sequentially or in parallel.
type ToolExecutionMode string

const (
	ToolExecutionModeSequential ToolExecutionMode = "sequential"
	ToolExecutionModeParallel   ToolExecutionMode = "parallel"
)

// QueueMode controls how queued steering or follow-up messages are drained.
type QueueMode string

const (
	QueueModeAll        QueueMode = "all"
	QueueModeOneAtATime QueueMode = "one-at-a-time"
)

// AgentToolCall mirrors ai.ToolCall with typed or prepared arguments for hooks.
type AgentToolCall struct {
	Type             string  `json:"type"`
	Id               string  `json:"id"`
	Name             string  `json:"name"`
	Arguments        any     `json:"arguments"`
	ThoughtSignature *string `json:"thoughtSignature,omitzero"`
	Namespace        *string `json:"namespace,omitzero"`
	ArgumentsError   *string `json:"argumentsError,omitzero"`
}

// BeforeToolCallResult is returned by AgentLoopConfig.BeforeToolCall to allow, block, or terminate execution.
type BeforeToolCallResult struct {
	// Block prevents the tool from executing when true.
	Block bool `json:"block,omitzero"`
	// Reason explains why the tool was blocked (used as error content).
	Reason string `json:"reason,omitzero"`
	// Terminate signals termination intent even when the tool call is blocked.
	Terminate bool `json:"terminate,omitzero"`
}

// AfterToolCallResult allows modifying a tool result after execution.
type AfterToolCallResult struct {
	Content           ai.ContentList `json:"content,omitzero"`
	Details           jsonx.Opt[any] `json:"details,omitzero"`
	StructuredContent jsonx.Opt[any] `json:"structuredContent,omitzero"`
	IsError           *bool          `json:"isError,omitzero"`
	Usage             *ai.Usage      `json:"usage,omitzero"`
	Terminate         *bool          `json:"terminate,omitzero"`
}

// BeforeToolCallContext provides context to AgentLoopConfig.BeforeToolCall.
type BeforeToolCallContext struct {
	AssistantMessage *ai.AssistantMessage
	ToolCall         *AgentToolCall
	Args             any
	Context          *AgentContext
}

// AfterToolCallContext provides context to AgentLoopConfig.AfterToolCall.
type AfterToolCallContext struct {
	AssistantMessage *ai.AssistantMessage
	ToolCall         *AgentToolCall
	Args             any
	Result           *AgentToolResult
	IsError          bool
	Context          *AgentContext
}

// AgentTurnContext provides turn completion state to FinishTurn and PrepareNextTurn.
type AgentTurnContext struct {
	Message     *ai.AssistantMessage    `json:"message"`
	ToolResults []*ai.ToolResultMessage `json:"toolResults"`
	Context     *AgentContext           `json:"context"`
	NewMessages AgentMessageList        `json:"newMessages"`
}

// AgentTurnDecisionAction selects how FinishTurn directs the loop after a turn completes.
type AgentTurnDecisionAction string

const (
	AgentTurnDecisionContinue AgentTurnDecisionAction = "continue"
	AgentTurnDecisionEnd      AgentTurnDecisionAction = "end"
)

// AgentTurnDecision is returned by FinishTurn to override loop scheduling.
type AgentTurnDecision struct {
	Action AgentTurnDecisionAction `json:"action"`
}

// FinishTurn inspects turn completion and decides whether to continue or end the agent loop.
type FinishTurn func(ctx context.Context, turn *AgentTurnContext) (*AgentTurnDecision, error)

// AgentLoopTurnUpdate describes dynamic overrides applied before the next turn.
type AgentLoopTurnUpdate struct {
	Context       *AgentContext    `json:"context,omitzero"`
	Messages      AgentMessageList `json:"messages,omitzero"`
	Model         *ai.Model        `json:"model,omitzero"`
	ThinkingLevel *ThinkingLevel   `json:"thinkingLevel,omitzero"`
}

// PrepareRequestContext provides request state before each LLM stream call.
type PrepareRequestContext struct {
	Context       *AgentContext `json:"context"`
	Model         *ai.Model     `json:"model"`
	ThinkingLevel ThinkingLevel `json:"thinkingLevel"`
}

// AgentRequestUpdate overrides context, model, or thinking level for an individual LLM request.
type AgentRequestUpdate struct {
	Context       *AgentContext  `json:"context,omitzero"`
	Model         *ai.Model      `json:"model,omitzero"`
	ThinkingLevel *ThinkingLevel `json:"thinkingLevel,omitzero"`
}

// PrepareRequest dynamically updates context, model, or thinking level before a stream call.
type PrepareRequest func(ctx context.Context, request *PrepareRequestContext) (*AgentRequestUpdate, error)

// PrepareNextTurnContext provides state from the just-completed turn to PrepareNextTurn.
type PrepareNextTurnContext = AgentTurnContext

// PrepareNextTurn dynamically updates context, turn messages, model, or thinking level before a subsequent turn.
type PrepareNextTurn func(ctx context.Context, nextContext *PrepareNextTurnContext) (*AgentLoopTurnUpdate, error)

// AgentLoopConfig configures one run of the agent loop.
type AgentLoopConfig struct {
	// SimpleStreamOptions fields flattened so callers can construct literals ergonomically.
	TelemetryContext          telemetry.TelemetryContext                                 `json:"-"`
	ApiKey                    *string                                                    `json:"apiKey,omitzero"`
	HTTPClient                *http.Client                                               `json:"-"`
	Env                       ai.ProviderEnv                                             `json:"env,omitzero"`
	OnPayload                 func(payload any, model *ai.Model) (any, error)            `json:"-"`
	OnResponse                func(response *ai.ProviderResponse, model *ai.Model) error `json:"-"`
	OnProviderStreamEvent     func(data any, model *ai.Model) error                      `json:"-"`
	Headers                   ai.ProviderHeaders                                         `json:"headers,omitzero"`
	Identity                  *ai.ClientIdentity                                         `json:"identity,omitzero"`
	TimeoutMs                 *int64                                                     `json:"timeoutMs,omitzero"`
	MaxRetries                *int                                                       `json:"maxRetries,omitzero"`
	MaxRetryDelayMs           *int64                                                     `json:"maxRetryDelayMs,omitzero"`
	Temperature               *float64                                                   `json:"temperature,omitzero"`
	SamplingParams            *jsonx.Object                                              `json:"samplingParams,omitzero"`
	MaxTokens                 *int                                                       `json:"maxTokens,omitzero"`
	Transport                 *ai.Transport                                              `json:"transport,omitzero"`
	CacheRetention            *ai.CacheRetention                                         `json:"cacheRetention,omitzero"`
	SessionId                 *string                                                    `json:"sessionId,omitzero"`
	DisableContinuation       *bool                                                      `json:"disableContinuation,omitzero"`
	WebsocketConnectTimeoutMs *int64                                                     `json:"websocketConnectTimeoutMs,omitzero"`
	Metadata                  *jsonx.Object                                              `json:"metadata,omitzero"`
	ThinkingBudgets           *ai.ThinkingBudgets                                        `json:"thinkingBudgets,omitzero"`

	Model         *ai.Model         `json:"model"`
	Reasoning     *ThinkingLevel    `json:"reasoning,omitzero"`
	ToolExecution ToolExecutionMode `json:"toolExecution,omitzero"`

	BeforeToolCall      func(ctx context.Context, tcContext *BeforeToolCallContext) (*BeforeToolCallResult, error) `json:"-"`
	AfterToolCall       func(ctx context.Context, tcContext *AfterToolCallContext) (*AfterToolCallResult, error)   `json:"-"`
	ConvertToLlm        func(ctx context.Context, messages []AgentMessage) ([]ai.Message, error)                   `json:"-"`
	TransformContext    func(ctx context.Context, messages []AgentMessage) ([]AgentMessage, error)                 `json:"-"`
	GetApiKey           func(ctx context.Context, provider string) (string, error)                                 `json:"-"`
	GetSteeringMessages func(ctx context.Context) ([]AgentMessage, error)                                          `json:"-"`
	GetFollowUpMessages func(ctx context.Context) ([]AgentMessage, error)                                          `json:"-"`
	FinishTurn          FinishTurn                                                                                 `json:"-"`
	PrepareRequest      PrepareRequest                                                                             `json:"-"`
	PrepareNextTurn     PrepareNextTurn                                                                            `json:"-"`
}

// AgentMessage is an open interface for messages carried by the agent loop.
// Every ai.Message (*ai.SystemMessage, *ai.UserMessage, *ai.AssistantMessage,
// *ai.ToolResultMessage, *ai.UnknownMessage) implements GetRole() string automatically,
// and applications may define custom message types with their own roles.
type AgentMessage interface {
	GetRole() string
}

// UnknownAgentMessage preserves an AgentMessage with an unrecognized role across JSON round-trips.
type UnknownAgentMessage struct {
	Raw *jsonx.Object
}

// GetRole returns the "role" field from Raw, or "" if absent.
func (m *UnknownAgentMessage) GetRole() string {
	if m == nil || m.Raw == nil {
		return ""
	}
	v, ok := m.Raw.Get("role")
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

func (m UnknownAgentMessage) MarshalJSON() ([]byte, error) {
	if m.Raw == nil {
		return []byte("null"), nil
	}
	return jsonx.Marshal(m.Raw)
}

func (m *UnknownAgentMessage) UnmarshalJSON(data []byte) error {
	v, err := jsonx.Parse(data)
	if err != nil {
		return err
	}
	obj, ok := v.(*jsonx.Object)
	if !ok || obj == nil {
		return fmt.Errorf("expected object for UnknownAgentMessage")
	}
	m.Raw = obj
	return nil
}

// AgentMessageList is a slice of AgentMessage that decodes each item polymorphically.
type AgentMessageList []AgentMessage

func (l AgentMessageList) MarshalJSON() ([]byte, error) {
	if l == nil {
		return []byte("null"), nil
	}
	if len(l) == 0 {
		return []byte("[]"), nil
	}
	return jsonx.Marshal([]AgentMessage(l))
}

func (l *AgentMessageList) UnmarshalJSON(data []byte) error {
	raw, err := jsonx.Parse(data)
	if err != nil {
		return err
	}
	if raw == nil {
		*l = nil
		return nil
	}
	arr, ok := raw.([]any)
	if !ok {
		return fmt.Errorf("expected array for AgentMessageList")
	}
	out := make(AgentMessageList, 0, len(arr))
	for i, item := range arr {
		m, err := DecodeAgentMessage(item)
		if err != nil {
			return fmt.Errorf("message[%d]: %w", i, err)
		}
		out = append(out, m)
	}
	*l = out
	return nil
}

// DecodeAgentMessage converts a parsed JSON value into a standard ai.Message or UnknownAgentMessage.
func DecodeAgentMessage(v any) (AgentMessage, error) {
	if m, ok := v.(AgentMessage); ok && m != nil {
		return m, nil
	}
	obj, ok := v.(*jsonx.Object)
	if !ok || obj == nil {
		if m, isMap := v.(map[string]any); isMap {
			b, err := jsonx.Marshal(m)
			if err != nil {
				return nil, err
			}
			parsed, err := jsonx.Parse(b)
			if err != nil {
				return nil, err
			}
			obj, _ = parsed.(*jsonx.Object)
		}
	}
	if obj == nil {
		return nil, fmt.Errorf("expected object for AgentMessage")
	}
	roleVal, _ := obj.Get("role")
	role, _ := roleVal.(string)
	switch role {
	case "system", "user", "assistant", "toolResult":
		return ai.DecodeMessage(obj)
	default:
		return &UnknownAgentMessage{Raw: obj}, nil
	}
}

// UnmarshalAgentMessage unmarshals JSON bytes into a standard ai.Message or UnknownAgentMessage.
func UnmarshalAgentMessage(data []byte) (AgentMessage, error) {
	v, err := jsonx.Parse(data)
	if err != nil {
		return nil, err
	}
	return DecodeAgentMessage(v)
}

// AgentState is a snapshot of an Agent's observable state.
type AgentState struct {
	SystemPrompt     string
	Model            *ai.Model
	ThinkingLevel    ThinkingLevel
	Tools            []*AgentTool
	Messages         []AgentMessage
	IsStreaming      bool
	StreamingMessage AgentMessage
	PendingToolCalls map[string]struct{}
	ErrorMessage     *string
}

// AgentToolResult is the output returned by executing an AgentTool.
type AgentToolResult struct {
	Content           ai.ContentList `json:"content"`
	Details           any            `json:"details,omitzero"`
	StructuredContent any            `json:"structuredContent,omitzero"`
	Usage             *ai.Usage      `json:"usage,omitzero"`
	IsError           *bool          `json:"isError,omitzero"`
	Terminate         *bool          `json:"terminate,omitzero"`
}

// AgentToolCallOutcome is the finalized outcome produced by RunToolCall before becoming a ToolResultMessage.
type AgentToolCallOutcome struct {
	ToolCall   *AgentToolCall  `json:"toolCall"`
	Result     AgentToolResult `json:"result"`
	IsError    bool            `json:"isError"`
	DurationMs *int64          `json:"durationMs,omitzero"`
}

// ToolReplayPolicy describes whether re-executing an interrupted tool call on replay is safe.
type ToolReplayPolicy string

const (
	ToolReplayNever ToolReplayPolicy = "never"
	ToolReplaySafe  ToolReplayPolicy = "safe"
)

// AgentToolUpdateCallback streams partial tool output updates during execution.
type AgentToolUpdateCallback func(partialResult AgentToolResult)

// AgentToolExecuteFn executes a validated tool call.
type AgentToolExecuteFn func(
	ctx context.Context,
	toolCallId string,
	params any,
	onUpdate AgentToolUpdateCallback,
) (AgentToolResult, error)

// AgentTool combines an LLM tool declaration with its local execution handler.
type AgentTool struct {
	Name                string                          `json:"name"`
	Label               string                          `json:"label"`
	Description         string                          `json:"description"`
	Parameters          *typebox.Schema                 `json:"parameters"`
	ConstrainedSampling ai.ToolConstrainedSamplingValue `json:"constrainedSampling,omitzero"`
	PrepareArguments    func(args any) any              `json:"-"`
	OutputSchema        *typebox.Schema                 `json:"outputSchema,omitzero"`
	Execute             AgentToolExecuteFn              `json:"-"`
	Replay              ToolReplayPolicy                `json:"replay,omitzero"`
	ExecutionMode       ToolExecutionMode               `json:"executionMode,omitzero"`
}

// AiTool returns the underlying *ai.Tool declaration (stripping executable fields).
func (t *AgentTool) AiTool() *ai.Tool {
	if t == nil {
		return nil
	}
	return &ai.Tool{
		Name:                t.Name,
		Description:         t.Description,
		Parameters:          t.Parameters,
		ConstrainedSampling: t.ConstrainedSampling,
	}
}

// AgentContext is the working context passed to AgentLoop and AgentLoopContinue.
type AgentContext struct {
	Messages AgentMessageList `json:"messages"`
	Tools    []*AgentTool     `json:"tools,omitzero"`
}

// Clone shallow-copies AgentContext slices so caller slices are not aliased.
func (c *AgentContext) Clone() *AgentContext {
	if c == nil {
		return &AgentContext{}
	}
	return &AgentContext{
		Messages: slices.Clone(c.Messages),
		Tools:    slices.Clone(c.Tools),
	}
}

// AgentEvent is the sealed discriminated union emitted during an agent run.
type AgentEvent interface{ isAgentEvent() }

// AgentEventAgentStart marks the beginning of an agent run.
type AgentEventAgentStart struct {
	Type string `json:"type"`
}

func (AgentEventAgentStart) isAgentEvent() {}

func (e AgentEventAgentStart) MarshalJSON() ([]byte, error) {
	if e.Type == "" {
		e.Type = "agent_start"
	}
	type raw AgentEventAgentStart
	return jsonx.Marshal(raw(e))
}

// AgentEventAgentEnd marks the completion of an agent run and carries newly produced messages.
type AgentEventAgentEnd struct {
	Type     string           `json:"type"`
	Messages AgentMessageList `json:"messages"`
}

func (AgentEventAgentEnd) isAgentEvent() {}

func (e AgentEventAgentEnd) MarshalJSON() ([]byte, error) {
	if e.Type == "" {
		e.Type = "agent_end"
	}
	if e.Messages == nil {
		e.Messages = AgentMessageList{}
	}
	type raw AgentEventAgentEnd
	return jsonx.Marshal(raw(e))
}

// AgentEventTurnStart marks the start of a single LLM/tool turn.
type AgentEventTurnStart struct {
	Type string `json:"type"`
}

func (AgentEventTurnStart) isAgentEvent() {}

func (e AgentEventTurnStart) MarshalJSON() ([]byte, error) {
	if e.Type == "" {
		e.Type = "turn_start"
	}
	type raw AgentEventTurnStart
	return jsonx.Marshal(raw(e))
}

// AgentEventTurnEnd marks the completion of a turn with its assistant message and tool results.
type AgentEventTurnEnd struct {
	Type        string                  `json:"type"`
	Message     AgentMessage            `json:"message"`
	ToolResults []*ai.ToolResultMessage `json:"toolResults"`
}

func (AgentEventTurnEnd) isAgentEvent() {}

func (e AgentEventTurnEnd) MarshalJSON() ([]byte, error) {
	if e.Type == "" {
		e.Type = "turn_end"
	}
	if e.ToolResults == nil {
		e.ToolResults = []*ai.ToolResultMessage{}
	}
	type raw AgentEventTurnEnd
	return jsonx.Marshal(raw(e))
}

func (e *AgentEventTurnEnd) UnmarshalJSON(data []byte) error {
	obj, err := typesParseObject(data, "AgentEventTurnEnd")
	if err != nil {
		return err
	}
	return e.decodeObject(obj)
}

func (e *AgentEventTurnEnd) decodeObject(obj *jsonx.Object) error {
	t, _ := typesGetString(obj, "type")
	if t == "" {
		t = "turn_end"
	}
	msgVal, _ := obj.Get("message")
	msg, err := DecodeAgentMessage(msgVal)
	if err != nil {
		return fmt.Errorf("turn_end message: %w", err)
	}
	var toolResults []*ai.ToolResultMessage
	if trVal, ok := obj.Get("toolResults"); ok && trVal != nil {
		arr, ok := trVal.([]any)
		if !ok {
			return fmt.Errorf("turn_end toolResults: expected array")
		}
		toolResults = make([]*ai.ToolResultMessage, 0, len(arr))
		for i, item := range arr {
			m, err := ai.DecodeMessage(item)
			if err != nil {
				return fmt.Errorf("turn_end toolResults[%d]: %w", i, err)
			}
			tr, ok := m.(*ai.ToolResultMessage)
			if !ok {
				return fmt.Errorf("turn_end toolResults[%d]: expected toolResult message", i)
			}
			toolResults = append(toolResults, tr)
		}
	} else {
		toolResults = []*ai.ToolResultMessage{}
	}
	e.Type = t
	e.Message = msg
	e.ToolResults = toolResults
	return nil
}

// AgentEventMessageStart marks the start of a message entering the transcript.
type AgentEventMessageStart struct {
	Type    string       `json:"type"`
	Message AgentMessage `json:"message"`
}

func (AgentEventMessageStart) isAgentEvent() {}

func (e AgentEventMessageStart) MarshalJSON() ([]byte, error) {
	if e.Type == "" {
		e.Type = "message_start"
	}
	type raw AgentEventMessageStart
	return jsonx.Marshal(raw(e))
}

func (e *AgentEventMessageStart) UnmarshalJSON(data []byte) error {
	obj, err := typesParseObject(data, "AgentEventMessageStart")
	if err != nil {
		return err
	}
	return e.decodeObject(obj)
}

func (e *AgentEventMessageStart) decodeObject(obj *jsonx.Object) error {
	t, _ := typesGetString(obj, "type")
	if t == "" {
		t = "message_start"
	}
	msgVal, _ := obj.Get("message")
	msg, err := DecodeAgentMessage(msgVal)
	if err != nil {
		return fmt.Errorf("message_start message: %w", err)
	}
	e.Type = t
	e.Message = msg
	return nil
}

// AgentEventMessageUpdate wraps a streaming delta for an assistant message.
type AgentEventMessageUpdate struct {
	Type                  string                   `json:"type"`
	Message               AgentMessage             `json:"message"`
	AssistantMessageEvent ai.AssistantMessageEvent `json:"assistantMessageEvent"`
}

func (AgentEventMessageUpdate) isAgentEvent() {}

func (e AgentEventMessageUpdate) MarshalJSON() ([]byte, error) {
	if e.Type == "" {
		e.Type = "message_update"
	}
	type raw AgentEventMessageUpdate
	return jsonx.Marshal(raw(e))
}

func (e *AgentEventMessageUpdate) UnmarshalJSON(data []byte) error {
	obj, err := typesParseObject(data, "AgentEventMessageUpdate")
	if err != nil {
		return err
	}
	return e.decodeObject(obj)
}

func (e *AgentEventMessageUpdate) decodeObject(obj *jsonx.Object) error {
	t, _ := typesGetString(obj, "type")
	if t == "" {
		t = "message_update"
	}
	msgVal, _ := obj.Get("message")
	msg, err := DecodeAgentMessage(msgVal)
	if err != nil {
		return fmt.Errorf("message_update message: %w", err)
	}
	evVal, _ := obj.Get("assistantMessageEvent")
	aev, err := ai.DecodeAssistantMessageEvent(evVal)
	if err != nil {
		return fmt.Errorf("message_update assistantMessageEvent: %w", err)
	}
	e.Type = t
	e.Message = msg
	e.AssistantMessageEvent = aev
	return nil
}

// AgentEventMessageEnd marks the completion of a message entering the transcript.
type AgentEventMessageEnd struct {
	Type    string       `json:"type"`
	Message AgentMessage `json:"message"`
}

func (AgentEventMessageEnd) isAgentEvent() {}

func (e AgentEventMessageEnd) MarshalJSON() ([]byte, error) {
	if e.Type == "" {
		e.Type = "message_end"
	}
	type raw AgentEventMessageEnd
	return jsonx.Marshal(raw(e))
}

func (e *AgentEventMessageEnd) UnmarshalJSON(data []byte) error {
	obj, err := typesParseObject(data, "AgentEventMessageEnd")
	if err != nil {
		return err
	}
	return e.decodeObject(obj)
}

func (e *AgentEventMessageEnd) decodeObject(obj *jsonx.Object) error {
	t, _ := typesGetString(obj, "type")
	if t == "" {
		t = "message_end"
	}
	msgVal, _ := obj.Get("message")
	msg, err := DecodeAgentMessage(msgVal)
	if err != nil {
		return fmt.Errorf("message_end message: %w", err)
	}
	e.Type = t
	e.Message = msg
	return nil
}

// AgentEventToolExecutionStart marks the start of executing one tool call.
type AgentEventToolExecutionStart struct {
	Type       string `json:"type"`
	ToolCallId string `json:"toolCallId"`
	ToolName   string `json:"toolName"`
	Args       any    `json:"args"`
}

func (AgentEventToolExecutionStart) isAgentEvent() {}

func (e AgentEventToolExecutionStart) MarshalJSON() ([]byte, error) {
	if e.Type == "" {
		e.Type = "tool_execution_start"
	}
	type raw AgentEventToolExecutionStart
	return jsonx.Marshal(raw(e))
}

// AgentEventToolExecutionUpdate delivers incremental partial results from a running tool.
type AgentEventToolExecutionUpdate struct {
	Type          string `json:"type"`
	ToolCallId    string `json:"toolCallId"`
	ToolName      string `json:"toolName"`
	Args          any    `json:"args"`
	PartialResult any    `json:"partialResult"`
}

func (AgentEventToolExecutionUpdate) isAgentEvent() {}

func (e AgentEventToolExecutionUpdate) MarshalJSON() ([]byte, error) {
	if e.Type == "" {
		e.Type = "tool_execution_update"
	}
	type raw AgentEventToolExecutionUpdate
	return jsonx.Marshal(raw(e))
}

// AgentEventToolExecutionEnd marks completion of a tool execution before its toolResult message is emitted.
type AgentEventToolExecutionEnd struct {
	Type       string `json:"type"`
	ToolCallId string `json:"toolCallId"`
	ToolName   string `json:"toolName"`
	Result     any    `json:"result"`
	IsError    bool   `json:"isError"`
	DurationMs *int64 `json:"durationMs,omitzero"`
}

func (AgentEventToolExecutionEnd) isAgentEvent() {}

func (e AgentEventToolExecutionEnd) MarshalJSON() ([]byte, error) {
	if e.Type == "" {
		e.Type = "tool_execution_end"
	}
	type raw AgentEventToolExecutionEnd
	return jsonx.Marshal(raw(e))
}

// UnknownAgentEvent preserves an unrecognized AgentEvent JSON object across round-trips.
type UnknownAgentEvent struct {
	Raw *jsonx.Object
}

func (UnknownAgentEvent) isAgentEvent() {}

func (e UnknownAgentEvent) MarshalJSON() ([]byte, error) {
	if e.Raw == nil {
		return []byte("null"), nil
	}
	return jsonx.Marshal(e.Raw)
}

func (e *UnknownAgentEvent) UnmarshalJSON(data []byte) error {
	obj, err := typesParseObject(data, "UnknownAgentEvent")
	if err != nil {
		return err
	}
	e.Raw = obj
	return nil
}

// DecodeAgentEvent decodes a parsed JSON value into a concrete AgentEvent variant.
func DecodeAgentEvent(v any) (AgentEvent, error) {
	obj, ok := v.(*jsonx.Object)
	if !ok || obj == nil {
		return nil, fmt.Errorf("expected object for AgentEvent")
	}
	t, _ := typesGetString(obj, "type")
	switch t {
	case "agent_start":
		var out AgentEventAgentStart
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case "agent_end":
		var out AgentEventAgentEnd
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case "turn_start":
		var out AgentEventTurnStart
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case "turn_end":
		var out AgentEventTurnEnd
		if err := out.decodeObject(obj); err != nil {
			return nil, err
		}
		return &out, nil
	case "message_start":
		var out AgentEventMessageStart
		if err := out.decodeObject(obj); err != nil {
			return nil, err
		}
		return &out, nil
	case "message_update":
		var out AgentEventMessageUpdate
		if err := out.decodeObject(obj); err != nil {
			return nil, err
		}
		return &out, nil
	case "message_end":
		var out AgentEventMessageEnd
		if err := out.decodeObject(obj); err != nil {
			return nil, err
		}
		return &out, nil
	case "tool_execution_start":
		var out AgentEventToolExecutionStart
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case "tool_execution_update":
		var out AgentEventToolExecutionUpdate
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case "tool_execution_end":
		var out AgentEventToolExecutionEnd
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	default:
		return &UnknownAgentEvent{Raw: obj}, nil
	}
}

// UnmarshalAgentEvent decodes JSON bytes into a concrete AgentEvent variant.
func UnmarshalAgentEvent(data []byte) (AgentEvent, error) {
	v, err := jsonx.Parse(data)
	if err != nil {
		return nil, err
	}
	return DecodeAgentEvent(v)
}

func typesParseObject(data []byte, name string) (*jsonx.Object, error) {
	v, err := jsonx.Parse(data)
	if err != nil {
		return nil, err
	}
	obj, ok := v.(*jsonx.Object)
	if !ok || obj == nil {
		return nil, fmt.Errorf("expected object for %s", name)
	}
	return obj, nil
}

func typesGetString(obj *jsonx.Object, key string) (string, bool) {
	if obj == nil {
		return "", false
	}
	v, ok := obj.Get(key)
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

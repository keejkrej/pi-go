package agentloop

// AssistantMessageEvent is a sealed union emitted by a StreamFn.
type AssistantMessageEvent interface{ isAssistantMessageEvent() }

// StartEvent signals the start of an assistant message.
type StartEvent struct{ Partial *AssistantMessage }

// TextStartEvent signals the start of a text content block.
type TextStartEvent struct {
	ContentIndex int
	Partial      *AssistantMessage
}

// TextDeltaEvent carries an incremental text fragment.
type TextDeltaEvent struct {
	ContentIndex int
	Delta        string
	Partial      *AssistantMessage
}

// TextEndEvent signals the end of a text content block.
type TextEndEvent struct {
	ContentIndex int
	Content      string
	Partial      *AssistantMessage
}

// ThinkingStartEvent signals the start of a thinking content block.
type ThinkingStartEvent struct {
	ContentIndex int
	Partial      *AssistantMessage
}

// ThinkingDeltaEvent carries an incremental thinking fragment.
type ThinkingDeltaEvent struct {
	ContentIndex int
	Delta        string
	Partial      *AssistantMessage
}

// ThinkingEndEvent signals the end of a thinking content block.
type ThinkingEndEvent struct {
	ContentIndex int
	Content      string
	Partial      *AssistantMessage
}

// ToolCallStartEvent signals the start of a tool-call content block.
type ToolCallStartEvent struct {
	ContentIndex int
	Partial      *AssistantMessage
}

// ToolCallDeltaEvent carries an incremental tool-call argument fragment.
type ToolCallDeltaEvent struct {
	ContentIndex int
	Delta        string
	Partial      *AssistantMessage
}

// ToolCallEndEvent signals the end of a tool-call content block.
type ToolCallEndEvent struct {
	ContentIndex int
	ToolCall     *ToolCall
	Partial      *AssistantMessage
}

// DoneEvent is the terminal success event of a StreamFn stream.
type DoneEvent struct {
	Reason  StopReason
	Message *AssistantMessage
}

// ErrorEvent is the terminal failure event of a StreamFn stream.
type ErrorEvent struct {
	Reason StopReason
	Error  *AssistantMessage
}

func (*StartEvent) isAssistantMessageEvent()         {}
func (*TextStartEvent) isAssistantMessageEvent()     {}
func (*TextDeltaEvent) isAssistantMessageEvent()     {}
func (*TextEndEvent) isAssistantMessageEvent()       {}
func (*ThinkingStartEvent) isAssistantMessageEvent() {}
func (*ThinkingDeltaEvent) isAssistantMessageEvent() {}
func (*ThinkingEndEvent) isAssistantMessageEvent()   {}
func (*ToolCallStartEvent) isAssistantMessageEvent() {}
func (*ToolCallDeltaEvent) isAssistantMessageEvent() {}
func (*ToolCallEndEvent) isAssistantMessageEvent()   {}
func (*DoneEvent) isAssistantMessageEvent()          {}
func (*ErrorEvent) isAssistantMessageEvent()         {}

// AgentEvent is a sealed union emitted by the agent loop.
type AgentEvent interface{ isAgentEvent() }

// AgentStartEvent signals the start of an agent run.
type AgentStartEvent struct{}

// AgentEndEvent is the terminal event of an agent run; Messages is the result.
type AgentEndEvent struct{ Messages []AgentMessage }

// TurnStartEvent signals the start of a turn.
type TurnStartEvent struct{}

// TurnEndEvent signals the end of a turn.
type TurnEndEvent struct {
	Message     AgentMessage
	ToolResults []*ToolResultMessage
}

// MessageStartEvent signals a transcript message has started.
type MessageStartEvent struct{ Message AgentMessage }

// MessageUpdateEvent carries a streamed update to the current message.
type MessageUpdateEvent struct {
	Message               AgentMessage
	AssistantMessageEvent AssistantMessageEvent
}

// MessageEndEvent signals a transcript message has finished.
type MessageEndEvent struct{ Message AgentMessage }

// ToolExecutionStartEvent signals a tool began executing.
type ToolExecutionStartEvent struct {
	ToolCallID, ToolName string
	Args                 any
}

// ToolExecutionUpdateEvent carries a partial tool result.
type ToolExecutionUpdateEvent struct {
	ToolCallID, ToolName string
	Args                 any
	PartialResult        any
}

// ToolExecutionEndEvent signals a tool finished executing.
type ToolExecutionEndEvent struct {
	ToolCallID, ToolName string
	Result               any
	IsError              bool
}

func (*AgentStartEvent) isAgentEvent()          {}
func (*AgentEndEvent) isAgentEvent()            {}
func (*TurnStartEvent) isAgentEvent()           {}
func (*TurnEndEvent) isAgentEvent()             {}
func (*MessageStartEvent) isAgentEvent()        {}
func (*MessageUpdateEvent) isAgentEvent()       {}
func (*MessageEndEvent) isAgentEvent()          {}
func (*ToolExecutionStartEvent) isAgentEvent()  {}
func (*ToolExecutionUpdateEvent) isAgentEvent() {}
func (*ToolExecutionEndEvent) isAgentEvent()    {}

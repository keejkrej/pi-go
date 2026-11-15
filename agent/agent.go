// Ported from packages/agent/src/agent.ts (pi v1.5.2).

package agent

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/telemetry"
)

// DefaultConvertToLlm converts standard LLM-compatible AgentMessages ("system",
// "user", "assistant", "toolResult") into []ai.Message and drops custom app-only
// message roles.
func DefaultConvertToLlm(messages []AgentMessage) []ai.Message {
	out := make([]ai.Message, 0, len(messages))
	for _, m := range messages {
		if m == nil {
			continue
		}
		switch m.GetRole() {
		case "system", "user", "assistant", "toolResult":
			if aiMsg, ok := m.(ai.Message); ok {
				out = append(out, aiMsg)
				continue
			}
			// Handle custom Go structs or UnknownAgentMessage carrying a standard role.
			if raw, err := jsonx.Marshal(m); err == nil {
				if decoded, err := ai.UnmarshalMessage(raw); err == nil && decoded != nil {
					out = append(out, decoded)
				}
			}
		}
	}
	return out
}

func agentEmptyUsage() ai.Usage {
	return ai.Usage{
		Input:       0,
		Output:      0,
		CacheRead:   0,
		CacheWrite:  0,
		TotalTokens: 0,
		Cost:        ai.UsageCost{},
	}
}

func agentDefaultModel() *ai.Model {
	return &ai.Model{
		BaseModel: ai.BaseModel{
			Id:       "gemini-2.5-flash-lite-preview-06-17",
			Name:     "Gemini 2.5 Flash Lite Preview 06-17",
			Api:      string(ai.KnownApiGoogleGenerativeAi),
			Provider: ai.KnownProviderGoogle,
			BaseUrl:  "https://generativelanguage.googleapis.com/v1beta",
			Input:    []ai.ModelModality{ai.ModelModalityText, ai.ModelModalityImage},
			Cost: ai.ModelCost{
				ModelCostRates: ai.ModelCostRates{
					Input:      0.1,
					Output:     0.4,
					CacheRead:  0.025,
					CacheWrite: 0,
				},
			},
		},
		Reasoning:     true,
		ContextWindow: 1000000,
		MaxTokens:     65536,
	}
}

// AgentInitialState initializes an Agent's transcript, model, reasoning level, and tools.
type AgentInitialState struct {
	SystemPrompt  string
	Model         *ai.Model
	ThinkingLevel ThinkingLevel
	Tools         []*AgentTool
	Messages      []AgentMessage
}

// AgentOptions configures a new Agent instance.
type AgentOptions struct {
	InitialState               *AgentInitialState
	ConvertToLlm               func(ctx context.Context, messages []AgentMessage) ([]ai.Message, error)
	TransformContext           func(ctx context.Context, messages []AgentMessage) ([]AgentMessage, error)
	SteeringMode               QueueMode
	FollowUpMode               QueueMode
	StreamFn                   StreamFn
	SessionId                  *string
	GetApiKey                  func(ctx context.Context, provider string) (string, error)
	OnPayload                  func(payload any, model *ai.Model) (any, error)
	OnResponse                 func(response *ai.ProviderResponse, model *ai.Model) error
	OnProviderStreamEvent      func(data any, model *ai.Model) error
	ThinkingBudgets            *ai.ThinkingBudgets
	Transport                  *ai.Transport
	MaxRetryDelayMs            *int64
	ToolExecution              ToolExecutionMode
	BeforeToolCall             func(ctx context.Context, tcContext *BeforeToolCallContext) (*BeforeToolCallResult, error)
	AfterToolCall              func(ctx context.Context, tcContext *AfterToolCallContext) (*AfterToolCallResult, error)
	FinishTurn                 FinishTurn
	PrepareRequest             PrepareRequest
	PrepareNextTurn            func(ctx context.Context) (*AgentLoopTurnUpdate, error)
	PrepareNextTurnWithContext PrepareNextTurn
	Identity                   *ai.ClientIdentity
	HTTPClient                 *http.Client
	TelemetryContext           telemetry.TelemetryContext
}

// QueuedMessages holds snapshots of pending steering and follow-up messages.
type QueuedMessages struct {
	Steering []AgentMessage
	FollowUp []AgentMessage
}

type agentPendingQueue struct {
	items []AgentMessage
}

func (q *agentPendingQueue) enqueue(msg AgentMessage) {
	if msg != nil {
		q.items = append(q.items, msg)
	}
}

func (q *agentPendingQueue) dequeue(mode QueueMode) []AgentMessage {
	if len(q.items) == 0 {
		return nil
	}
	if mode == QueueModeOneAtATime {
		first := q.items[0]
		q.items = slices.Delete(q.items, 0, 1)
		return []AgentMessage{first}
	}
	all := slices.Clone(q.items)
	q.items = nil
	return all
}

func (q *agentPendingQueue) clear() {
	q.items = nil
}

func (q *agentPendingQueue) hasItems() bool {
	return len(q.items) > 0
}

func (q *agentPendingQueue) peek() []AgentMessage {
	if len(q.items) == 0 {
		return []AgentMessage{}
	}
	return slices.Clone(q.items)
}

type agentListenerEntry struct {
	id uint64
	fn func(event AgentEvent)
}

// Agent coordinates multi-turn LLM execution, tool execution, state tracking,
// and steering/follow-up message queues in a thread-safe manner.
type Agent struct {
	mu sync.Mutex

	model            *ai.Model
	thinkingLevel    ThinkingLevel
	tools            []*AgentTool
	messages         []AgentMessage
	isStreaming      bool
	streamingMessage AgentMessage
	pendingToolCalls map[string]struct{}
	errorMessage     *string

	listeners      []agentListenerEntry
	nextListenerId uint64

	convertToLlm               func(ctx context.Context, messages []AgentMessage) ([]ai.Message, error)
	transformContext           func(ctx context.Context, messages []AgentMessage) ([]AgentMessage, error)
	steeringQueue              agentPendingQueue
	followUpQueue              agentPendingQueue
	steeringMode               QueueMode
	followUpMode               QueueMode
	streamFn                   StreamFn
	sessionId                  *string
	getApiKey                  func(ctx context.Context, provider string) (string, error)
	onPayload                  func(payload any, model *ai.Model) (any, error)
	onResponse                 func(response *ai.ProviderResponse, model *ai.Model) error
	onProviderStreamEvent      func(data any, model *ai.Model) error
	thinkingBudgets            *ai.ThinkingBudgets
	transport                  ai.Transport
	maxRetryDelayMs            *int64
	toolExecution              ToolExecutionMode
	beforeToolCall             func(ctx context.Context, tcContext *BeforeToolCallContext) (*BeforeToolCallResult, error)
	afterToolCall              func(ctx context.Context, tcContext *AfterToolCallContext) (*AfterToolCallResult, error)
	finishTurn                 FinishTurn
	prepareRequest             PrepareRequest
	prepareNextTurn            func(ctx context.Context) (*AgentLoopTurnUpdate, error)
	prepareNextTurnWithContext PrepareNextTurn
	identity                   *ai.ClientIdentity
	httpClient                 *http.Client
	telemetryContext           telemetry.TelemetryContext

	runCtx    context.Context
	runCancel context.CancelFunc
	idleCh    chan struct{}
}

// NewAgent constructs an Agent with optional initial state and lifecycle configuration.
func NewAgent(options *AgentOptions) *Agent {
	a := &Agent{
		model:            agentDefaultModel(),
		thinkingLevel:    ThinkingLevelOff,
		pendingToolCalls: make(map[string]struct{}),
		steeringMode:     QueueModeOneAtATime,
		followUpMode:     QueueModeOneAtATime,
		streamFn:         GetDefaultStreamFn(),
		transport:        ai.TransportAuto,
		toolExecution:    ToolExecutionModeParallel,
	}

	var initialPrompt string
	if options != nil {
		if options.ConvertToLlm != nil {
			a.convertToLlm = options.ConvertToLlm
		}
		a.transformContext = options.TransformContext
		if options.SteeringMode != "" {
			a.steeringMode = options.SteeringMode
		}
		if options.FollowUpMode != "" {
			a.followUpMode = options.FollowUpMode
		}
		if options.StreamFn != nil {
			a.streamFn = options.StreamFn
		}
		a.sessionId = proxyCloneStringPtr(options.SessionId)
		a.getApiKey = options.GetApiKey
		a.onPayload = options.OnPayload
		a.onResponse = options.OnResponse
		a.onProviderStreamEvent = options.OnProviderStreamEvent
		a.thinkingBudgets = options.ThinkingBudgets
		if options.Transport != nil {
			a.transport = *options.Transport
		}
		a.maxRetryDelayMs = proxyCloneInt64Ptr(options.MaxRetryDelayMs)
		if options.ToolExecution != "" {
			a.toolExecution = options.ToolExecution
		}
		a.beforeToolCall = options.BeforeToolCall
		a.afterToolCall = options.AfterToolCall
		a.finishTurn = options.FinishTurn
		a.prepareRequest = options.PrepareRequest
		a.prepareNextTurn = options.PrepareNextTurn
		a.prepareNextTurnWithContext = options.PrepareNextTurnWithContext
		a.identity = options.Identity
		a.httpClient = options.HTTPClient
		a.telemetryContext = options.TelemetryContext

		if init := options.InitialState; init != nil {
			initialPrompt = init.SystemPrompt
			if init.Model != nil {
				a.model = init.Model
			}
			if init.ThinkingLevel != "" {
				a.thinkingLevel = init.ThinkingLevel
			}
			if init.Tools != nil {
				a.tools = slices.Clone(init.Tools)
			}
			if init.Messages != nil {
				a.messages = slices.Clone(init.Messages)
			}
		}
	}

	var spPtr *string
	if initialPrompt != "" {
		sp := initialPrompt
		spPtr = &sp
	}
	if sys := ai.CreateInitialSystemMessage(spPtr, alDeclaredAiTools(a.tools)); sys != nil {
		if len(a.messages) == 0 || a.messages[0] == nil || a.messages[0].GetRole() != "system" {
			a.messages = append([]AgentMessage{sys}, a.messages...)
		}
	}

	return a
}

// Subscribe registers an event listener invoked synchronously after state updates
// without holding Agent's mutex. The returned function unregisters the listener.
func (a *Agent) Subscribe(fn func(event AgentEvent)) func() {
	if fn == nil {
		return func() {}
	}
	a.mu.Lock()
	a.nextListenerId++
	id := a.nextListenerId
	a.listeners = append(a.listeners, agentListenerEntry{id: id, fn: fn})
	a.mu.Unlock()

	return func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		for i, entry := range a.listeners {
			if entry.id == id {
				a.listeners = slices.Delete(a.listeners, i, i+1)
				break
			}
		}
	}
}

// State returns a thread-safe snapshot of the agent's current state.
func (a *Agent) State() AgentState {
	a.mu.Lock()
	defer a.mu.Unlock()

	pending := make(map[string]struct{}, len(a.pendingToolCalls))
	for k := range a.pendingToolCalls {
		pending[k] = struct{}{}
	}
	msgs := slices.Clone(a.messages)
	if msgs == nil {
		msgs = []AgentMessage{}
	}
	tools := a.toolsLocked()
	if tools == nil {
		tools = []*AgentTool{}
	}

	return AgentState{
		SystemPrompt:     a.systemPromptLocked(),
		Model:            a.model,
		ThinkingLevel:    a.thinkingLevel,
		Tools:            tools,
		Messages:         msgs,
		IsStreaming:      a.isStreaming,
		StreamingMessage: a.streamingMessage,
		PendingToolCalls: pending,
		ErrorMessage:     proxyCloneStringPtr(a.errorMessage),
	}
}

func (a *Agent) systemPromptLocked() string {
	return ai.GetCurrentSystemPrompt(DefaultConvertToLlm(a.messages))
}

func (a *Agent) toolsLocked() []*AgentTool {
	return slices.Clone(a.tools)
}

// SystemPrompt returns the effective system prompt string replayed from the transcript.
func (a *Agent) SystemPrompt() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.systemPromptLocked()
}

// SetSystemPrompt updates the current transcript system prompt message or prepends
// an initial system message if none exists yet.
func (a *Agent) SetSystemPrompt(v string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if ai.GetCurrentSystemMessage(DefaultConvertToLlm(a.messages)) != nil {
		alReplaceSystemPromptInMessages(a.messages, v)
		return
	}

	if v != "" || len(a.tools) > 0 {
		var spPtr *string
		if v != "" {
			sp := v
			spPtr = &sp
		}
		if sm := ai.CreateInitialSystemMessage(spPtr, alDeclaredAiTools(a.tools)); sm != nil {
			a.messages = append([]AgentMessage{sm}, a.messages...)
		}
	}
}

// Model returns the active model.
func (a *Agent) Model() *ai.Model {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.model
}

// SetModel updates the active model.
func (a *Agent) SetModel(m *ai.Model) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.model = m
}

// ThinkingLevel returns the current thinking level.
func (a *Agent) ThinkingLevel() ThinkingLevel {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.thinkingLevel
}

// SetThinkingLevel updates the current thinking level.
func (a *Agent) SetThinkingLevel(l ThinkingLevel) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.thinkingLevel = l
}

// Tools returns a shallow copy of the currently active tools.
func (a *Agent) Tools() []*AgentTool {
	a.mu.Lock()
	defer a.mu.Unlock()
	tools := a.toolsLocked()
	if tools == nil {
		return []*AgentTool{}
	}
	return tools
}

// SetTools replaces the executable tool slice without mutating the transcript.
// Tool changes are declared lazily as transcript deltas before the next LLM request.
func (a *Agent) SetTools(tools []*AgentTool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tools = slices.Clone(tools)
}

// Messages returns a shallow copy of the transcript messages.
func (a *Agent) Messages() []AgentMessage {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.messages == nil {
		return []AgentMessage{}
	}
	return slices.Clone(a.messages)
}

// ReplaceMessages replaces the transcript messages with a shallow copy of ms.
func (a *Agent) ReplaceMessages(ms []AgentMessage) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if ms == nil {
		a.messages = []AgentMessage{}
		return
	}
	a.messages = slices.Clone(ms)
}

// AppendMessage appends a message to the transcript.
func (a *Agent) AppendMessage(m AgentMessage) {
	if m == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.messages = append(a.messages, m)
}

// ClearMessages empties the transcript messages slice.
func (a *Agent) ClearMessages() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.messages = nil
}

// IsStreaming reports whether an agent run is currently active.
func (a *Agent) IsStreaming() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.isStreaming
}

// StreamingMessage returns the current in-flight assistant message snapshot, or nil.
func (a *Agent) StreamingMessage() AgentMessage {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.streamingMessage
}

// PendingToolCalls returns a copy of currently executing tool call IDs.
func (a *Agent) PendingToolCalls() map[string]struct{} {
	a.mu.Lock()
	defer a.mu.Unlock()
	cp := make(map[string]struct{}, len(a.pendingToolCalls))
	for k := range a.pendingToolCalls {
		cp[k] = struct{}{}
	}
	return cp
}

// ErrorMessage returns the last run error message, or nil if none.
func (a *Agent) ErrorMessage() *string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return proxyCloneStringPtr(a.errorMessage)
}

// SessionId returns the session ID forwarded to providers.
func (a *Agent) SessionId() *string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return proxyCloneStringPtr(a.sessionId)
}

// SetSessionId updates the session ID forwarded to providers.
func (a *Agent) SetSessionId(id *string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sessionId = proxyCloneStringPtr(id)
}

// ThinkingBudgets returns custom thinking token budgets, or nil.
func (a *Agent) ThinkingBudgets() *ai.ThinkingBudgets {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.thinkingBudgets
}

// SetThinkingBudgets updates custom thinking token budgets.
func (a *Agent) SetThinkingBudgets(b *ai.ThinkingBudgets) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.thinkingBudgets = b
}

// Transport returns the preferred transport mode (defaulting to ai.TransportAuto).
func (a *Agent) Transport() ai.Transport {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.transport
}

// SetTransport updates the preferred transport mode.
func (a *Agent) SetTransport(t ai.Transport) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.transport = t
}

// MaxRetryDelayMs returns the maximum backoff delay cap in milliseconds.
func (a *Agent) MaxRetryDelayMs() *int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return proxyCloneInt64Ptr(a.maxRetryDelayMs)
}

// SetMaxRetryDelayMs updates the maximum backoff delay cap in milliseconds.
func (a *Agent) SetMaxRetryDelayMs(ms *int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.maxRetryDelayMs = proxyCloneInt64Ptr(ms)
}

// Identity returns the client attribution / user-agent identity, or nil.
func (a *Agent) Identity() *ai.ClientIdentity {
	a.mu.Lock()
	defer a.mu.Unlock()
	return ai.MergeClientIdentity(a.identity)
}

// SetIdentity updates the client attribution / user-agent identity.
func (a *Agent) SetIdentity(identity *ai.ClientIdentity) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.identity = ai.MergeClientIdentity(identity)
}

// SteeringMode returns the queue draining mode for steering messages.
func (a *Agent) SteeringMode() QueueMode {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.steeringMode
}

// SetSteeringMode updates the queue draining mode for steering messages.
func (a *Agent) SetSteeringMode(mode QueueMode) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if mode != "" {
		a.steeringMode = mode
	}
}

// FollowUpMode returns the queue draining mode for follow-up messages.
func (a *Agent) FollowUpMode() QueueMode {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.followUpMode
}

// SetFollowUpMode updates the queue draining mode for follow-up messages.
func (a *Agent) SetFollowUpMode(mode QueueMode) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if mode != "" {
		a.followUpMode = mode
	}
}

// ToolExecution returns the tool execution scheduling mode.
func (a *Agent) ToolExecution() ToolExecutionMode {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.toolExecution
}

// SetToolExecution updates the tool execution scheduling mode.
func (a *Agent) SetToolExecution(mode ToolExecutionMode) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if mode != "" {
		a.toolExecution = mode
	}
}

// StreamFn returns the current StreamFn configured on the agent.
func (a *Agent) StreamFn() StreamFn {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.streamFn
}

// SetStreamFn updates the StreamFn used by subsequent runs (nil restores default).
func (a *Agent) SetStreamFn(fn StreamFn) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if fn == nil {
		a.streamFn = GetDefaultStreamFn()
		return
	}
	a.streamFn = fn
}

func defaultAgentConvertToLlm(_ context.Context, msgs []AgentMessage) ([]ai.Message, error) {
	return DefaultConvertToLlm(msgs), nil
}

// ConvertToLlm returns the message converter invoked before LLM requests
// (returning a default wrapper over DefaultConvertToLlm when none is configured).
func (a *Agent) ConvertToLlm() func(ctx context.Context, messages []AgentMessage) ([]ai.Message, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.convertToLlm == nil {
		return defaultAgentConvertToLlm
	}
	return a.convertToLlm
}

// SetConvertToLlm updates the message converter invoked before LLM requests (nil restores default).
func (a *Agent) SetConvertToLlm(fn func(ctx context.Context, messages []AgentMessage) ([]ai.Message, error)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.convertToLlm = fn
}

// TransformContext returns the optional context transformer applied before ConvertToLlm, or nil.
func (a *Agent) TransformContext() func(ctx context.Context, messages []AgentMessage) ([]AgentMessage, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.transformContext
}

// SetTransformContext updates the optional context transformer applied before ConvertToLlm.
func (a *Agent) SetTransformContext(fn func(ctx context.Context, messages []AgentMessage) ([]AgentMessage, error)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.transformContext = fn
}

// GetApiKey returns the dynamic API key resolver callback, or nil.
func (a *Agent) GetApiKey() func(ctx context.Context, provider string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.getApiKey
}

// SetGetApiKey updates the dynamic API key resolver callback.
func (a *Agent) SetGetApiKey(fn func(ctx context.Context, provider string) (string, error)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.getApiKey = fn
}

// OnPayload returns the outbound request payload inspection hook, or nil.
func (a *Agent) OnPayload() func(payload any, model *ai.Model) (any, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.onPayload
}

// SetOnPayload updates the outbound request payload inspection hook.
func (a *Agent) SetOnPayload(fn func(payload any, model *ai.Model) (any, error)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onPayload = fn
}

// OnResponse returns the HTTP provider response inspection hook, or nil.
func (a *Agent) OnResponse() func(response *ai.ProviderResponse, model *ai.Model) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.onResponse
}

// SetOnResponse updates the HTTP provider response inspection hook.
func (a *Agent) SetOnResponse(fn func(response *ai.ProviderResponse, model *ai.Model) error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onResponse = fn
}

// OnProviderStreamEvent returns the raw provider stream event inspection hook, or nil.
func (a *Agent) OnProviderStreamEvent() func(data any, model *ai.Model) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.onProviderStreamEvent
}

// SetOnProviderStreamEvent updates the raw provider stream event inspection hook.
func (a *Agent) SetOnProviderStreamEvent(fn func(data any, model *ai.Model) error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onProviderStreamEvent = fn
}

// BeforeToolCall returns the pre-execution tool call hook, or nil.
func (a *Agent) BeforeToolCall() func(ctx context.Context, tcContext *BeforeToolCallContext) (*BeforeToolCallResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.beforeToolCall
}

// SetBeforeToolCall updates the pre-execution tool call hook.
func (a *Agent) SetBeforeToolCall(fn func(ctx context.Context, tcContext *BeforeToolCallContext) (*BeforeToolCallResult, error)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.beforeToolCall = fn
}

// AfterToolCall returns the post-execution tool call hook, or nil.
func (a *Agent) AfterToolCall() func(ctx context.Context, tcContext *AfterToolCallContext) (*AfterToolCallResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.afterToolCall
}

// SetAfterToolCall updates the post-execution tool call hook.
func (a *Agent) SetAfterToolCall(fn func(ctx context.Context, tcContext *AfterToolCallContext) (*AfterToolCallResult, error)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.afterToolCall = fn
}

// FinishTurn returns the turn-completion hook, or nil.
func (a *Agent) FinishTurn() FinishTurn {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.finishTurn
}

// SetFinishTurn updates the turn-completion hook.
func (a *Agent) SetFinishTurn(fn FinishTurn) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.finishTurn = fn
}

// PrepareRequest returns the pre-stream request preparation hook, or nil.
func (a *Agent) PrepareRequest() PrepareRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.prepareRequest
}

// SetPrepareRequest updates the pre-stream request preparation hook.
func (a *Agent) SetPrepareRequest(fn PrepareRequest) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.prepareRequest = fn
}

// PrepareNextTurn returns the legacy next-turn preparation hook, or nil.
func (a *Agent) PrepareNextTurn() func(ctx context.Context) (*AgentLoopTurnUpdate, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.prepareNextTurn
}

// SetPrepareNextTurn updates the legacy next-turn preparation hook.
func (a *Agent) SetPrepareNextTurn(fn func(ctx context.Context) (*AgentLoopTurnUpdate, error)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.prepareNextTurn = fn
}

// PrepareNextTurnWithContext returns the context-aware next-turn preparation hook, or nil.
func (a *Agent) PrepareNextTurnWithContext() PrepareNextTurn {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.prepareNextTurnWithContext
}

// SetPrepareNextTurnWithContext updates the context-aware next-turn preparation hook.
func (a *Agent) SetPrepareNextTurnWithContext(fn PrepareNextTurn) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.prepareNextTurnWithContext = fn
}

// HTTPClient returns the custom HTTP client forwarded to provider calls, or nil.
func (a *Agent) HTTPClient() *http.Client {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.httpClient
}

// SetHTTPClient updates the custom HTTP client forwarded to provider calls.
func (a *Agent) SetHTTPClient(client *http.Client) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.httpClient = client
}

// Steer queues a steering message delivered after the current tool turn.
func (a *Agent) Steer(m AgentMessage) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.steeringQueue.enqueue(m)
}

// FollowUp queues a follow-up message delivered after the agent would otherwise stop.
func (a *Agent) FollowUp(m AgentMessage) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.followUpQueue.enqueue(m)
}

// ClearSteeringQueue removes all queued steering messages.
func (a *Agent) ClearSteeringQueue() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.steeringQueue.clear()
}

// ClearFollowUpQueue removes all queued follow-up messages.
func (a *Agent) ClearFollowUpQueue() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.followUpQueue.clear()
}

// ClearAllQueues clears both steering and follow-up queues.
func (a *Agent) ClearAllQueues() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.steeringQueue.clear()
	a.followUpQueue.clear()
}

// HasQueuedMessages reports whether either steering or follow-up messages are queued.
func (a *Agent) HasQueuedMessages() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.steeringQueue.hasItems() || a.followUpQueue.hasItems()
}

// PeekQueuedMessages returns copies of the queued steering and follow-up messages.
func (a *Agent) PeekQueuedMessages() QueuedMessages {
	a.mu.Lock()
	defer a.mu.Unlock()
	return QueuedMessages{
		Steering: a.steeringQueue.peek(),
		FollowUp: a.followUpQueue.peek(),
	}
}

// Signal returns the active run's cancellation context while streaming, or nil when idle.
func (a *Agent) Signal() context.Context {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.runCtx
}

// Abort cancels the currently active agent run, if any.
func (a *Agent) Abort() {
	a.mu.Lock()
	cancel := a.runCancel
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// WaitForIdle blocks until the currently active run completes or ctx is cancelled.
func (a *Agent) WaitForIdle(ctx context.Context) error {
	a.mu.Lock()
	ch := a.idleCh
	a.mu.Unlock()
	if ch == nil {
		return nil
	}
	if ctx == nil {
		<-ch
		return nil
	}
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Reset clears conversation state and queues while retaining the replayed prompt/tool baseline.
// Returns an error without mutating state if the agent is currently streaming.
func (a *Agent) Reset() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.isStreaming {
		return errors.New("Agent is already processing. Wait for completion before resetting.")
	}
	baseline := ai.GetCurrentSystemMessage(DefaultConvertToLlm(a.messages))
	if baseline != nil {
		a.messages = []AgentMessage{baseline}
	} else {
		a.messages = []AgentMessage{}
	}
	a.streamingMessage = nil
	a.pendingToolCalls = make(map[string]struct{})
	a.errorMessage = nil
	a.steeringQueue.clear()
	a.followUpQueue.clear()
	return nil
}

// Prompt starts an agent run with one or more AgentMessage prompts.
func (a *Agent) Prompt(ctx context.Context, messages ...AgentMessage) error {
	if len(messages) == 0 {
		return errors.New("Prompt requires at least one message")
	}
	msgs := slices.Clone(messages)
	a.mu.Lock()
	return a.runWithLifecycleLocked(ctx, false, func(
		runCtx context.Context,
		agentContext *AgentContext,
		cfg *AgentLoopConfig,
		emit AgentEventSink,
		streamFn StreamFn,
	) ([]AgentMessage, error) {
		return RunAgentLoop(runCtx, msgs, agentContext, cfg, emit, streamFn)
	})
}

// PromptText starts an agent run with a user text prompt and optional image attachments.
func (a *Agent) PromptText(ctx context.Context, text string, images ...*ai.ImageContent) error {
	blocks := make(ai.ContentList, 0, 1+len(images))
	blocks = append(blocks, &ai.TextContent{
		Type: "text",
		Text: text,
	})
	for _, img := range images {
		if img != nil {
			blocks = append(blocks, img)
		}
	}
	userMsg := &ai.UserMessage{
		Role:      "user",
		Content:   ai.UserMessageContent{Blocks: blocks},
		Timestamp: time.Now().UnixMilli(),
	}
	return a.Prompt(ctx, userMsg)
}

// Continue resumes the agent from the existing transcript context.
// If the transcript ends with an assistant message, queued steering (with initial
// poll skipped so one-at-a-time draining is preserved) or follow-up messages are
// drained and sent as prompts; otherwise returns an error without draining queues.
func (a *Agent) Continue(ctx context.Context) error {
	a.mu.Lock()
	if a.isStreaming {
		a.mu.Unlock()
		return errors.New("Agent is already processing. Wait for completion before continuing.")
	}
	hasNonSystem := false
	var lastMsg AgentMessage
	for _, m := range a.messages {
		if m != nil && m.GetRole() != "system" {
			hasNonSystem = true
		}
		lastMsg = m
	}
	if len(a.messages) == 0 || !hasNonSystem {
		a.mu.Unlock()
		return errors.New("No messages to continue from")
	}
	if lastMsg != nil && lastMsg.GetRole() == "assistant" {
		queuedSteering := a.steeringQueue.dequeue(a.steeringMode)
		if len(queuedSteering) > 0 {
			return a.runWithLifecycleLocked(ctx, true, func(
				runCtx context.Context,
				agentContext *AgentContext,
				cfg *AgentLoopConfig,
				emit AgentEventSink,
				streamFn StreamFn,
			) ([]AgentMessage, error) {
				return RunAgentLoop(runCtx, queuedSteering, agentContext, cfg, emit, streamFn)
			})
		}
		queuedFollowUp := a.followUpQueue.dequeue(a.followUpMode)
		if len(queuedFollowUp) > 0 {
			return a.runWithLifecycleLocked(ctx, false, func(
				runCtx context.Context,
				agentContext *AgentContext,
				cfg *AgentLoopConfig,
				emit AgentEventSink,
				streamFn StreamFn,
			) ([]AgentMessage, error) {
				return RunAgentLoop(runCtx, queuedFollowUp, agentContext, cfg, emit, streamFn)
			})
		}
		a.mu.Unlock()
		return errors.New("Cannot continue from message role: assistant")
	}

	return a.runWithLifecycleLocked(ctx, false, func(
		runCtx context.Context,
		agentContext *AgentContext,
		cfg *AgentLoopConfig,
		emit AgentEventSink,
		streamFn StreamFn,
	) ([]AgentMessage, error) {
		return RunAgentLoopContinue(runCtx, agentContext, cfg, emit, streamFn)
	})
}

// runWithLifecycleLocked enters an active run with a.mu held on entry and releases a.mu before executing runner.
func (a *Agent) runWithLifecycleLocked(
	ctx context.Context,
	skipInitialSteeringPoll bool,
	runner func(
		runCtx context.Context,
		agentContext *AgentContext,
		cfg *AgentLoopConfig,
		emit AgentEventSink,
		streamFn StreamFn,
	) ([]AgentMessage, error),
) error {
	if ctx == nil {
		ctx = context.Background()
	}

	if a.isStreaming {
		a.mu.Unlock()
		return errors.New("Agent is already processing a prompt. Use steer() or followUp() to queue messages, or wait for completion.")
	}
	model := a.model
	if model == nil {
		a.mu.Unlock()
		return errors.New("No model configured")
	}

	idleCh := make(chan struct{})
	runCtx, runCancel := context.WithCancel(ctx)
	a.isStreaming = true
	a.streamingMessage = nil
	a.errorMessage = nil
	a.runCtx = runCtx
	a.runCancel = runCancel
	a.idleCh = idleCh

	agentContext := &AgentContext{
		Messages: slices.Clone(a.messages),
		Tools:    slices.Clone(a.tools),
	}

	transport := a.transport
	var reasoning *ThinkingLevel
	if a.thinkingLevel != "" && a.thinkingLevel != ThinkingLevelOff {
		tl := a.thinkingLevel
		reasoning = &tl
	}
	convertToLlm := a.convertToLlm
	if convertToLlm == nil {
		convertToLlm = defaultAgentConvertToLlm
	}

	var pollMu sync.Mutex
	skipPoll := skipInitialSteeringPoll

	cfg := &AgentLoopConfig{
		TelemetryContext:      a.telemetryContext,
		HTTPClient:            a.httpClient,
		OnPayload:             a.onPayload,
		OnResponse:            a.onResponse,
		OnProviderStreamEvent: a.onProviderStreamEvent,
		Identity:              a.identity,
		MaxRetryDelayMs:       proxyCloneInt64Ptr(a.maxRetryDelayMs),
		Transport:             &transport,
		SessionId:             proxyCloneStringPtr(a.sessionId),
		ThinkingBudgets:       a.thinkingBudgets,
		Model:                 model,
		Reasoning:             reasoning,
		ToolExecution:         a.toolExecution,
		BeforeToolCall:        a.beforeToolCall,
		AfterToolCall:         a.afterToolCall,
		ConvertToLlm:          convertToLlm,
		TransformContext:      a.transformContext,
		GetApiKey:             a.getApiKey,
		FinishTurn:            a.finishTurn,
		PrepareRequest:        a.prepareRequest,
		GetSteeringMessages: func(context.Context) ([]AgentMessage, error) {
			pollMu.Lock()
			if skipPoll {
				skipPoll = false
				pollMu.Unlock()
				return nil, nil
			}
			pollMu.Unlock()
			a.mu.Lock()
			defer a.mu.Unlock()
			return a.steeringQueue.dequeue(a.steeringMode), nil
		},
		GetFollowUpMessages: func(context.Context) ([]AgentMessage, error) {
			a.mu.Lock()
			defer a.mu.Unlock()
			return a.followUpQueue.dequeue(a.followUpMode), nil
		},
	}

	basePrepareNextTurn := a.prepareNextTurn
	basePrepareNextTurnWithContext := a.prepareNextTurnWithContext
	if basePrepareNextTurnWithContext != nil || basePrepareNextTurn != nil {
		cfg.PrepareNextTurn = func(c context.Context, nextCtx *PrepareNextTurnContext) (*AgentLoopTurnUpdate, error) {
			if basePrepareNextTurnWithContext != nil {
				return basePrepareNextTurnWithContext(c, nextCtx)
			}
			return basePrepareNextTurn(c)
		}
	}
	streamFn := a.streamFn
	a.mu.Unlock()

	defer func() {
		runCancel()
		a.mu.Lock()
		a.isStreaming = false
		a.streamingMessage = nil
		a.pendingToolCalls = make(map[string]struct{})
		a.runCtx = nil
		a.runCancel = nil
		a.idleCh = nil
		a.mu.Unlock()
		close(idleCh)
	}()

	var trackMu sync.Mutex
	hadAgentStart := false
	turnOpen := false
	hadAgentEnd := false
	var emittedNewMessages []AgentMessage

	emit := func(ev AgentEvent) {
		trackMu.Lock()
		switch e := ev.(type) {
		case *AgentEventAgentStart:
			hadAgentStart = true
		case *AgentEventTurnStart:
			turnOpen = true
		case *AgentEventTurnEnd:
			turnOpen = false
		case *AgentEventMessageEnd:
			if e.Message != nil {
				emittedNewMessages = append(emittedNewMessages, e.Message)
			}
		case *AgentEventAgentEnd:
			hadAgentEnd = true
		}
		trackMu.Unlock()

		a.processEvent(ev)
	}

	_, err := runner(runCtx, agentContext, cfg, emit, streamFn)

	trackMu.Lock()
	started := hadAgentStart
	open := turnOpen
	ended := hadAgentEnd
	soFar := slices.Clone(emittedNewMessages)
	trackMu.Unlock()

	if err != nil {
		errText := err.Error()
		stopReason := ai.StopReasonError
		if runCtx.Err() != nil {
			stopReason = ai.StopReasonAborted
		}
		errMsg := &ai.AssistantMessage{
			Role: "assistant",
			Content: ai.ContentList{
				&ai.TextContent{
					Type: "text",
					Text: "",
				},
			},
			Api:          ai.Api(model.Api),
			Provider:     model.Provider,
			Model:        model.Id,
			Usage:        agentEmptyUsage(),
			StopReason:   stopReason,
			ErrorMessage: &errText,
			Timestamp:    time.Now().UnixMilli(),
		}

		if !started {
			emit(&AgentEventAgentStart{Type: "agent_start"})
		}
		if !open {
			emit(&AgentEventTurnStart{Type: "turn_start"})
		}
		emit(&AgentEventMessageStart{Type: "message_start", Message: errMsg})
		emit(&AgentEventMessageEnd{Type: "message_end", Message: errMsg})
		emit(&AgentEventTurnEnd{
			Type:        "turn_end",
			Message:     errMsg,
			ToolResults: []*ai.ToolResultMessage{},
		})
		if !ended {
			allNew := append(soFar, errMsg)
			emit(&AgentEventAgentEnd{
				Type:     "agent_end",
				Messages: allNew,
			})
		}
	}

	return nil
}

func (a *Agent) processEvent(ev AgentEvent) {
	a.mu.Lock()
	switch e := ev.(type) {
	case *AgentEventMessageStart:
		a.streamingMessage = e.Message
	case *AgentEventMessageUpdate:
		a.streamingMessage = e.Message
	case *AgentEventMessageEnd:
		a.streamingMessage = nil
		if e.Message != nil {
			a.messages = append(a.messages, e.Message)
		}
	case *AgentEventToolExecutionStart:
		a.pendingToolCalls[e.ToolCallId] = struct{}{}
	case *AgentEventToolExecutionEnd:
		delete(a.pendingToolCalls, e.ToolCallId)
	case *AgentEventTurnEnd:
		if am, ok := e.Message.(*ai.AssistantMessage); ok && am != nil && am.ErrorMessage != nil && *am.ErrorMessage != "" {
			s := *am.ErrorMessage
			a.errorMessage = &s
		}
	case *AgentEventAgentEnd:
		a.streamingMessage = nil
	}

	listeners := make([]func(AgentEvent), len(a.listeners))
	for i, entry := range a.listeners {
		listeners[i] = entry.fn
	}
	a.mu.Unlock()

	for _, fn := range listeners {
		fn(ev)
	}
}

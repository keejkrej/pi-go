// Ported from packages/coding-agent/src/core/agent-session.ts (pi v1.5.2, part 1).

package core

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/compat"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// DefaultStreamFn is the standard non-custom StreamFn dispatching to compat.StreamSimple,
// mirroring pi-ai's streamSimple so summarization auth treats it as default transport
// rather than a caller-provided custom StreamFn.
func DefaultStreamFn(
	streamCtx context.Context,
	model *ai.Model,
	c *ai.TranscriptContext,
	so *ai.SimpleStreamOptions,
) (*ai.AssistantMessageEventStream, error) {
	var messages []ai.Message
	if c != nil {
		messages = c.Messages
	}
	return compat.StreamSimple(streamCtx, model, &ai.Context{Messages: messages}, so), nil
}

var (
	as1RuntimeStreamMu    sync.RWMutex
	as1RuntimeStreamFuncs = map[uintptr]*ModelRuntime{}
	as1SkillBlockRegexp   = regexp.MustCompile(`(?s)^<skill name="([^"]+)" location="([^"]+)">\n(.*?)\n</skill>(?:\n\n(.+))?$`)
)

// MarkRuntimeStreamFunction registers streamFn as resolving authentication
// through modelRuntime on its own (so auxiliary callers do not inject an
// explicit API key that would override runtime auth such as OAuth logins).
func MarkRuntimeStreamFunction(streamFn agent.StreamFn, modelRuntime *ModelRuntime) {
	if streamFn == nil || modelRuntime == nil {
		return
	}
	ptr := reflect.ValueOf(streamFn).Pointer()
	if ptr == 0 {
		return
	}
	as1RuntimeStreamMu.Lock()
	as1RuntimeStreamFuncs[ptr] = modelRuntime
	as1RuntimeStreamMu.Unlock()
}

func as1IsRuntimeStreamFunction(streamFn agent.StreamFn, modelRuntime *ModelRuntime) bool {
	if streamFn == nil || modelRuntime == nil {
		return false
	}
	ptr := reflect.ValueOf(streamFn).Pointer()
	if ptr == 0 {
		return false
	}
	as1RuntimeStreamMu.RLock()
	registered := as1RuntimeStreamFuncs[ptr]
	as1RuntimeStreamMu.RUnlock()
	return registered == modelRuntime
}

func as1IsDefaultStreamFn(streamFn agent.StreamFn) bool {
	if streamFn == nil {
		return true
	}
	ptr := reflect.ValueOf(streamFn).Pointer()
	if ptr == 0 {
		return false
	}
	if def := agent.GetDefaultStreamFn(); def != nil && ptr == reflect.ValueOf(def).Pointer() {
		return true
	}
	return ptr == reflect.ValueOf(DefaultStreamFn).Pointer()
}

// ParsedSkillBlock represents a parsed `<skill ...>` prompt block and any
// trailing user message that followed it.
type ParsedSkillBlock struct {
	Name        string  `json:"name"`
	Location    string  `json:"location"`
	Content     string  `json:"content"`
	UserMessage *string `json:"userMessage,omitzero"`
}

// ParseSkillBlock parses a skill block from expanded prompt text. Returns nil
// if the text does not start with a well-formed `<skill>` block.
func ParseSkillBlock(text string) *ParsedSkillBlock {
	match := as1SkillBlockRegexp.FindStringSubmatch(text)
	if len(match) == 0 {
		return nil
	}
	var userMessage *string
	if len(match) > 4 {
		trimmed := strings.TrimSpace(match[4])
		if trimmed != "" {
			userMessage = &trimmed
		}
	}
	return &ParsedSkillBlock{
		Name:        match[1],
		Location:    match[2],
		Content:     match[3],
		UserMessage: userMessage,
	}
}

// ExtensionRunnerRef holds a mutable pointer to the active ExtensionRunner so
// Agent hooks and stream callbacks observe reloads cleanly without rebinding.
type ExtensionRunnerRef struct {
	mu      sync.RWMutex
	Current *ExtensionRunner
}

func (r *ExtensionRunnerRef) currentOrNil() *ExtensionRunner {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	runner := r.Current
	r.mu.RUnlock()
	return runner
}

// SetCurrent updates the active ExtensionRunner atomically.
func (r *ExtensionRunnerRef) SetCurrent(runner *ExtensionRunner) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.Current = runner
	r.mu.Unlock()
}

// ============================================================================
// AgentSessionEvent
// ============================================================================

// AgentSessionEvent is the sealed union of events emitted by AgentSession.Subscribe.
type AgentSessionEvent interface {
	isAgentSessionEvent()
	AgentSessionEventType() string
}

// AgentSessionEventAgentStart marks the beginning of an agent run.
type AgentSessionEventAgentStart struct {
	Type string `json:"type"`
}

func (*AgentSessionEventAgentStart) isAgentSessionEvent() {}
func (*AgentSessionEventAgentStart) AgentSessionEventType() string {
	return "agent_start"
}
func (e AgentSessionEventAgentStart) MarshalJSON() ([]byte, error) {
	type alias AgentSessionEventAgentStart
	if e.Type == "" {
		e.Type = "agent_start"
	}
	return jsonx.Marshal(alias(e))
}

// AgentSessionEventAgentEnd marks completion of an agent loop cycle, including
// whether AgentSession plans an automatic continuation (auto-retry / auto-compaction).
type AgentSessionEventAgentEnd struct {
	Type      string                 `json:"type"`
	Messages  agent.AgentMessageList `json:"messages"`
	WillRetry bool                   `json:"willRetry"`
}

func (*AgentSessionEventAgentEnd) isAgentSessionEvent() {}
func (*AgentSessionEventAgentEnd) AgentSessionEventType() string {
	return "agent_end"
}
func (e AgentSessionEventAgentEnd) MarshalJSON() ([]byte, error) {
	type alias AgentSessionEventAgentEnd
	if e.Type == "" {
		e.Type = "agent_end"
	}
	if e.Messages == nil {
		e.Messages = agent.AgentMessageList{}
	}
	return jsonx.Marshal(alias(e))
}

// AgentSessionEventTurnStart marks the start of one turn inside an agent run.
type AgentSessionEventTurnStart struct {
	Type string `json:"type"`
}

func (*AgentSessionEventTurnStart) isAgentSessionEvent() {}
func (*AgentSessionEventTurnStart) AgentSessionEventType() string {
	return "turn_start"
}
func (e AgentSessionEventTurnStart) MarshalJSON() ([]byte, error) {
	type alias AgentSessionEventTurnStart
	if e.Type == "" {
		e.Type = "turn_start"
	}
	return jsonx.Marshal(alias(e))
}

// AgentSessionEventTurnEnd marks the completion of one turn and its tool results.
type AgentSessionEventTurnEnd struct {
	Type        string                  `json:"type"`
	Message     agent.AgentMessage      `json:"message"`
	ToolResults []*ai.ToolResultMessage `json:"toolResults"`
}

func (*AgentSessionEventTurnEnd) isAgentSessionEvent() {}
func (*AgentSessionEventTurnEnd) AgentSessionEventType() string {
	return "turn_end"
}
func (e AgentSessionEventTurnEnd) MarshalJSON() ([]byte, error) {
	type alias AgentSessionEventTurnEnd
	if e.Type == "" {
		e.Type = "turn_end"
	}
	if e.ToolResults == nil {
		e.ToolResults = []*ai.ToolResultMessage{}
	}
	return jsonx.Marshal(alias(e))
}

// AgentSessionEventMessageStart marks the start of a message entering the run.
type AgentSessionEventMessageStart struct {
	Type    string             `json:"type"`
	Message agent.AgentMessage `json:"message"`
}

func (*AgentSessionEventMessageStart) isAgentSessionEvent() {}
func (*AgentSessionEventMessageStart) AgentSessionEventType() string {
	return "message_start"
}
func (e AgentSessionEventMessageStart) MarshalJSON() ([]byte, error) {
	type alias AgentSessionEventMessageStart
	if e.Type == "" {
		e.Type = "message_start"
	}
	return jsonx.Marshal(alias(e))
}

// AgentSessionEventMessageUpdate streams an assistant message update event.
type AgentSessionEventMessageUpdate struct {
	Type                  string                   `json:"type"`
	Message               agent.AgentMessage       `json:"message"`
	AssistantMessageEvent ai.AssistantMessageEvent `json:"assistantMessageEvent"`
}

func (*AgentSessionEventMessageUpdate) isAgentSessionEvent() {}
func (*AgentSessionEventMessageUpdate) AgentSessionEventType() string {
	return "message_update"
}
func (e AgentSessionEventMessageUpdate) MarshalJSON() ([]byte, error) {
	type alias AgentSessionEventMessageUpdate
	if e.Type == "" {
		e.Type = "message_update"
	}
	return jsonx.Marshal(alias(e))
}

// AgentSessionEventMessageEnd marks completion of a message in the run.
type AgentSessionEventMessageEnd struct {
	Type    string             `json:"type"`
	Message agent.AgentMessage `json:"message"`
}

func (*AgentSessionEventMessageEnd) isAgentSessionEvent() {}
func (*AgentSessionEventMessageEnd) AgentSessionEventType() string {
	return "message_end"
}
func (e AgentSessionEventMessageEnd) MarshalJSON() ([]byte, error) {
	type alias AgentSessionEventMessageEnd
	if e.Type == "" {
		e.Type = "message_end"
	}
	return jsonx.Marshal(alias(e))
}

// AgentSessionEventToolExecutionStart marks the start of a top-level or nested tool call.
type AgentSessionEventToolExecutionStart struct {
	Type             string  `json:"type"`
	ToolCallId       string  `json:"toolCallId"`
	ToolName         string  `json:"toolName"`
	Args             any     `json:"args"`
	ParentToolCallId *string `json:"parentToolCallId,omitzero"`
}

func (*AgentSessionEventToolExecutionStart) isAgentSessionEvent() {}
func (*AgentSessionEventToolExecutionStart) AgentSessionEventType() string {
	return "tool_execution_start"
}
func (e AgentSessionEventToolExecutionStart) MarshalJSON() ([]byte, error) {
	type alias AgentSessionEventToolExecutionStart
	if e.Type == "" {
		e.Type = "tool_execution_start"
	}
	return jsonx.Marshal(alias(e))
}

// AgentSessionEventToolExecutionUpdate streams partial output from a tool call.
type AgentSessionEventToolExecutionUpdate struct {
	Type             string  `json:"type"`
	ToolCallId       string  `json:"toolCallId"`
	ToolName         string  `json:"toolName"`
	Args             any     `json:"args"`
	PartialResult    any     `json:"partialResult"`
	ParentToolCallId *string `json:"parentToolCallId,omitzero"`
}

func (*AgentSessionEventToolExecutionUpdate) isAgentSessionEvent() {}
func (*AgentSessionEventToolExecutionUpdate) AgentSessionEventType() string {
	return "tool_execution_update"
}
func (e AgentSessionEventToolExecutionUpdate) MarshalJSON() ([]byte, error) {
	type alias AgentSessionEventToolExecutionUpdate
	if e.Type == "" {
		e.Type = "tool_execution_update"
	}
	return jsonx.Marshal(alias(e))
}

// AgentSessionEventToolExecutionEnd marks completion of a top-level or nested tool call.
type AgentSessionEventToolExecutionEnd struct {
	Type             string  `json:"type"`
	ToolCallId       string  `json:"toolCallId"`
	ToolName         string  `json:"toolName"`
	Result           any     `json:"result"`
	IsError          bool    `json:"isError"`
	DurationMs       *int64  `json:"durationMs,omitzero"`
	ParentToolCallId *string `json:"parentToolCallId,omitzero"`
}

func (*AgentSessionEventToolExecutionEnd) isAgentSessionEvent() {}
func (*AgentSessionEventToolExecutionEnd) AgentSessionEventType() string {
	return "tool_execution_end"
}
func (e AgentSessionEventToolExecutionEnd) MarshalJSON() ([]byte, error) {
	type alias AgentSessionEventToolExecutionEnd
	if e.Type == "" {
		e.Type = "tool_execution_end"
	}
	return jsonx.Marshal(alias(e))
}

// AgentSessionEventAgentSettled fires after a run and its continuations settle.
type AgentSessionEventAgentSettled struct {
	Type    string `json:"type"`
	Aborted bool   `json:"aborted"`
}

func (*AgentSessionEventAgentSettled) isAgentSessionEvent() {}
func (*AgentSessionEventAgentSettled) AgentSessionEventType() string {
	return "agent_settled"
}
func (e AgentSessionEventAgentSettled) MarshalJSON() ([]byte, error) {
	type alias AgentSessionEventAgentSettled
	if e.Type == "" {
		e.Type = "agent_settled"
	}
	return jsonx.Marshal(alias(e))
}

// AgentSessionEventQueueUpdate reports updated steering and follow-up text queues.
type AgentSessionEventQueueUpdate struct {
	Type     string   `json:"type"`
	Steering []string `json:"steering"`
	FollowUp []string `json:"followUp"`
}

func (*AgentSessionEventQueueUpdate) isAgentSessionEvent() {}
func (*AgentSessionEventQueueUpdate) AgentSessionEventType() string {
	return "queue_update"
}
func (e AgentSessionEventQueueUpdate) MarshalJSON() ([]byte, error) {
	type alias AgentSessionEventQueueUpdate
	if e.Type == "" {
		e.Type = "queue_update"
	}
	if e.Steering == nil {
		e.Steering = []string{}
	}
	if e.FollowUp == nil {
		e.FollowUp = []string{}
	}
	return jsonx.Marshal(alias(e))
}

// AgentSessionEventCompactionStart announces compaction startup ("manual", "threshold", or "overflow").
type AgentSessionEventCompactionStart struct {
	Type   string `json:"type"`
	Reason string `json:"reason"`
}

func (*AgentSessionEventCompactionStart) isAgentSessionEvent() {}
func (*AgentSessionEventCompactionStart) AgentSessionEventType() string {
	return "compaction_start"
}
func (e AgentSessionEventCompactionStart) MarshalJSON() ([]byte, error) {
	type alias AgentSessionEventCompactionStart
	if e.Type == "" {
		e.Type = "compaction_start"
	}
	return jsonx.Marshal(alias(e))
}

// AgentSessionEventCompactionEnd announces completion or cancellation of compaction.
type AgentSessionEventCompactionEnd struct {
	Type         string            `json:"type"`
	Reason       string            `json:"reason"`
	Result       *CompactionResult `json:"result,omitzero"`
	Aborted      bool              `json:"aborted"`
	WillRetry    bool              `json:"willRetry"`
	ErrorMessage *string           `json:"errorMessage,omitzero"`
}

func (*AgentSessionEventCompactionEnd) isAgentSessionEvent() {}
func (*AgentSessionEventCompactionEnd) AgentSessionEventType() string {
	return "compaction_end"
}
func (e AgentSessionEventCompactionEnd) MarshalJSON() ([]byte, error) {
	type alias AgentSessionEventCompactionEnd
	if e.Type == "" {
		e.Type = "compaction_end"
	}
	return jsonx.Marshal(alias(e))
}

// AgentSessionEventEntryAppended announces a non-message session entry appended outside message_end.
type AgentSessionEventEntryAppended struct {
	Type  string       `json:"type"`
	Entry SessionEntry `json:"entry"`
}

func (*AgentSessionEventEntryAppended) isAgentSessionEvent() {}
func (*AgentSessionEventEntryAppended) AgentSessionEventType() string {
	return "entry_appended"
}
func (e AgentSessionEventEntryAppended) MarshalJSON() ([]byte, error) {
	type alias AgentSessionEventEntryAppended
	if e.Type == "" {
		e.Type = "entry_appended"
	}
	return jsonx.Marshal(alias(e))
}

// AgentSessionEventSessionInfoChanged announces a session display name update.
type AgentSessionEventSessionInfoChanged struct {
	Type string  `json:"type"`
	Name *string `json:"name,omitzero"`
}

func (*AgentSessionEventSessionInfoChanged) isAgentSessionEvent() {}
func (*AgentSessionEventSessionInfoChanged) AgentSessionEventType() string {
	return "session_info_changed"
}
func (e AgentSessionEventSessionInfoChanged) MarshalJSON() ([]byte, error) {
	type alias AgentSessionEventSessionInfoChanged
	if e.Type == "" {
		e.Type = "session_info_changed"
	}
	return jsonx.Marshal(alias(e))
}

// AgentSessionEventThinkingLevelChanged announces an effective thinking level change.
type AgentSessionEventThinkingLevelChanged struct {
	Type  string              `json:"type"`
	Level agent.ThinkingLevel `json:"level"`
}

func (*AgentSessionEventThinkingLevelChanged) isAgentSessionEvent() {}
func (*AgentSessionEventThinkingLevelChanged) AgentSessionEventType() string {
	return "thinking_level_changed"
}
func (e AgentSessionEventThinkingLevelChanged) MarshalJSON() ([]byte, error) {
	type alias AgentSessionEventThinkingLevelChanged
	if e.Type == "" {
		e.Type = "thinking_level_changed"
	}
	return jsonx.Marshal(alias(e))
}

// AgentSessionEventAutoRetryStart announces a scheduled automatic retry.
type AgentSessionEventAutoRetryStart struct {
	Type         string `json:"type"`
	Attempt      int    `json:"attempt"`
	MaxAttempts  int    `json:"maxAttempts"`
	DelayMs      int64  `json:"delayMs"`
	ErrorMessage string `json:"errorMessage"`
}

func (*AgentSessionEventAutoRetryStart) isAgentSessionEvent() {}
func (*AgentSessionEventAutoRetryStart) AgentSessionEventType() string {
	return "auto_retry_start"
}
func (e AgentSessionEventAutoRetryStart) MarshalJSON() ([]byte, error) {
	type alias AgentSessionEventAutoRetryStart
	if e.Type == "" {
		e.Type = "auto_retry_start"
	}
	return jsonx.Marshal(alias(e))
}

// AgentSessionEventAutoRetryEnd announces the outcome of an automatic retry sequence.
type AgentSessionEventAutoRetryEnd struct {
	Type       string  `json:"type"`
	Success    bool    `json:"success"`
	Attempt    int     `json:"attempt"`
	FinalError *string `json:"finalError,omitzero"`
}

func (*AgentSessionEventAutoRetryEnd) isAgentSessionEvent() {}
func (*AgentSessionEventAutoRetryEnd) AgentSessionEventType() string {
	return "auto_retry_end"
}
func (e AgentSessionEventAutoRetryEnd) MarshalJSON() ([]byte, error) {
	type alias AgentSessionEventAutoRetryEnd
	if e.Type == "" {
		e.Type = "auto_retry_end"
	}
	return jsonx.Marshal(alias(e))
}

// AgentSessionEventSummarizationRetryScheduled announces a retry scheduled for compaction or branch summarization.
type AgentSessionEventSummarizationRetryScheduled struct {
	Type         string `json:"type"`
	Attempt      int    `json:"attempt"`
	MaxAttempts  int    `json:"maxAttempts"`
	DelayMs      int64  `json:"delayMs"`
	ErrorMessage string `json:"errorMessage"`
}

func (*AgentSessionEventSummarizationRetryScheduled) isAgentSessionEvent() {}
func (*AgentSessionEventSummarizationRetryScheduled) AgentSessionEventType() string {
	return "summarization_retry_scheduled"
}
func (e AgentSessionEventSummarizationRetryScheduled) MarshalJSON() ([]byte, error) {
	type alias AgentSessionEventSummarizationRetryScheduled
	if e.Type == "" {
		e.Type = "summarization_retry_scheduled"
	}
	return jsonx.Marshal(alias(e))
}

// AgentSessionEventSummarizationRetryAttemptStart announces the start of a compaction or branch-summary retry attempt.
type AgentSessionEventSummarizationRetryAttemptStart struct {
	Type   string  `json:"type"`
	Source string  `json:"source"`
	Reason *string `json:"reason,omitzero"`
}

func (*AgentSessionEventSummarizationRetryAttemptStart) isAgentSessionEvent() {}
func (*AgentSessionEventSummarizationRetryAttemptStart) AgentSessionEventType() string {
	return "summarization_retry_attempt_start"
}
func (e AgentSessionEventSummarizationRetryAttemptStart) MarshalJSON() ([]byte, error) {
	type alias AgentSessionEventSummarizationRetryAttemptStart
	if e.Type == "" {
		e.Type = "summarization_retry_attempt_start"
	}
	return jsonx.Marshal(alias(e))
}

// AgentSessionEventSummarizationRetryFinished announces completion of a summarization retry attempt.
type AgentSessionEventSummarizationRetryFinished struct {
	Type string `json:"type"`
}

func (*AgentSessionEventSummarizationRetryFinished) isAgentSessionEvent() {}
func (*AgentSessionEventSummarizationRetryFinished) AgentSessionEventType() string {
	return "summarization_retry_finished"
}
func (e AgentSessionEventSummarizationRetryFinished) MarshalJSON() ([]byte, error) {
	type alias AgentSessionEventSummarizationRetryFinished
	if e.Type == "" {
		e.Type = "summarization_retry_finished"
	}
	return jsonx.Marshal(alias(e))
}

// AgentSessionEventBashExecutionUpdate streams incremental output chunks from ExecuteBash.
type AgentSessionEventBashExecutionUpdate struct {
	Type  string  `json:"type"`
	Id    *string `json:"id,omitzero"`
	Delta string  `json:"delta"`
}

func (*AgentSessionEventBashExecutionUpdate) isAgentSessionEvent() {}
func (*AgentSessionEventBashExecutionUpdate) AgentSessionEventType() string {
	return "bash_execution_update"
}
func (e AgentSessionEventBashExecutionUpdate) MarshalJSON() ([]byte, error) {
	type alias AgentSessionEventBashExecutionUpdate
	if e.Type == "" {
		e.Type = "bash_execution_update"
	}
	return jsonx.Marshal(alias(e))
}

// AgentSessionEventListener consumes AgentSession events.
type AgentSessionEventListener func(event AgentSessionEvent)

// ============================================================================
// Configuration & Public Types
// ============================================================================

// AgentSessionConfig configures NewAgentSession.
type AgentSessionConfig struct {
	Agent                  *agent.Agent
	SessionManager         *SessionManager
	SettingsManager        *SettingsManager
	Cwd                    string
	ScopedModels           []ScopedModel
	ResourceLoader         ResourceLoader
	CustomTools            []ToolDefinition
	ModelRuntime           *ModelRuntime
	ModelRegistry          *ModelRegistry
	CacheWarmer            *CacheWarmer
	InitialActiveToolNames []string
	UsesDefaultTools       bool
	DefaultToolModifiers   []string
	AllowedToolNames       []string
	ExcludedToolNames      []string
	SuppressBuiltinTools   bool
	BaseToolsOverride      map[string]*agent.AgentTool
	ExtensionRunnerRef     *ExtensionRunnerRef
	SessionStartEvent      *SessionStartEvent
	FlagValues             map[string]any
}

// ShutdownHandler is invoked when extensions request host shutdown.
type ShutdownHandler func()

// ExtensionBindings configures host callbacks when binding extensions/modes.
type ExtensionBindings struct {
	UIContext             ExtensionUIContext
	Mode                  ExtensionMode
	CommandContextActions *ExtensionCommandContextActions
	AbortHandler          func()
	ShutdownHandler       ShutdownHandler
	OnError               ExtensionErrorListener
}

// QueuedInputDisposition reports how Steer or FollowUp resolved an input.
type QueuedInputDisposition string

const (
	QueuedInputDispositionHandled QueuedInputDisposition = "handled"
	QueuedInputDispositionQueued  QueuedInputDisposition = "queued"
)

// PromptDisposition reports how Prompt dispatched an accepted input.
type PromptDisposition string

const (
	PromptDispositionHandled PromptDisposition = "handled"
	PromptDispositionQueued  PromptDisposition = "queued"
	PromptDispositionStarted PromptDisposition = "started"
)

// PromptOptions configures AgentSession.Prompt.
type PromptOptions struct {
	ExpandPromptTemplates *bool
	Images                []*ai.ImageContent
	StreamingBehavior     string // "steer" | "followUp"
	Source                InputSource
	PreflightResult       func(disposition PromptDisposition)
}

// QueueInputOptions configures AgentSession.Steer and AgentSession.FollowUp.
type QueueInputOptions struct {
	Source InputSource
}

// SendCustomMessageOptions configures AgentSession.SendCustomMessage.
type SendCustomMessageOptions struct {
	TriggerTurn bool
	DeliverAs   string // "steer" | "followUp" | "nextTurn"
}

// ModelMutationOptions configures SetModel and SetThinkingLevel persistence.
type ModelMutationOptions struct {
	Persist bool
}

// ModelCycleResult describes the result of CycleModel.
type ModelCycleResult struct {
	Model         *ai.Model           `json:"model"`
	ThinkingLevel agent.ThinkingLevel `json:"thinkingLevel"`
	IsScoped      bool                `json:"isScoped"`
}

// RoutedModelResult reports the physical model and thinking level of the latest
// assistant turn under a virtual model selection.
type RoutedModelResult struct {
	Model         *ai.Model            `json:"model"`
	ThinkingLevel *agent.ThinkingLevel `json:"thinkingLevel,omitzero"`
}

// SessionStatsTokens aggregates token counters across assistant turns.
type SessionStatsTokens struct {
	Input      int `json:"input"`
	Output     int `json:"output"`
	CacheRead  int `json:"cacheRead"`
	CacheWrite int `json:"cacheWrite"`
	Total      int `json:"total"`
}

// SessionStats aggregates session counters and context usage for UI/RPC reporting.
type SessionStats struct {
	SessionFile       *string            `json:"sessionFile,omitzero"`
	SessionId         string             `json:"sessionId"`
	UserMessages      int                `json:"userMessages"`
	AssistantMessages int                `json:"assistantMessages"`
	ToolCalls         int                `json:"toolCalls"`
	ToolResults       int                `json:"toolResults"`
	TotalMessages     int                `json:"totalMessages"`
	Tokens            SessionStatsTokens `json:"tokens"`
	Cost              float64            `json:"cost"`
	ContextUsage      *ContextUsage      `json:"contextUsage,omitzero"`
}

// QueuedMessagesText holds copies of queued steering and follow-up texts.
type QueuedMessagesText struct {
	Steering []string `json:"steering"`
	FollowUp []string `json:"followUp"`
}

type as1EventListenerEntry struct {
	id uint64
	fn AgentSessionEventListener
}

type as1ToolDefinitionEntry struct {
	definition ToolDefinition
	sourceInfo utils.SourceInfo
}

type as1RequestAuth struct {
	model   *ai.Model
	apiKey  *string
	headers map[string]string
	env     ai.ProviderEnv
}

type as1SummarizationAuth struct {
	as1RequestAuth
	thinkingLevel agent.ThinkingLevel
}

// ============================================================================
// AgentSession
// ============================================================================

// AgentSession orchestrates an agent.Agent, SessionManager, SettingsManager,
// ModelRuntime, ResourceLoader, CacheWarmer, compaction, retries, and tools.
type AgentSession struct {
	Agent           *agent.Agent
	SessionManager  *SessionManager
	SettingsManager *SettingsManager
	FlagValues      map[string]any

	mu sync.Mutex

	scopedModels    []ScopedModel
	resourceLoader  ResourceLoader
	customTools     []ToolDefinition
	cwd             string
	modelRuntime    *ModelRuntime
	modelRegistry   *ModelRegistry
	cacheWarmer     *CacheWarmer
	sessionStartEv  SessionStartEvent
	extensionRunRef *ExtensionRunnerRef
	extensionRunner *ExtensionRunner
	extUIContext    ExtensionUIContext
	extMode         ExtensionMode
	extCmdActions   *ExtensionCommandContextActions
	extAbortHandler func()
	extShutdown     ShutdownHandler
	extErrListener  ExtensionErrorListener
	extErrUnsub     func()

	// Subscription & run lifecycle
	unsubscribeAgent       func()
	nextListenerID         uint64
	eventListeners         []as1EventListenerEntry
	isAgentRunActive       bool
	promptPreflightCount   int
	agentRunAbortRequested bool
	idleCh                 chan struct{}

	// Queues & pending context
	steeringMessages        []string
	followUpMessages        []string
	queuedMessagesWithImage map[any]struct{}
	pendingNextTurnMessages []*CustomMessage
	pendingCustomMessages   []*CustomMessage

	// Compaction / branch summary / retry / bash active cancellations
	compactionCancel        context.CancelFunc
	compactionToken         uint64
	autoCompactionCancel    context.CancelFunc
	autoCompactionToken     uint64
	overflowRecoveryAttempt bool
	branchSummaryCancel     context.CancelFunc
	branchSummaryToken      uint64
	retryCancel             context.CancelFunc
	retryToken              uint64
	retryAttempt            int
	failedResponse          *ai.AssistantMessage
	nextBashId              uint64
	bashCancels             map[uint64]context.CancelFunc
	bashRecordMu            sync.Mutex
	pendingBashMessages     []*BashExecutionMessage

	// Extension/boundary tracking
	turnIndex                  int
	entryIdsByMessage          map[any]string
	boundaryDispatchedMessages map[any]struct{}
	lastAssistantMessage       *ai.AssistantMessage
	lastActivityOutcome        string
	isBeforeSettle             bool
	abortDuringBeforeSettle    bool
	isEmittingAgentSettled     bool
	deferredSettledActions     []func(context.Context) error

	// Tool registry & system prompt state
	initialActiveToolNames []string
	pendingToolNames       map[string]struct{}
	pendingToolOrder       []string
	restoredToolNames      map[string]struct{}
	usesDefaultTools       bool
	defaultToolModifiers   []string
	allowedTools           func(name string) bool
	allowlistFiltersMcp    bool
	excludedTools          func(name string) bool
	suppressBuiltinTools   bool
	baseToolsOverride      map[string]*agent.AgentTool
	baseToolDefinitions    map[string]ToolDefinition
	baseToolOrder          []string
	toolRegistry           map[string]*agent.AgentTool
	toolRegistryOrder      []string
	nestedToolCalls        *NestedToolCallRunner
	hiddenDeclarations     map[string]struct{}
	toolDefinitions        map[string]as1ToolDefinitionEntry
	toolPromptSnippets     map[string]string
	toolPromptGuidelines   map[string][]string
	baseSystemPromptOpts   NormalizedBuildSystemPromptOptions
	runSystemPromptOpts    *NormalizedBuildSystemPromptOptions
}

// NewAgentSession constructs an AgentSession, attaches agent event listeners and
// loop hooks, builds the initial tool registry/system prompt, and restores any
// transcript tool loadout when using defaultTools.
func NewAgentSession(config AgentSessionConfig) *AgentSession {
	modelRuntime := config.ModelRuntime
	modelRegistry := config.ModelRegistry
	if modelRuntime == nil && modelRegistry != nil {
		modelRuntime = modelRegistry.GetRuntime()
	}
	if modelRegistry == nil && modelRuntime != nil {
		modelRegistry = NewModelRegistry(modelRuntime)
	}

	sessionStart := SessionStartEvent{Type: "session_start", Reason: "startup"}
	if config.SessionStartEvent != nil {
		sessionStart = *config.SessionStartEvent
	}

	s := &AgentSession{
		Agent:                      config.Agent,
		SessionManager:             config.SessionManager,
		SettingsManager:            config.SettingsManager,
		FlagValues:                 assCloneFlagValues(config.FlagValues),
		scopedModels:               slices.Clone(config.ScopedModels),
		resourceLoader:             config.ResourceLoader,
		customTools:                slices.Clone(config.CustomTools),
		cwd:                        config.Cwd,
		modelRuntime:               modelRuntime,
		modelRegistry:              modelRegistry,
		cacheWarmer:                config.CacheWarmer,
		sessionStartEv:             sessionStart,
		extensionRunRef:            config.ExtensionRunnerRef,
		extMode:                    ExtensionModePrint,
		initialActiveToolNames:     slices.Clone(config.InitialActiveToolNames),
		usesDefaultTools:           config.UsesDefaultTools,
		defaultToolModifiers:       slices.Clone(config.DefaultToolModifiers),
		suppressBuiltinTools:       config.SuppressBuiltinTools,
		baseToolsOverride:          config.BaseToolsOverride,
		lastActivityOutcome:        "completed",
		queuedMessagesWithImage:    map[any]struct{}{},
		bashCancels:                map[uint64]context.CancelFunc{},
		entryIdsByMessage:          map[any]string{},
		boundaryDispatchedMessages: map[any]struct{}{},
		pendingToolNames:           map[string]struct{}{},
		restoredToolNames:          map[string]struct{}{},
		baseToolDefinitions:        map[string]ToolDefinition{},
		toolRegistry:               map[string]*agent.AgentTool{},
		hiddenDeclarations:         map[string]struct{}{},
		toolDefinitions:            map[string]as1ToolDefinitionEntry{},
		toolPromptSnippets:         map[string]string{},
		toolPromptGuidelines:       map[string][]string{},
	}

	if config.AllowedToolNames != nil {
		s.allowedTools = CreateToolNameMatcher(config.AllowedToolNames)
		s.allowlistFiltersMcp = len(config.AllowedToolNames) == 0 ||
			slices.ContainsFunc(config.AllowedToolNames, func(entry string) bool {
				return strings.HasPrefix(entry, "mcp__")
			})
	}
	if config.ExcludedToolNames != nil {
		s.excludedTools = CreateToolNameMatcher(config.ExcludedToolNames)
	}

	if s.cacheWarmer != nil {
		s.cacheWarmer.OnWarmed = func(entry *UsageEntry) {
			if entry != nil {
				s.as1Emit(&AgentSessionEventEntryAppended{
					Type:  "entry_appended",
					Entry: entry,
				})
			}
		}
	}

	if s.Agent != nil {
		s.unsubscribeAgent = s.Agent.Subscribe(s.as1HandleAgentEvent)
		s.as1InstallAgentToolHooks()
		s.as1InstallAgentNextTurnRefresh()
		s.as1InstallAgentRequestProjection()
		s.as1InstallAgentBoundaryHooks()
		s.as1InstallHiddenDeclarationsProjection()
		s.as1InstallAgentForcedPromptProjection()
	}

	s.as3BuildRuntime(as3BuildRuntimeOptions{
		ActiveToolNames:          s.initialActiveToolNames,
		IncludeAllExtensionTools: true,
	})
	if s.usesDefaultTools {
		s.as3RestoreToolsFromTranscript(s.defaultToolModifiers)
	}

	return s
}

func (s *AgentSession) currentExtensionRunner() *ExtensionRunner {
	s.mu.Lock()
	runner := s.extensionRunner
	ref := s.extensionRunRef
	s.mu.Unlock()
	if runner != nil {
		return runner
	}
	return ref.currentOrNil()
}

// ============================================================================
// Read-only Accessors
// ============================================================================

// Cwd returns the session working directory.
func (s *AgentSession) Cwd() string { return s.cwd }

// ModelRuntime returns the canonical model/auth runtime associated with the session.
func (s *AgentSession) ModelRuntime() *ModelRuntime { return s.modelRuntime }

// ModelRegistry returns the ModelRegistry compatibility facade for this session.
func (s *AgentSession) ModelRegistry() *ModelRegistry { return s.modelRegistry }

// ResourceLoader returns the session's resource loader.
func (s *AgentSession) ResourceLoader() ResourceLoader { return s.resourceLoader }

// ExtensionRunner returns the currently bound ExtensionRunner, if any.
func (s *AgentSession) ExtensionRunner() *ExtensionRunner { return s.currentExtensionRunner() }

// State returns a snapshot of the underlying agent state.
func (s *AgentSession) State() agent.AgentState {
	if s.Agent == nil {
		return agent.AgentState{}
	}
	return s.Agent.State()
}

// Messages returns the finalized messages currently in agent state.
func (s *AgentSession) Messages() agent.AgentMessageList {
	if s.Agent == nil {
		return nil
	}
	return s.Agent.Messages()
}

// SystemPrompt returns the effective system prompt for the current session or active run.
func (s *AgentSession) SystemPrompt() string {
	s.mu.Lock()
	opts := s.baseSystemPromptOpts
	if s.runSystemPromptOpts != nil {
		opts = *s.runSystemPromptOpts
	}
	s.mu.Unlock()
	prompt, _ := BuildSystemPrompt(as1NormalizedToBuildSystemPromptOptions(opts))
	return prompt
}

// CacheWarmingStatus returns current prompt-cache warming state, if a CacheWarmer is attached.
func (s *AgentSession) CacheWarmingStatus() *CacheWarmingStatus {
	if s.cacheWarmer == nil {
		return nil
	}
	st := s.cacheWarmer.Status()
	return &st
}

// SetCacheWarmingMode persists cache-warming mode and immediately notifies the warmer.
func (s *AgentSession) SetCacheWarmingMode(mode CacheWarmingMode) error {
	if s.SettingsManager != nil {
		s.SettingsManager.SetCacheWarmingMode(mode)
	}
	if s.cacheWarmer != nil {
		s.cacheWarmer.OnModeChanged()
	}
	return nil
}

// Model returns the currently selected model, or nil if none is set.
func (s *AgentSession) Model() *ai.Model {
	if s.Agent == nil {
		return nil
	}
	return s.Agent.Model()
}

// ThinkingLevel returns the currently configured thinking level.
func (s *AgentSession) ThinkingLevel() agent.ThinkingLevel {
	if s.Agent == nil {
		return ""
	}
	return s.Agent.ThinkingLevel()
}

// RoutedModel returns the physical model and thinking level of the most recent
// assistant response when the active selection is a virtual model.
func (s *AgentSession) RoutedModel() *RoutedModelResult {
	model := s.Model()
	if model == nil || !IsVirtualModel(model) || s.modelRuntime == nil {
		return nil
	}
	latest := FindLatestResponse(s.Messages())
	if latest == nil {
		return nil
	}
	physical := s.modelRuntime.GetPhysicalModel(string(latest.Provider), latest.Model)
	if physical == nil {
		return nil
	}
	return &RoutedModelResult{
		Model:         physical,
		ThinkingLevel: latest.ThinkingLevel,
	}
}

// IsStreaming reports whether an agent run or post-run continuation is active.
func (s *AgentSession) IsStreaming() bool {
	s.mu.Lock()
	active := s.isAgentRunActive
	s.mu.Unlock()
	return active
}

// IsCompacting reports whether manual, threshold, overflow compaction, or branch summarization is active.
func (s *AgentSession) IsCompacting() bool {
	s.mu.Lock()
	active := s.compactionCancel != nil || s.autoCompactionCancel != nil || s.branchSummaryCancel != nil
	s.mu.Unlock()
	return active
}

// IsRetrying reports whether an auto-retry attempt or delay wait is active.
func (s *AgentSession) IsRetrying() bool {
	s.mu.Lock()
	active := s.retryCancel != nil
	s.mu.Unlock()
	return active
}

// IsIdle reports whether no agent run or compaction is active.
func (s *AgentSession) IsIdle() bool {
	s.mu.Lock()
	idle := s.as1IsIdleLocked()
	s.mu.Unlock()
	return idle
}

func (s *AgentSession) as1IsIdleLocked() bool {
	return !s.isAgentRunActive && s.compactionCancel == nil && s.autoCompactionCancel == nil
}

// RetryAttempt returns the current auto-retry attempt counter (0 when idle).
func (s *AgentSession) RetryAttempt() int {
	s.mu.Lock()
	attempt := s.retryAttempt
	s.mu.Unlock()
	return attempt
}

// SteeringMode returns the current agent steering queue mode.
func (s *AgentSession) SteeringMode() agent.QueueMode {
	if s.Agent == nil {
		return agent.QueueModeOneAtATime
	}
	return s.Agent.SteeringMode()
}

// FollowUpMode returns the current agent follow-up queue mode.
func (s *AgentSession) FollowUpMode() agent.QueueMode {
	if s.Agent == nil {
		return agent.QueueModeOneAtATime
	}
	return s.Agent.FollowUpMode()
}

// SessionFile returns the active JSONL file path, or nil when running in-memory.
func (s *AgentSession) SessionFile() *string {
	if s.SessionManager == nil {
		return nil
	}
	return s.SessionManager.GetSessionFile()
}

// SessionId returns the active session UUID.
func (s *AgentSession) SessionId() string {
	if s.SessionManager == nil {
		return ""
	}
	return s.SessionManager.GetSessionId()
}

// SessionName returns the active session display name, if set.
func (s *AgentSession) SessionName() *string {
	if s.SessionManager == nil {
		return nil
	}
	return s.SessionManager.GetSessionName()
}

// ScopedModels returns a copy of the configured model cycling scope.
func (s *AgentSession) ScopedModels() []ScopedModel {
	s.mu.Lock()
	out := slices.Clone(s.scopedModels)
	s.mu.Unlock()
	return out
}

// SetScopedModels replaces the configured model cycling scope.
func (s *AgentSession) SetScopedModels(scopedModels []ScopedModel) {
	s.mu.Lock()
	s.scopedModels = slices.Clone(scopedModels)
	s.mu.Unlock()
}

// PromptTemplates returns the file-based prompt templates loaded by ResourceLoader.
func (s *AgentSession) PromptTemplates() []PromptTemplate {
	if s.resourceLoader == nil {
		return nil
	}
	return slices.Clone(s.resourceLoader.GetPrompts().Templates)
}

// RefreshContext reloads Agent state messages from SessionManager's canonical projection.
func (s *AgentSession) RefreshContext() {
	s.as1RefreshFinalizedContext()
}

// HasExtensionHandlers reports whether the active extension runner has handlers for eventType.
func (s *AgentSession) HasExtensionHandlers(eventType string) bool {
	runner := s.currentExtensionRunner()
	return runner != nil && runner.HasHandlers(eventType)
}

// ============================================================================
// Runtime Auth & Model Recording Helpers
// ============================================================================

func as1WithoutDeletedHeaders(headers ai.ProviderHeaders) map[string]string {
	if headers == nil {
		return nil
	}
	out := map[string]string{}
	for k, v := range headers.All() {
		if v != nil {
			out[k] = *v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (s *AgentSession) as1LimitsModel() *ai.Model {
	if routed := s.RoutedModel(); routed != nil && routed.Model != nil {
		return routed.Model
	}
	return s.Model()
}

func (s *AgentSession) as1ModelForMessage(msg *ai.AssistantMessage) *ai.Model {
	if msg == nil {
		return nil
	}
	model := s.Model()
	if model != nil && IsVirtualModel(model) && s.modelRuntime != nil {
		return s.modelRuntime.GetPhysicalModel(string(msg.Provider), msg.Model)
	}
	if model != nil && model.Provider == msg.Provider && model.Id == msg.Model {
		return model
	}
	return nil
}

func (s *AgentSession) as1GetRequiredRequestAuth(ctx context.Context, model *ai.Model) (*as1RequestAuth, error) {
	if model == nil {
		return nil, errors.New(FormatNoModelSelectedMessage())
	}
	if s.modelRuntime == nil {
		return nil, errors.New(FormatNoApiKeyFoundMessage(string(model.Provider)))
	}
	result, err := s.modelRuntime.GetAuthModel(ctx, model, nil)
	if err != nil {
		if strings.Contains(err.Error(), "authHeader requires a resolved API key") {
			return nil, errors.New(FormatNoApiKeyFoundMessage(string(model.Provider)))
		}
		return nil, err
	}
	if result != nil && (result.Auth.ApiKey != nil && *result.Auth.ApiKey != "" || (result.Auth.Headers != nil && result.Auth.Headers.Len() > 0)) {
		reqModel := model
		if result.Auth.BaseUrl != nil && *result.Auth.BaseUrl != "" {
			cloned := *model
			cloned.BaseUrl = *result.Auth.BaseUrl
			reqModel = &cloned
		}
		return &as1RequestAuth{
			model:   reqModel,
			apiKey:  result.Auth.ApiKey,
			headers: as1WithoutDeletedHeaders(result.Auth.Headers),
			env:     result.Env,
		}, nil
	}
	if s.modelRuntime.IsUsingOAuth(string(model.Provider)) {
		return nil, fmt.Errorf(
			"Authentication failed for %q. Credentials may have expired or network is unavailable. Run '/login %s' to re-authenticate.",
			model.Provider,
			model.Provider,
		)
	}
	return nil, errors.New(FormatNoApiKeyFoundMessage(string(model.Provider)))
}

func (s *AgentSession) as1GetSummarizationRequestAuth(ctx context.Context, selectedModel *ai.Model) (*as1SummarizationAuth, error) {
	if selectedModel == nil {
		return nil, errors.New(FormatNoModelSelectedMessage())
	}
	model := selectedModel
	thinkingLevel := s.ThinkingLevel()
	if IsVirtualModel(selectedModel) && s.modelRuntime != nil {
		route, err := s.modelRuntime.ResolveModel(ctx, selectedModel, ConvertToLlm(s.Messages()), ResolveModelOptions{
			Reason:        "direct",
			ThinkingLevel: thinkingLevel,
		})
		if err != nil {
			return nil, err
		}
		if route.Model != nil {
			model = route.Model
			thinkingLevel = route.ThinkingLevel
		}
	}

	var streamFn agent.StreamFn
	if s.Agent != nil {
		streamFn = s.Agent.StreamFn()
	}
	if streamFn == nil || as1IsDefaultStreamFn(streamFn) {
		auth, err := s.as1GetRequiredRequestAuth(ctx, model)
		if err != nil {
			return nil, err
		}
		return &as1SummarizationAuth{as1RequestAuth: *auth, thinkingLevel: thinkingLevel}, nil
	}
	if as1IsRuntimeStreamFunction(streamFn, s.modelRuntime) {
		return &as1SummarizationAuth{
			as1RequestAuth: as1RequestAuth{model: model},
			thinkingLevel:  thinkingLevel,
		}, nil
	}
	if s.modelRuntime == nil {
		return &as1SummarizationAuth{
			as1RequestAuth: as1RequestAuth{model: model},
			thinkingLevel:  thinkingLevel,
		}, nil
	}

	result, err := s.modelRuntime.GetAuthModel(ctx, model, nil)
	if err != nil {
		if ctx != nil && ctx.Err() != nil {
			return nil, err
		}
		return &as1SummarizationAuth{
			as1RequestAuth: as1RequestAuth{model: model},
			thinkingLevel:  thinkingLevel,
		}, nil
	}
	if result == nil {
		return &as1SummarizationAuth{
			as1RequestAuth: as1RequestAuth{model: model},
			thinkingLevel:  thinkingLevel,
		}, nil
	}
	reqModel := model
	if result.Auth.BaseUrl != nil && *result.Auth.BaseUrl != "" {
		cloned := *model
		cloned.BaseUrl = *result.Auth.BaseUrl
		reqModel = &cloned
	}
	return &as1SummarizationAuth{
		as1RequestAuth: as1RequestAuth{
			model:   reqModel,
			apiKey:  result.Auth.ApiKey,
			headers: as1WithoutDeletedHeaders(result.Auth.Headers),
			env:     result.Env,
		},
		thinkingLevel: thinkingLevel,
	}, nil
}

func (s *AgentSession) as1RecordSelection() {
	if s.SessionManager == nil {
		return
	}
	branch := s.SessionManager.GetBranch()
	s.as1RecordModelSelection(branch)
	s.as1RecordThinkingLevel(branch)
}

func (s *AgentSession) as1RecordModelSelection(branch []SessionEntry) {
	model := s.Model()
	if model == nil || s.SessionManager == nil {
		return
	}
	var getModelFn func(provider, modelId string) *ai.Model
	if mr := s.ModelRuntime(); mr != nil {
		getModelFn = mr.GetModel
	}
	recorded := GetBranchSelection(branch, getModelFn)
	if recorded == nil || (recorded.Provider == string(model.Provider) && recorded.ModelId == model.Id) {
		return
	}
	var recordedModel *ai.Model
	if getModelFn != nil {
		recordedModel = getModelFn(recorded.Provider, recorded.ModelId)
	}
	if !IsVirtualModel(model) && (recordedModel == nil || !IsVirtualModel(recordedModel)) {
		return
	}
	_, _ = s.SessionManager.AppendModelChange(string(model.Provider), model.Id)
}

func (s *AgentSession) as1RecordThinkingLevel(branch []SessionEntry) {
	if s.SessionManager == nil {
		return
	}
	var recorded agent.ThinkingLevel
	found := false
	for i := len(branch) - 1; i >= 0; i-- {
		if tl, ok := branch[i].(*ThinkingLevelChangeEntry); ok {
			recorded = agent.ThinkingLevel(tl.ThinkingLevel)
			found = true
			break
		}
	}
	if !found {
		return
	}
	available := s.GetAvailableThinkingLevels()
	effective := recorded
	if !slices.Contains(available, recorded) {
		effective = s.as2ClampThinkingLevel(recorded, available)
	}
	if effective == s.ThinkingLevel() {
		return
	}
	_, _ = s.SessionManager.AppendThinkingLevelChange(string(s.ThinkingLevel()))
}

// ============================================================================
// Agent Hooks
// ============================================================================

func (s *AgentSession) as1InstallAgentToolHooks() {
	s.Agent.SetBeforeToolCall(func(ctx context.Context, tc *agent.BeforeToolCallContext) (*agent.BeforeToolCallResult, error) {
		return s.as1BeforeToolCall(ctx, tc, nil)
	})
	s.Agent.SetAfterToolCall(func(ctx context.Context, tc *agent.AfterToolCallContext) (*agent.AfterToolCallResult, error) {
		return s.as1AfterToolCall(ctx, tc, nil)
	})
}

func as1AnyToJsonObject(v any) *jsonx.Object {
	if v == nil {
		return nil
	}
	switch obj := v.(type) {
	case *jsonx.Object:
		return obj
	case jsonx.Object:
		cp := obj
		return &cp
	case map[string]any:
		out := jsonx.NewObject()
		for k, val := range obj {
			out.Set(k, val)
		}
		return out
	default:
		if raw, err := jsonx.Marshal(v); err == nil {
			var decoded jsonx.Object
			if err := jsonx.Unmarshal(raw, &decoded); err == nil {
				return &decoded
			}
		}
		return nil
	}
}

func (s *AgentSession) as1BeforeToolCall(ctx context.Context, tc *agent.BeforeToolCallContext, parentToolCallId *string) (*agent.BeforeToolCallResult, error) {
	if tc == nil || tc.ToolCall == nil {
		return nil, nil
	}
	runner := s.currentExtensionRunner()
	if runner == nil || !runner.HasHandlers("tool_call") {
		return nil, nil
	}
	res := runner.EmitToolCall(ctx, ToolCallEvent{
		Type:             "tool_call",
		ToolName:         tc.ToolCall.Name,
		ToolCallId:       tc.ToolCall.Id,
		ParentToolCallId: parentToolCallId,
		Input:            as1AnyToJsonObject(tc.Args),
	})
	if res == nil {
		return nil, nil
	}
	var reason string
	if res.Reason != nil {
		reason = *res.Reason
	}
	return &agent.BeforeToolCallResult{
		Block:     res.Block != nil && *res.Block,
		Reason:    reason,
		Terminate: res.Terminate != nil && *res.Terminate,
	}, nil
}

func (s *AgentSession) as1AfterToolCall(ctx context.Context, tc *agent.AfterToolCallContext, parentToolCallId *string) (*agent.AfterToolCallResult, error) {
	if tc == nil || tc.ToolCall == nil {
		return nil, nil
	}
	var resultContent ai.ContentList
	var resultDetails any
	var resultStructured any
	var resultUsage *ai.Usage
	if tc.Result != nil {
		resultContent = tc.Result.Content
		resultDetails = tc.Result.Details
		resultStructured = tc.Result.StructuredContent
		resultUsage = tc.Result.Usage
	}

	var hookResult *ToolResultEventResult
	runner := s.currentExtensionRunner()
	if runner != nil && runner.HasHandlers("tool_result") {
		hookResult = runner.EmitToolResult(ctx, ToolResultEvent{
			Type:              "tool_result",
			ToolName:          tc.ToolCall.Name,
			ToolCallId:        tc.ToolCall.Id,
			ParentToolCallId:  parentToolCallId,
			Input:             as1AnyToJsonObject(tc.Args),
			Content:           resultContent,
			Details:           resultDetails,
			StructuredContent: resultStructured,
			IsError:           tc.IsError,
			Usage:             resultUsage,
		})
	}

	content := resultContent
	if hookResult != nil && hookResult.Content != nil {
		content = hookResult.Content
	}
	var resizeOpts *utils.ImageResizeOptions
	if lm := s.as1LimitsModel(); lm != nil && lm.InputLimits != nil && lm.InputLimits.Images != nil {
		resizeOpts = lm.InputLimits.Images.Resize
	}
	autoResize := true
	if s.SettingsManager != nil {
		autoResize = s.SettingsManager.GetImageAutoResize()
	}
	normalized := utils.NormalizeToolResultImages(content, &utils.NormalizeToolResultImagesOptions{
		AutoResizeImages: &autoResize,
		Resize:           resizeOpts,
	})

	changed := hookResult != nil || !as1ContentSliceEqual(normalized, content)
	if !changed {
		return nil, nil
	}

	out := &agent.AfterToolCallResult{
		Content: normalized,
	}
	if hookResult != nil {
		if hookResult.Details != nil {
			out.Details = jsonx.Some(hookResult.Details)
		}
		if hookResult.StructuredContent != nil {
			out.StructuredContent = jsonx.Some(hookResult.StructuredContent)
		}
		out.IsError = hookResult.IsError
		out.Usage = hookResult.Usage
	}
	return out, nil
}

func as1ContentSliceEqual(a, b ai.ContentList) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

type as1NestedHost struct {
	session *AgentSession
}

func (h *as1NestedHost) GetTools() []*agent.AgentTool {
	return h.session.as3GetCallableTools()
}

func (h *as1NestedHost) IsSequential() bool {
	return h.session.Agent != nil && h.session.Agent.ToolExecution() == agent.ToolExecutionModeSequential
}

func (h *as1NestedHost) RunToolCall(
	ctx context.Context,
	toolCall *agent.AgentToolCall,
	parentToolCallId string,
	onUpdate func(ctx context.Context, partialResult agent.AgentToolResult) error,
) (agent.AgentToolCallOutcome, error) {
	assistantMsg := h.session.as1FindLastAssistantMessage()
	if assistantMsg == nil {
		return agent.AgentToolCallOutcome{
			ToolCall: toolCall,
			Result: agent.AgentToolResult{
				Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "No assistant message issued this call"}},
			},
			IsError: true,
		}, nil
	}
	state := h.session.State()
	var updateCb agent.AgentToolUpdateCallback
	if onUpdate != nil {
		updateCb = func(partial agent.AgentToolResult) {
			_ = onUpdate(ctx, partial)
		}
	}
	return agent.RunToolCall(ctx, toolCall, &agent.RunToolCallOptions{
		Tools:            h.session.as3GetCallableTools(),
		AssistantMessage: assistantMsg,
		Context:          &agent.AgentContext{Messages: state.Messages, Tools: state.Tools},
		OnUpdate:         updateCb,
		ToolCallHooks: agent.ToolCallHooks{
			BeforeToolCall: func(bCtx context.Context, tc *agent.BeforeToolCallContext) (*agent.BeforeToolCallResult, error) {
				p := parentToolCallId
				return h.session.as1BeforeToolCall(bCtx, tc, &p)
			},
			AfterToolCall: func(aCtx context.Context, tc *agent.AfterToolCallContext) (*agent.AfterToolCallResult, error) {
				p := parentToolCallId
				return h.session.as1AfterToolCall(aCtx, tc, &p)
			},
		},
	}), nil
}

func (h *as1NestedHost) Emit(ctx context.Context, ev NestedToolExecutionEvent) error {
	if ext := h.session.currentExtensionRunner(); ext != nil {
		_ = ext.Emit(ctx, ev)
	}
	switch e := ev.(type) {
	case *ToolExecutionStartEvent:
		h.session.as1Emit(&AgentSessionEventToolExecutionStart{
			Type:             "tool_execution_start",
			ToolCallId:       e.ToolCallId,
			ToolName:         e.ToolName,
			Args:             e.Args,
			ParentToolCallId: e.ParentToolCallId,
		})
	case *ToolExecutionUpdateEvent:
		h.session.as1Emit(&AgentSessionEventToolExecutionUpdate{
			Type:             "tool_execution_update",
			ToolCallId:       e.ToolCallId,
			ToolName:         e.ToolName,
			Args:             e.Args,
			PartialResult:    e.PartialResult,
			ParentToolCallId: e.ParentToolCallId,
		})
	case *ToolExecutionEndEvent:
		h.session.as1Emit(&AgentSessionEventToolExecutionEnd{
			Type:             "tool_execution_end",
			ToolCallId:       e.ToolCallId,
			ToolName:         e.ToolName,
			Result:           e.Result,
			IsError:          e.IsError,
			ParentToolCallId: e.ParentToolCallId,
		})
	}
	return nil
}

func (s *AgentSession) as1ExecuteNestedToolCall(
	ctx context.Context,
	parentToolCallId string,
	name string,
	args any,
	options *ExecuteToolOptions,
) (agent.AgentToolCallOutcome, error) {
	s.mu.Lock()
	runner := s.nestedToolCalls
	if runner == nil {
		runner = NewNestedToolCallRunner(&as1NestedHost{session: s})
		s.nestedToolCalls = runner
	}
	s.mu.Unlock()
	var nestedOpts *NestedToolCallOptions
	if options != nil {
		nestedOpts = &NestedToolCallOptions{
			Signal:   options.Signal,
			OnUpdate: options.OnUpdate,
		}
	}
	return runner.Execute(ctx, parentToolCallId, name, args, nestedOpts)
}

func (s *AgentSession) as1ExceedsCompactionThreshold(model *ai.Model, projection SessionProjection) bool {
	if model == nil || model.ContextWindow <= 0 || s.SessionManager == nil || s.SettingsManager == nil {
		return false
	}
	settings, err := s.SettingsManager.GetCompactionSettings(s.Model())
	if err != nil {
		return false
	}
	tokens := EstimateProjectedContextTokens(projection, s.SessionManager.GetBranch()).Tokens
	return ShouldCompact(tokens, model.ContextWindow, settings)
}

func (s *AgentSession) as1CompactBeforeNextAssistantResponse(ctx context.Context, agentCtx *agent.AgentContext) (*agent.AgentContext, error) {
	if s.SessionManager == nil {
		return agentCtx, nil
	}
	projection := s.SessionManager.BuildSessionProjection()
	model := s.Model()
	var tools []*agent.AgentTool
	if agentCtx != nil {
		tools = agentCtx.Tools
	}
	if model == nil || IsVirtualModel(model) || !s.as1ExceedsCompactionThreshold(model, projection) {
		return &agent.AgentContext{Messages: projection.Messages, Tools: tools}, nil
	}
	if _, err := s.as2RunAutoCompaction(ctx, "threshold", false); err != nil {
		return nil, err
	}
	return &agent.AgentContext{
		Messages: s.SessionManager.BuildSessionProjection().Messages,
		Tools:    tools,
	}, nil
}

func (s *AgentSession) as1InstallAgentRequestProjection() {
	prev := s.Agent.PrepareRequest()
	s.Agent.SetPrepareRequest(func(ctx context.Context, req *agent.PrepareRequestContext) (*agent.AgentRequestUpdate, error) {
		s.mu.Lock()
		failed := s.failedResponse
		s.failedResponse = nil
		s.mu.Unlock()

		prepare := func() (*agent.AgentRequestUpdate, *agent.AgentContext, SessionProjection, error) {
			var projection SessionProjection
			if s.SessionManager != nil {
				projection = s.SessionManager.BuildSessionProjection()
			}
			var reqTools []*agent.AgentTool
			if s.Agent != nil {
				reqTools = slices.Clone(s.Agent.Tools())
			}
			canonical := &agent.AgentContext{
				Messages: projection.Messages,
				Tools:    reqTools,
			}
			var prevRes *agent.AgentRequestUpdate
			if prev != nil {
				var err error
				prevRes, err = prev(ctx, &agent.PrepareRequestContext{
					Context:       canonical,
					Model:         s.Agent.Model(),
					ThinkingLevel: s.Agent.ThinkingLevel(),
				})
				if err != nil {
					return nil, nil, projection, err
				}
			}
			outCtx := canonical
			if prevRes != nil && prevRes.Context != nil {
				outCtx = prevRes.Context
			}
			return prevRes, outCtx, projection, nil
		}

		prevRes, nextCtx, projection, err := prepare()
		if err != nil {
			return nil, err
		}
		model := s.Agent.Model()
		if prevRes != nil && prevRes.Model != nil {
			model = prevRes.Model
		}
		thinkingLevel := s.Agent.ThinkingLevel()
		if prevRes != nil && prevRes.ThinkingLevel != nil {
			thinkingLevel = *prevRes.ThinkingLevel
		}
		if model == nil || !IsVirtualModel(model) || s.modelRuntime == nil {
			tl := thinkingLevel
			return &agent.AgentRequestUpdate{
				Context:       nextCtx,
				Model:         model,
				ThinkingLevel: &tl,
			}, nil
		}

		lastResponse := -1
		for i := len(nextCtx.Messages) - 1; i >= 0; i-- {
			if nextCtx.Messages[i] != nil && nextCtx.Messages[i].GetRole() == "assistant" {
				lastResponse = i
				break
			}
		}
		userTurn := false
		for _, m := range nextCtx.Messages[lastResponse+1:] {
			if m != nil && m.GetRole() == "user" {
				userTurn = true
				break
			}
		}
		branch := s.SessionManager.GetBranch()
		state := GetVirtualModelState(branch, string(model.Provider), model.Id)
		reason := "continuation"
		if failed != nil {
			reason = "retry"
		} else if userTurn {
			reason = "user"
		}
		route, err := s.modelRuntime.ResolveModel(ctx, model, ConvertToLlm(nextCtx.Messages), ResolveModelOptions{
			Reason:        ModelRouteReason(reason),
			ThinkingLevel: thinkingLevel,
			Failed:        failed,
			State:         state,
		})
		if err != nil {
			return nil, err
		}
		if route.State != nil && !reflect.DeepEqual(route.State, state) {
			data := VirtualModelStateData{
				Provider: string(model.Provider),
				ModelId:  model.Id,
				State:    route.State,
			}
			entryId, _ := s.SessionManager.AppendCustomEntry(VirtualModelStateEntry, data)
			if entry := s.SessionManager.GetEntry(entryId); entry != nil {
				s.as1Emit(&AgentSessionEventEntryAppended{Type: "entry_appended", Entry: entry})
			}
		}
		if route.Model != nil && s.as1ExceedsCompactionThreshold(route.Model, projection) {
			if _, compErr := s.as2RunAutoCompaction(ctx, "threshold", false); compErr != nil {
				return nil, compErr
			}
			prevRes, nextCtx, _, err = prepare()
			if err != nil {
				return nil, err
			}
		}
		routedModel := model
		routedThinking := thinkingLevel
		if route.Model != nil {
			routedModel = route.Model
		}
		if route.ThinkingLevel != "" {
			routedThinking = route.ThinkingLevel
		}
		return &agent.AgentRequestUpdate{
			Context:       nextCtx,
			Model:         routedModel,
			ThinkingLevel: &routedThinking,
		}, nil
	})
}

func (s *AgentSession) as1InstallAgentNextTurnRefresh() {
	prevWithCtx := s.Agent.PrepareNextTurnWithContext()
	if prevWithCtx == nil {
		if legacy := s.Agent.PrepareNextTurn(); legacy != nil {
			prevWithCtx = func(ctx context.Context, _ *agent.PrepareNextTurnContext) (*agent.AgentLoopTurnUpdate, error) {
				return legacy(ctx)
			}
		}
	}
	s.Agent.SetPrepareNextTurnWithContext(func(ctx context.Context, turn *agent.PrepareNextTurnContext) (*agent.AgentLoopTurnUpdate, error) {
		var turnCtx *agent.AgentContext
		if turn != nil {
			turnCtx = turn.Context
		}
		compactedCtx, err := s.as1CompactBeforeNextAssistantResponse(ctx, turnCtx)
		if err != nil {
			return nil, err
		}
		var prevSnap *agent.AgentLoopTurnUpdate
		if prevWithCtx != nil {
			nextTurn := &agent.PrepareNextTurnContext{
				Context: compactedCtx,
			}
			if turn != nil {
				nextTurn.Message = turn.Message
				nextTurn.ToolResults = turn.ToolResults
				nextTurn.NewMessages = turn.NewMessages
			}
			prevSnap, err = prevWithCtx(ctx, nextTurn)
			if err != nil {
				return nil, err
			}
		}
		nextCtx := compactedCtx
		if prevSnap != nil && prevSnap.Context != nil {
			nextCtx = prevSnap.Context
		}

		s.mu.Lock()
		baseOpts := s.baseSystemPromptOpts
		runOpts := baseOpts
		if s.runSystemPromptOpts != nil {
			runOpts = *s.runSystemPromptOpts
		}
		s.mu.Unlock()

		mergedSnippets := make(map[string]string, len(baseOpts.ToolSnippets)+len(runOpts.ToolSnippets))
		for k, v := range baseOpts.ToolSnippets {
			mergedSnippets[k] = v
		}
		for k, v := range runOpts.ToolSnippets {
			mergedSnippets[k] = v
		}
		mergedGuidelines := make(map[string][]string, len(baseOpts.ToolGuidelines)+len(runOpts.ToolGuidelines))
		for k, v := range baseOpts.ToolGuidelines {
			mergedGuidelines[k] = slices.Clone(v)
		}
		for k, v := range runOpts.ToolGuidelines {
			mergedGuidelines[k] = slices.Clone(v)
		}

		turnInput := as1NormalizedToBuildSystemPromptOptions(runOpts)
		turnInput.SelectedTools = s.GetActiveToolNames()
		turnInput.ToolSnippets = mergedSnippets
		turnInput.ToolGuidelines = mergedGuidelines
		normOpts := NormalizeBuildSystemPromptOptions(turnInput)
		var msgs agent.AgentMessageList
		if nextCtx != nil {
			msgs = nextCtx.Messages
		}
		updateMsg := s.as3PreparePromptAndToolLoadout(&normOpts, msgs)

		s.mu.Lock()
		s.runSystemPromptOpts = &normOpts
		s.mu.Unlock()

		var snapMsgs agent.AgentMessageList
		if prevSnap != nil && len(prevSnap.Messages) > 0 {
			snapMsgs = slices.Clone(prevSnap.Messages)
		}
		if updateMsg != nil {
			snapMsgs = append(snapMsgs, updateMsg)
		}
		tl := s.Agent.ThinkingLevel()
		return &agent.AgentLoopTurnUpdate{
			Context: &agent.AgentContext{
				Messages: msgs,
				Tools:    slices.Clone(s.Agent.Tools()),
			},
			Messages:      snapMsgs,
			Model:         s.Agent.Model(),
			ThinkingLevel: &tl,
		}, nil
	})
}

func (s *AgentSession) as1InstallAgentBoundaryHooks() {
	prevFinishTurn := s.Agent.FinishTurn()
	s.Agent.SetFinishTurn(func(ctx context.Context, turn *agent.AgentTurnContext) (*agent.AgentTurnDecision, error) {
		if turn != nil && turn.Message != nil {
			s.as1MarkBoundaryDispatched(turn.Message)
		}
		var assistantMsg *ai.AssistantMessage
		var toolResults []*ai.ToolResultMessage
		if turn != nil {
			assistantMsg = turn.Message
			toolResults = turn.ToolResults
		}
		extContinue := s.as1DispatchTurnEndBoundary(ctx, assistantMsg, toolResults)
		var prevDecision *agent.AgentTurnDecision
		if prevFinishTurn != nil {
			var err error
			prevDecision, err = prevFinishTurn(ctx, turn)
			if err != nil {
				return nil, err
			}
		}
		if prevDecision != nil && prevDecision.Action == agent.AgentTurnDecisionEnd {
			return prevDecision, nil
		}
		if extContinue || (prevDecision != nil && prevDecision.Action == agent.AgentTurnDecisionContinue) {
			return &agent.AgentTurnDecision{Action: agent.AgentTurnDecisionContinue}, nil
		}
		return nil, nil
	})
}

func (s *AgentSession) as1InstallHiddenDeclarationsProjection() {
	prevTransform := s.Agent.TransformContext()
	s.Agent.SetTransformContext(func(ctx context.Context, messages []agent.AgentMessage) ([]agent.AgentMessage, error) {
		transformed := messages
		if prevTransform != nil {
			var err error
			transformed, err = prevTransform(ctx, messages)
			if err != nil {
				return nil, err
			}
		}
		s.mu.Lock()
		hiddenCount := len(s.hiddenDeclarations)
		var hidden map[string]struct{}
		if hiddenCount > 0 {
			hidden = make(map[string]struct{}, hiddenCount)
			for k := range s.hiddenDeclarations {
				hidden[k] = struct{}{}
			}
		}
		s.mu.Unlock()
		if len(hidden) == 0 {
			return transformed, nil
		}

		out := make([]agent.AgentMessage, len(transformed))
		for i, msg := range transformed {
			sys, ok := msg.(*ai.SystemMessage)
			if !ok || (len(sys.ToolsAdded) == 0 && len(sys.ToolsRemoved) == 0) {
				out[i] = msg
				continue
			}
			var added []*ai.Tool
			for _, t := range sys.ToolsAdded {
				if t != nil {
					if _, hide := hidden[t.Name]; !hide {
						added = append(added, t)
					}
				}
			}
			var removed []ai.ToolReference
			for _, t := range sys.ToolsRemoved {
				if _, hide := hidden[t.Name]; !hide {
					removed = append(removed, t)
				}
			}
			out[i] = &ai.SystemMessage{
				Role:         sys.Role,
				Content:      sys.Content,
				Sections:     sys.Sections,
				ToolsAdded:   added,
				ToolsRemoved: removed,
				Timestamp:    sys.Timestamp,
			}
		}
		return out, nil
	})
}

func (s *AgentSession) as1InstallAgentForcedPromptProjection() {
	prevTransform := s.Agent.TransformContext()
	s.Agent.SetTransformContext(func(ctx context.Context, messages []agent.AgentMessage) ([]agent.AgentMessage, error) {
		transformed := messages
		if prevTransform != nil {
			var err error
			transformed, err = prevTransform(ctx, messages)
			if err != nil {
				return nil, err
			}
		}
		s.mu.Lock()
		var forced *string
		if s.runSystemPromptOpts != nil {
			forced = s.runSystemPromptOpts.ForceSystemPrompt
		}
		s.mu.Unlock()
		if forced == nil {
			return transformed, nil
		}

		current := ai.GetCurrentSystemMessage(as1AgentMessagesToAiMessages(transformed))
		forcedCopy := *forced
		var toolsAdded []*ai.Tool
		ts := time.Now().UnixMilli()
		if current != nil {
			toolsAdded = current.ToolsAdded
			if current.Timestamp != 0 {
				ts = current.Timestamp
			}
		}
		head := &ai.SystemMessage{
			Role:       "system",
			Content:    ai.SystemMessageContent{Text: &forcedCopy},
			ToolsAdded: toolsAdded,
			Timestamp:  ts,
		}
		out := make([]agent.AgentMessage, 0, 1+len(transformed))
		out = append(out, head)
		for _, m := range transformed {
			if m != nil && m.GetRole() != "system" {
				out = append(out, m)
			}
		}
		return out, nil
	})
}

// ============================================================================
// Boundary / Finalized Context Helpers
// ============================================================================

func as1AgentMessagesToAiMessages(messages []agent.AgentMessage) []ai.Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]ai.Message, 0, len(messages))
	for _, m := range messages {
		if aiMsg, ok := m.(ai.Message); ok && aiMsg != nil {
			out = append(out, aiMsg)
		}
	}
	return out
}

func as1MapKey(msg any) (any, bool) {
	if msg == nil {
		return nil, false
	}
	t := reflect.TypeOf(msg)
	if t == nil || !t.Comparable() {
		return nil, false
	}
	return msg, true
}

func (s *AgentSession) as1SetEntryIdForMessage(msg agent.AgentMessage, entryId string) {
	if entryId == "" {
		return
	}
	if key, ok := as1MapKey(msg); ok {
		s.mu.Lock()
		s.entryIdsByMessage[key] = entryId
		s.mu.Unlock()
	}
}

func (s *AgentSession) as1GetEntryIdForMessage(msg agent.AgentMessage) (string, bool) {
	key, ok := as1MapKey(msg)
	if !ok {
		return "", false
	}
	s.mu.Lock()
	id, exists := s.entryIdsByMessage[key]
	s.mu.Unlock()
	return id, exists
}

func (s *AgentSession) as1MarkBoundaryDispatched(msg agent.AgentMessage) {
	if key, ok := as1MapKey(msg); ok {
		s.mu.Lock()
		s.boundaryDispatchedMessages[key] = struct{}{}
		s.mu.Unlock()
	}
}

func (s *AgentSession) as1TakeBoundaryDispatched(msg agent.AgentMessage) bool {
	key, ok := as1MapKey(msg)
	if !ok {
		return false
	}
	s.mu.Lock()
	_, exists := s.boundaryDispatchedMessages[key]
	if exists {
		delete(s.boundaryDispatchedMessages, key)
	}
	s.mu.Unlock()
	return exists
}

func (s *AgentSession) as1RefreshFinalizedContext() {
	if s.SessionManager == nil || s.Agent == nil {
		return
	}
	projection := s.SessionManager.BuildSessionProjection()
	s.mu.Lock()
	for _, entry := range projection.Entries {
		srcId := entry.SourceEntry.GetId()
		for _, m := range entry.Messages {
			if key, ok := as1MapKey(m); ok {
				s.entryIdsByMessage[key] = srcId
			}
		}
	}
	s.mu.Unlock()
	s.Agent.ReplaceMessages(projection.Messages)
}

func (s *AgentSession) as1AppendCompactionDraft(manager *SessionManager, d *CompactionEntryDraft) string {
	tokensBefore := EstimateProjectedContextTokens(manager.BuildSessionProjection(), manager.GetBranch()).Tokens
	var firstKept *string
	if fk, ok := d.FirstKeptEntryId.Get(); ok {
		firstKept = &fk
	}
	fromHook := true
	entryId, _ := manager.AppendCompaction(d.Summary, firstKept, tokensBefore, &AppendCompactionOptions{
		Details:  d.Details,
		FromHook: &fromHook,
		Usage:    d.Usage,
	})
	return entryId
}

func (s *AgentSession) as1ApplyBoundaryDrafts(manager *SessionManager, drafts []SessionBoundaryDraft) []SessionEntry {
	var appended []SessionEntry
	for _, draft := range drafts {
		var entryId string
		switch d := draft.(type) {
		case CustomEntryDraft:
			entryId, _ = manager.AppendCustomEntry(d.CustomType, d.Data)
		case *CustomEntryDraft:
			if d != nil {
				entryId, _ = manager.AppendCustomEntry(d.CustomType, d.Data)
			}
		case CustomMessageEntryDraft:
			entryId, _ = manager.AppendCustomMessageEntry(d.CustomType, d.Content, d.Display, d.Details)
		case *CustomMessageEntryDraft:
			if d != nil {
				entryId, _ = manager.AppendCustomMessageEntry(d.CustomType, d.Content, d.Display, d.Details)
			}
		case ContextEditEntryDraft:
			rep := d.Replacement
			entryId, _ = manager.AppendContextEdit(d.TargetId, &rep)
		case *ContextEditEntryDraft:
			if d != nil {
				rep := d.Replacement
				entryId, _ = manager.AppendContextEdit(d.TargetId, &rep)
			}
		case CompactionEntryDraft:
			entryId = s.as1AppendCompactionDraft(manager, &d)
		case *CompactionEntryDraft:
			if d != nil {
				entryId = s.as1AppendCompactionDraft(manager, d)
			}
		}
		if entryId != "" {
			if entry := manager.GetEntry(entryId); entry != nil {
				appended = append(appended, entry)
			}
		}
	}
	return appended
}

func (s *AgentSession) as1CreateBoundaryPreviewManager(drafts []SessionBoundaryDraft) (*SessionManager, error) {
	header := s.SessionManager.GetHeader()
	if header == nil {
		return nil, errors.New("Session header is missing")
	}
	entries := make([]FileEntry, 0, 1+len(s.SessionManager.GetBranch()))
	entries = append(entries, header)
	for _, e := range s.SessionManager.GetBranch() {
		entries = append(entries, e)
	}
	manager, err := SessionManagerInMemoryWith(s.cwd, nil, entries)
	if err != nil {
		return nil, err
	}
	s.as1ApplyBoundaryDrafts(manager, drafts)
	return manager, nil
}

func (s *AgentSession) as1GetPendingBoundaryMessages() []agent.AgentMessage {
	var out []agent.AgentMessage
	if s.Agent != nil {
		queued := s.Agent.PeekQueuedMessages()
		out = append(out, queued.Steering...)
		out = append(out, queued.FollowUp...)
	}
	s.mu.Lock()
	for _, m := range s.pendingCustomMessages {
		out = append(out, m)
	}
	s.mu.Unlock()
	return out
}

func (s *AgentSession) as1BuildBoundaryContext(drafts []SessionBoundaryDraft, boundary string) BoundaryContextPreview {
	mgr, err := s.as1CreateBoundaryPreviewManager(drafts)
	if err != nil {
		return BoundaryContextPreview{}
	}
	projection := mgr.BuildSessionProjection()
	pendingMessages := s.as1GetPendingBoundaryMessages()
	llmMessages := ConvertToLlm(projection.Messages)
	var finalRole string
	hasNonSystem := false
	for _, m := range llmMessages {
		if m == nil {
			continue
		}
		role := m.GetRole()
		finalRole = role
		if role != "system" {
			hasNonSystem = true
		}
	}
	contextCanContinue := hasNonSystem && finalRole != "assistant"

	s.mu.Lock()
	pendingCustomCount := len(s.pendingCustomMessages)
	s.mu.Unlock()
	hasQueued := s.Agent != nil && s.Agent.HasQueuedMessages()
	queueContinue := hasQueued
	if boundary != "turn_end" {
		queueContinue = finalRole == "assistant" && hasQueued
	}
	return BoundaryContextPreview{
		ContextEntries:  projection.Entries,
		ContextMessages: projection.Messages,
		LlmMessages:     llmMessages,
		PendingMessages: pendingMessages,
		CanContinue:     contextCanContinue || pendingCustomCount > 0 || queueContinue,
	}
}

func (s *AgentSession) as1CommitBoundaryDrafts(drafts []SessionBoundaryDraft) {
	if len(drafts) == 0 || s.SessionManager == nil {
		return
	}
	appended := s.as1ApplyBoundaryDrafts(s.SessionManager, drafts)
	s.as1RefreshFinalizedContext()
	for _, entry := range appended {
		s.as1Emit(&AgentSessionEventEntryAppended{
			Type:  "entry_appended",
			Entry: entry,
		})
	}
}

func (s *AgentSession) as1DispatchTurnEndBoundary(ctx context.Context, message *ai.AssistantMessage, toolResults []*ai.ToolResultMessage) bool {
	outcome := "completed"
	if message != nil {
		switch message.StopReason {
		case ai.StopReasonAborted:
			outcome = "aborted"
		case ai.StopReasonError:
			outcome = "error"
		}
	}
	s.mu.Lock()
	s.lastActivityOutcome = outcome
	ti := s.turnIndex
	s.mu.Unlock()

	runner := s.currentExtensionRunner()
	if runner == nil || !runner.HasHandlers("turn_end") {
		return false
	}
	_, ok := s.as1FindPersistedMessageEntryId(message)
	if !ok {
		runner.EmitError(ExtensionError{
			ExtensionPath: "<boundary>",
			Event:         "turn_end",
			Error:         "turn_end could not resolve the persisted assistant entry ID",
		})
		return false
	}
	boundary, err := runner.EmitBoundary(ctx, BoundaryBaseEvent{
		Type:        "turn_end",
		TurnIndex:   ti,
		Message:     message,
		ToolResults: toolResults,
		Outcome:     AgentActivityOutcome(outcome),
	}, func(entries []SessionBoundaryDraft) (BoundaryContextPreview, error) {
		return s.as1BuildBoundaryContext(entries, "turn_end"), nil
	})
	if err != nil {
		return false
	}
	s.as1CommitBoundaryDrafts(boundary.Entries)
	if boundary.Continue && !s.as1BuildBoundaryContext(nil, "turn_end").CanContinue {
		runner.EmitError(ExtensionError{
			ExtensionPath: "<boundary>",
			Event:         "turn_end",
			Error:         "turn_end requested continuation without runnable model context",
		})
		return false
	}
	return boundary.Continue
}

// ============================================================================
// Event Subscription & Processing
// ============================================================================

func (s *AgentSession) as1Emit(event AgentSessionEvent) {
	if event == nil {
		return
	}
	s.mu.Lock()
	listeners := make([]as1EventListenerEntry, len(s.eventListeners))
	copy(listeners, s.eventListeners)
	s.mu.Unlock()
	for _, l := range listeners {
		l.fn(event)
	}
}

func (s *AgentSession) as1EmitQueueUpdate() {
	s.mu.Lock()
	steering := slices.Clone(s.steeringMessages)
	followUp := slices.Clone(s.followUpMessages)
	s.mu.Unlock()
	s.as1Emit(&AgentSessionEventQueueUpdate{
		Type:     "queue_update",
		Steering: steering,
		FollowUp: followUp,
	})
}

func (s *AgentSession) as1ResolveIdleWaitIfIdle() {
	s.mu.Lock()
	if !s.as1IsIdleLocked() || s.idleCh == nil {
		s.mu.Unlock()
		return
	}
	ch := s.idleCh
	s.idleCh = nil
	s.mu.Unlock()
	close(ch)
}

func (s *AgentSession) as1EmitAgentSettled(ctx context.Context) {
	if s.cacheWarmer != nil {
		s.cacheWarmer.OnAgentSettled()
	}
	s.mu.Lock()
	s.isAgentRunActive = false
	s.isEmittingAgentSettled = true
	aborted := s.agentRunAbortRequested
	s.mu.Unlock()

	if runner := s.currentExtensionRunner(); runner != nil {
		_ = runner.Emit(ctx, AgentSettledEvent{Type: "agent_settled", Aborted: aborted})
	}
	s.as1Emit(&AgentSessionEventAgentSettled{Type: "agent_settled", Aborted: aborted})

	s.mu.Lock()
	s.isEmittingAgentSettled = false
	deferred := slices.Clone(s.deferredSettledActions)
	s.deferredSettledActions = nil
	s.mu.Unlock()

	if len(deferred) > 0 {
		for _, action := range deferred {
			_ = action(ctx)
		}
	}
	s.as1ResolveIdleWaitIfIdle()
}

func (s *AgentSession) as1HandleAgentEvent(event agent.AgentEvent) {
	if event == nil {
		return
	}
	ctx := context.Background()
	if s.Agent != nil {
		if sig := s.Agent.Signal(); sig != nil {
			ctx = sig
		}
	}

	switch e := event.(type) {
	case *agent.AgentEventAgentStart:
		s.mu.Lock()
		abortReq := s.agentRunAbortRequested
		s.mu.Unlock()
		if abortReq && s.Agent != nil {
			s.Agent.Abort()
		}

	case *agent.AgentEventMessageStart:
		if tr, ok := e.Message.(*ai.ToolResultMessage); ok && tr != nil {
			s.mu.Lock()
			nested := s.nestedToolCalls
			s.mu.Unlock()
			if nested != nil {
				if summary := nested.TakeRecord(tr.ToolCallId); summary != nil {
					if summary.Calls != nil {
						tr.NestedCalls = summary.Calls
					}
					if summary.Usage != nil {
						if tr.Usage != nil {
							combined := CombineUsage(*tr.Usage, *summary.Usage)
							tr.Usage = &combined
						} else {
							u := *summary.Usage
							tr.Usage = &u
						}
					}
				}
			}
		}
		if um, ok := e.Message.(*ai.UserMessage); ok && um != nil {
			s.mu.Lock()
			s.overflowRecoveryAttempt = false
			msgText := ai.ContentText(um.Content, "")
			queueUpdated := false
			if idx := slices.Index(s.steeringMessages, msgText); idx >= 0 {
				s.steeringMessages = slices.Delete(s.steeringMessages, idx, idx+1)
				queueUpdated = true
			} else if idx := slices.Index(s.followUpMessages, msgText); idx >= 0 {
				s.followUpMessages = slices.Delete(s.followUpMessages, idx, idx+1)
				queueUpdated = true
			}
			needsImageNorm := false
			if key, ok := as1MapKey(e.Message); ok {
				if _, exists := s.queuedMessagesWithImage[key]; exists {
					delete(s.queuedMessagesWithImage, key)
					needsImageNorm = true
				}
			}
			s.mu.Unlock()
			if queueUpdated {
				s.as1EmitQueueUpdate()
			}
			if needsImageNorm {
				s.as1NormalizeQueuedMessageImages(um)
			}
		}

	case *agent.AgentEventAgentEnd:
		s.mu.Lock()
		nested := s.nestedToolCalls
		s.mu.Unlock()
		if nested != nil {
			nested.Clear()
		}
	}

	s.as1EmitExtensionEvent(ctx, event)

	var willRetry bool
	if endEv, ok := event.(*agent.AgentEventAgentEnd); ok {
		willRetry = s.as1WillRetryAfterAgentEnd(endEv.Messages)
	}
	if sessionEv := as1ToAgentSessionEvent(event, willRetry); sessionEv != nil {
		s.as1Emit(sessionEv)
	}

	switch e := event.(type) {
	case *agent.AgentEventMessageEnd:
		if s.SessionManager != nil && e.Message != nil {
			var entryId string
			if cm, ok := e.Message.(*CustomMessage); ok && cm != nil {
				entryId, _ = s.SessionManager.AppendCustomMessageEntry(cm.CustomType, cm.Content, cm.Display, cm.Details)
			} else {
				switch e.Message.GetRole() {
				case "system", "user", "assistant", "toolResult":
					entryId, _ = s.SessionManager.AppendMessage(e.Message)
				}
			}
			s.as1SetEntryIdForMessage(e.Message, entryId)
		}
		if am, ok := e.Message.(*ai.AssistantMessage); ok && am != nil {
			s.mu.Lock()
			s.lastAssistantMessage = am
			if am.StopReason != ai.StopReasonError && am.StopReason != ai.StopReasonLength {
				s.overflowRecoveryAttempt = false
			}
			completedRetry := 0
			if am.StopReason != ai.StopReasonError && s.retryAttempt > 0 {
				completedRetry = s.retryAttempt
				s.retryAttempt = 0
			}
			s.mu.Unlock()
			if completedRetry > 0 {
				s.as1Emit(&AgentSessionEventAutoRetryEnd{
					Type:    "auto_retry_end",
					Success: true,
					Attempt: completedRetry,
				})
			}
		}

	case *agent.AgentEventTurnEnd:
		s.as1FlushPendingCustomMessages()
	}
}

func as1ToAgentSessionEvent(event agent.AgentEvent, willRetry bool) AgentSessionEvent {
	switch e := event.(type) {
	case *agent.AgentEventAgentStart:
		return &AgentSessionEventAgentStart{Type: "agent_start"}
	case *agent.AgentEventAgentEnd:
		return &AgentSessionEventAgentEnd{Type: "agent_end", Messages: e.Messages, WillRetry: willRetry}
	case *agent.AgentEventTurnStart:
		return &AgentSessionEventTurnStart{Type: "turn_start"}
	case *agent.AgentEventTurnEnd:
		return &AgentSessionEventTurnEnd{Type: "turn_end", Message: e.Message, ToolResults: e.ToolResults}
	case *agent.AgentEventMessageStart:
		return &AgentSessionEventMessageStart{Type: "message_start", Message: e.Message}
	case *agent.AgentEventMessageUpdate:
		return &AgentSessionEventMessageUpdate{
			Type:                  "message_update",
			Message:               e.Message,
			AssistantMessageEvent: e.AssistantMessageEvent,
		}
	case *agent.AgentEventMessageEnd:
		return &AgentSessionEventMessageEnd{Type: "message_end", Message: e.Message}
	case *agent.AgentEventToolExecutionStart:
		return &AgentSessionEventToolExecutionStart{
			Type:       "tool_execution_start",
			ToolCallId: e.ToolCallId,
			ToolName:   e.ToolName,
			Args:       e.Args,
		}
	case *agent.AgentEventToolExecutionUpdate:
		return &AgentSessionEventToolExecutionUpdate{
			Type:          "tool_execution_update",
			ToolCallId:    e.ToolCallId,
			ToolName:      e.ToolName,
			Args:          e.Args,
			PartialResult: e.PartialResult,
		}
	case *agent.AgentEventToolExecutionEnd:
		return &AgentSessionEventToolExecutionEnd{
			Type:       "tool_execution_end",
			ToolCallId: e.ToolCallId,
			ToolName:   e.ToolName,
			Result:     e.Result,
			IsError:    e.IsError,
			DurationMs: e.DurationMs,
		}
	default:
		return nil
	}
}

func (s *AgentSession) as1EmitExtensionEvent(ctx context.Context, event agent.AgentEvent) {
	runner := s.currentExtensionRunner()
	switch e := event.(type) {
	case *agent.AgentEventAgentStart:
		s.mu.Lock()
		s.turnIndex = 0
		s.mu.Unlock()
		if runner != nil {
			_ = runner.Emit(ctx, AgentStartEvent{Type: "agent_start"})
		}
	case *agent.AgentEventAgentEnd:
		if runner != nil {
			_ = runner.Emit(ctx, AgentEndEvent{Type: "agent_end", Messages: e.Messages})
		}
	case *agent.AgentEventTurnStart:
		s.mu.Lock()
		ti := s.turnIndex
		s.mu.Unlock()
		if runner != nil {
			_ = runner.Emit(ctx, TurnStartEvent{Type: "turn_start", TurnIndex: ti, Timestamp: time.Now().UnixMilli()})
		}
	case *agent.AgentEventTurnEnd:
		if am, ok := e.Message.(*ai.AssistantMessage); ok && am != nil && !s.as1TakeBoundaryDispatched(am) {
			_ = s.as1DispatchTurnEndBoundary(ctx, am, e.ToolResults)
		}
		s.mu.Lock()
		s.turnIndex++
		s.mu.Unlock()
	case *agent.AgentEventMessageStart:
		if runner != nil {
			_ = runner.Emit(ctx, MessageStartEvent{Type: "message_start", Message: e.Message})
		}
	case *agent.AgentEventMessageUpdate:
		if runner != nil {
			_ = runner.Emit(ctx, MessageUpdateEvent{
				Type:                  "message_update",
				Message:               e.Message,
				AssistantMessageEvent: e.AssistantMessageEvent,
			})
		}
	case *agent.AgentEventMessageEnd:
		if runner != nil {
			replacement := runner.EmitMessageEnd(ctx, MessageEndEvent{Type: "message_end", Message: e.Message})
			if replacement != nil {
				s.as1ReplaceMessageInPlace(e.Message, replacement)
			}
		}
	case *agent.AgentEventToolExecutionStart:
		if runner != nil {
			_ = runner.Emit(ctx, ToolExecutionStartEvent{
				Type:       "tool_execution_start",
				ToolCallId: e.ToolCallId,
				ToolName:   e.ToolName,
				Args:       e.Args,
			})
		}
	case *agent.AgentEventToolExecutionUpdate:
		if runner != nil {
			_ = runner.Emit(ctx, ToolExecutionUpdateEvent{
				Type:          "tool_execution_update",
				ToolCallId:    e.ToolCallId,
				ToolName:      e.ToolName,
				Args:          e.Args,
				PartialResult: e.PartialResult,
			})
		}
	case *agent.AgentEventToolExecutionEnd:
		if runner != nil {
			_ = runner.Emit(ctx, ToolExecutionEndEvent{
				Type:       "tool_execution_end",
				ToolCallId: e.ToolCallId,
				ToolName:   e.ToolName,
				Result:     e.Result,
				IsError:    e.IsError,
				DurationMs: e.DurationMs,
			})
		}
	}
}

func (s *AgentSession) as1WillRetryAfterAgentEnd(messages agent.AgentMessageList) bool {
	s.mu.Lock()
	abortReq := s.agentRunAbortRequested
	attempt := s.retryAttempt
	s.mu.Unlock()
	if abortReq || s.SettingsManager == nil {
		return false
	}
	settings := s.SettingsManager.GetRetrySettings()
	if !settings.Enabled || attempt >= settings.MaxRetries {
		return false
	}
	for i := len(messages) - 1; i >= 0; i-- {
		if am, ok := messages[i].(*ai.AssistantMessage); ok && am != nil {
			return s.as2IsRetryableError(am)
		}
	}
	return false
}

func (s *AgentSession) as1FindPersistedMessageEntryId(msg agent.AgentMessage) (string, bool) {
	if msg == nil || s.SessionManager == nil {
		return "", false
	}
	if mapped, ok := s.as1GetEntryIdForMessage(msg); ok {
		return mapped, true
	}
	branch := s.SessionManager.GetBranch()
	for i := len(branch) - 1; i >= 0; i-- {
		if me, ok := branch[i].(*SessionMessageEntry); ok && as1SameMessage(me.Message, msg) {
			return me.Id, true
		}
	}
	stateMsgs := s.Messages()
	msgIdx := -1
	for i, candidate := range stateMsgs {
		if as1SameMessage(candidate, msg) {
			msgIdx = i
			break
		}
	}
	if msgIdx < 0 {
		return "", false
	}
	projection := s.SessionManager.BuildSessionProjection()
	projectedIdx := 0
	for _, entry := range projection.Entries {
		for range entry.Messages {
			if projectedIdx == msgIdx {
				srcId := entry.SourceEntry.GetId()
				s.as1SetEntryIdForMessage(msg, srcId)
				return srcId, true
			}
			projectedIdx++
		}
	}
	return "", false
}

func as1SameMessage(a, b agent.AgentMessage) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	ka, okA := as1MapKey(a)
	kb, okB := as1MapKey(b)
	if okA && okB {
		return ka == kb
	}
	return false
}

func (s *AgentSession) as1OmitRecoveryAttempt(msg *ai.AssistantMessage) error {
	if msg == nil || s.SessionManager == nil {
		return nil
	}
	toolCallIds := map[string]struct{}{}
	for _, block := range msg.Content {
		if tc, ok := block.(*ai.ToolCall); ok && tc != nil {
			toolCallIds[tc.Id] = struct{}{}
		}
	}
	entryId, _ := s.as1FindPersistedMessageEntryId(msg)
	messages := s.Messages()
	idx := -1
	for i, m := range messages {
		if as1SameMessage(m, msg) {
			idx = i
			break
		}
		if entryId != "" {
			if mId, ok := s.as1GetEntryIdForMessage(m); ok && mId == entryId {
				idx = i
				break
			}
		}
	}
	var toolResults []agent.AgentMessage
	if idx >= 0 {
		for _, m := range messages[idx+1:] {
			if m == nil {
				continue
			}
			if m.GetRole() == "assistant" {
				break
			}
			if tr, ok := m.(*ai.ToolResultMessage); ok && tr != nil {
				if _, match := toolCallIds[tr.ToolCallId]; match {
					toolResults = append(toolResults, m)
				}
			}
		}
	}
	targets := make([]agent.AgentMessage, 0, 1+len(toolResults))
	targets = append(targets, msg)
	targets = append(targets, toolResults...)
	targetIds := make([]string, len(targets))
	for i, target := range targets {
		tid, found := s.as1FindPersistedMessageEntryId(target)
		if !found {
			for _, stateMsg := range messages {
				if as1SameMessage(stateMsg, target) {
					return errors.New("Cannot persist recovery omission because a projected message has no source entry")
				}
			}
			continue
		}
		targetIds[i] = tid
	}
	for _, tid := range targetIds {
		if tid == "" {
			continue
		}
		editId, _ := s.SessionManager.AppendContextEdit(tid, nil)
		if entry := s.SessionManager.GetEntry(editId); entry != nil {
			s.as1Emit(&AgentSessionEventEntryAppended{Type: "entry_appended", Entry: entry})
		}
	}
	s.as1RefreshFinalizedContext()
	return nil
}

func (s *AgentSession) as1FindLastAssistantMessage() *ai.AssistantMessage {
	messages := s.Messages()
	for i := len(messages) - 1; i >= 0; i-- {
		if am, ok := messages[i].(*ai.AssistantMessage); ok && am != nil {
			return am
		}
	}
	return nil
}

func (s *AgentSession) as1ReplaceMessageInPlace(target, replacement agent.AgentMessage) {
	if target == nil || replacement == nil || as1SameMessage(target, replacement) {
		return
	}
	switch dst := target.(type) {
	case *ai.UserMessage:
		if src, ok := replacement.(*ai.UserMessage); ok {
			*dst = *src
			return
		}
	case *ai.ToolResultMessage:
		if src, ok := replacement.(*ai.ToolResultMessage); ok {
			*dst = *src
			return
		}
	case *CustomMessage:
		if src, ok := replacement.(*CustomMessage); ok {
			*dst = *src
			return
		}
	}
	if s.Agent == nil {
		return
	}
	msgs := s.Agent.Messages()
	for i, m := range msgs {
		if as1SameMessage(m, target) {
			msgs[i] = replacement
			s.Agent.ReplaceMessages(msgs)
			return
		}
	}
}

// Subscribe attaches an event listener and returns a function that unsubscribes it.
func (s *AgentSession) Subscribe(listener AgentSessionEventListener) func() {
	if listener == nil {
		return func() {}
	}
	s.mu.Lock()
	s.nextListenerID++
	id := s.nextListenerID
	s.eventListeners = append(s.eventListeners, as1EventListenerEntry{id: id, fn: listener})
	s.mu.Unlock()

	return func() {
		s.mu.Lock()
		for i, entry := range s.eventListeners {
			if entry.id == id {
				s.eventListeners = append(s.eventListeners[:i], s.eventListeners[i+1:]...)
				break
			}
		}
		s.mu.Unlock()
	}
}

func (s *AgentSession) as1DisconnectFromAgent() {
	s.mu.Lock()
	unsub := s.unsubscribeAgent
	s.unsubscribeAgent = nil
	s.mu.Unlock()
	if unsub != nil {
		unsub()
	}
}

// ============================================================================
// Prompting, Queues, Abort & Idle Waiting
// ============================================================================

func (s *AgentSession) as1NormalizePromptImages(images []*ai.ImageContent) ([]*ai.ImageContent, []string) {
	if len(images) == 0 {
		return nil, nil
	}
	var resizeOpts *utils.ImageResizeOptions
	if lm := s.as1LimitsModel(); lm != nil && lm.InputLimits != nil && lm.InputLimits.Images != nil {
		resizeOpts = lm.InputLimits.Images.Resize
	}
	autoResize := true
	if s.SettingsManager != nil {
		autoResize = s.SettingsManager.GetImageAutoResize()
	}

	normalized := make([]*ai.ImageContent, 0, len(images))
	var hints []string
	for _, img := range images {
		if img == nil {
			continue
		}
		rawBytes, err := base64.StdEncoding.DecodeString(img.Data)
		if err != nil {
			rawBytes = nil
		}
		processed := utils.ProcessImage(rawBytes, img.MimeType, &utils.ProcessImageOptions{
			AutoResizeImages: &autoResize,
			Resize:           resizeOpts,
		})
		if !processed.Ok {
			hints = append(hints, processed.Message)
			continue
		}
		normalized = append(normalized, &ai.ImageContent{
			Type:     "image",
			Data:     processed.Data,
			MimeType: processed.MimeType,
		})
		hints = append(hints, processed.Hints...)
	}
	return normalized, hints
}

func (s *AgentSession) as1NormalizeQueuedMessageImages(message *ai.UserMessage) {
	if message == nil || message.Content.Text != nil {
		return
	}
	blocks := message.Content.Blocks
	if len(blocks) == 0 {
		return
	}
	firstText, ok := blocks[0].(*ai.TextContent)
	if !ok || firstText == nil {
		return
	}
	var images []*ai.ImageContent
	var nonImages ai.ContentList
	for _, b := range blocks[1:] {
		if img, isImg := b.(*ai.ImageContent); isImg && img != nil {
			images = append(images, img)
		} else {
			nonImages = append(nonImages, b)
		}
	}
	normImages, hints := s.as1NormalizePromptImages(images)
	text := firstText.Text
	if len(hints) > 0 {
		text = text + "\n\n" + strings.Join(hints, "\n")
	}
	newBlocks := make(ai.ContentList, 0, 1+len(nonImages)+len(normImages))
	newBlocks = append(newBlocks, &ai.TextContent{Type: "text", Text: text})
	newBlocks = append(newBlocks, nonImages...)
	for _, img := range normImages {
		newBlocks = append(newBlocks, img)
	}
	message.Content = ai.UserMessageContent{Blocks: newBlocks}
}

func (s *AgentSession) as1RunInputHandlers(
	ctx context.Context,
	text string,
	images []*ai.ImageContent,
	source InputSource,
	streamingBehavior string,
) (string, []*ai.ImageContent, bool, error) {
	runner := s.currentExtensionRunner()
	if runner == nil || !runner.HasHandlers("input") {
		return text, images, true, nil
	}
	var valImages []ai.ImageContent
	for _, img := range images {
		if img != nil {
			valImages = append(valImages, *img)
		}
	}
	res := runner.EmitInput(ctx, text, valImages, source, streamingBehavior)
	switch res.Action {
	case "handled":
		return "", nil, false, nil
	case "transform":
		nextImages := images
		if res.Images != nil {
			nextImages = make([]*ai.ImageContent, 0, len(res.Images))
			for i := range res.Images {
				cp := res.Images[i]
				nextImages = append(nextImages, &cp)
			}
		}
		return res.Text, nextImages, true, nil
	default:
		return text, images, true, nil
	}
}

// Prompt sends a prompt to the agent, dispatching extension commands, expanding
// skill commands and prompt templates, queuing while streaming, and validating
// model/authentication before starting an agent run.
func (s *AgentSession) Prompt(ctx context.Context, text string, options ...*PromptOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	var opts PromptOptions
	if len(options) > 0 && options[0] != nil {
		opts = *options[0]
	}

	s.mu.Lock()
	if s.isEmittingAgentSettled {
		s.deferredSettledActions = append(s.deferredSettledActions, func(c context.Context) error {
			return s.Prompt(c, text, &opts)
		})
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	expandTemplates := true
	if opts.ExpandPromptTemplates != nil {
		expandTemplates = *opts.ExpandPromptTemplates
	}
	if expandTemplates && strings.HasPrefix(text, "/") {
		handled := s.as1TryExecuteExtensionCommand(ctx, text)
		if handled {
			if opts.PreflightResult != nil {
				opts.PreflightResult(PromptDispositionHandled)
			}
			return nil
		}
	}

	s.mu.Lock()
	compacting := s.compactionCancel != nil || s.branchSummaryCancel != nil
	if !compacting {
		s.promptPreflightCount++
	}
	s.mu.Unlock()

	if compacting {
		return errors.New("Cannot submit a prompt while compaction is in progress. Wait for compaction to finish and retry.")
	}

	trigger, err := s.as1PreparePrompt(ctx, text, opts)
	s.mu.Lock()
	s.promptPreflightCount--
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if trigger == nil {
		return nil
	}
	var onStarted func()
	if opts.PreflightResult != nil {
		onStarted = func() { opts.PreflightResult(PromptDispositionStarted) }
	}
	return s.as1StartRun(ctx, *trigger, onStarted)
}

func (s *AgentSession) as1PreparePrompt(ctx context.Context, text string, opts PromptOptions) (*BeforeAgentStartTrigger, error) {
	expandTemplates := true
	if opts.ExpandPromptTemplates != nil {
		expandTemplates = *opts.ExpandPromptTemplates
	}
	source := opts.Source
	if source == "" {
		source = "interactive"
	}
	streamingBehavior := ""
	if s.IsStreaming() {
		streamingBehavior = opts.StreamingBehavior
	}
	curText, curImages, proceed, err := s.as1RunInputHandlers(ctx, text, opts.Images, source, streamingBehavior)
	if err != nil {
		return nil, err
	}
	if !proceed {
		if opts.PreflightResult != nil {
			opts.PreflightResult(PromptDispositionHandled)
		}
		return nil, nil
	}

	expanded := curText
	if expandTemplates {
		expanded = s.as3ExpandSkillCommand(expanded)
		expanded = ExpandPromptTemplate(expanded, s.PromptTemplates())
	}

	if s.IsStreaming() {
		if opts.StreamingBehavior == "" {
			return nil, errors.New("Agent is already processing. Specify streamingBehavior ('steer' or 'followUp') to queue the message.")
		}
		if opts.StreamingBehavior == "followUp" {
			s.as1QueueFollowUp(expanded, curImages)
		} else {
			s.as1QueueSteer(expanded, curImages)
		}
		if opts.PreflightResult != nil {
			opts.PreflightResult(PromptDispositionQueued)
		}
		return nil, nil
	}

	model := s.Model()
	if model == nil {
		return nil, errors.New(FormatNoModelSelectedMessage())
	}
	if s.modelRuntime != nil {
		hasAuth := s.modelRuntime.HasConfiguredAuth(string(model.Provider))
		if !hasAuth {
			chk, _ := s.modelRuntime.CheckAuth(ctx, string(model.Provider), nil)
			hasAuth = chk != nil
		}
		if !hasAuth {
			if s.modelRuntime.IsUsingOAuth(string(model.Provider)) {
				return nil, fmt.Errorf(
					"Authentication failed for %q. Credentials may have expired or network is unavailable. Run '/login %s' to re-authenticate.",
					model.Provider,
					model.Provider,
				)
			}
			return nil, errors.New(FormatNoApiKeyFoundMessage(string(model.Provider)))
		}
	}

	var triggerImages []ai.ImageContent
	if len(curImages) > 0 {
		triggerImages = make([]ai.ImageContent, 0, len(curImages))
		for _, img := range curImages {
			if img != nil {
				triggerImages = append(triggerImages, *img)
			}
		}
	}
	return &BeforeAgentStartTrigger{
		MessageType: "user",
		Prompt:      expanded,
		Images:      triggerImages,
	}, nil
}

func (s *AgentSession) as1TryExecuteExtensionCommand(ctx context.Context, text string) bool {
	runner := s.currentExtensionRunner()
	if runner == nil {
		return false
	}
	trimmed := strings.TrimPrefix(text, "/")
	commandName := trimmed
	args := ""
	if idx := strings.IndexByte(trimmed, ' '); idx >= 0 {
		commandName = trimmed[:idx]
		args = trimmed[idx+1:]
	}
	cmd := runner.GetCommand(commandName)
	if cmd == nil || cmd.Handler == nil {
		return false
	}
	ectx := runner.CreateCommandContext()
	if err := cmd.Handler(ctx, args, ectx); err != nil {
		runner.EmitError(ExtensionError{
			ExtensionPath: "command:" + commandName,
			Event:         "command",
			Error:         err.Error(),
		})
	}
	return true
}

func (s *AgentSession) as1ThrowIfExtensionCommand(text string) error {
	runner := s.currentExtensionRunner()
	if runner == nil {
		return nil
	}
	trimmed := strings.TrimPrefix(text, "/")
	commandName := trimmed
	if idx := strings.IndexByte(trimmed, ' '); idx >= 0 {
		commandName = trimmed[:idx]
	}
	if runner.GetCommand(commandName) != nil {
		return fmt.Errorf("Extension command \"/%s\" cannot be queued. Use prompt() or execute the command when not streaming.", commandName)
	}
	return nil
}

func (s *AgentSession) as1StartRun(ctx context.Context, trigger BeforeAgentStartTrigger, onStarted func()) error {
	s.mu.Lock()
	s.isAgentRunActive = true
	s.agentRunAbortRequested = false
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		if s.agentRunAbortRequested {
			s.as2FinishCancelledRetryLocked()
		}
		s.failedResponse = nil
		s.runSystemPromptOpts = nil
		s.mu.Unlock()
		s.as2FlushPendingBashMessages()
		s.as1FlushPendingCustomMessages()
		s.as1EmitAgentSettled(ctx)
	}()

	messages, err := s.as1PrepareRun(ctx, trigger)
	if err != nil {
		return err
	}

	s.as2FlushPendingBashMessages()
	s.as1FlushPendingCustomMessages()
	if onStarted != nil {
		onStarted()
	}

	s.mu.Lock()
	s.failedResponse = nil
	s.pendingToolNames = map[string]struct{}{}
	s.pendingToolOrder = nil
	s.mu.Unlock()

	s.as1RecordSelection()

	if err := s.Agent.Prompt(ctx, messages...); err != nil {
		return err
	}
	for {
		s.mu.Lock()
		abortReq := s.agentRunAbortRequested
		s.mu.Unlock()
		if abortReq {
			break
		}
		shouldContinue, err := s.as1HandlePostAgentRun(ctx)
		if err != nil {
			return err
		}
		if shouldContinue {
			s.mu.Lock()
			abortReq = s.agentRunAbortRequested
			s.mu.Unlock()
			if abortReq {
				break
			}
			if err := s.Agent.Continue(ctx); err != nil {
				return err
			}
			continue
		}
		s.mu.Lock()
		abortReq = s.agentRunAbortRequested
		s.mu.Unlock()
		if abortReq || !s.as1RunBeforeSettleBoundary(ctx) {
			break
		}
		s.mu.Lock()
		abortReq = s.agentRunAbortRequested
		s.mu.Unlock()
		if abortReq {
			break
		}
		if err := s.Agent.Continue(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (s *AgentSession) as1PrepareRun(ctx context.Context, trigger BeforeAgentStartTrigger) (agent.AgentMessageList, error) {
	if lastAssistant := s.as1FindLastAssistantMessage(); lastAssistant != nil {
		if _, err := s.as2CheckCompaction(ctx, lastAssistant, false); err != nil {
			return nil, err
		}
	}

	s.mu.Lock()
	baseOpts := s.baseSystemPromptOpts
	s.mu.Unlock()

	selectedToolsBefore := slices.Clone(baseOpts.SelectedTools)
	var beforeRes BeforeAgentStartCombinedResult
	if runner := s.currentExtensionRunner(); runner != nil {
		beforeRes = runner.EmitBeforeAgentStart(ctx, trigger, as1NormalizedToBuildSystemPromptOptions(baseOpts))
	} else {
		beforeRes = BeforeAgentStartCombinedResult{SystemPromptOptions: baseOpts}
	}
	runSysOpts := beforeRes.SystemPromptOptions

	handlerEditedTools := len(runSysOpts.SelectedTools) != len(selectedToolsBefore)
	if !handlerEditedTools {
		for i, name := range runSysOpts.SelectedTools {
			if name != selectedToolsBefore[i] {
				handlerEditedTools = true
				break
			}
		}
	}
	if !handlerEditedTools {
		runSysOpts.SelectedTools = s.GetActiveToolNames()
	}

	var messages agent.AgentMessageList
	if trigger.MessageType == "custom" && trigger.Message != nil {
		messages = append(messages, trigger.Message)
	} else {
		var imgPtrs []*ai.ImageContent
		if len(trigger.Images) > 0 {
			imgPtrs = make([]*ai.ImageContent, len(trigger.Images))
			for i := range trigger.Images {
				cp := trigger.Images[i]
				imgPtrs[i] = &cp
			}
		}
		normImages, hints := s.as1NormalizePromptImages(imgPtrs)
		text := trigger.Prompt
		if len(hints) > 0 {
			text = text + "\n\n" + strings.Join(hints, "\n")
		}
		blocks := make(ai.ContentList, 0, 1+len(normImages))
		blocks = append(blocks, &ai.TextContent{Type: "text", Text: text})
		for _, img := range normImages {
			blocks = append(blocks, img)
		}
		messages = append(messages, &ai.UserMessage{
			Role:      "user",
			Content:   ai.UserMessageContent{Blocks: blocks},
			Timestamp: time.Now().UnixMilli(),
		})
	}

	s.mu.Lock()
	pendingNext := s.pendingNextTurnMessages
	s.pendingNextTurnMessages = nil
	s.mu.Unlock()
	for _, m := range pendingNext {
		messages = append(messages, m)
	}
	for _, cm := range beforeRes.Messages {
		messages = append(messages, CreateCustomMessage(cm.CustomType, cm.Content, cm.Display, cm.Details, ""))
	}

	updateMsg := s.as3PreparePromptAndToolLoadout(&runSysOpts, s.Messages())
	s.mu.Lock()
	s.runSystemPromptOpts = &runSysOpts
	s.mu.Unlock()
	if updateMsg != nil {
		messages = append(agent.AgentMessageList{updateMsg}, messages...)
	}
	return messages, nil
}

func (s *AgentSession) as1HandlePostAgentRun(ctx context.Context) (bool, error) {
	s.mu.Lock()
	msg := s.lastAssistantMessage
	s.lastAssistantMessage = nil
	abortReq := s.agentRunAbortRequested
	if abortReq {
		cancelledAttempt := s.as2FinishCancelledRetryLocked()
		s.mu.Unlock()
		s.as2EmitCancelledRetryIfAny(cancelledAttempt)
		return false, nil
	}
	s.mu.Unlock()

	if msg == nil {
		return s.Agent != nil && s.Agent.HasQueuedMessages(), nil
	}
	if s.as2IsRetryableError(msg) {
		retried, err := s.as2PrepareRetry(ctx, msg)
		if err != nil {
			return false, err
		}
		if retried {
			s.mu.Lock()
			if s.agentRunAbortRequested {
				cancelledAttempt := s.as2FinishCancelledRetryLocked()
				s.mu.Unlock()
				s.as2EmitCancelledRetryIfAny(cancelledAttempt)
				return false, nil
			}
			s.failedResponse = msg
			s.mu.Unlock()
			return true, nil
		}
	}

	s.mu.Lock()
	if s.agentRunAbortRequested {
		cancelledAttempt := s.as2FinishCancelledRetryLocked()
		s.mu.Unlock()
		s.as2EmitCancelledRetryIfAny(cancelledAttempt)
		return false, nil
	}
	failedRetryAttempt := 0
	if msg.StopReason == ai.StopReasonError && s.retryAttempt > 0 {
		failedRetryAttempt = s.retryAttempt
		s.retryAttempt = 0
	}
	s.mu.Unlock()

	if failedRetryAttempt > 0 {
		s.as1Emit(&AgentSessionEventAutoRetryEnd{
			Type:       "auto_retry_end",
			Success:    false,
			Attempt:    failedRetryAttempt,
			FinalError: msg.ErrorMessage,
		})
	}

	willRetryCompaction, err := s.as2CheckCompaction(ctx, msg, true)
	if err != nil {
		return false, err
	}
	if willRetryCompaction {
		s.mu.Lock()
		abortNow := s.agentRunAbortRequested
		s.mu.Unlock()
		return !abortNow, nil
	}

	s.mu.Lock()
	abortNow := s.agentRunAbortRequested
	s.mu.Unlock()
	return !abortNow && s.Agent != nil && s.Agent.HasQueuedMessages(), nil
}

func (s *AgentSession) as1RunBeforeSettleBoundary(ctx context.Context) bool {
	runner := s.currentExtensionRunner()
	if runner == nil || !runner.HasHandlers("agent_before_settle") {
		return s.Agent != nil && s.Agent.HasQueuedMessages()
	}

	s.mu.Lock()
	s.isBeforeSettle = true
	s.abortDuringBeforeSettle = false
	outcome := s.lastActivityOutcome
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.isBeforeSettle = false
		s.mu.Unlock()
	}()

	res, err := runner.EmitBoundary(ctx, BoundaryBaseEvent{
		Type:    "agent_before_settle",
		Outcome: AgentActivityOutcome(outcome),
	}, func(entries []SessionBoundaryDraft) (BoundaryContextPreview, error) {
		return s.as1BuildBoundaryContext(entries, "agent_before_settle"), nil
	})
	if err != nil {
		return false
	}
	s.as1CommitBoundaryDrafts(res.Entries)
	s.as1FlushPendingCustomMessages()
	finalCtx := s.as1BuildBoundaryContext(nil, "agent_before_settle")

	s.mu.Lock()
	aborted := s.abortDuringBeforeSettle
	s.mu.Unlock()
	if aborted {
		return false
	}
	shouldContinue := res.Continue || (s.Agent != nil && s.Agent.HasQueuedMessages())
	if shouldContinue && !finalCtx.CanContinue {
		if res.Continue {
			runner.EmitError(ExtensionError{
				ExtensionPath: "<boundary>",
				Event:         "agent_before_settle",
				Error:         "agent_before_settle requested continuation without runnable model context",
			})
		}
		return false
	}
	return shouldContinue
}

func (s *AgentSession) as1QueueUserInput(ctx context.Context, text string, images []*ai.ImageContent, behavior string, source InputSource) (QueuedInputDisposition, error) {
	if strings.HasPrefix(text, "/") {
		if err := s.as1ThrowIfExtensionCommand(text); err != nil {
			return "", err
		}
	}
	if source == "" {
		source = "interactive"
	}
	streamingBehavior := ""
	if s.IsStreaming() {
		streamingBehavior = behavior
	}
	curText, curImages, proceed, err := s.as1RunInputHandlers(ctx, text, images, source, streamingBehavior)
	if err != nil {
		return "", err
	}
	if !proceed {
		return QueuedInputDispositionHandled, nil
	}
	expanded := s.as3ExpandSkillCommand(curText)
	expanded = ExpandPromptTemplate(expanded, s.PromptTemplates())
	if behavior == "steer" {
		s.as1QueueSteer(expanded, curImages)
	} else {
		s.as1QueueFollowUp(expanded, curImages)
	}
	return QueuedInputDispositionQueued, nil
}

// Steer queues a steering message to interrupt after the current turn's tool execution.
func (s *AgentSession) Steer(ctx context.Context, text string, images []*ai.ImageContent, options ...*QueueInputOptions) (QueuedInputDisposition, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var src InputSource = "interactive"
	if len(options) > 0 && options[0] != nil && options[0].Source != "" {
		src = options[0].Source
	}
	return s.as1QueueUserInput(ctx, text, images, "steer", src)
}

// FollowUp queues a follow-up message to run after the agent finishes without more tool calls.
func (s *AgentSession) FollowUp(ctx context.Context, text string, images []*ai.ImageContent, options ...*QueueInputOptions) (QueuedInputDisposition, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var src InputSource = "interactive"
	if len(options) > 0 && options[0] != nil && options[0].Source != "" {
		src = options[0].Source
	}
	return s.as1QueueUserInput(ctx, text, images, "followUp", src)
}

func (s *AgentSession) as1BuildQueuedUserMessage(text string, images []*ai.ImageContent) *ai.UserMessage {
	blocks := make(ai.ContentList, 0, 1+len(images))
	blocks = append(blocks, &ai.TextContent{Type: "text", Text: text})
	for _, img := range images {
		if img != nil {
			blocks = append(blocks, img)
		}
	}
	return &ai.UserMessage{
		Role:      "user",
		Content:   ai.UserMessageContent{Blocks: blocks},
		Timestamp: time.Now().UnixMilli(),
	}
}

func (s *AgentSession) as1QueueSteer(text string, images []*ai.ImageContent) {
	msg := s.as1BuildQueuedUserMessage(text, images)
	s.mu.Lock()
	s.steeringMessages = append(s.steeringMessages, text)
	if len(images) > 0 {
		if key, ok := as1MapKey(msg); ok {
			s.queuedMessagesWithImage[key] = struct{}{}
		}
	}
	s.mu.Unlock()
	s.as1EmitQueueUpdate()
	if s.Agent != nil {
		s.Agent.Steer(msg)
	}
}

func (s *AgentSession) as1QueueFollowUp(text string, images []*ai.ImageContent) {
	msg := s.as1BuildQueuedUserMessage(text, images)
	s.mu.Lock()
	s.followUpMessages = append(s.followUpMessages, text)
	if len(images) > 0 {
		if key, ok := as1MapKey(msg); ok {
			s.queuedMessagesWithImage[key] = struct{}{}
		}
	}
	s.mu.Unlock()
	s.as1EmitQueueUpdate()
	if s.Agent != nil {
		s.Agent.FollowUp(msg)
	}
}

// SendCustomMessage appends or queues a custom message entry, optionally triggering a turn.
func (s *AgentSession) SendCustomMessage(
	ctx context.Context,
	customType string,
	content any,
	display bool,
	details any,
	options ...*SendCustomMessageOptions,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	var opts SendCustomMessageOptions
	if len(options) > 0 && options[0] != nil {
		opts = *options[0]
	}
	if content == nil {
		content = ai.ContentList{}
	}
	appMsg := CreateCustomMessage(customType, content, display, details, "")
	switch {
	case opts.DeliverAs == "nextTurn":
		s.mu.Lock()
		s.pendingNextTurnMessages = append(s.pendingNextTurnMessages, appMsg)
		s.mu.Unlock()
	case s.IsStreaming() && opts.TriggerTurn:
		if opts.DeliverAs == "followUp" {
			if s.Agent != nil {
				s.Agent.FollowUp(appMsg)
			}
		} else {
			if s.Agent != nil {
				s.Agent.Steer(appMsg)
			}
		}
	case s.IsStreaming() && !opts.TriggerTurn:
		// Wait: matches TS behavior where triggerTurn !== false steers/follows-up if DeliverAs != "nextTurn",
		// while explicit TriggerTurn:false during streaming defers until turn_end.
		// Note Go zero value TriggerTurn==false requires checking DeliverAs!
		if opts.DeliverAs == "followUp" {
			if s.Agent != nil {
				s.Agent.FollowUp(appMsg)
			}
		} else if opts.DeliverAs == "steer" {
			if s.Agent != nil {
				s.Agent.Steer(appMsg)
			}
		} else {
			s.mu.Lock()
			s.pendingCustomMessages = append(s.pendingCustomMessages, appMsg)
			s.mu.Unlock()
		}
	case opts.TriggerTurn:
		s.mu.Lock()
		if s.isEmittingAgentSettled {
			s.deferredSettledActions = append(s.deferredSettledActions, func(c context.Context) error {
				return s.SendCustomMessage(c, customType, content, display, details, &opts)
			})
			s.mu.Unlock()
			return nil
		}
		s.mu.Unlock()
		return s.as1StartRun(ctx, BeforeAgentStartTrigger{MessageType: "custom", Message: appMsg}, nil)
	default:
		s.as1AppendCustomMessage(appMsg)
	}
	return nil
}

func (s *AgentSession) as1AppendCustomMessage(appMsg *CustomMessage) {
	if appMsg == nil {
		return
	}
	if s.SessionManager != nil {
		s.SessionManager.AppendCustomMessageEntry(appMsg.CustomType, appMsg.Content, appMsg.Display, appMsg.Details)
	}
	s.as1RefreshFinalizedContext()
	s.as1Emit(&AgentSessionEventMessageStart{Type: "message_start", Message: appMsg})
	s.as1Emit(&AgentSessionEventMessageEnd{Type: "message_end", Message: appMsg})
}

func (s *AgentSession) as1FlushPendingCustomMessages() {
	s.mu.Lock()
	if len(s.pendingCustomMessages) == 0 {
		s.mu.Unlock()
		return
	}
	pending := s.pendingCustomMessages
	s.pendingCustomMessages = nil
	s.mu.Unlock()

	for _, msg := range pending {
		s.as1AppendCustomMessage(msg)
	}
}

// SendUserMessage sends a user text/content message, always triggering a turn
// (or queuing when streaming).
func (s *AgentSession) SendUserMessage(ctx context.Context, text string, images []*ai.ImageContent, options ...*SendUserMessageOptions) error {
	var opts SendUserMessageOptions
	if len(options) > 0 && options[0] != nil {
		opts = *options[0]
	}
	expand := opts.ExpandPromptTemplates
	return s.Prompt(ctx, text, &PromptOptions{
		ExpandPromptTemplates: &expand,
		StreamingBehavior:     opts.DeliverAs,
		Images:                images,
		Source:                "extension",
	})
}

// ClearQueue clears all queued steering and follow-up messages and returns the
// previously queued texts.
func (s *AgentSession) ClearQueue() QueuedMessagesText {
	s.mu.Lock()
	steering := slices.Clone(s.steeringMessages)
	followUp := slices.Clone(s.followUpMessages)
	s.steeringMessages = nil
	s.followUpMessages = nil
	s.mu.Unlock()
	if s.Agent != nil {
		s.Agent.ClearAllQueues()
	}
	s.as1EmitQueueUpdate()
	return QueuedMessagesText{
		Steering: steering,
		FollowUp: followUp,
	}
}

// PendingMessageCount returns the total number of queued steering + follow-up texts.
func (s *AgentSession) PendingMessageCount() int {
	s.mu.Lock()
	n := len(s.steeringMessages) + len(s.followUpMessages)
	s.mu.Unlock()
	return n
}

// GetSteeringMessages returns a snapshot copy of pending steering texts.
func (s *AgentSession) GetSteeringMessages() []string {
	s.mu.Lock()
	out := slices.Clone(s.steeringMessages)
	s.mu.Unlock()
	return out
}

// GetFollowUpMessages returns a snapshot copy of pending follow-up texts.
func (s *AgentSession) GetFollowUpMessages() []string {
	s.mu.Lock()
	out := slices.Clone(s.followUpMessages)
	s.mu.Unlock()
	return out
}

// Abort cancels the active run, compaction, branch summary, and retry, then waits until idle.
func (s *AgentSession) Abort(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	if s.isAgentRunActive {
		s.agentRunAbortRequested = true
	}
	if s.isBeforeSettle {
		s.abortDuringBeforeSettle = true
	}
	s.mu.Unlock()

	s.AbortRetry()
	s.AbortCompaction()
	s.AbortBranchSummary()
	if s.Agent != nil {
		s.Agent.Abort()
	}
	return s.WaitForIdle(ctx)
}

// WaitForIdle blocks until the session is idle or ctx is cancelled.
func (s *AgentSession) WaitForIdle(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	if s.as1IsIdleLocked() {
		s.mu.Unlock()
		return nil
	}
	if s.idleCh == nil {
		s.idleCh = make(chan struct{})
	}
	ch := s.idleCh
	s.mu.Unlock()

	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ============================================================================
// AgentSessionRuntime Integration Helpers
// ============================================================================

func (s *AgentSession) emitBeforeSwitch(ctx context.Context, reason string, targetSessionFile *string) (bool, error) {
	runner := s.currentExtensionRunner()
	if runner == nil || !runner.HasHandlers("session_before_switch") {
		return false, nil
	}
	res := runner.Emit(ctx, SessionBeforeSwitchEvent{
		Type:              "session_before_switch",
		Reason:            reason,
		TargetSessionFile: targetSessionFile,
	})
	if swRes, ok := res.(*SessionBeforeSwitchResult); ok && swRes != nil {
		return swRes.Cancel, nil
	}
	return false, nil
}

func (s *AgentSession) emitBeforeFork(ctx context.Context, entryId string, position string) (bool, error) {
	runner := s.currentExtensionRunner()
	if runner == nil || !runner.HasHandlers("session_before_fork") {
		return false, nil
	}
	res := runner.Emit(ctx, SessionBeforeForkEvent{
		Type:     "session_before_fork",
		EntryId:  entryId,
		Position: position,
	})
	if fkRes, ok := res.(*SessionBeforeForkResult); ok && fkRes != nil {
		return fkRes.Cancel, nil
	}
	return false, nil
}

func (s *AgentSession) emitShutdown(reason string, targetSessionFile *string) {
	runner := s.currentExtensionRunner()
	if runner == nil {
		return
	}
	_ = EmitSessionShutdownEvent(context.Background(), runner, SessionShutdownEvent{
		Type:              "session_shutdown",
		Reason:            reason,
		TargetSessionFile: targetSessionFile,
	})
}

func as1NormalizedToBuildSystemPromptOptions(opts NormalizedBuildSystemPromptOptions) BuildSystemPromptOptions {
	return erNormalizedToBuildSystemPromptOptions(opts)
}

func (s *AgentSession) settleForReplacement(ctx context.Context) {
	_ = s.Abort(ctx)
	s.AbortBash()
	for {
		s.mu.Lock()
		pending := s.promptPreflightCount > 0 || !s.as1IsIdleLocked()
		s.mu.Unlock()
		if !pending {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Millisecond):
		}
	}
}

func as1ExtractUserMessageText(msg agent.AgentMessage) (string, bool) {
	um, ok := msg.(*ai.UserMessage)
	if !ok || um == nil {
		return "", false
	}
	text := ai.ContentText(um.Content, "")
	return text, true
}

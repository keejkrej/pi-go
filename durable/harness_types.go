// Ported from packages/durable/src/harness/types.ts (pi v1.0.0).

package durable

import (
	"context"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable/env"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/typebox"
)

// ModelRef identifies a provider and model ID resolved through ai.Models.
type ModelRef struct {
	Provider string `json:"provider"`
	ModelId  string `json:"modelId"`
}

// UserInput is user message content accepted by a submission or prompt hook.
type UserInput = ai.UserMessageContent

// SubmissionWhenBusy selects how a user input behaves when a conversation already has an active run.
type SubmissionWhenBusy string

const (
	SubmissionWhenBusySteer    SubmissionWhenBusy = "steer"
	SubmissionWhenBusyFollowUp SubmissionWhenBusy = "followUp"
	SubmissionWhenBusyReject   SubmissionWhenBusy = "reject"
)

// SubmissionDraft admits either user input that may start a run or a passive entry write.
type SubmissionDraft struct {
	RequestId string             `json:"requestId,omitzero"`
	Type      SubmissionType     `json:"type"`
	Content   UserInput          `json:"content,omitzero"`
	WhenBusy  SubmissionWhenBusy `json:"whenBusy,omitzero"`
	Entry     *EntryDraft        `json:"entry,omitzero"`
}

// InputSubmissionDraft creates a user-input SubmissionDraft accepted by ConversationHandle.Submit.
type InputSubmissionDraft struct {
	RequestId string             `json:"requestId,omitzero"`
	Content   UserInput          `json:"content"`
	WhenBusy  SubmissionWhenBusy `json:"whenBusy,omitzero"`
}

// SettledSubmissionRecord aliases SubmissionRecord when settled ("done" or "unanswered").
type SettledSubmissionRecord = SubmissionRecord

// SubmissionAbortResult reports the outcome of Submission.Abort or Harness.AbortSubmission.
type SubmissionAbortResult string

const (
	SubmissionAbortAborted       SubmissionAbortResult = "aborted"
	SubmissionAbortAlreadyPlaced SubmissionAbortResult = "already_placed"
	SubmissionAbortSettled       SubmissionAbortResult = "settled"
	SubmissionAbortNotFound      SubmissionAbortResult = "not_found"
)

// Submission is an awaitable host handle for one durably admitted submission.
type Submission interface {
	Id() SubmissionId
	Status(ctx chord.Context) (*SubmissionRecord, error)
	Wait(ctx chord.Context) (*SettledSubmissionRecord, error)
	Abort(ctx chord.Context) (SubmissionAbortResult, error)
}

// SettledTask aliases a terminal TaskRecord returned by WaitForTask.
type SettledTask = TaskRecord

// ConversationAbortOptions configures Conversation.Abort().
type ConversationAbortOptions struct {
	// Background crosses background boundaries, aborting live background work present at admission.
	Background bool `json:"background,omitzero"`
}

// ConversationHandle provides invocation-bound conversation operations for tasks and tools.
type ConversationHandle interface {
	Id() ConversationId
	Submit(submission InputSubmissionDraft, ctx chord.Context) (Submission, error)
	Abort(ctx chord.Context, options ...ConversationAbortOptions) error
	WaitForIdle(ctx chord.Context) error
}

// ToolControl requests post-round tool effects on a conversation.
type ToolControl struct {
	AddTools  []string `json:"addTools,omitzero"`
	Terminate bool     `json:"terminate,omitzero"`
	Handoff   *string  `json:"handoff,omitzero"`
}

// ToolDiagnosticSeverity is the severity of a ToolDiagnostic.
type ToolDiagnosticSeverity string

const (
	ToolDiagnosticInfo  ToolDiagnosticSeverity = "info"
	ToolDiagnosticWarn  ToolDiagnosticSeverity = "warn"
	ToolDiagnosticError ToolDiagnosticSeverity = "error"
)

// ToolDiagnostic is a model-visible remark about a tool call, separate from its data.
type ToolDiagnostic struct {
	Severity ToolDiagnosticSeverity `json:"severity"`
	Message  string                 `json:"message"`
	Code     *string                `json:"code,omitzero"`
}

// ToolExecutionResult is the output produced by a tool execution.
type ToolExecutionResult struct {
	Content     ai.ContentList   `json:"content,omitzero"`
	HasContent  bool             `json:"-"`
	IsError     bool             `json:"isError,omitzero"`
	Details     JsonValue        `json:"details,omitzero"`
	HasDetails  bool             `json:"-"`
	Diagnostics []ToolDiagnostic `json:"diagnostics,omitzero"`
	Usage       *ai.Usage        `json:"usage,omitzero"`
	Control     *ToolControl     `json:"control,omitzero"`
}

// ToolExecutionMode selects whether tools in a round execute concurrently or one after another.
type ToolExecutionMode string

const (
	ToolExecutionModeParallel   ToolExecutionMode = "parallel"
	ToolExecutionModeSequential ToolExecutionMode = "sequential"
)

// QueueMode selects how queued steering or follow-up items drain at a boundary.
type QueueMode string

const (
	QueueModeAll        QueueMode = "all"
	QueueModeOneAtATime QueueMode = "one-at-a-time"
)

// ToolReplayPolicy selects whether an interrupted tool call may rerun on recovery.
type ToolReplayPolicy string

const (
	ToolReplayUnsafe ToolReplayPolicy = "unsafe"
	ToolReplaySafe   ToolReplayPolicy = "safe"
)

// ToolOutputRetain selects whether truncation keeps the head or tail of tool output.
type ToolOutputRetain string

const (
	ToolOutputRetainHead ToolOutputRetain = "head"
	ToolOutputRetainTail ToolOutputRetain = "tail"
)

// ToolOutputLimits configures retained running output truncation limits.
type ToolOutputLimits struct {
	MaxBytes *int             `json:"maxBytes,omitzero"`
	MaxLines *int             `json:"maxLines,omitzero"`
	Retain   ToolOutputRetain `json:"retain,omitzero"`
}

// ToolExecutionApi exposes operations available to one tool invocation.
type ToolExecutionApi interface {
	DocumentObserver
	DocumentReader

	TaskId() TaskId
	ConversationId() ConversationId
	CallId() string
	Registry() RegistrySnapshot
	Agent(ctx chord.Context) (*Agent, error)
	Env(ctx chord.Context) (env.ExecutionEnv, error)
	Output(text string)
	OutputBytes(data []byte)
	Diagnostic(diagnostic ToolDiagnostic)
	Details(value JsonValue, ctx chord.Context) error
	Commit(ctx chord.Context, change func(tx Tx) (any, error)) (any, error)
	Memo(ctx chord.Context, name string) (JsonValue, bool, error)
	MemoPut(ctx chord.Context, name string, candidate JsonValue) (JsonValue, error)
	CreateTask(ctx chord.Context, task AnyTask, input any, options TaskOptions) (TaskId, error)
	GetTask(ctx chord.Context, id TaskId) (*TaskRecord, error)
	WaitForTask(ctx chord.Context, id TaskId) (*SettledTask, error)
	Conversation(ctx chord.Context, id ConversationId) (ConversationHandle, error)
}

// ToolExecuteFn executes a validated tool call.
type ToolExecuteFn func(args any, api ToolExecutionApi, ctx chord.Context) (*ToolExecutionResult, error)

// ToolRegistration describes an executable tool registered in an Extension.
type ToolRegistration struct {
	Name                string                          `json:"name"`
	Description         string                          `json:"description"`
	Parameters          *typebox.Schema                 `json:"parameters"`
	ConstrainedSampling ai.ToolConstrainedSamplingValue `json:"constrainedSampling,omitzero"`
	Replay              ToolReplayPolicy                `json:"replay,omitzero"`
	ExecutionMode       ToolExecutionMode               `json:"executionMode,omitzero"`
	PrepareArguments    func(args any) any              `json:"-"`
	OutputLimits        *ToolOutputLimits               `json:"outputLimits,omitzero"`
	Execute             ToolExecuteFn                   `json:"-"`
}

// AiTool returns the model-facing ai.Tool declaration for this registration.
func (t *ToolRegistration) AiTool() *ai.Tool {
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

// PromptInput carries resolved state passed into PromptSection rendering.
type PromptInput struct {
	ConversationId ConversationId
	Agent          *Agent
	Env            env.ExecutionEnv
	Shown          map[string]string
	Read           DocumentReader
}

// PromptRenderFn renders a section's text; ok=false omits the section.
type PromptRenderFn func(input PromptInput, ctx chord.Context) (text string, ok bool, err error)

// PromptSection is one system prompt section rendered in order before each request.
type PromptSection struct {
	Key    string
	Render PromptRenderFn
	// Tag wraps rendered text as <key>\n...\n</key> when nil or true.
	Tag *bool
}

// HookRegistration associates a task name with handler functions built by Hook().
type HookRegistration struct {
	Task     string
	Handlers any
}

// ToolWrapFn wraps a tool definition into a replacement definition with the same name.
type ToolWrapFn func(tool *ToolRegistration) *ToolRegistration

// SectionWrapFn wraps a prompt section definition into a replacement section with the same key.
type SectionWrapFn func(section *PromptSection) *PromptSection

// Wrap targets a tool name or section key inside a Registry extension resolution.
type Wrap struct {
	Tool        string
	WrapTool    ToolWrapFn
	Section     string
	WrapSection SectionWrapFn
}

// Extension is a named bundle of tools, prompt sections, hooks, wraps, and custom tasks.
type Extension struct {
	Name     string
	Tools    []*ToolRegistration
	Sections []*PromptSection
	Hooks    []HookRegistration
	Wraps    []Wrap
	Tasks    []AnyTask
}

// RegistryTool pairs an installed tool with the extension that owns it.
type RegistryTool struct {
	Extension *Extension
	Tool      *ToolRegistration
}

// RegistrySection pairs an installed prompt section with the extension that owns it.
type RegistrySection struct {
	Extension *Extension
	Section   *PromptSection
}

// RegistrySnapshot is an immutable view of one published registry state.
type RegistrySnapshot interface {
	Installed() []*Extension
	Extension(name string) *Extension
	Tools() []RegistryTool
	Sections() []RegistrySection
	Tasks() []AnyTask
	Task(name string) AnyTask
}

// RegistryReader exposes read and subscribe access to an Extension registry.
type RegistryReader interface {
	Snapshot() RegistrySnapshot
	Subscribe(listener func()) func()
}

// Registry is an application-owned mutable extension registry.
type Registry interface {
	RegistryReader
	Install(extension *Extension) error
	Uninstall(extension *Extension)
}

// AgentExtensionsState represents stored pi.agent.extensions: either an explicit list or an {add, remove} edit.
type AgentExtensionsState struct {
	List   []string `json:"list,omitzero"`
	Add    []string `json:"add,omitzero"`
	Remove []string `json:"remove,omitzero"`
}

// AgentToolsState represents stored pi.agent.tools: either an explicit list or a {remove} filter.
type AgentToolsState struct {
	List   []string `json:"list,omitzero"`
	Remove []string `json:"remove,omitzero"`
}

// AgentState is the persisted document shape of pi.agent for one conversation.
type AgentState struct {
	Model         *ModelRef              `json:"model,omitzero"`
	ThinkingLevel *ai.ModelThinkingLevel `json:"thinkingLevel,omitzero"`
	Extensions    *AgentExtensionsState  `json:"extensions,omitzero"`
	Tools         *AgentToolsState       `json:"tools,omitzero"`
	Instructions  *string                `json:"instructions,omitzero"`
	Cwd           *string                `json:"cwd,omitzero"`
}

// AgentExtensionsChange sets or edits pi.agent.extensions inside AgentChange.
// Non-nil List replaces stored selection; nil List treats Add/Remove as an edit to host defaults.
type AgentExtensionsChange struct {
	List   []*Extension
	Add    []*Extension
	Remove []*Extension
}

// AgentToolsChange sets or filters pi.agent.tools inside AgentChange.
// Non-nil List replaces the tool filter; nil List filters out Remove.
type AgentToolsChange struct {
	List   []*ToolRegistration
	Remove []*ToolRegistration
}

// AgentChange updates pi.agent: Absent keeps current field, Null clears it, Some sets it.
type AgentChange struct {
	Model         jsonx.Opt[ModelRef]
	ThinkingLevel jsonx.Opt[ai.ModelThinkingLevel]
	Extensions    jsonx.Opt[AgentExtensionsChange]
	Tools         jsonx.Opt[AgentToolsChange]
	Instructions  jsonx.Opt[string]
	Cwd           jsonx.Opt[string]
}

// Agent is a conversation's resolved agent against a RegistrySnapshot and Settings.
type Agent struct {
	Model         *ModelRef
	ThinkingLevel ai.ModelThinkingLevel
	Extensions    []*Extension
	Tools         []*ToolRegistration
	Sections      []*PromptSection
	Instructions  *string
	Cwd           *string
}

// ConversationInit runs inside a creating commit after creation hook and agent config.
type ConversationInit func(tx Tx, conversationId ConversationId) error

// ConversationCreateOptions configures Harness.CreateConversation or Conversation.Fork.
type ConversationCreateOptions struct {
	Ownership ConversationOwnership
	Agent     *AgentChange
	Init      ConversationInit
}

// RootOptions configures Harness.Root.
type RootOptions struct {
	Agent *AgentChange
	Init  ConversationInit
}

// ConversationDeferredOptions configures deferred response execution window.
type ConversationDeferredOptions struct {
	Enabled bool
	Window  string // "15m" | "1h" | "24h"
}

// ConversationStreamOptions holds curated pi-ai request options; nil fields use defaults.
type ConversationStreamOptions struct {
	Transport       *ai.Transport                `json:"transport,omitzero"`
	TimeoutMs       *int64                       `json:"timeoutMs,omitzero"`
	MaxRetries      *int                         `json:"maxRetries,omitzero"`
	MaxRetryDelayMs *int64                       `json:"maxRetryDelayMs,omitzero"`
	Headers         map[string]string            `json:"headers,omitzero"`
	Metadata        JsonObject                   `json:"metadata,omitzero"`
	CacheRetention  *ai.CacheRetention           `json:"cacheRetention,omitzero"`
	Deferred        *ConversationDeferredOptions `json:"deferred,omitzero"`
}

// ConversationRetryPolicy configures durable generation attempt retries.
type ConversationRetryPolicy struct {
	Enabled         bool   `json:"enabled"`
	MaxRetries      int    `json:"maxRetries"`
	BaseDelayMs     int64  `json:"baseDelayMs"`
	MaxAgentDelayMs *int64 `json:"maxAgentDelayMs,omitzero"`
}

// ConversationRetryPolicyPatch provides optional overrides over DEFAULT_RETRY_POLICY.
type ConversationRetryPolicyPatch struct {
	Enabled         *bool  `json:"enabled,omitzero"`
	MaxRetries      *int   `json:"maxRetries,omitzero"`
	BaseDelayMs     *int64 `json:"baseDelayMs,omitzero"`
	MaxAgentDelayMs *int64 `json:"maxAgentDelayMs,omitzero"`
}

// CompactionPolicy configures automatic compaction thresholds.
type CompactionPolicy struct {
	Enabled          bool `json:"enabled"`
	ReserveTokens    int  `json:"reserveTokens"`
	KeepRecentTokens int  `json:"keepRecentTokens"`
	BackgroundTokens int  `json:"backgroundTokens"`
}

// CompactionPolicyPatch provides optional overrides over DEFAULT_COMPACTION_POLICY.
type CompactionPolicyPatch struct {
	Enabled          *bool `json:"enabled,omitzero"`
	ReserveTokens    *int  `json:"reserveTokens,omitzero"`
	KeepRecentTokens *int  `json:"keepRecentTokens,omitzero"`
	BackgroundTokens *int  `json:"backgroundTokens,omitzero"`
}

// CompactionReason describes why a compaction runs.
type CompactionReason string

const (
	CompactionReasonManual    CompactionReason = "manual"
	CompactionReasonThreshold CompactionReason = "threshold"
	CompactionReasonOverflow  CompactionReason = "overflow"
)

// CompactionResult holds the terminal receipt of a compaction task.
type CompactionResult struct {
	EntryId      *EntryId      `json:"entryId,omitzero"`
	SubmissionId *SubmissionId `json:"submissionId,omitzero"`
}

// HarnessSettings configures Harness-wide defaults. Read dynamically; must remain safe on the Session line.
type HarnessSettings struct {
	Extensions    []*Extension                  `json:"-"`
	HasExtensions bool                          `json:"-"`
	Stream        *ConversationStreamOptions    `json:"stream,omitzero"`
	Retry         *ConversationRetryPolicyPatch `json:"retry,omitzero"`
	Compaction    *CompactionPolicyPatch        `json:"compaction,omitzero"`
	ToolExecution ToolExecutionMode             `json:"toolExecution,omitzero"`
	SteeringMode  QueueMode                     `json:"steeringMode,omitzero"`
	FollowUpMode  QueueMode                     `json:"followUpMode,omitzero"`
}

// Settings holds resolved settings over built-in defaults.
type Settings struct {
	Extensions    []*Extension
	HasExtensions bool
	Stream        ConversationStreamOptions
	Retry         ConversationRetryPolicy
	Compaction    CompactionPolicy
	ToolExecution ToolExecutionMode
	SteeringMode  QueueMode
	FollowUpMode  QueueMode
}

// EnvTarget identifies the conversation target passed to HarnessOptions.Env.
type EnvTarget struct {
	ConversationId ConversationId
	Cwd            *string
	Read           DocumentReader
}

// HarnessOptions configures HarnessOpen.
type HarnessOptions struct {
	Models              ai.Models
	Registry            RegistryReader
	Settings            func() HarnessSettings // Optional dynamic settings supplier; if nil uses StaticSettings.
	StaticSettings      *HarnessSettings
	Env                 func(target EnvTarget, ctx chord.Context) (env.ExecutionEnv, error)
	ConversationCreated func(tx Tx, conversation ConversationRecord) error
	Now                 func() int64
	OnReport            func(error)
}

// TaskInspectionKind classifies what the scheduler would do with a live task under the current registry.
type TaskInspectionKind string

const (
	TaskInspectionRunning    TaskInspectionKind = "running"
	TaskInspectionReady      TaskInspectionKind = "ready"
	TaskInspectionWaiting    TaskInspectionKind = "waiting"
	TaskInspectionCompleting TaskInspectionKind = "completing"
	TaskInspectionBlocked    TaskInspectionKind = "blocked"
)

// TaskBlockedReason identifies why a live task is blocked in TaskInspection.
type TaskBlockedReason string

const (
	TaskBlockedMissingTask     TaskBlockedReason = "missing_task"
	TaskBlockedTaskTooOld      TaskBlockedReason = "task_too_old"
	TaskBlockedMigrationFailed TaskBlockedReason = "migration_failed"
)

// TaskInspectionState describes one live task's scheduler state.
type TaskInspectionState struct {
	Kind     TaskInspectionKind `json:"kind"`
	Migrates bool               `json:"migrates,omitzero"`
	On       []TaskId           `json:"on,omitzero"`
	Reason   TaskBlockedReason  `json:"reason,omitzero"`
	Error    error              `json:"-"`
}

// TaskInspection pairs a live task record with its scheduler inspection state.
type TaskInspection struct {
	Record TaskRecord          `json:"record"`
	State  TaskInspectionState `json:"state"`
}

// HarnessSchedulingState describes whether the task scheduler is paused, running, or closing.
type HarnessSchedulingState string

const (
	HarnessSchedulingPaused  HarnessSchedulingState = "paused"
	HarnessSchedulingRunning HarnessSchedulingState = "running"
	HarnessSchedulingClosing HarnessSchedulingState = "closing"
)

// HarnessInspection is a point-in-time snapshot of live tasks and unsettled submissions.
type HarnessInspection struct {
	Scheduling  HarnessSchedulingState `json:"scheduling"`
	Tasks       []TaskInspection       `json:"tasks"`
	Submissions []SubmissionRecord     `json:"submissions"`
}

// ContextView holds a conversation's derived active transcript and model messages.
type ContextView struct {
	Head          *EntryRecord   `json:"head,omitzero"`
	Entries       []EntryRecord  `json:"entries"`
	Contributions [][]ai.Message `json:"contributions"`
	Messages      []ai.Message   `json:"messages"`
}

// ConversationEntryQuery bounds a newest-first fork-aware scan of a single conversation's history.
type ConversationEntryQuery struct {
	MinEntryId *EntryId `json:"minEntryId,omitzero"`
	MaxEntryId *EntryId `json:"maxEntryId,omitzero"`
}

// ConversationWatch aliases WatchHandle[ConversationView].
type ConversationWatch = WatchHandle[ConversationView]

// Conversation is a stateless handle for one conversation bound to its Harness.
type Conversation interface {
	Id() ConversationId
	Agent(ctx chord.Context) (*Agent, error)
	Configure(change AgentChange, ctx chord.Context) error
	Submit(submission SubmissionDraft, ctx chord.Context) (Submission, error)
	Reset(handoff *string, ctx chord.Context) error
	Compact(instructions *string, ctx chord.Context) (TaskId, error)
	Commit(ctx chord.Context, change func(tx Tx) (any, error)) (any, error)
	Context(ctx chord.Context) (*ContextView, error)
	Entries(query ConversationEntryQuery, limit int, cursor Cursor, ctx chord.Context) (Page[EntryRecord, Cursor], error)
	Fork(at EntryId, options ConversationCreateOptions, ctx chord.Context) (Conversation, error)
	Abort(ctx chord.Context, options ...ConversationAbortOptions) error
	WaitForIdle(ctx chord.Context) error
	ViewState(ctx chord.Context) (chord.AttachedReplicatedState[ConversationView], error)
	Watch(ctx chord.Context) (ConversationWatch, error)
}

// TaskAbortResult reports whether Harness.AbortTask marked a task or found it already terminal.
type TaskAbortResult string

const (
	TaskAbortMarked   TaskAbortResult = "marked"
	TaskAbortTerminal TaskAbortResult = "terminal"
)

// Harness is a durable agent harness layered over a Session.
type Harness interface {
	Session

	Resume() error
	Root(ctx chord.Context, options ...RootOptions) (Conversation, error)
	Conversation(id ConversationId, ctx chord.Context) (Conversation, error)
	CreateConversation(options ConversationCreateOptions, ctx chord.Context) (Conversation, error)
	GetTask(ctx chord.Context, id TaskId) (*TaskRecord, error)
	Inspect(ctx chord.Context) (*HarnessInspection, error)
	Submission(ctx chord.Context, id SubmissionId) (Submission, error)
	AbortSubmission(ctx chord.Context, id SubmissionId, conversationId ...ConversationId) (SubmissionAbortResult, error)
	AbortTask(ctx chord.Context, id TaskId) (TaskAbortResult, error)
	WaitForTask(ctx chord.Context, id TaskId) (*SettledTask, error)
	WaitForIdle(ctx chord.Context) error
	Usage(ctx chord.Context) (*UsageState, error)
	TaskGraph(ctx chord.Context) (chord.AttachedReplicatedState[TaskGraph], error)
	WatchTaskGraph(ctx chord.Context) (TaskGraphWatch, error)
}

// HookApi exposes committed document reads and task memos shared between a task and its hooks.
type HookApi interface {
	DocumentReader

	TaskId() TaskId
	ConversationId() ConversationId
	Memo(ctx chord.Context, name string) (JsonValue, bool, error)
	MemoPut(ctx chord.Context, name string, candidate JsonValue) (JsonValue, error)
}

// GenerationRequest is passed to GenerationHooks.BeforeRequest.
type GenerationRequest struct {
	Messages []ai.Message
}

// GenerationYieldDecision is returned by GenerationHooks.OnYield to continue the run.
type GenerationYieldDecision struct {
	Continue UserInput
}

// GenerationHooks defines hooks of the built-in generation task. All fields are optional.
type GenerationHooks struct {
	BeforeRequest func(request GenerationRequest, api HookApi, ctx chord.Context) (*GenerationRequest, error)
	AfterResponse func(message *ai.AssistantMessage, api HookApi, ctx chord.Context) error
	OnYield       func(answer *ai.AssistantMessage, api HookApi, ctx chord.Context) (*GenerationYieldDecision, error)
	AfterTools    func(assistant EntryId, results []EntryId, api HookApi, ctx chord.Context) error
}

// BeforeToolDecision is returned by ToolHooks.BeforeTool to replace arguments or block the call.
type BeforeToolDecision struct {
	Arguments JsonObject
	Block     *string
}

// ToolHooks defines hooks of the built-in tool task. All fields are optional.
type ToolHooks struct {
	BeforeTool func(call *ai.ToolCall, api HookApi, ctx chord.Context) (*BeforeToolDecision, error)
	AfterTool  func(call *ai.ToolCall, result *ToolExecutionResult, api HookApi, ctx chord.Context) (*ToolExecutionResult, error)
}

// CompactionHookInput describes a selected compaction range passed to CompactionHooks.BeforeCompact.
type CompactionHookInput struct {
	Reason       CompactionReason
	Entries      []EntryRecord
	Messages     []ai.Message
	FirstKept    EntryId
	Instructions *string
}

// BeforeCompactDecision is returned by CompactionHooks.BeforeCompact to decline or supply a custom summary.
type BeforeCompactDecision struct {
	Decline bool
	Summary *string
}

// CompactionHooks defines hooks of the built-in compaction task. All fields are optional.
type CompactionHooks struct {
	BeforeCompact func(compaction CompactionHookInput, api HookApi, ctx chord.Context) (*BeforeCompactDecision, error)
}

var _ context.Context = nil

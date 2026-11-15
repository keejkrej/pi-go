// Ported from packages/coding-agent/src/core/extensions/types.ts (pi v1.0.0).

package core

import (
	"context"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/typebox"
	"github.com/keejkrej/pi-go/tui"
)

// NoResult marks extension event handlers that return no result payload.
type NoResult = struct{}

// ExtensionUIDialogOptions configures blocking extension UI dialogs.
type ExtensionUIDialogOptions struct {
	Ctx     context.Context
	Timeout *int64
}

// WidgetPlacement controls where an extension widget renders relative to the editor.
type WidgetPlacement string

const (
	WidgetPlacementAboveEditor WidgetPlacement = "aboveEditor"
	WidgetPlacementBelowEditor WidgetPlacement = "belowEditor"
)

// ExtensionWidgetOptions configures a UI widget.
type ExtensionWidgetOptions struct {
	Placement WidgetPlacement
}

// TerminalInputResult is returned by TerminalInputHandler to optionally consume or rewrite input.
type TerminalInputResult struct {
	Consume bool
	Data    *string
}

// TerminalInputHandler intercepts raw terminal input in interactive mode.
type TerminalInputHandler func(data string) *TerminalInputResult

// WorkingIndicatorOptions configures the interactive streaming spinner/indicator.
type WorkingIndicatorOptions struct {
	Frames     []string
	IntervalMs *int64
}

// AutocompleteProviderFactory wraps or replaces the editor autocomplete provider.
type AutocompleteProviderFactory func(current tui.AutocompleteProvider) tui.AutocompleteProvider

// EditorFactory constructs a custom editor component.
type EditorFactory func(t tui.TUI, editorTheme tui.EditorTheme, keybindings *tui.KeybindingsManager) tui.EditorComponent

// KeybindingHints formats the host's configured keybindings for display.
type KeybindingHints struct {
	KeyText func(keybinding tui.Keybinding) string
	KeyHint func(keybinding tui.Keybinding, description string) string
}

// ExtensionCustomComponent is a UI component with an optional Dispose lifecycle method.
type ExtensionCustomComponent interface {
	tui.Component
}

// ExtensionCustomBlocked describes blocked program status while a custom UI prompt waits for input.
type ExtensionCustomBlocked struct {
	Kind    tui.ProgramStatusKind
	Message string
}

// ExtensionCustomOptions configures ExtensionUIContext.Custom.
type ExtensionCustomOptions struct {
	Overlay        bool
	OverlayOptions *tui.OverlayOptions
	OverlayFn      func() tui.OverlayOptions
	OnHandle       func(handle tui.OverlayHandle)
	Blocked        *ExtensionCustomBlocked
}

// ThemeInfo describes an available theme name and optional file path.
type ThemeInfo struct {
	Name string
	Path *string
}

// SetThemeResult reports the outcome of setting a theme.
type SetThemeResult struct {
	Success bool
	Error   *string
}

// ProjectTrustUIContext is the UI subset exposed to project_trust extension handlers.
type ProjectTrustUIContext interface {
	Select(ctx context.Context, title string, options []string, opts ...*ExtensionUIDialogOptions) (*string, error)
	Confirm(ctx context.Context, title, message string, opts ...*ExtensionUIDialogOptions) (bool, error)
	Input(ctx context.Context, title string, placeholder *string, opts ...*ExtensionUIDialogOptions) (*string, error)
	Notify(message string, notifyType ...string)
}

// ExtensionUIContext provides UI interaction methods for modes (interactive, RPC, print).
type ExtensionUIContext interface {
	ProjectTrustUIContext
	OnTerminalInput(handler TerminalInputHandler) func()
	SetStatus(key string, text *string)
	SetWorkingMessage(message ...string)
	SetWorkingVisible(visible bool)
	SetWorkingIndicator(options *WorkingIndicatorOptions)
	SetHiddenThinkingLabel(label ...string)
	SetWidgetLines(key string, content []string, options ...*ExtensionWidgetOptions)
	SetWidgetFactory(key string, factory func(t tui.TUI, th *theme.Theme) tui.Component, options ...*ExtensionWidgetOptions)
	SetFooter(factory func(t tui.TUI, th *theme.Theme, footerData ReadonlyFooterDataProvider) tui.Component)
	SetHeader(factory func(t tui.TUI, th *theme.Theme) tui.Component)
	SetTitle(title string)
	Custom(
		ctx context.Context,
		factory func(t tui.TUI, th *theme.Theme, keybindings *tui.KeybindingsManager, done func(any)) (tui.Component, error),
		options ...*ExtensionCustomOptions,
	) (any, error)
	PasteToEditor(text string)
	SetEditorText(text string)
	GetEditorText() string
	Editor(ctx context.Context, title string, prefill ...string) (*string, error)
	AddAutocompleteProvider(factory AutocompleteProviderFactory)
	SetEditorComponent(factory EditorFactory)
	GetEditorComponent() EditorFactory
	Theme() *theme.Theme
	GetAllThemes() []ThemeInfo
	GetTheme(name string) *theme.Theme
	SetThemeByName(name string) SetThemeResult
	SetTheme(th *theme.Theme) SetThemeResult
	GetToolsExpanded() bool
	SetToolsExpanded(expanded bool)
}

// ContextUsage reports current token usage relative to the active model's context window.
type ContextUsage struct {
	Tokens        jsonx.Opt[int]     `json:"tokens"`
	ContextWindow int                `json:"contextWindow"`
	Percent       jsonx.Opt[float64] `json:"percent"`
}

// CompactOptions configures ExtensionContext.Compact.
type CompactOptions struct {
	CustomInstructions string
	OnComplete         func(result CompactionResult)
	OnError            func(err error)
}

// ExtensionMode identifies the active host presentation mode.
type ExtensionMode string

const (
	ExtensionModeTui   ExtensionMode = "tui"
	ExtensionModeRpc   ExtensionMode = "rpc"
	ExtensionModeJson  ExtensionMode = "json"
	ExtensionModePrint ExtensionMode = "print"
)

// ExtensionContext provides read-only runtime state and actions to extension handlers.
type ExtensionContext interface {
	UI() ExtensionUIContext
	Mode() ExtensionMode
	HasUI() bool
	Cwd() string
	SessionManager() ReadonlySessionManager
	ModelRegistry() *ModelRegistry
	Model() *ai.Model
	ScopedModels() []ScopedModel
	ThinkingLevel() agent.ThinkingLevel
	IsIdle() bool
	IsProjectTrusted() bool
	Signal() context.Context
	Abort()
	HasPendingMessages() bool
	Shutdown()
	GetContextUsage() *ContextUsage
	Compact(options ...*CompactOptions)
	GetSystemPrompt() string
}

// ExecuteToolOptions configures nested tool invocation via ExtensionToolContext.ExecuteTool.
type ExecuteToolOptions struct {
	Signal   context.Context
	OnUpdate agent.AgentToolUpdateCallback
}

// ExtensionToolContext extends ExtensionContext with nested tool execution inside a tool call.
type ExtensionToolContext interface {
	ExtensionContext
	Tools() []*agent.AgentTool
	ExecuteTool(ctx context.Context, name string, args any, options ...*ExecuteToolOptions) (agent.AgentToolCallOutcome, error)
}

// ExtensionCommandContextActions backs interactive session commands on ExtensionCommandContext.
type ExtensionCommandContextActions struct {
	WaitForIdle   func(ctx context.Context) error
	NewSession    func(ctx context.Context, options ...*ExtensionNewSessionOptions) (CancelledResult, error)
	Fork          func(ctx context.Context, entryId string, options ...*ExtensionForkOptions) (CancelledResult, error)
	NavigateTree  func(ctx context.Context, targetId string, options ...*ExtensionNavigateTreeOptions) (CancelledResult, error)
	SwitchSession func(ctx context.Context, sessionPath string, options ...*ExtensionSwitchSessionOptions) (CancelledResult, error)
	Reload        func(ctx context.Context) error
}

// CancelledResult reports whether a cancellable session command was cancelled.
type CancelledResult struct {
	Cancelled bool `json:"cancelled"`
}

// ExtensionNewSessionOptions configures ExtensionCommandContext.NewSession.
type ExtensionNewSessionOptions struct {
	ParentSession *string
	Setup         func(ctx context.Context, sessionManager *SessionManager) error
	WithSession   func(ctx context.Context, rctx ReplacedSessionContext) error
}

// ExtensionForkOptions configures ExtensionCommandContext.Fork.
type ExtensionForkOptions struct {
	Position    string
	WithSession func(ctx context.Context, rctx ReplacedSessionContext) error
}

// ExtensionNavigateTreeOptions configures ExtensionCommandContext.NavigateTree.
type ExtensionNavigateTreeOptions struct {
	Summarize           bool
	CustomInstructions  *string
	ReplaceInstructions bool
	Label               *string
}

// ExtensionSwitchSessionOptions configures ExtensionCommandContext.SwitchSession.
type ExtensionSwitchSessionOptions struct {
	WithSession func(ctx context.Context, rctx ReplacedSessionContext) error
}

// ExtensionCommandContext extends ExtensionContext for slash command handlers.
type ExtensionCommandContext interface {
	ExtensionContext
	WaitForIdle(ctx context.Context) error
	NewSession(ctx context.Context, options ...*ExtensionNewSessionOptions) (CancelledResult, error)
	Fork(ctx context.Context, entryId string, options ...*ExtensionForkOptions) (CancelledResult, error)
	NavigateTree(ctx context.Context, targetId string, options ...*ExtensionNavigateTreeOptions) (CancelledResult, error)
	SwitchSession(ctx context.Context, sessionPath string, options ...*ExtensionSwitchSessionOptions) (CancelledResult, error)
	Reload(ctx context.Context) error
	GetSystemPromptOptions() BuildSystemPromptOptions
	SendUserMessage(
		ctx context.Context,
		content any,
		options ...*SendUserMessageOptions,
	) error
}

// ReplacedSessionContext is passed to withSession callbacks during session replacement.
type ReplacedSessionContext interface {
	ExtensionCommandContext
}

// ToolRenderResultOptions controls tool result rendering state.
type ToolRenderResultOptions struct {
	Expanded  bool
	IsPartial bool
}

// ToolRenderContext is passed to custom tool RenderCall and RenderResult functions.
type ToolRenderContext struct {
	KeybindingHints
	Args             *jsonx.Object
	ToolCallId       string
	Invalidate       func()
	LastComponent    tui.Component
	State            any
	Cwd              string
	ExecutionStarted bool
	ArgsComplete     bool
	IsPartial        bool
	Expanded         bool
	ShowImages       bool
	IsError          bool
	DurationMs       *int64
	OutputPad        int
}

// ToolRenderShell selects whether ToolExecutionComponent renders the standard shell or the tool frames itself.
type ToolRenderShell string

const (
	ToolRenderShellDefault ToolRenderShell = "default"
	ToolRenderShellSelf    ToolRenderShell = "self"
)

// ToolExposure controls how a registered tool is exposed to the model and nested callers.
type ToolExposure string

const (
	ToolExposureDirect    ToolExposure = "direct"
	ToolExposureModelOnly ToolExposure = "model-only"
	ToolExposureCodemode  ToolExposure = "codemode"
	ToolExposureDeferred  ToolExposure = "deferred"
	ToolExposureHidden    ToolExposure = "hidden"
)

// ToolAnnotations provides MCP-style behavioral hints about a tool.
type ToolAnnotations struct {
	ReadOnlyHint    *bool `json:"readOnlyHint,omitzero"`
	DestructiveHint *bool `json:"destructiveHint,omitzero"`
	IdempotentHint  *bool `json:"idempotentHint,omitzero"`
	OpenWorldHint   *bool `json:"openWorldHint,omitzero"`
}

// ToolNamespace groups related tools under a shared namespace.
type ToolNamespace struct {
	Name         string   `json:"name"`
	Aliases      []string `json:"aliases,omitzero"`
	Description  *string  `json:"description,omitzero"`
	Instructions *string  `json:"instructions,omitzero"`
}

// ToolLoadout describes the active session tools visible to prepareLoadout hooks.
type ToolLoadout struct {
	Declared            []*agent.AgentTool
	Available           []*agent.AgentTool
	GetExposure         func(name string) ToolExposure
	GetNamespace        func(name string) *ToolNamespace
	GetPromptGuidelines func(name string) []string
}

// ToolLoadoutChanges describes loadout presentation adjustments returned by PrepareLoadout.
type ToolLoadoutChanges struct {
	Descriptions       map[string]string
	HideDeclarations   []string
	PromptContribution *string
}

// ToolReachesResult represents `"all" | readonly string[]` returned by ToolDefinition.ReachesTools.
type ToolReachesResult struct {
	All   bool
	Tools []string
}

// ToolExecuteFn executes a ToolDefinition invocation.
type ToolExecuteFn func(
	ctx context.Context,
	toolCallId string,
	params *jsonx.Object,
	onUpdate agent.AgentToolUpdateCallback,
	ectx ExtensionToolContext,
) (agent.AgentToolResult, error)

// ToolRenderCallFn renders a tool call header.
type ToolRenderCallFn func(args *jsonx.Object, th *theme.Theme, rctx ToolRenderContext) tui.Component

// ToolRenderResultFn renders a tool result body.
type ToolRenderResultFn func(result agent.AgentToolResult, options ToolRenderResultOptions, th *theme.Theme, rctx ToolRenderContext) tui.Component

// ToolDefinition defines a tool along with optional prompt metadata and TUI renderers.
type ToolDefinition struct {
	Name                string
	Label               string
	Description         string
	PromptSnippet       *string
	PromptGuidelines    []string
	Parameters          *typebox.Schema
	ConstrainedSampling ai.ToolConstrainedSamplingValue
	RenderShell         ToolRenderShell
	PrepareArguments    func(args any) any
	OutputSchema        *typebox.Schema
	Exposure            ToolExposure
	Namespace           *ToolNamespace
	ReachesTools        func(input *jsonx.Object) ToolReachesResult
	Annotations         *ToolAnnotations
	DefaultActive       *bool
	PrepareLoadout      func(loadout ToolLoadout) *ToolLoadoutChanges
	ExecutionMode       agent.ToolExecutionMode
	Execute             ToolExecuteFn
	RenderCall          ToolRenderCallFn
	RenderResult        ToolRenderResultFn
}

// ToolRenderers holds the rendering subset of ToolDefinition.
type ToolRenderers struct {
	RenderShell  ToolRenderShell
	RenderCall   ToolRenderCallFn
	RenderResult ToolRenderResultFn
}

// ToolRendererResolver chooses how calls to a tool are rendered.
type ToolRendererResolver func(toolName string, next func() *ToolRenderers) *ToolRenderers

// DefineTool preserves parameter inference / returns tool unchanged in Go.
func DefineTool(tool ToolDefinition) ToolDefinition {
	return tool
}

// ProjectTrustEvent fires on startup/reload to resolve project trust for cwd.
type ProjectTrustEvent struct {
	Type string `json:"type"`
	Cwd  string `json:"cwd"`
}

func (ProjectTrustEvent) ExtensionEventType() string { return "project_trust" }

// ProjectTrustEventDecision is an extension's trust decision for a project directory.
type ProjectTrustEventDecision string

const (
	ProjectTrustEventDecisionYes       ProjectTrustEventDecision = "yes"
	ProjectTrustEventDecisionNo        ProjectTrustEventDecision = "no"
	ProjectTrustEventDecisionUndecided ProjectTrustEventDecision = "undecided"
)

// ProjectTrustEventResult is returned by project_trust extension handlers.
type ProjectTrustEventResult struct {
	Trusted  ProjectTrustEventDecision `json:"trusted"`
	Remember *bool                     `json:"remember,omitzero"`
}

// ProjectTrustContext carries host metadata to project_trust handlers.
type ProjectTrustContext struct {
	Cwd   string
	Mode  ExtensionMode
	HasUI bool
	Ui    ProjectTrustUIContext
}

// ProjectTrustHandler handles a ProjectTrustEvent.
type ProjectTrustHandler func(event ProjectTrustEvent, ptctx ProjectTrustContext) (ProjectTrustEventResult, error)

// ResourcesDiscoverReason describes why resource discovery fired.
type ResourcesDiscoverReason string

const (
	ResourcesDiscoverReasonStartup ResourcesDiscoverReason = "startup"
	ResourcesDiscoverReasonReload  ResourcesDiscoverReason = "reload"
)

// ResourcesDiscoverEvent fires after session_start to let extensions supply additional resources.
type ResourcesDiscoverEvent struct {
	Type   string                  `json:"type"`
	Cwd    string                  `json:"cwd"`
	Reason ResourcesDiscoverReason `json:"reason"`
}

func (ResourcesDiscoverEvent) ExtensionEventType() string { return "resources_discover" }

// ResourcesDiscoverResult returns discovered skill, prompt, and theme paths.
type ResourcesDiscoverResult struct {
	SkillPaths  []string `json:"skillPaths,omitzero"`
	PromptPaths []string `json:"promptPaths,omitzero"`
	ThemePaths  []string `json:"themePaths,omitzero"`
}

// McpServersChangeEvent fires when extension-registered MCP servers change after binding.
type McpServersChangeEvent struct {
	Type    string                `json:"type"`
	Servers []RegisteredMcpServer `json:"servers"`
}

func (McpServersChangeEvent) ExtensionEventType() string { return "mcp_servers_change" }

// SessionStartEvent fires when a session starts, loads, or reloads.
type SessionStartEvent struct {
	Type                string  `json:"type"`
	Reason              string  `json:"reason"` // "startup" | "reload" | "new" | "resume" | "fork"
	PreviousSessionFile *string `json:"previousSessionFile,omitzero"`
}

func (SessionStartEvent) ExtensionEventType() string { return "session_start" }

// SessionInfoChangedEvent fires when the normalized session display name changes.
type SessionInfoChangedEvent struct {
	Type string  `json:"type"`
	Name *string `json:"name"`
}

func (SessionInfoChangedEvent) ExtensionEventType() string { return "session_info_changed" }

// SessionBeforeSwitchEvent fires before switching sessions (cancellable).
type SessionBeforeSwitchEvent struct {
	Type              string  `json:"type"`
	Reason            string  `json:"reason"` // "new" | "resume"
	TargetSessionFile *string `json:"targetSessionFile,omitzero"`
}

func (SessionBeforeSwitchEvent) ExtensionEventType() string { return "session_before_switch" }

// SessionBeforeForkEvent fires before forking a session (cancellable).
type SessionBeforeForkEvent struct {
	Type     string `json:"type"`
	EntryId  string `json:"entryId"`
	Position string `json:"position"` // "before" | "at"
}

func (SessionBeforeForkEvent) ExtensionEventType() string { return "session_before_fork" }

// SessionBeforeCompactEvent fires before context compaction (cancellable or customizable).
type SessionBeforeCompactEvent struct {
	Type               string                `json:"type"`
	Preparation        CompactionPreparation `json:"preparation"`
	BranchEntries      []SessionEntry        `json:"branchEntries"`
	CustomInstructions *string               `json:"customInstructions,omitzero"`
	Reason             string                `json:"reason"` // "manual" | "threshold" | "overflow"
	WillRetry          bool                  `json:"willRetry"`
	Ctx                context.Context       `json:"-"`
}

func (SessionBeforeCompactEvent) ExtensionEventType() string { return "session_before_compact" }

// SessionCompactEvent fires after compaction succeeds.
type SessionCompactEvent struct {
	Type            string           `json:"type"`
	CompactionEntry *CompactionEntry `json:"compactionEntry"`
	FromExtension   bool             `json:"fromExtension"`
	Reason          string           `json:"reason"` // "manual" | "threshold" | "overflow"
	WillRetry       bool             `json:"willRetry"`
}

func (SessionCompactEvent) ExtensionEventType() string { return "session_compact" }

// SessionCompactFailedEvent fires after compaction fails or is aborted.
type SessionCompactFailedEvent struct {
	Type          string  `json:"type"`
	Reason        string  `json:"reason"` // "manual" | "threshold" | "overflow"
	ErrorMessage  *string `json:"errorMessage,omitzero"`
	Aborted       bool    `json:"aborted"`
	WillRetry     bool    `json:"willRetry"`
	FromExtension bool    `json:"fromExtension"`
}

func (SessionCompactFailedEvent) ExtensionEventType() string { return "session_compact_failed" }

// SessionShutdownEvent fires before an extension runtime tears down.
type SessionShutdownEvent struct {
	Type              string  `json:"type"`
	Reason            string  `json:"reason"` // "quit" | "reload" | "new" | "resume" | "fork"
	TargetSessionFile *string `json:"targetSessionFile,omitzero"`
}

func (SessionShutdownEvent) ExtensionEventType() string { return "session_shutdown" }

// TreePreparation captures metadata for an upcoming session tree navigation.
type TreePreparation struct {
	TargetId            string         `json:"targetId"`
	OldLeafId           *string        `json:"oldLeafId"`
	CommonAncestorId    *string        `json:"commonAncestorId"`
	EntriesToSummarize  []SessionEntry `json:"entriesToSummarize"`
	UserWantsSummary    bool           `json:"userWantsSummary"`
	CustomInstructions  *string        `json:"customInstructions,omitzero"`
	ReplaceInstructions bool           `json:"replaceInstructions,omitzero"`
	Label               *string        `json:"label,omitzero"`
}

// SessionBeforeTreeEvent fires before session tree navigation (cancellable).
type SessionBeforeTreeEvent struct {
	Type        string          `json:"type"`
	Preparation TreePreparation `json:"preparation"`
	Ctx         context.Context `json:"-"`
}

func (SessionBeforeTreeEvent) ExtensionEventType() string { return "session_before_tree" }

// SessionTreeEvent fires after session tree navigation completes.
type SessionTreeEvent struct {
	Type          string              `json:"type"`
	NewLeafId     *string             `json:"newLeafId"`
	OldLeafId     *string             `json:"oldLeafId"`
	SummaryEntry  *BranchSummaryEntry `json:"summaryEntry,omitzero"`
	FromExtension *bool               `json:"fromExtension,omitzero"`
}

func (SessionTreeEvent) ExtensionEventType() string { return "session_tree" }

// ContextEvent fires before each LLM call with non-system conversation messages.
type ContextEvent struct {
	Type     string               `json:"type"`
	Messages []agent.AgentMessage `json:"messages"`
}

func (ContextEvent) ExtensionEventType() string { return "context" }

// ContextWithSystemEvent fires after prompt and tool state restoration with full messages.
type ContextWithSystemEvent struct {
	Type     string               `json:"type"`
	Messages []agent.AgentMessage `json:"messages"`
}

func (ContextWithSystemEvent) ExtensionEventType() string { return "context_with_system" }

// BeforeProviderRequestEvent fires before a provider request is sent.
type BeforeProviderRequestEvent struct {
	Type    string `json:"type"`
	Payload any    `json:"payload"`
}

func (BeforeProviderRequestEvent) ExtensionEventType() string { return "before_provider_request" }

// BeforeProviderHeadersEvent fires before sending provider HTTP headers.
type BeforeProviderHeadersEvent struct {
	Type    string             `json:"type"`
	Headers ai.ProviderHeaders `json:"headers"`
}

func (BeforeProviderHeadersEvent) ExtensionEventType() string { return "before_provider_headers" }

// AfterProviderResponseEvent fires after receiving a provider response and before consuming its stream.
type AfterProviderResponseEvent struct {
	Type    string            `json:"type"`
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
}

func (AfterProviderResponseEvent) ExtensionEventType() string { return "after_provider_response" }

// ProviderStreamEvent fires for each parsed provider stream event before normalization.
type ProviderStreamEvent struct {
	Type     string        `json:"type"`
	Provider ai.ProviderId `json:"provider"`
	Api      ai.Api        `json:"api"`
	Model    string        `json:"model"`
	Data     any           `json:"data"`
}

func (ProviderStreamEvent) ExtensionEventType() string { return "provider_stream_event" }

// BeforeAgentStartEvent fires before an agent turn starts from user or custom input.
type BeforeAgentStartEvent struct {
	Type                string                             `json:"type"`
	SystemPrompt        string                             `json:"systemPrompt"`
	SystemPromptOptions NormalizedBuildSystemPromptOptions `json:"systemPromptOptions"`
	MessageType         string                             `json:"messageType"` // "user" | "custom"
	Prompt              string                             `json:"prompt,omitzero"`
	Images              []ai.ImageContent                  `json:"images,omitzero"`
	Message             *CustomMessage                     `json:"message,omitzero"`
}

func (BeforeAgentStartEvent) ExtensionEventType() string { return "before_agent_start" }

// BeforeAgentStartTrigger represents the user or custom trigger for EmitBeforeAgentStart.
type BeforeAgentStartTrigger struct {
	MessageType string
	Prompt      string
	Images      []ai.ImageContent
	Message     *CustomMessage
}

// BeforeAgentStartCustomMessage carries a custom message injected by before_agent_start handlers.
type BeforeAgentStartCustomMessage struct {
	CustomType string `json:"customType"`
	Content    any    `json:"content"`
	Display    bool   `json:"display"`
	Details    any    `json:"details,omitzero"`
}

// BeforeAgentStartEventResult holds optional overrides returned by a before_agent_start handler.
type BeforeAgentStartEventResult struct {
	Message      *BeforeAgentStartCustomMessage `json:"message,omitzero"`
	SystemPrompt *string                        `json:"systemPrompt,omitzero"`
}

// AgentStartEvent fires when an agent loop starts.
type AgentStartEvent struct {
	Type string `json:"type"`
}

func (AgentStartEvent) ExtensionEventType() string { return "agent_start" }

// AgentEndEvent fires when an agent loop ends.
type AgentEndEvent struct {
	Type     string               `json:"type"`
	Messages []agent.AgentMessage `json:"messages"`
}

func (AgentEndEvent) ExtensionEventType() string { return "agent_end" }

// AgentActivityOutcome classifies how a turn or run concluded before boundary hooks.
type AgentActivityOutcome string

const (
	AgentActivityCompleted AgentActivityOutcome = "completed"
	AgentActivityAborted   AgentActivityOutcome = "aborted"
	AgentActivityError     AgentActivityOutcome = "error"
)

// SessionBoundaryDraft represents an entry draft queued by turn_end or agent_before_settle handlers.
type SessionBoundaryDraft interface {
	SessionBoundaryDraftType() string
}

// CustomEntryDraft drafts a custom persisted entry at a boundary.
type CustomEntryDraft struct {
	Type       string `json:"type"`
	CustomType string `json:"customType"`
	Data       any    `json:"data,omitzero"`
}

func (CustomEntryDraft) SessionBoundaryDraftType() string { return "custom" }

// CustomMessageEntryDraft drafts a custom_message entry at a boundary.
type CustomMessageEntryDraft struct {
	Type       string `json:"type"`
	CustomType string `json:"customType"`
	Content    any    `json:"content"`
	Display    bool   `json:"display"`
	Details    any    `json:"details,omitzero"`
}

func (CustomMessageEntryDraft) SessionBoundaryDraftType() string { return "custom_message" }

// ContextEditEntryDraft drafts a context_edit entry at a boundary.
type ContextEditEntryDraft struct {
	Type        string                 `json:"type"`
	TargetId    string                 `json:"targetId"`
	Replacement ContextEditReplacement `json:"replacement"`
}

func (ContextEditEntryDraft) SessionBoundaryDraftType() string { return "context_edit" }

// CompactionEntryDraft drafts a compaction entry at a boundary.
type CompactionEntryDraft struct {
	Type             string            `json:"type"`
	Summary          string            `json:"summary"`
	FirstKeptEntryId jsonx.Opt[string] `json:"firstKeptEntryId"`
	Details          any               `json:"details,omitzero"`
	Usage            *ai.Usage         `json:"usage,omitzero"`
}

func (CompactionEntryDraft) SessionBoundaryDraftType() string { return "compaction" }

// BoundaryContextPreview previews session/LLM state at a turn or settle boundary.
type BoundaryContextPreview struct {
	ContextEntries  []ProjectedSessionEntry `json:"contextEntries"`
	ContextMessages []agent.AgentMessage    `json:"contextMessages"`
	LlmMessages     []ai.Message            `json:"llmMessages"`
	PendingMessages []agent.AgentMessage    `json:"pendingMessages"`
	CanContinue     bool                    `json:"canContinue"`
}

// BoundaryState carries accumulated boundary entries, continuation flag, and context preview.
type BoundaryState struct {
	Entries  []SessionBoundaryDraft `json:"entries"`
	Continue bool                   `json:"continue"`
	Context  BoundaryContextPreview `json:"context"`
	Outcome  AgentActivityOutcome   `json:"outcome"`
}

// BoundaryResult is returned by turn_end and agent_before_settle handlers.
type BoundaryResult struct {
	Entries  []SessionBoundaryDraft `json:"entries,omitzero"`
	Continue *bool                  `json:"continue,omitzero"`
}

// AgentBeforeSettleEvent fires before final settlement.
type AgentBeforeSettleEvent struct {
	Type string `json:"type"`
	BoundaryState
}

func (AgentBeforeSettleEvent) ExtensionEventType() string { return "agent_before_settle" }

// AgentSettledEvent fires once an agent run has fully settled.
type AgentSettledEvent struct {
	Type    string `json:"type"`
	Aborted bool   `json:"aborted"`
}

func (AgentSettledEvent) ExtensionEventType() string { return "agent_settled" }

// UIPromptKind classifies a blocking extension UI prompt.
type UIPromptKind string

const (
	UIPromptKindSelect  UIPromptKind = "select"
	UIPromptKindConfirm UIPromptKind = "confirm"
	UIPromptKindInput   UIPromptKind = "input"
	UIPromptKindEditor  UIPromptKind = "editor"
	UIPromptKindCustom  UIPromptKind = "custom"
)

// UIPromptStartEvent fires when Pi begins waiting on a blocking UI prompt.
type UIPromptStartEvent struct {
	Type   string       `json:"type"`
	Reason string       `json:"reason"`
	Kind   UIPromptKind `json:"kind"`
	Title  *string      `json:"title,omitzero"`
}

func (UIPromptStartEvent) ExtensionEventType() string { return "ui_prompt_start" }

// UIPromptEndEvent fires when Pi stops waiting on a blocking UI prompt.
type UIPromptEndEvent struct {
	Type   string       `json:"type"`
	Reason string       `json:"reason"`
	Kind   UIPromptKind `json:"kind"`
	Title  *string      `json:"title,omitzero"`
}

func (UIPromptEndEvent) ExtensionEventType() string { return "ui_prompt_end" }

// TurnStartEvent fires at the start of each turn.
type TurnStartEvent struct {
	Type      string `json:"type"`
	TurnIndex int    `json:"turnIndex"`
	Timestamp int64  `json:"timestamp"`
}

func (TurnStartEvent) ExtensionEventType() string { return "turn_start" }

// TurnEndEvent fires at the end of each turn.
type TurnEndEvent struct {
	Type string `json:"type"`
	BoundaryState
	TurnIndex          int                    `json:"turnIndex"`
	Message            agent.AgentMessage     `json:"message"`
	ToolResults        []ai.ToolResultMessage `json:"toolResults"`
	MessageEntryId     string                 `json:"messageEntryId"`
	ToolResultEntryIds []string               `json:"toolResultEntryIds"`
}

func (TurnEndEvent) ExtensionEventType() string { return "turn_end" }

// MessageStartEvent fires when a message enters the stream.
type MessageStartEvent struct {
	Type    string             `json:"type"`
	Message agent.AgentMessage `json:"message"`
}

func (MessageStartEvent) ExtensionEventType() string { return "message_start" }

// MessageUpdateEvent fires for assistant message streaming deltas.
type MessageUpdateEvent struct {
	Type                  string                   `json:"type"`
	Message               agent.AgentMessage       `json:"message"`
	AssistantMessageEvent ai.AssistantMessageEvent `json:"assistantMessageEvent"`
}

func (MessageUpdateEvent) ExtensionEventType() string { return "message_update" }

// MessageEndEvent fires when a message completes.
type MessageEndEvent struct {
	Type    string             `json:"type"`
	Message agent.AgentMessage `json:"message"`
}

func (MessageEndEvent) ExtensionEventType() string { return "message_end" }

// ToolExecutionStartEvent fires when a tool starts executing.
type ToolExecutionStartEvent struct {
	Type             string  `json:"type"`
	ToolCallId       string  `json:"toolCallId"`
	ToolName         string  `json:"toolName"`
	Args             any     `json:"args"`
	ParentToolCallId *string `json:"parentToolCallId,omitzero"`
}

func (ToolExecutionStartEvent) ExtensionEventType() string   { return "tool_execution_start" }
func (*ToolExecutionStartEvent) isNestedToolExecutionEvent() {}

// ToolExecutionUpdateEvent fires for incremental partial tool results.
type ToolExecutionUpdateEvent struct {
	Type             string  `json:"type"`
	ToolCallId       string  `json:"toolCallId"`
	ToolName         string  `json:"toolName"`
	Args             any     `json:"args"`
	PartialResult    any     `json:"partialResult"`
	ParentToolCallId *string `json:"parentToolCallId,omitzero"`
}

func (ToolExecutionUpdateEvent) ExtensionEventType() string   { return "tool_execution_update" }
func (*ToolExecutionUpdateEvent) isNestedToolExecutionEvent() {}

// ToolExecutionEndEvent fires when a tool completes execution.
type ToolExecutionEndEvent struct {
	Type             string  `json:"type"`
	ToolCallId       string  `json:"toolCallId"`
	ToolName         string  `json:"toolName"`
	Result           any     `json:"result"`
	IsError          bool    `json:"isError"`
	DurationMs       *int64  `json:"durationMs,omitzero"`
	ParentToolCallId *string `json:"parentToolCallId,omitzero"`
}

func (ToolExecutionEndEvent) ExtensionEventType() string   { return "tool_execution_end" }
func (*ToolExecutionEndEvent) isNestedToolExecutionEvent() {}

// ModelSelectSource identifies why a model selection occurred.
type ModelSelectSource string

const (
	ModelSelectSourceSet     ModelSelectSource = "set"
	ModelSelectSourceCycle   ModelSelectSource = "cycle"
	ModelSelectSourceRestore ModelSelectSource = "restore"
)

// ModelSelectEvent fires when a new model is selected.
type ModelSelectEvent struct {
	Type          string            `json:"type"`
	Model         *ai.Model         `json:"model"`
	PreviousModel *ai.Model         `json:"previousModel"`
	Source        ModelSelectSource `json:"source"`
}

func (ModelSelectEvent) ExtensionEventType() string { return "model_select" }

// ThinkingLevelSelectEvent fires when a new thinking level is selected.
type ThinkingLevelSelectEvent struct {
	Type          string              `json:"type"`
	Level         agent.ThinkingLevel `json:"level"`
	PreviousLevel agent.ThinkingLevel `json:"previousLevel"`
}

func (ThinkingLevelSelectEvent) ExtensionEventType() string { return "thinking_level_select" }

// UserBashEvent fires when the user runs a ! or !! bash command.
type UserBashEvent struct {
	Type               string `json:"type"`
	Command            string `json:"command"`
	ExcludeFromContext bool   `json:"excludeFromContext"`
	Cwd                string `json:"cwd"`
}

func (UserBashEvent) ExtensionEventType() string { return "user_bash" }

// InputSource identifies where user input originated.
type InputSource string

const (
	InputSourceInteractive InputSource = "interactive"
	InputSourceRpc         InputSource = "rpc"
	InputSourceExtension   InputSource = "extension"
)

// InputEvent fires when user input arrives before agent processing.
type InputEvent struct {
	Type              string            `json:"type"`
	Text              string            `json:"text"`
	Images            []ai.ImageContent `json:"images,omitzero"`
	Source            InputSource       `json:"source"`
	StreamingBehavior *string           `json:"streamingBehavior,omitzero"` // "steer" | "followUp"
}

func (InputEvent) ExtensionEventType() string { return "input" }

// InputEventResult controls continuation/transformation/handling of InputEvent.
type InputEventResult struct {
	Action string            `json:"action"` // "continue" | "transform" | "handled"
	Text   string            `json:"text,omitzero"`
	Images []ai.ImageContent `json:"images,omitzero"`
}

// ToolCallEvent fires before tool execution and may mutate Input or block execution.
type ToolCallEvent struct {
	Type             string        `json:"type"`
	ToolCallId       string        `json:"toolCallId"`
	ParentToolCallId *string       `json:"parentToolCallId,omitzero"`
	ToolName         string        `json:"toolName"`
	Input            *jsonx.Object `json:"input"`
}

func (ToolCallEvent) ExtensionEventType() string { return "tool_call" }

// ToolResultEvent fires after tool execution and may override content/details/isError.
type ToolResultEvent struct {
	Type              string         `json:"type"`
	ToolCallId        string         `json:"toolCallId"`
	ParentToolCallId  *string        `json:"parentToolCallId,omitzero"`
	ToolName          string         `json:"toolName"`
	Input             *jsonx.Object  `json:"input"`
	Content           ai.ContentList `json:"content"`
	Details           any            `json:"details,omitzero"`
	StructuredContent any            `json:"structuredContent,omitzero"`
	IsError           bool           `json:"isError"`
	Usage             *ai.Usage      `json:"usage,omitzero"`
}

func (ToolResultEvent) ExtensionEventType() string { return "tool_result" }

// ExtensionEvent is implemented by all extension event structs.
type ExtensionEvent interface {
	ExtensionEventType() string
}

// ContextEventResult optionally replaces messages passed to the LLM.
type ContextEventResult struct {
	Messages []agent.AgentMessage `json:"messages,omitzero"`
}

// ToolCallEventResult allows tool_call handlers to block execution or hint early termination.
type ToolCallEventResult struct {
	Block     *bool   `json:"block,omitzero"`
	Reason    *string `json:"reason,omitzero"`
	Terminate *bool   `json:"terminate,omitzero"`
}

// UserBashEventResult allows user_bash handlers to provide custom operations or a full result.
type UserBashEventResult struct {
	Operations BashOperations `json:"-"`
	Result     *BashResult    `json:"result,omitzero"`
}

// ToolResultEventResult allows tool_result handlers to modify a tool result.
type ToolResultEventResult struct {
	Content           ai.ContentList `json:"content,omitzero"`
	Details           any            `json:"details,omitzero"`
	StructuredContent any            `json:"structuredContent,omitzero"`
	IsError           *bool          `json:"isError,omitzero"`
	Usage             *ai.Usage      `json:"usage,omitzero"`
}

// MessageEndEventResult allows message_end handlers to replace a finalized message.
type MessageEndEventResult struct {
	Message agent.AgentMessage `json:"message,omitzero"`
}

// SessionBeforeSwitchResult can cancel a session switch.
type SessionBeforeSwitchResult struct {
	Cancel bool `json:"cancel,omitzero"`
}

// SessionBeforeForkResult can cancel a session fork or skip conversation restore.
type SessionBeforeForkResult struct {
	Cancel                  bool `json:"cancel,omitzero"`
	SkipConversationRestore bool `json:"skipConversationRestore,omitzero"`
}

// SessionBeforeCompactResult can cancel compaction or supply a custom CompactionResult.
type SessionBeforeCompactResult struct {
	Cancel     bool              `json:"cancel,omitzero"`
	Compaction *CompactionResult `json:"compaction,omitzero"`
}

// SessionBeforeTreeSummary supplies custom summary content from session_before_tree.
type SessionBeforeTreeSummary struct {
	Summary string    `json:"summary"`
	Details any       `json:"details,omitzero"`
	Usage   *ai.Usage `json:"usage,omitzero"`
}

// SessionBeforeTreeResult can cancel tree navigation or supply summary/instruction overrides.
type SessionBeforeTreeResult struct {
	Cancel              bool                      `json:"cancel,omitzero"`
	Summary             *SessionBeforeTreeSummary `json:"summary,omitzero"`
	CustomInstructions  *string                   `json:"customInstructions,omitzero"`
	ReplaceInstructions *bool                     `json:"replaceInstructions,omitzero"`
	Label               *string                   `json:"label,omitzero"`
}

// MessageRenderOptions carries options to custom message renderers.
type MessageRenderOptions struct {
	KeybindingHints
	Expanded  bool
	OutputPad int
}

// MarkdownMessageType identifies the rendering surface passed to MarkdownTransformer.
type MarkdownMessageType string

const (
	MarkdownMessageTypeUser              MarkdownMessageType = "user"
	MarkdownMessageTypeAssistant         MarkdownMessageType = "assistant"
	MarkdownMessageTypeAssistantThinking MarkdownMessageType = "assistant-thinking"
	MarkdownMessageTypeCustom            MarkdownMessageType = "custom"
	MarkdownMessageTypeSummary           MarkdownMessageType = "summary"
	MarkdownMessageTypeChangelog         MarkdownMessageType = "changelog"
)

// MarkdownTransformContext provides message surface metadata to MarkdownTransformer.
type MarkdownTransformContext struct {
	MessageType    MarkdownMessageType
	IsStreaming    bool
	AvailableWidth int
	MessageId      *string
	Timestamp      *string
}

// MarkdownTransformer rewrites Markdown before interactive rendering.
type MarkdownTransformer func(markdown string, rctx MarkdownTransformContext) string

// MarkdownTransformerOptions selects the surfaces a MarkdownTransformer applies to.
type MarkdownTransformerOptions struct {
	MessageTypes []MarkdownMessageType
}

// MarkdownTransformerRegistration pairs a transformer with its configured surfaces.
type MarkdownTransformerRegistration struct {
	Transformer  MarkdownTransformer
	MessageTypes map[MarkdownMessageType]struct{}
}

// EntryRenderOptions carries options to custom session entry renderers.
type EntryRenderOptions struct {
	KeybindingHints
	Expanded bool
}

// MessageRenderer renders a CustomMessage in interactive/export contexts.
type MessageRenderer func(message *CustomMessage, options MessageRenderOptions, th *theme.Theme) tui.Component

// EntryRenderer renders a CustomEntry in interactive contexts.
type EntryRenderer func(entry *CustomEntry, options EntryRenderOptions, th *theme.Theme) tui.Component

// RegisteredCommand represents a slash command registered by an extension.
type RegisteredCommand struct {
	Name                   string
	SourceInfo             utils.SourceInfo
	Description            *string
	GetArgumentCompletions func(ctx context.Context, argumentPrefix string) ([]tui.AutocompleteItem, error)
	Handler                func(ctx context.Context, args string, ectx ExtensionCommandContext) error
}

// ResolvedCommand is a RegisteredCommand paired with its effective invocation name.
type ResolvedCommand struct {
	RegisteredCommand
	InvocationName string
}

// ExtensionHandler handles an extension event of type E and optionally returns *R.
type ExtensionHandler[E any, R any] func(ctx context.Context, event *E, ectx ExtensionContext) (*R, error)

// SendMessageOptions configures ExtensionAPI.SendMessage.
type SendMessageOptions struct {
	TriggerTurn bool
	DeliverAs   string // "steer" | "followUp" | "nextTurn"
}

// SendUserMessageOptions configures ExtensionAPI.SendUserMessage.
type SendUserMessageOptions struct {
	DeliverAs             string // "steer" | "followUp"
	ExpandPromptTemplates bool
}

// CustomMessageInput describes a custom message payload passed to SendMessage.
type CustomMessageInput struct {
	CustomType string `json:"customType"`
	Content    any    `json:"content"`
	Display    bool   `json:"display"`
	Details    any    `json:"details,omitzero"`
}

// ExtensionFlagOptions configures a CLI flag registered via ExtensionAPI.RegisterFlag.
type ExtensionFlagOptions struct {
	Description *string
	Type        string // "boolean" | "string"
	Default     any    // bool | string | nil
}

// ExtensionShortcutOptions configures a shortcut registered via ExtensionAPI.RegisterShortcut.
type ExtensionShortcutOptions struct {
	Description *string
	Handler     func(ctx context.Context, ectx ExtensionContext) error
}

// ExtensionAPI is passed to ExtensionFactory functions.
type ExtensionAPI interface {
	OnProjectTrust(h ProjectTrustHandler) func()
	OnResourcesDiscover(h ExtensionHandler[ResourcesDiscoverEvent, ResourcesDiscoverResult]) func()
	OnSessionStart(h ExtensionHandler[SessionStartEvent, NoResult]) func()
	OnSessionInfoChanged(h ExtensionHandler[SessionInfoChangedEvent, NoResult]) func()
	OnSessionBeforeSwitch(h ExtensionHandler[SessionBeforeSwitchEvent, SessionBeforeSwitchResult]) func()
	OnSessionBeforeFork(h ExtensionHandler[SessionBeforeForkEvent, SessionBeforeForkResult]) func()
	OnSessionBeforeCompact(h ExtensionHandler[SessionBeforeCompactEvent, SessionBeforeCompactResult]) func()
	OnSessionCompact(h ExtensionHandler[SessionCompactEvent, NoResult]) func()
	OnSessionCompactFailed(h ExtensionHandler[SessionCompactFailedEvent, NoResult]) func()
	OnSessionShutdown(h ExtensionHandler[SessionShutdownEvent, NoResult]) func()
	OnMcpServersChange(h ExtensionHandler[McpServersChangeEvent, NoResult]) func()
	OnSessionBeforeTree(h ExtensionHandler[SessionBeforeTreeEvent, SessionBeforeTreeResult]) func()
	OnSessionTree(h ExtensionHandler[SessionTreeEvent, NoResult]) func()
	OnContext(h ExtensionHandler[ContextEvent, ContextEventResult]) func()
	OnContextWithSystem(h ExtensionHandler[ContextWithSystemEvent, ContextEventResult]) func()
	OnCacheWarmingDecision(h ExtensionHandler[CacheWarmingDecisionEvent, CacheWarmingDecisionEventResult]) func()
	OnBeforeProviderRequest(h ExtensionHandler[BeforeProviderRequestEvent, any]) func()
	OnBeforeProviderHeaders(h ExtensionHandler[BeforeProviderHeadersEvent, NoResult]) func()
	OnAfterProviderResponse(h ExtensionHandler[AfterProviderResponseEvent, NoResult]) func()
	OnProviderStreamEvent(h ExtensionHandler[ProviderStreamEvent, NoResult]) func()
	OnBeforeAgentStart(h ExtensionHandler[BeforeAgentStartEvent, BeforeAgentStartEventResult]) func()
	OnAgentStart(h ExtensionHandler[AgentStartEvent, NoResult]) func()
	OnAgentEnd(h ExtensionHandler[AgentEndEvent, NoResult]) func()
	OnAgentBeforeSettle(h ExtensionHandler[AgentBeforeSettleEvent, BoundaryResult]) func()
	OnAgentSettled(h ExtensionHandler[AgentSettledEvent, NoResult]) func()
	OnUIPromptStart(h ExtensionHandler[UIPromptStartEvent, NoResult]) func()
	OnUIPromptEnd(h ExtensionHandler[UIPromptEndEvent, NoResult]) func()
	OnTurnStart(h ExtensionHandler[TurnStartEvent, NoResult]) func()
	OnTurnEnd(h ExtensionHandler[TurnEndEvent, BoundaryResult]) func()
	OnMessageStart(h ExtensionHandler[MessageStartEvent, NoResult]) func()
	OnMessageUpdate(h ExtensionHandler[MessageUpdateEvent, NoResult]) func()
	OnMessageEnd(h ExtensionHandler[MessageEndEvent, MessageEndEventResult]) func()
	OnToolExecutionStart(h ExtensionHandler[ToolExecutionStartEvent, NoResult]) func()
	OnToolExecutionUpdate(h ExtensionHandler[ToolExecutionUpdateEvent, NoResult]) func()
	OnToolExecutionEnd(h ExtensionHandler[ToolExecutionEndEvent, NoResult]) func()
	OnModelSelect(h ExtensionHandler[ModelSelectEvent, NoResult]) func()
	OnThinkingLevelSelect(h ExtensionHandler[ThinkingLevelSelectEvent, NoResult]) func()
	OnToolCall(h ExtensionHandler[ToolCallEvent, ToolCallEventResult]) func()
	OnToolResult(h ExtensionHandler[ToolResultEvent, ToolResultEventResult]) func()
	OnUserBash(h ExtensionHandler[UserBashEvent, UserBashEventResult]) func()
	OnInput(h ExtensionHandler[InputEvent, InputEventResult]) func()

	RegisterTool(tool ToolDefinition)
	RegisterCommand(name string, options RegisteredCommand)
	RegisterShortcut(shortcut tui.KeyId, options ExtensionShortcutOptions)
	RegisterFlag(name string, options ExtensionFlagOptions)
	GetFlag(name string) any
	RegisterMessageRenderer(customType string, renderer MessageRenderer)
	RegisterMarkdownTransformer(transformer MarkdownTransformer, options ...*MarkdownTransformerOptions)
	RegisterEntryRenderer(customType string, renderer EntryRenderer)
	RegisterToolRenderer(resolver ToolRendererResolver)
	SendMessage(message CustomMessageInput, options ...*SendMessageOptions)
	SendUserMessage(content any, options ...*SendUserMessageOptions)
	AppendEntry(customType string, data any)
	SetSessionName(name string)
	GetSessionName() *string
	SetLabel(entryId string, label *string)
	Exec(ctx context.Context, command string, args []string, options ...*ExecOptions) (ExecResult, error)
	GetActiveTools() []string
	GetAllTools() []ToolInfo
	GetSettings() Settings
	SetActiveTools(toolNames []string)
	GetCommands() []SlashCommandInfo
	SetModel(ctx context.Context, model *ai.Model) (bool, error)
	GetThinkingLevel() agent.ThinkingLevel
	SetThinkingLevel(level agent.ThinkingLevel)
	RegisterNativeProvider(provider *ai.Provider)
	RegisterProvider(name string, config ProviderConfig)
	UnregisterProvider(name string)
	RegisterMcpServer(name string, config McpServerConfig)
	UnregisterMcpServer(name string)
	GetMcpServers() []RegisteredMcpServer
	RegisterVirtualModel(model VirtualModelDefinition)
	UnregisterVirtualModel(provider, id string)
	Events() EventBus
}

// ExtensionFactory initializes an extension against an ExtensionAPI.
type ExtensionFactory func(pi ExtensionAPI) error

// InlineExtension represents an inline or built-in Go extension factory with registration flags.
type InlineExtension struct {
	Name        string
	Factory     ExtensionFactory
	Hidden      bool
	Replaceable bool
	Builtin     bool
}

// RegisteredTool pairs a registered ToolDefinition with its provenance SourceInfo.
type RegisteredTool struct {
	Definition ToolDefinition
	SourceInfo utils.SourceInfo
}

// ExtensionFlag records a CLI flag declared by an extension.
type ExtensionFlag struct {
	Name          string
	Description   *string
	Type          string // "boolean" | "string"
	Default       any    // bool | string | nil
	ExtensionPath string
}

// ExtensionShortcut records a keyboard shortcut declared by an extension.
type ExtensionShortcut struct {
	Shortcut      tui.KeyId
	Description   *string
	Handler       func(ctx context.Context, ectx ExtensionContext) error
	ExtensionPath string
}

// ToolInfo summarizes a configured tool for ExtensionAPI.GetAllTools.
type ToolInfo struct {
	Name             string
	Description      string
	Parameters       *typebox.Schema
	PromptGuidelines []string
	ReachesTools     func(input *jsonx.Object) ToolReachesResult
	Exposure         ToolExposure
	Namespace        *ToolNamespace
	Annotations      *ToolAnnotations
	SourceInfo       utils.SourceInfo
}

// PendingProviderRegistration stores a provider config registered before runtime binding.
type PendingProviderRegistration struct {
	Name          string
	Config        ProviderConfig
	ExtensionPath string
}

// PendingNativeProviderRegistration stores a native provider registered before runtime binding.
type PendingNativeProviderRegistration struct {
	Provider      *ai.Provider
	ExtensionPath string
}

// PendingVirtualModelRegistration stores a virtual model registered before runtime binding.
type PendingVirtualModelRegistration struct {
	Definition    VirtualModelDefinition
	ExtensionPath string
}

// ExtensionRuntime holds shared state and host-bound action callbacks for extensions.
type ExtensionRuntime struct {
	FlagValues                         map[string]any
	PendingProviderRegistrations       []PendingProviderRegistration
	PendingNativeProviderRegistrations []PendingNativeProviderRegistration
	PendingVirtualModelRegistrations   []PendingVirtualModelRegistration
	CreateContext                      func() ExtensionContext
	AssertActive                       func()
	Invalidate                         func(message ...string)
	TrackEventBusSubscription          func(unsubscribe func()) func()
	RegisterProvider                   func(name string, config ProviderConfig, extensionPath ...string)
	RegisterNativeProvider             func(provider *ai.Provider, extensionPath ...string)
	UnregisterProvider                 func(name string, extensionPath ...string)
	McpServers                         *McpServerRegistry
	RegisterVirtualModel               func(definition VirtualModelDefinition, extensionPath ...string)
	UnregisterVirtualModel             func(provider, id string)

	SendMessage      func(message CustomMessageInput, options ...*SendMessageOptions)
	SendUserMessage  func(content any, options ...*SendUserMessageOptions)
	AppendEntry      func(customType string, data any)
	SetSessionName   func(name string)
	GetSessionName   func() *string
	SetLabel         func(entryId string, label *string)
	GetActiveTools   func() []string
	GetAllTools      func() []ToolInfo
	GetSettings      func() Settings
	SetActiveTools   func(toolNames []string)
	RefreshTools     func()
	GetCommands      func() []SlashCommandInfo
	SetModel         func(ctx context.Context, model *ai.Model) (bool, error)
	GetThinkingLevel func() agent.ThinkingLevel
	SetThinkingLevel func(level agent.ThinkingLevel)
}

// ExtensionActions configures host action callbacks passed to ExtensionRunner.BindCore.
type ExtensionActions struct {
	SendMessage      func(message CustomMessageInput, options ...*SendMessageOptions)
	SendUserMessage  func(content any, options ...*SendUserMessageOptions)
	AppendEntry      func(customType string, data any)
	SetSessionName   func(name string)
	GetSessionName   func() *string
	SetLabel         func(entryId string, label *string)
	GetActiveTools   func() []string
	GetAllTools      func() []ToolInfo
	GetSettings      func() Settings
	SetActiveTools   func(toolNames []string)
	RefreshTools     func()
	GetCommands      func() []SlashCommandInfo
	SetModel         func(ctx context.Context, model *ai.Model) (bool, error)
	GetThinkingLevel func() agent.ThinkingLevel
	SetThinkingLevel func(level agent.ThinkingLevel)
}

// ExtensionContextActions configures ExtensionContext accessors passed to ExtensionRunner.BindCore.
type ExtensionContextActions struct {
	GetModel               func() *ai.Model
	GetScopedModels        func() []ScopedModel
	GetThinkingLevel       func() agent.ThinkingLevel
	IsIdle                 func() bool
	IsProjectTrusted       func() bool
	GetSignal              func() context.Context
	Abort                  func()
	HasPendingMessages     func() bool
	Shutdown               func()
	GetContextUsage        func() *ContextUsage
	Compact                func(options ...*CompactOptions)
	GetSystemPrompt        func() string
	GetSystemPromptOptions func() BuildSystemPromptOptions
	ExecuteTool            func(ctx context.Context, callerId, name string, args any, options *ExecuteToolOptions) (agent.AgentToolCallOutcome, error)
	GetCallableTools       func() []*agent.AgentTool
}

// ExtensionProviderActions optionally overrides provider/virtual-model registration in ExtensionRunner.BindCore.
type ExtensionProviderActions struct {
	RegisterProvider       func(name string, config ProviderConfig)
	RegisterNativeProvider func(provider *ai.Provider)
	UnregisterProvider     func(name string)
	RegisterVirtualModel   func(definition VirtualModelDefinition)
	UnregisterVirtualModel func(provider, id string)
}

// Extension represents a loaded extension and its registered items.
type Extension struct {
	Path                string
	ResolvedPath        string
	Hidden              bool
	Replaceable         bool
	SourceInfo          utils.SourceInfo
	Handlers            map[string][]any
	Tools               map[string]RegisteredTool
	MessageRenderers    map[string]MessageRenderer
	ToolRenderers       []ToolRendererResolver
	MarkdownTransformer *MarkdownTransformerRegistration
	EntryRenderers      map[string]EntryRenderer
	Commands            map[string]RegisteredCommand
	Flags               map[string]ExtensionFlag
	Shortcuts           map[tui.KeyId]ExtensionShortcut
}

// ExtensionLoadError records a failure encountered while discovering or loading an extension.
type ExtensionLoadError struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}

// ExtensionLoadWarning records a non-fatal warning from extension discovery or loading.
type ExtensionLoadWarning struct {
	Path    string `json:"path"`
	Warning string `json:"warning"`
}

// LoadExtensionsResult is the outcome of extension discovery and loading.
type LoadExtensionsResult struct {
	Extensions []Extension
	Errors     []ExtensionLoadError
	Warnings   []ExtensionLoadWarning
	Runtime    *ExtensionRuntime
}

// ExtensionError represents an error thrown by an extension event handler.
type ExtensionError struct {
	ExtensionPath string  `json:"extensionPath"`
	Event         string  `json:"event"`
	Error         string  `json:"error"`
	Stack         *string `json:"stack,omitzero"`
}

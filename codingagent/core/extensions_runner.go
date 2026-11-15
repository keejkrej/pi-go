// Ported from packages/coding-agent/src/core/extensions/runner.ts (pi v1.0.0).

package core

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/tui"
)

// ExtensionErrorListener receives errors thrown by extension handlers.
type ExtensionErrorListener func(err ExtensionError)

// DiscoveredResourcePath pairs a discovered resource path with its owning extension path.
type DiscoveredResourcePath struct {
	Path          string
	ExtensionPath string
}

// ResourcesDiscoverCombinedResult aggregates resource paths contributed by extensions.
type ResourcesDiscoverCombinedResult struct {
	SkillPaths  []DiscoveredResourcePath
	PromptPaths []DiscoveredResourcePath
	ThemePaths  []DiscoveredResourcePath
}

// BeforeAgentStartCombinedResult holds injected messages and normalized system prompt options.
type BeforeAgentStartCombinedResult struct {
	Messages            []BeforeAgentStartCustomMessage
	SystemPromptOptions NormalizedBuildSystemPromptOptions
}

// BoundaryBaseEvent identifies the turn or settle boundary being dispatched.
type BoundaryBaseEvent struct {
	Type        string
	Outcome     AgentActivityOutcome
	TurnIndex   int
	Message     agent.AgentMessage
	ToolResults []*ai.ToolResultMessage
}

// BoundaryDispatchResult is the combined outcome of boundary handlers.
type BoundaryDispatchResult struct {
	Entries  []SessionBoundaryDraft
	Continue bool
	Context  BoundaryContextPreview
	Valid    bool
}

type erNoOpUIContext struct{}

func (*erNoOpUIContext) Select(context.Context, string, []string, ...*ExtensionUIDialogOptions) (*string, error) {
	return nil, nil
}

func (*erNoOpUIContext) Confirm(context.Context, string, string, ...*ExtensionUIDialogOptions) (bool, error) {
	return false, nil
}

func (*erNoOpUIContext) Input(context.Context, string, *string, ...*ExtensionUIDialogOptions) (*string, error) {
	return nil, nil
}

func (*erNoOpUIContext) Notify(string, ...string) {}

func (*erNoOpUIContext) OnTerminalInput(TerminalInputHandler) func() {
	return func() {}
}

func (*erNoOpUIContext) SetStatus(string, *string)                                   {}
func (*erNoOpUIContext) SetWorkingMessage(...string)                                 {}
func (*erNoOpUIContext) SetWorkingVisible(bool)                                      {}
func (*erNoOpUIContext) SetWorkingIndicator(*WorkingIndicatorOptions)                {}
func (*erNoOpUIContext) SetHiddenThinkingLabel(...string)                            {}
func (*erNoOpUIContext) SetWidgetLines(string, []string, ...*ExtensionWidgetOptions) {}
func (*erNoOpUIContext) SetWidgetFactory(string, func(tui.TUI, *theme.Theme) tui.Component, ...*ExtensionWidgetOptions) {
}
func (*erNoOpUIContext) SetFooter(func(tui.TUI, *theme.Theme, ReadonlyFooterDataProvider) tui.Component) {
}
func (*erNoOpUIContext) SetHeader(func(tui.TUI, *theme.Theme) tui.Component) {}
func (*erNoOpUIContext) SetTitle(string)                                     {}

func (*erNoOpUIContext) Custom(
	context.Context,
	func(tui.TUI, *theme.Theme, *tui.KeybindingsManager, func(any)) (tui.Component, error),
	...*ExtensionCustomOptions,
) (any, error) {
	return nil, nil
}

func (*erNoOpUIContext) PasteToEditor(string) {}
func (*erNoOpUIContext) SetEditorText(string) {}
func (*erNoOpUIContext) GetEditorText() string {
	return ""
}

func (*erNoOpUIContext) Editor(context.Context, string, ...string) (*string, error) {
	return nil, nil
}

func (*erNoOpUIContext) AddAutocompleteProvider(AutocompleteProviderFactory) {}
func (*erNoOpUIContext) SetEditorComponent(EditorFactory)                    {}
func (*erNoOpUIContext) GetEditorComponent() EditorFactory                   { return nil }
func (*erNoOpUIContext) Theme() *theme.Theme                                 { return theme.CurrentTheme() }
func (*erNoOpUIContext) GetAllThemes() []ThemeInfo                           { return nil }
func (*erNoOpUIContext) GetTheme(string) *theme.Theme                        { return nil }
func (*erNoOpUIContext) SetThemeByName(string) SetThemeResult                { return SetThemeResult{Success: false} }
func (*erNoOpUIContext) SetTheme(*theme.Theme) SetThemeResult                { return SetThemeResult{Success: false} }
func (*erNoOpUIContext) GetToolsExpanded() bool                              { return false }
func (*erNoOpUIContext) SetToolsExpanded(bool)                               {}

// NoOpUIContext returns the default non-interactive ExtensionUIContext.
func NoOpUIContext() ExtensionUIContext {
	return &erNoOpUIContext{}
}

// ExtensionRunner is a nil-safe 0-extension barebone runner hosting core context/action bindings.
type ExtensionRunner struct {
	mu             sync.RWMutex
	extensions     []Extension
	runtime        *ExtensionRuntime
	uiContext      ExtensionUIContext
	mode           ExtensionMode
	cwd            string
	sessionManager *SessionManager
	modelRegistry  *ModelRegistry
	contextActions ExtensionContextActions
	commandActions ExtensionCommandContextActions

	staleMessage   string
	nextListenerId uint64
	errorListeners map[uint64]ExtensionErrorListener
}

// NewExtensionRunner creates an ExtensionRunner bound to runtime, sessionManager, and modelRegistry.
func NewExtensionRunner(
	extensions []Extension,
	runtime *ExtensionRuntime,
	cwd string,
	sessionManager *SessionManager,
	modelRegistry *ModelRegistry,
) *ExtensionRunner {
	if runtime == nil {
		runtime = CreateExtensionRuntime()
	}
	return &ExtensionRunner{
		extensions:     extensions,
		runtime:        runtime,
		uiContext:      NoOpUIContext(),
		mode:           ExtensionModePrint,
		cwd:            cwd,
		sessionManager: sessionManager,
		modelRegistry:  modelRegistry,
		errorListeners: make(map[uint64]ExtensionErrorListener),
	}
}

// BindCore wires host session actions and context accessors into the runner and its shared runtime.
func (r *ExtensionRunner) BindCore(
	actions ExtensionActions,
	contextActions ExtensionContextActions,
	providerActions ...*ExtensionProviderActions,
) {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.runtime == nil {
		r.runtime = CreateExtensionRuntime()
	}
	r.runtime.SendMessage = actions.SendMessage
	r.runtime.SendUserMessage = actions.SendUserMessage
	r.runtime.AppendEntry = actions.AppendEntry
	r.runtime.SetSessionName = actions.SetSessionName
	r.runtime.GetSessionName = actions.GetSessionName
	r.runtime.SetLabel = actions.SetLabel
	r.runtime.GetActiveTools = actions.GetActiveTools
	r.runtime.GetAllTools = actions.GetAllTools
	r.runtime.GetSettings = actions.GetSettings
	r.runtime.SetActiveTools = actions.SetActiveTools
	r.runtime.RefreshTools = actions.RefreshTools
	r.runtime.GetCommands = actions.GetCommands
	r.runtime.SetModel = actions.SetModel
	r.runtime.GetThinkingLevel = actions.GetThinkingLevel
	r.runtime.SetThinkingLevel = actions.SetThinkingLevel
	r.runtime.CreateContext = func() ExtensionContext {
		return r.CreateContext()
	}
	r.contextActions = contextActions
	if len(providerActions) > 0 && providerActions[0] != nil {
		pa := providerActions[0]
		if pa.RegisterProvider != nil {
			fn := pa.RegisterProvider
			r.runtime.RegisterProvider = func(name string, config ProviderConfig, _ ...string) {
				fn(name, config)
			}
		}
		if pa.RegisterNativeProvider != nil {
			fn := pa.RegisterNativeProvider
			r.runtime.RegisterNativeProvider = func(provider *ai.Provider, _ ...string) {
				fn(provider)
			}
		}
		if pa.UnregisterProvider != nil {
			fn := pa.UnregisterProvider
			r.runtime.UnregisterProvider = func(name string, _ ...string) {
				fn(name)
			}
		}
		if pa.RegisterVirtualModel != nil {
			fn := pa.RegisterVirtualModel
			r.runtime.RegisterVirtualModel = func(definition VirtualModelDefinition, _ ...string) {
				fn(definition)
			}
		}
		if pa.UnregisterVirtualModel != nil {
			r.runtime.UnregisterVirtualModel = pa.UnregisterVirtualModel
		}
	}
	r.mu.Unlock()
}

// BindCommandContext wires interactive session mutation actions for ExtensionCommandContext.
func (r *ExtensionRunner) BindCommandContext(actions *ExtensionCommandContextActions) {
	if r == nil {
		return
	}
	r.mu.Lock()
	if actions != nil {
		r.commandActions = *actions
	} else {
		r.commandActions = ExtensionCommandContextActions{}
	}
	r.mu.Unlock()
}

// SetUIContext updates the active UI context and presentation mode.
func (r *ExtensionRunner) SetUIContext(ui ExtensionUIContext, mode ...ExtensionMode) {
	if r == nil {
		return
	}
	r.mu.Lock()
	if ui != nil {
		r.uiContext = ui
	} else {
		r.uiContext = NoOpUIContext()
	}
	if len(mode) > 0 && mode[0] != "" {
		r.mode = mode[0]
	} else {
		r.mode = ExtensionModePrint
	}
	r.mu.Unlock()
}

// GetUIContext returns the active UI context (falling back to a no-op UI context).
func (r *ExtensionRunner) GetUIContext() ExtensionUIContext {
	if r == nil {
		return NoOpUIContext()
	}
	r.mu.RLock()
	ui := r.uiContext
	r.mu.RUnlock()
	if ui == nil {
		return NoOpUIContext()
	}
	return ui
}

// HasUI reports whether an interactive UI context (non-no-op) is currently bound.
func (r *ExtensionRunner) HasUI() bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	ui := r.uiContext
	r.mu.RUnlock()
	if ui == nil {
		return false
	}
	_, isNoOp := ui.(*erNoOpUIContext)
	return !isNoOp
}

// Invalidate marks previously created extension contexts as stale.
func (r *ExtensionRunner) Invalidate(message ...string) {
	if r == nil {
		return
	}
	msg := "This extension ctx is stale after session replacement or reload."
	if len(message) > 0 && message[0] != "" {
		msg = message[0]
	}
	r.mu.Lock()
	if r.staleMessage == "" {
		r.staleMessage = msg
		if r.runtime != nil && r.runtime.Invalidate != nil {
			r.runtime.Invalidate(msg)
		}
	}
	r.mu.Unlock()
}

func (r *ExtensionRunner) assertActive() {
	if r == nil {
		return
	}
	r.mu.RLock()
	msg := r.staleMessage
	r.mu.RUnlock()
	if msg != "" {
		panic(errors.New(msg))
	}
}

// OnError registers a listener for extension errors and returns an unsubscribe function.
func (r *ExtensionRunner) OnError(listener ExtensionErrorListener) func() {
	if r == nil || listener == nil {
		return func() {}
	}
	r.mu.Lock()
	if r.errorListeners == nil {
		r.errorListeners = make(map[uint64]ExtensionErrorListener)
	}
	r.nextListenerId++
	id := r.nextListenerId
	r.errorListeners[id] = listener
	r.mu.Unlock()
	return func() {
		r.mu.Lock()
		delete(r.errorListeners, id)
		r.mu.Unlock()
	}
}

// EmitError notifies registered error listeners.
func (r *ExtensionRunner) EmitError(err ExtensionError) {
	if r == nil {
		return
	}
	r.mu.RLock()
	listeners := make([]ExtensionErrorListener, 0, len(r.errorListeners))
	for _, l := range r.errorListeners {
		listeners = append(listeners, l)
	}
	r.mu.RUnlock()
	for _, l := range listeners {
		if l != nil {
			l(err)
		}
	}
}

// ReportUnhandledMcpServers is a no-op in barebone 0-extension mode.
func (r *ExtensionRunner) ReportUnhandledMcpServers() {}

// HasHandlers always returns false in barebone 0-extension mode.
func (r *ExtensionRunner) HasHandlers(string) bool {
	return false
}

// GetExtensionPaths returns nil in barebone 0-extension mode.
func (r *ExtensionRunner) GetExtensionPaths() []string { return nil }

// GetAllRegisteredTools returns nil in barebone 0-extension mode.
func (r *ExtensionRunner) GetAllRegisteredTools() []RegisteredTool { return nil }

// GetToolDefinition returns nil in barebone 0-extension mode.
func (r *ExtensionRunner) GetToolDefinition(string) *ToolDefinition { return nil }

// GetFlags returns an empty map in barebone 0-extension mode.
func (r *ExtensionRunner) GetFlags() map[string]ExtensionFlag {
	return map[string]ExtensionFlag{}
}

// SetFlagValue records a CLI extension flag value in the runner's runtime.
func (r *ExtensionRunner) SetFlagValue(name string, value any) {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.runtime != nil {
		if r.runtime.FlagValues == nil {
			r.runtime.FlagValues = make(map[string]any)
		}
		r.runtime.FlagValues[name] = value
	}
	r.mu.Unlock()
}

// GetFlagValues returns recorded extension flag values.
func (r *ExtensionRunner) GetFlagValues() map[string]any {
	if r == nil || r.runtime == nil || r.runtime.FlagValues == nil {
		return map[string]any{}
	}
	r.mu.RLock()
	out := make(map[string]any, len(r.runtime.FlagValues))
	for k, v := range r.runtime.FlagValues {
		out[k] = v
	}
	r.mu.RUnlock()
	return out
}

// GetShortcuts returns an empty map in barebone 0-extension mode.
func (r *ExtensionRunner) GetShortcuts(tui.KeybindingsConfig) map[tui.KeyId]ExtensionShortcut {
	return map[tui.KeyId]ExtensionShortcut{}
}

// GetShortcutDiagnostics returns nil in barebone 0-extension mode.
func (r *ExtensionRunner) GetShortcutDiagnostics() []ResourceDiagnostic { return nil }

// GetMessageRenderer returns nil in barebone 0-extension mode.
func (r *ExtensionRunner) GetMessageRenderer(string) MessageRenderer { return nil }

// GetMarkdownTransformers returns nil in barebone 0-extension mode.
func (r *ExtensionRunner) GetMarkdownTransformers() []MarkdownTransformer { return nil }

// ResolveToolRenderers calls base in barebone 0-extension mode.
func (r *ExtensionRunner) ResolveToolRenderers(_ string, base func() *ToolRenderers) *ToolRenderers {
	if base == nil {
		return nil
	}
	return base()
}

// GetEntryRenderer returns nil in barebone 0-extension mode.
func (r *ExtensionRunner) GetEntryRenderer(string) EntryRenderer { return nil }

// GetModelRegistry returns the ModelRegistry bound to this runner.
func (r *ExtensionRunner) GetModelRegistry() *ModelRegistry {
	if r == nil {
		return nil
	}
	return r.modelRegistry
}

// GetRegisteredCommands returns nil in barebone 0-extension mode.
func (r *ExtensionRunner) GetRegisteredCommands() []ResolvedCommand { return nil }

// GetCommandDiagnostics returns nil in barebone 0-extension mode.
func (r *ExtensionRunner) GetCommandDiagnostics() []ResourceDiagnostic { return nil }

// GetCommand returns nil in barebone 0-extension mode.
func (r *ExtensionRunner) GetCommand(string) *ResolvedCommand { return nil }

// Shutdown is a nil-safe no-op in barebone 0-extension mode.
func (r *ExtensionRunner) Shutdown() {}

// GetActiveTools returns the currently active tool names from the bound host action.
func (r *ExtensionRunner) GetActiveTools() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	rt := r.runtime
	r.mu.RUnlock()
	if rt != nil && rt.GetActiveTools != nil {
		return rt.GetActiveTools()
	}
	return nil
}

// CreateContext returns an ExtensionContext backed by this runner's bound state.
func (r *ExtensionRunner) CreateContext() ExtensionContext {
	return &erContext{runner: r}
}

// CreateToolContext returns an ExtensionToolContext for executing a tool call and its nested calls.
func (r *ExtensionRunner) CreateToolContext(toolCallId string, signal context.Context) ExtensionToolContext {
	return &erToolContext{
		erContext:  erContext{runner: r},
		toolCallId: toolCallId,
		signal:     signal,
	}
}

// CreateCommandContext returns an ExtensionCommandContext for slash command execution.
func (r *ExtensionRunner) CreateCommandContext() ExtensionCommandContext {
	return &erCommandContext{
		erContext: erContext{runner: r},
	}
}

// Emit is a no-op returning nil in barebone 0-extension mode.
func (r *ExtensionRunner) Emit(context.Context, ExtensionEvent) any {
	return nil
}

// EmitBoundary builds the boundary context once and returns a clean no-op dispatch result.
func (r *ExtensionRunner) EmitBoundary(
	ctx context.Context,
	_ BoundaryBaseEvent,
	buildContext func(entries []SessionBoundaryDraft) (BoundaryContextPreview, error),
) (BoundaryDispatchResult, error) {
	if buildContext == nil {
		return BoundaryDispatchResult{Valid: true}, nil
	}
	preview, err := buildContext(nil)
	if err != nil {
		return BoundaryDispatchResult{
			Entries:  nil,
			Continue: false,
			Context:  preview,
			Valid:    false,
		}, err
	}
	return BoundaryDispatchResult{
		Entries:  nil,
		Continue: false,
		Context:  preview,
		Valid:    true,
	}, nil
}

// EmitCacheWarmingDecision returns event.Action unchanged in barebone 0-extension mode.
func (r *ExtensionRunner) EmitCacheWarmingDecision(_ context.Context, event CacheWarmingDecisionEvent) ai.CacheWarmingAction {
	return event.Action
}

// EmitMessageEnd returns nil in barebone 0-extension mode.
func (r *ExtensionRunner) EmitMessageEnd(context.Context, MessageEndEvent) agent.AgentMessage {
	return nil
}

// EmitToolResult returns nil in barebone 0-extension mode.
func (r *ExtensionRunner) EmitToolResult(context.Context, ToolResultEvent) *ToolResultEventResult {
	return nil
}

// EmitToolCall returns nil in barebone 0-extension mode.
func (r *ExtensionRunner) EmitToolCall(context.Context, ToolCallEvent) *ToolCallEventResult {
	return nil
}

// EmitUserBash returns nil in barebone 0-extension mode.
func (r *ExtensionRunner) EmitUserBash(context.Context, UserBashEvent) *UserBashEventResult {
	return nil
}

// EmitContext returns messages unchanged in barebone 0-extension mode.
func (r *ExtensionRunner) EmitContext(_ context.Context, messages []agent.AgentMessage, _ ...any) []agent.AgentMessage {
	return messages
}

// EmitBeforeProviderRequest returns payload unchanged in barebone 0-extension mode.
func (r *ExtensionRunner) EmitBeforeProviderRequest(_ context.Context, payload any) any {
	return payload
}

// EmitBeforeProviderHeaders returns headers unchanged in barebone 0-extension mode.
func (r *ExtensionRunner) EmitBeforeProviderHeaders(_ context.Context, headers ai.ProviderHeaders) ai.ProviderHeaders {
	return headers
}

// EmitBeforeAgentStart returns normalized options with no injected messages in barebone 0-extension mode.
func (r *ExtensionRunner) EmitBeforeAgentStart(
	_ context.Context,
	_ BeforeAgentStartTrigger,
	systemPromptOptions BuildSystemPromptOptions,
) BeforeAgentStartCombinedResult {
	return BeforeAgentStartCombinedResult{
		Messages:            nil,
		SystemPromptOptions: NormalizeBuildSystemPromptOptions(systemPromptOptions),
	}
}

// EmitResourcesDiscover returns empty discovered paths in barebone 0-extension mode.
func (r *ExtensionRunner) EmitResourcesDiscover(context.Context, string, ResourcesDiscoverReason) ResourcesDiscoverCombinedResult {
	return ResourcesDiscoverCombinedResult{}
}

// EmitInput returns action "continue" in barebone 0-extension mode.
func (r *ExtensionRunner) EmitInput(
	_ context.Context,
	_ string,
	_ []ai.ImageContent,
	_ InputSource,
	_ ...string,
) InputEventResult {
	return InputEventResult{Action: "continue"}
}

// EmitSessionShutdownEvent returns false in barebone 0-extension mode (no session_shutdown handlers).
func EmitSessionShutdownEvent(_ context.Context, _ *ExtensionRunner, _ SessionShutdownEvent) bool {
	return false
}

// EmitProjectTrustEvent returns nil, nil in barebone 0-extension mode.
func EmitProjectTrustEvent(
	_ *LoadExtensionsResult,
	_ ProjectTrustEvent,
	_ ProjectTrustContext,
) (*ProjectTrustEventResult, []ExtensionError) {
	return nil, nil
}

type erContext struct {
	runner *ExtensionRunner
}

func (c *erContext) UI() ExtensionUIContext {
	c.runner.assertActive()
	return c.runner.GetUIContext()
}

func (c *erContext) Mode() ExtensionMode {
	c.runner.assertActive()
	if c.runner == nil {
		return ExtensionModePrint
	}
	c.runner.mu.RLock()
	m := c.runner.mode
	c.runner.mu.RUnlock()
	if m == "" {
		return ExtensionModePrint
	}
	return m
}

func (c *erContext) HasUI() bool {
	c.runner.assertActive()
	return c.runner.HasUI()
}

func (c *erContext) Cwd() string {
	c.runner.assertActive()
	if c.runner == nil {
		return ""
	}
	return c.runner.cwd
}

func (c *erContext) SessionManager() ReadonlySessionManager {
	c.runner.assertActive()
	if c.runner == nil || c.runner.sessionManager == nil {
		return nil
	}
	return c.runner.sessionManager
}

func (c *erContext) ModelRegistry() *ModelRegistry {
	c.runner.assertActive()
	if c.runner == nil {
		return nil
	}
	return c.runner.modelRegistry
}

func (c *erContext) Model() *ai.Model {
	c.runner.assertActive()
	if c.runner == nil {
		return nil
	}
	c.runner.mu.RLock()
	fn := c.runner.contextActions.GetModel
	c.runner.mu.RUnlock()
	if fn == nil {
		return nil
	}
	return fn()
}

func (c *erContext) ScopedModels() []ScopedModel {
	c.runner.assertActive()
	if c.runner == nil {
		return nil
	}
	c.runner.mu.RLock()
	fn := c.runner.contextActions.GetScopedModels
	c.runner.mu.RUnlock()
	if fn == nil {
		return nil
	}
	return fn()
}

func (c *erContext) ThinkingLevel() agent.ThinkingLevel {
	c.runner.assertActive()
	if c.runner == nil {
		return ""
	}
	c.runner.mu.RLock()
	fn := c.runner.contextActions.GetThinkingLevel
	c.runner.mu.RUnlock()
	if fn == nil {
		return ""
	}
	return fn()
}

func (c *erContext) IsIdle() bool {
	c.runner.assertActive()
	if c.runner == nil {
		return true
	}
	c.runner.mu.RLock()
	fn := c.runner.contextActions.IsIdle
	c.runner.mu.RUnlock()
	if fn == nil {
		return true
	}
	return fn()
}

func (c *erContext) IsProjectTrusted() bool {
	c.runner.assertActive()
	if c.runner == nil {
		return true
	}
	c.runner.mu.RLock()
	fn := c.runner.contextActions.IsProjectTrusted
	c.runner.mu.RUnlock()
	if fn == nil {
		return true
	}
	return fn()
}

func (c *erContext) Signal() context.Context {
	c.runner.assertActive()
	if c.runner == nil {
		return context.Background()
	}
	c.runner.mu.RLock()
	fn := c.runner.contextActions.GetSignal
	c.runner.mu.RUnlock()
	if fn == nil {
		return context.Background()
	}
	return fn()
}

func (c *erContext) Abort() {
	c.runner.assertActive()
	if c.runner == nil {
		return
	}
	c.runner.mu.RLock()
	fn := c.runner.contextActions.Abort
	c.runner.mu.RUnlock()
	if fn != nil {
		fn()
	}
}

func (c *erContext) HasPendingMessages() bool {
	c.runner.assertActive()
	if c.runner == nil {
		return false
	}
	c.runner.mu.RLock()
	fn := c.runner.contextActions.HasPendingMessages
	c.runner.mu.RUnlock()
	if fn == nil {
		return false
	}
	return fn()
}

func (c *erContext) Shutdown() {
	c.runner.assertActive()
	if c.runner == nil {
		return
	}
	c.runner.mu.RLock()
	fn := c.runner.contextActions.Shutdown
	c.runner.mu.RUnlock()
	if fn != nil {
		fn()
	}
}

func (c *erContext) GetContextUsage() *ContextUsage {
	c.runner.assertActive()
	if c.runner == nil {
		return nil
	}
	c.runner.mu.RLock()
	fn := c.runner.contextActions.GetContextUsage
	c.runner.mu.RUnlock()
	if fn == nil {
		return nil
	}
	return fn()
}

func (c *erContext) Compact(options ...*CompactOptions) {
	c.runner.assertActive()
	if c.runner == nil {
		return
	}
	c.runner.mu.RLock()
	fn := c.runner.contextActions.Compact
	c.runner.mu.RUnlock()
	if fn != nil {
		fn(options...)
	}
}

func (c *erContext) GetSystemPrompt() string {
	c.runner.assertActive()
	if c.runner == nil {
		return ""
	}
	c.runner.mu.RLock()
	fn := c.runner.contextActions.GetSystemPrompt
	c.runner.mu.RUnlock()
	if fn == nil {
		return ""
	}
	return fn()
}

type erToolContext struct {
	erContext
	toolCallId string
	signal     context.Context
}

func (c *erToolContext) Tools() []*agent.AgentTool {
	c.runner.assertActive()
	if c.runner == nil {
		return nil
	}
	c.runner.mu.RLock()
	fn := c.runner.contextActions.GetCallableTools
	c.runner.mu.RUnlock()
	if fn == nil {
		return nil
	}
	return fn()
}

func (c *erToolContext) ExecuteTool(
	ctx context.Context,
	name string,
	args any,
	options ...*ExecuteToolOptions,
) (agent.AgentToolCallOutcome, error) {
	c.runner.assertActive()
	var opt ExecuteToolOptions
	if len(options) > 0 && options[0] != nil {
		opt = *options[0]
	}
	if opt.Signal == nil {
		if ctx != nil {
			opt.Signal = ctx
		} else {
			opt.Signal = c.signal
		}
	}
	if opt.Signal == nil {
		opt.Signal = context.Background()
	}

	if c.runner == nil {
		return erUnavailableNestedOutcome(c.toolCallId, name), nil
	}
	c.runner.mu.RLock()
	fn := c.runner.contextActions.ExecuteTool
	c.runner.mu.RUnlock()
	if fn == nil {
		return erUnavailableNestedOutcome(c.toolCallId, name), nil
	}
	return fn(opt.Signal, c.toolCallId, name, args, &opt)
}

func erUnavailableNestedOutcome(callerId, name string) agent.AgentToolCallOutcome {
	return agent.AgentToolCallOutcome{
		ToolCall: &agent.AgentToolCall{
			Type: "toolCall",
			Id:   fmt.Sprintf("%s/0", callerId),
			Name: name,
		},
		Result: agent.AgentToolResult{
			Content: ai.ContentList{
				&ai.TextContent{
					Type: "text",
					Text: "Nested tool calls are not available in this context",
				},
			},
		},
		IsError: true,
	}
}

type erCommandContext struct {
	erContext
}

func (c *erCommandContext) WaitForIdle(ctx context.Context) error {
	c.runner.assertActive()
	if c.runner == nil {
		return nil
	}
	c.runner.mu.RLock()
	fn := c.runner.commandActions.WaitForIdle
	c.runner.mu.RUnlock()
	if fn == nil {
		return nil
	}
	return fn(ctx)
}

func (c *erCommandContext) NewSession(ctx context.Context, options ...*ExtensionNewSessionOptions) (CancelledResult, error) {
	c.runner.assertActive()
	if c.runner == nil {
		return CancelledResult{Cancelled: false}, nil
	}
	c.runner.mu.RLock()
	fn := c.runner.commandActions.NewSession
	c.runner.mu.RUnlock()
	if fn == nil {
		return CancelledResult{Cancelled: false}, nil
	}
	return fn(ctx, options...)
}

func (c *erCommandContext) Fork(ctx context.Context, entryId string, options ...*ExtensionForkOptions) (CancelledResult, error) {
	c.runner.assertActive()
	if c.runner == nil {
		return CancelledResult{Cancelled: false}, nil
	}
	c.runner.mu.RLock()
	fn := c.runner.commandActions.Fork
	c.runner.mu.RUnlock()
	if fn == nil {
		return CancelledResult{Cancelled: false}, nil
	}
	return fn(ctx, entryId, options...)
}

func (c *erCommandContext) NavigateTree(ctx context.Context, targetId string, options ...*ExtensionNavigateTreeOptions) (CancelledResult, error) {
	c.runner.assertActive()
	if c.runner == nil {
		return CancelledResult{Cancelled: false}, nil
	}
	c.runner.mu.RLock()
	fn := c.runner.commandActions.NavigateTree
	c.runner.mu.RUnlock()
	if fn == nil {
		return CancelledResult{Cancelled: false}, nil
	}
	return fn(ctx, targetId, options...)
}

func (c *erCommandContext) SwitchSession(ctx context.Context, sessionPath string, options ...*ExtensionSwitchSessionOptions) (CancelledResult, error) {
	c.runner.assertActive()
	if c.runner == nil {
		return CancelledResult{Cancelled: false}, nil
	}
	c.runner.mu.RLock()
	fn := c.runner.commandActions.SwitchSession
	c.runner.mu.RUnlock()
	if fn == nil {
		return CancelledResult{Cancelled: false}, nil
	}
	return fn(ctx, sessionPath, options...)
}

func (c *erCommandContext) Reload(ctx context.Context) error {
	c.runner.assertActive()
	if c.runner == nil {
		return nil
	}
	c.runner.mu.RLock()
	fn := c.runner.commandActions.Reload
	c.runner.mu.RUnlock()
	if fn == nil {
		return nil
	}
	return fn(ctx)
}

func (c *erCommandContext) GetSystemPromptOptions() BuildSystemPromptOptions {
	c.runner.assertActive()
	if c.runner == nil {
		return BuildSystemPromptOptions{}
	}
	c.runner.mu.RLock()
	fn := c.runner.contextActions.GetSystemPromptOptions
	cwd := c.runner.cwd
	c.runner.mu.RUnlock()
	if fn == nil {
		return BuildSystemPromptOptions{Cwd: cwd}
	}
	return fn()
}

func (c *erCommandContext) SendUserMessage(_ context.Context, content any, options ...*SendUserMessageOptions) error {
	c.runner.assertActive()
	if c.runner == nil {
		return nil
	}
	c.runner.mu.RLock()
	rt := c.runner.runtime
	c.runner.mu.RUnlock()
	if rt != nil && rt.SendUserMessage != nil {
		rt.SendUserMessage(content, options...)
	}
	return nil
}

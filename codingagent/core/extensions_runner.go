// Ported from packages/coding-agent/src/core/extensions/runner.ts (pi v1.0.0).

package core

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"runtime/debug"
	"slices"
	"strings"
	"sync"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/internal/jsonx"
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

type erActiveUIPrompt struct {
	kind  UIPromptKind
	title *string
}

var (
	erRuntimeCleanupMu sync.Mutex
	erRuntimeCleanups  = make(map[*ExtensionRuntime][]func())
)

// erRegisterRuntimeCleanup attaches an idempotent teardown callback (e.g., closing a QuickJS VM)
// to rt so ExtensionRunner.Shutdown runs it when the runtime is torn down.
func erRegisterRuntimeCleanup(rt *ExtensionRuntime, fn func()) {
	if rt == nil || fn == nil {
		return
	}
	erRuntimeCleanupMu.Lock()
	erRuntimeCleanups[rt] = append(erRuntimeCleanups[rt], fn)
	erRuntimeCleanupMu.Unlock()
}

// erRunRuntimeCleanups pops and invokes all cleanup hooks registered for rt.
func erRunRuntimeCleanups(rt *ExtensionRuntime) {
	if rt == nil {
		return
	}
	erRuntimeCleanupMu.Lock()
	hooks := erRuntimeCleanups[rt]
	delete(erRuntimeCleanups, rt)
	erRuntimeCleanupMu.Unlock()
	for _, fn := range hooks {
		if fn == nil {
			continue
		}
		func() {
			defer func() { _ = recover() }()
			fn()
		}()
	}
}

// ExtensionRunner executes loaded extensions, manages lifecycle hooks, and dispatches events.
type ExtensionRunner struct {
	mu                  sync.RWMutex
	extensions          []Extension
	runtime             *ExtensionRuntime
	uiContext           ExtensionUIContext
	rawUIContext        ExtensionUIContext
	mode                ExtensionMode
	cwd                 string
	sessionManager      *SessionManager
	modelRegistry       *ModelRegistry
	contextActions      ExtensionContextActions
	commandActions      ExtensionCommandContextActions
	shortcutDiagnostics []ResourceDiagnostic
	commandDiagnostics  []ResourceDiagnostic
	reportedMcpServers  map[string]struct{}
	uiPromptDepth       int
	activeUIPrompt      *erActiveUIPrompt
	cleanupHooks        []func()

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
	filtered, _ := erFilterReplacedExtensions(extensions)
	noop := NoOpUIContext()
	return &ExtensionRunner{
		extensions:         filtered,
		runtime:            runtime,
		uiContext:          noop,
		rawUIContext:       noop,
		mode:               ExtensionModePrint,
		cwd:                cwd,
		sessionManager:     sessionManager,
		modelRegistry:      modelRegistry,
		reportedMcpServers: make(map[string]struct{}),
		errorListeners:     make(map[uint64]ExtensionErrorListener),
	}
}

// RegisterCleanup registers a teardown callback invoked during Shutdown.
func (r *ExtensionRunner) RegisterCleanup(fn func()) {
	if r == nil || fn == nil {
		return
	}
	r.mu.Lock()
	r.cleanupHooks = append(r.cleanupHooks, fn)
	r.mu.Unlock()
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
	var pa *ExtensionProviderActions
	if len(providerActions) > 0 {
		pa = providerActions[0]
	}

	r.mu.Lock()
	if r.runtime == nil {
		r.runtime = CreateExtensionRuntime()
	}
	rt := r.runtime
	rt.SendMessage = actions.SendMessage
	rt.SendUserMessage = actions.SendUserMessage
	rt.AppendEntry = actions.AppendEntry
	rt.SetSessionName = actions.SetSessionName
	rt.GetSessionName = actions.GetSessionName
	rt.SetLabel = actions.SetLabel
	rt.GetActiveTools = actions.GetActiveTools
	rt.GetAllTools = actions.GetAllTools
	rt.GetSettings = actions.GetSettings
	rt.SetActiveTools = actions.SetActiveTools
	rt.RefreshTools = actions.RefreshTools
	rt.GetCommands = actions.GetCommands
	rt.SetModel = actions.SetModel
	rt.GetThinkingLevel = actions.GetThinkingLevel
	rt.SetThinkingLevel = actions.SetThinkingLevel
	rt.CreateContext = func() ExtensionContext {
		return r.CreateContext()
	}
	r.contextActions = contextActions

	modelRegistry := r.modelRegistry
	pendingProviders := slices.Clone(rt.PendingProviderRegistrations)
	rt.PendingProviderRegistrations = nil
	pendingNativeProviders := slices.Clone(rt.PendingNativeProviderRegistrations)
	rt.PendingNativeProviderRegistrations = nil
	pendingVirtualModels := slices.Clone(rt.PendingVirtualModelRegistrations)
	rt.PendingVirtualModelRegistrations = nil

	registerProvider := func(name string, config ProviderConfig) error {
		if pa != nil && pa.RegisterProvider != nil {
			pa.RegisterProvider(name, config)
			return nil
		}
		if modelRegistry != nil {
			return modelRegistry.RegisterProvider(name, config)
		}
		return nil
	}
	registerNativeProvider := func(provider *ai.Provider) error {
		if pa != nil && pa.RegisterNativeProvider != nil {
			pa.RegisterNativeProvider(provider)
			return nil
		}
		if modelRegistry != nil {
			return modelRegistry.RegisterNativeProvider(provider)
		}
		return nil
	}
	unregisterProvider := func(name string) {
		if pa != nil && pa.UnregisterProvider != nil {
			pa.UnregisterProvider(name)
			return
		}
		if modelRegistry != nil {
			modelRegistry.UnregisterProvider(name)
		}
	}
	registerVirtualModel := func(definition VirtualModelDefinition) error {
		if pa != nil && pa.RegisterVirtualModel != nil {
			pa.RegisterVirtualModel(definition)
			return nil
		}
		if modelRegistry != nil {
			return modelRegistry.RegisterVirtualModel(definition)
		}
		return nil
	}
	unregisterVirtualModel := func(provider, id string) {
		if pa != nil && pa.UnregisterVirtualModel != nil {
			pa.UnregisterVirtualModel(provider, id)
			return
		}
		if modelRegistry != nil {
			modelRegistry.UnregisterVirtualModel(provider, id)
		}
	}

	rt.RegisterProvider = func(name string, config ProviderConfig, extensionPath ...string) {
		if err := registerProvider(name, config); err != nil {
			extPath := ""
			if len(extensionPath) > 0 {
				extPath = extensionPath[0]
			}
			r.EmitError(ExtensionError{
				ExtensionPath: extPath,
				Event:         "register_provider",
				Error:         err.Error(),
			})
		}
	}
	rt.RegisterNativeProvider = func(provider *ai.Provider, extensionPath ...string) {
		if err := registerNativeProvider(provider); err != nil {
			extPath := ""
			if len(extensionPath) > 0 {
				extPath = extensionPath[0]
			}
			r.EmitError(ExtensionError{
				ExtensionPath: extPath,
				Event:         "register_provider",
				Error:         err.Error(),
			})
		}
	}
	rt.UnregisterProvider = func(name string, _ ...string) {
		unregisterProvider(name)
	}
	rt.RegisterVirtualModel = func(definition VirtualModelDefinition, extensionPath ...string) {
		if err := registerVirtualModel(definition); err != nil {
			extPath := ""
			if len(extensionPath) > 0 {
				extPath = extensionPath[0]
			}
			r.EmitError(ExtensionError{
				ExtensionPath: extPath,
				Event:         "register_virtual_model",
				Error:         err.Error(),
			})
		}
	}
	rt.UnregisterVirtualModel = unregisterVirtualModel
	mcpServers := rt.McpServers
	r.mu.Unlock()

	if mcpServers != nil {
		mcpServers.SetChangeListener(func() {
			_ = r.Emit(context.Background(), McpServersChangeEvent{
				Type:    "mcp_servers_change",
				Servers: mcpServers.List(),
			})
			r.ReportUnhandledMcpServers()
		})
	}

	for _, reg := range pendingProviders {
		if err := registerProvider(reg.Name, reg.Config); err != nil {
			r.EmitError(ExtensionError{
				ExtensionPath: reg.ExtensionPath,
				Event:         "register_provider",
				Error:         err.Error(),
			})
		}
	}
	for _, reg := range pendingNativeProviders {
		if err := registerNativeProvider(reg.Provider); err != nil {
			r.EmitError(ExtensionError{
				ExtensionPath: reg.ExtensionPath,
				Event:         "register_provider",
				Error:         err.Error(),
			})
		}
	}
	for _, reg := range pendingVirtualModels {
		if err := registerVirtualModel(reg.Definition); err != nil {
			r.EmitError(ExtensionError{
				ExtensionPath: reg.ExtensionPath,
				Event:         "register_virtual_model",
				Error:         err.Error(),
			})
		}
	}
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
	if ui == nil {
		noop := NoOpUIContext()
		r.rawUIContext = noop
		r.uiContext = noop
	} else if _, isNoOp := ui.(*erNoOpUIContext); isNoOp {
		r.rawUIContext = ui
		r.uiContext = ui
	} else {
		r.rawUIContext = ui
		r.uiContext = &erUIPromptWrapper{runner: r, inner: ui}
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
	ui := r.rawUIContext
	if ui == nil {
		ui = r.uiContext
	}
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
	var invalidateFn func(...string)
	r.mu.Lock()
	if r.staleMessage == "" {
		r.staleMessage = msg
		if r.runtime != nil && r.runtime.Invalidate != nil {
			invalidateFn = r.runtime.Invalidate
		}
	}
	r.mu.Unlock()
	if invalidateFn != nil {
		invalidateFn(msg)
	}
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

// ReportUnhandledMcpServers emits an error for each registered MCP server when no extension
// handles `mcp_servers_change`.
func (r *ExtensionRunner) ReportUnhandledMcpServers() {
	if r == nil || r.HasHandlers("mcp_servers_change") {
		return
	}
	r.mu.RLock()
	rt := r.runtime
	r.mu.RUnlock()
	if rt == nil || rt.McpServers == nil {
		return
	}
	servers := rt.McpServers.List()
	if len(servers) == 0 {
		return
	}
	var toReport []RegisteredMcpServer
	r.mu.Lock()
	if r.reportedMcpServers == nil {
		r.reportedMcpServers = make(map[string]struct{})
	}
	for _, srv := range servers {
		if _, seen := r.reportedMcpServers[srv.Name]; seen {
			continue
		}
		r.reportedMcpServers[srv.Name] = struct{}{}
		toReport = append(toReport, srv)
	}
	r.mu.Unlock()

	for _, srv := range toReport {
		r.EmitError(ExtensionError{
			ExtensionPath: srv.ExtensionPath,
			Event:         "register_mcp_server",
			Error:         fmt.Sprintf(`MCP server "%s" is registered, but no loaded extension connects MCP servers; another extension may have replaced the built-in MCP support`, srv.Name),
		})
	}
}

// HasHandlers reports whether any loaded extension has registered a handler for eventType.
func (r *ExtensionRunner) HasHandlers(eventType string) bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for i := range r.extensions {
		if len(r.extensions[i].Handlers[eventType]) > 0 {
			return true
		}
	}
	return false
}

// GetExtensionPaths returns the load paths of active extensions in load order.
func (r *ExtensionRunner) GetExtensionPaths() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.extensions) == 0 {
		return nil
	}
	paths := make([]string, len(r.extensions))
	for i := range r.extensions {
		paths[i] = r.extensions[i].Path
	}
	return paths
}

// GetAllRegisteredTools returns all registered tools in load order (first registration per name wins).
func (r *ExtensionRunner) GetAllRegisteredTools() []RegisteredTool {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	exts := slices.Clone(r.extensions)
	r.mu.RUnlock()

	seen := make(map[string]struct{})
	var out []RegisteredTool
	for _, ext := range exts {
		for _, name := range erSortedKeys(ext.Tools) {
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			out = append(out, ext.Tools[name])
		}
	}
	return out
}

// GetToolDefinition returns the first registered tool definition matching toolName.
func (r *ExtensionRunner) GetToolDefinition(toolName string) *ToolDefinition {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for i := range r.extensions {
		if tool, ok := r.extensions[i].Tools[toolName]; ok {
			def := tool.Definition
			return &def
		}
	}
	return nil
}

// GetFlags returns all registered extension CLI flags (first registration per name wins).
func (r *ExtensionRunner) GetFlags() map[string]ExtensionFlag {
	allFlags := make(map[string]ExtensionFlag)
	if r == nil {
		return allFlags
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for i := range r.extensions {
		for _, name := range erSortedKeys(r.extensions[i].Flags) {
			if _, exists := allFlags[name]; !exists {
				allFlags[name] = r.extensions[i].Flags[name]
			}
		}
	}
	return allFlags
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
	if r == nil {
		return map[string]any{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.runtime == nil || r.runtime.FlagValues == nil {
		return map[string]any{}
	}
	return maps.Clone(r.runtime.FlagValues)
}

// GetShortcuts resolves extension keyboard shortcuts against resolvedKeybindings and records conflicts.
func (r *ExtensionRunner) GetShortcuts(resolvedKeybindings tui.KeybindingsConfig) map[tui.KeyId]ExtensionShortcut {
	extensionShortcuts := make(map[tui.KeyId]ExtensionShortcut)
	if r == nil {
		return extensionShortcuts
	}
	builtinKeybindings := erBuildBuiltinKeybindings(resolvedKeybindings)

	r.mu.RLock()
	exts := slices.Clone(r.extensions)
	r.mu.RUnlock()

	var diagnostics []ResourceDiagnostic
	addDiag := func(msg, extPath string) {
		p := extPath
		diagnostics = append(diagnostics, ResourceDiagnostic{
			Type:    ResourceDiagnosticTypeWarning,
			Message: msg,
			Path:    &p,
		})
	}

	for _, ext := range exts {
		for _, rawKey := range erSortedKeys(ext.Shortcuts) {
			shortcut := ext.Shortcuts[rawKey]
			normalizedKey := tui.KeyId(strings.ToLower(string(rawKey)))
			if bi, ok := builtinKeybindings[normalizedKey]; ok {
				if bi.restrictOverride {
					addDiag(
						fmt.Sprintf("Extension shortcut '%s' from %s conflicts with built-in shortcut. Skipping.", rawKey, shortcut.ExtensionPath),
						shortcut.ExtensionPath,
					)
					continue
				}
				addDiag(
					fmt.Sprintf("Extension shortcut conflict: '%s' is built-in shortcut for %s and %s. Using %s.", rawKey, bi.keybinding, shortcut.ExtensionPath, shortcut.ExtensionPath),
					shortcut.ExtensionPath,
				)
			}
			if existing, ok := extensionShortcuts[normalizedKey]; ok {
				addDiag(
					fmt.Sprintf("Extension shortcut conflict: '%s' registered by both %s and %s. Using %s.", rawKey, existing.ExtensionPath, shortcut.ExtensionPath, shortcut.ExtensionPath),
					shortcut.ExtensionPath,
				)
			}
			extensionShortcuts[normalizedKey] = shortcut
		}
	}

	r.mu.Lock()
	r.shortcutDiagnostics = diagnostics
	r.mu.Unlock()
	return extensionShortcuts
}

// GetShortcutDiagnostics returns diagnostics collected by the last GetShortcuts call.
func (r *ExtensionRunner) GetShortcutDiagnostics() []ResourceDiagnostic {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return slices.Clone(r.shortcutDiagnostics)
}

// GetMessageRenderer returns the first registered MessageRenderer for customType.
func (r *ExtensionRunner) GetMessageRenderer(customType string) MessageRenderer {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for i := range r.extensions {
		if renderer, ok := r.extensions[i].MessageRenderers[customType]; ok && renderer != nil {
			return renderer
		}
	}
	return nil
}

// GetMarkdownTransformers returns registered Markdown transformers in extension load order.
func (r *ExtensionRunner) GetMarkdownTransformers() []MarkdownTransformer {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	exts := slices.Clone(r.extensions)
	r.mu.RUnlock()

	var out []MarkdownTransformer
	for _, ext := range exts {
		if ext.MarkdownTransformer == nil || ext.MarkdownTransformer.Transformer == nil {
			continue
		}
		reg := ext.MarkdownTransformer
		out = append(out, func(markdown string, rctx MarkdownTransformContext) string {
			if _, ok := reg.MessageTypes[rctx.MessageType]; ok {
				return reg.Transformer(markdown, rctx)
			}
			return markdown
		})
	}
	return out
}

// ResolveToolRenderers chains extension tool renderer resolvers in load order, ending at base.
func (r *ExtensionRunner) ResolveToolRenderers(toolName string, base func() *ToolRenderers) *ToolRenderers {
	if r == nil {
		if base == nil {
			return nil
		}
		return base()
	}
	r.mu.RLock()
	var resolvers []ToolRendererResolver
	for i := range r.extensions {
		for _, res := range r.extensions[i].ToolRenderers {
			if res != nil {
				resolvers = append(resolvers, res)
			}
		}
	}
	r.mu.RUnlock()

	var resolveAt func(idx int) *ToolRenderers
	resolveAt = func(idx int) *ToolRenderers {
		if idx < len(resolvers) {
			return resolvers[idx](toolName, func() *ToolRenderers { return resolveAt(idx + 1) })
		}
		if base == nil {
			return nil
		}
		return base()
	}
	return resolveAt(0)
}

// GetEntryRenderer returns the first registered EntryRenderer for customType.
func (r *ExtensionRunner) GetEntryRenderer(customType string) EntryRenderer {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for i := range r.extensions {
		if renderer, ok := r.extensions[i].EntryRenderers[customType]; ok && renderer != nil {
			return renderer
		}
	}
	return nil
}

// GetModelRegistry returns the ModelRegistry bound to this runner.
func (r *ExtensionRunner) GetModelRegistry() *ModelRegistry {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.modelRegistry
}

// GetRegisteredCommands returns all registered commands with conflict-resolved invocation names.
func (r *ExtensionRunner) GetRegisteredCommands() []ResolvedCommand {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	r.commandDiagnostics = nil
	exts := slices.Clone(r.extensions)
	r.mu.Unlock()
	return erResolveRegisteredCommands(exts)
}

// GetCommandDiagnostics returns command diagnostics collected during command resolution.
func (r *ExtensionRunner) GetCommandDiagnostics() []ResourceDiagnostic {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return slices.Clone(r.commandDiagnostics)
}

// GetCommand looks up a resolved command by effective invocationName.
func (r *ExtensionRunner) GetCommand(name string) *ResolvedCommand {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	exts := slices.Clone(r.extensions)
	r.mu.RUnlock()
	for _, cmd := range erResolveRegisteredCommands(exts) {
		if cmd.InvocationName == name {
			cp := cmd
			return &cp
		}
	}
	return nil
}

// Dispose runs registered runtime and runner cleanup hooks (such as QuickJS VM teardown)
// without triggering host shutdown callbacks. Safe to call multiple times or on nil runners.
func (r *ExtensionRunner) Dispose() {
	if r == nil {
		return
	}
	r.mu.Lock()
	hooks := slices.Clone(r.cleanupHooks)
	r.cleanupHooks = nil
	rt := r.runtime
	r.mu.Unlock()

	erRunRuntimeCleanups(rt)
	for _, fn := range hooks {
		if fn == nil {
			continue
		}
		func() {
			defer func() { _ = recover() }()
			fn()
		}()
	}
}

// Shutdown runs registered extension cleanup hooks (such as QuickJS VM teardown) and the bound
// host shutdown handler. Safe on nil runners.
func (r *ExtensionRunner) Shutdown() {
	if r == nil {
		return
	}
	r.mu.RLock()
	shutdownFn := r.contextActions.Shutdown
	r.mu.RUnlock()

	r.Dispose()
	if shutdownFn != nil {
		shutdownFn()
	}
}

// GetActiveTools returns the currently active tool names from the bound host action.
func (r *ExtensionRunner) GetActiveTools() []string {
	if r == nil {
		return nil
	}
	r.assertActive()
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

// Emit dispatches generic lifecycle and session_before_* events in extension load order.
// For cancellable session_before_* events, the first handler returning Cancel=true short-circuits.
func (r *ExtensionRunner) Emit(ctx context.Context, event ExtensionEvent) any {
	if r == nil || event == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	eventType := event.ExtensionEventType()
	ectx := r.CreateContext()
	snaps := r.snapshotHandlers(eventType)
	isSessionBefore := erIsSessionBeforeEvent(eventType)

	var result any
	for _, snap := range snaps {
		for _, handler := range snap.handlers {
			res, err := erCallHandlerIsolated(ctx, event, ectx, handler)
			if err != nil {
				r.EmitError(ExtensionError{
					ExtensionPath: snap.ext.Path,
					Event:         eventType,
					Error:         err.message,
					Stack:         err.stack,
				})
				continue
			}
			if isSessionBefore && !erIsTypedNil(res) {
				result = res
				if erResultCancels(res) {
					return result
				}
			}
		}
	}
	return result
}

// EmitBoundary dispatches actionable turn_end or agent_before_settle boundaries, chaining draft
// entries, continuation flags, and rebuilt context previews across handlers.
func (r *ExtensionRunner) EmitBoundary(
	ctx context.Context,
	baseEvent BoundaryBaseEvent,
	buildContext func(entries []SessionBoundaryDraft) (BoundaryContextPreview, error),
) (BoundaryDispatchResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var entries []SessionBoundaryDraft
	shouldContinue := false

	var preview BoundaryContextPreview
	if buildContext != nil {
		var err error
		preview, err = buildContext(entries)
		if err != nil {
			return BoundaryDispatchResult{
				Entries:  nil,
				Continue: false,
				Context:  preview,
				Valid:    false,
			}, err
		}
	}
	if r == nil {
		return BoundaryDispatchResult{
			Entries:  nil,
			Continue: false,
			Context:  preview,
			Valid:    true,
		}, nil
	}

	valid := true
	ectx := r.CreateContext()
	snaps := r.snapshotHandlers(baseEvent.Type)
	for _, snap := range snaps {
		for _, handler := range snap.handlers {
			ev := r.buildBoundaryEvent(baseEvent, entries, shouldContinue, preview)
			res, err := erCallHandlerIsolated(ctx, ev, ectx, handler)
			if err != nil {
				r.EmitError(ExtensionError{
					ExtensionPath: snap.ext.Path,
					Event:         baseEvent.Type,
					Error:         err.message,
					Stack:         err.stack,
				})
			} else if br, ok := erAsBoundaryResult(res); ok && br != nil {
				if br.Entries != nil {
					entries = br.Entries
				}
				if br.Continue != nil {
					shouldContinue = *br.Continue
				}
			}

			if buildContext != nil {
				nextPreview, buildErr := buildContext(entries)
				if buildErr != nil {
					valid = false
					r.EmitError(ExtensionError{
						ExtensionPath: snap.ext.Path,
						Event:         baseEvent.Type,
						Error:         "Invalid boundary entries: " + buildErr.Error(),
					})
				} else {
					preview = nextPreview
					valid = true
				}
			}
		}
	}

	if !valid {
		return BoundaryDispatchResult{
			Entries:  []SessionBoundaryDraft{},
			Continue: false,
			Context:  preview,
			Valid:    false,
		}, nil
	}
	return BoundaryDispatchResult{
		Entries:  entries,
		Continue: shouldContinue,
		Context:  preview,
		Valid:    true,
	}, nil
}

// EmitCacheWarmingDecision dispatches cache_warming_decision handlers (last non-nil action wins).
func (r *ExtensionRunner) EmitCacheWarmingDecision(ctx context.Context, event CacheWarmingDecisionEvent) ai.CacheWarmingAction {
	action := event.Action
	if r == nil {
		return action
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if event.Type == "" {
		event.Type = "cache_warming_decision"
	}
	ectx := r.CreateContext()
	for _, snap := range r.snapshotHandlers("cache_warming_decision") {
		for _, handler := range snap.handlers {
			res, err := erCallHandlerIsolated(ctx, event, ectx, handler)
			if err != nil {
				r.EmitError(ExtensionError{
					ExtensionPath: snap.ext.Path,
					Event:         "cache_warming_decision",
					Error:         err.message,
					Stack:         err.stack,
				})
				continue
			}
			if dr, ok := erAsPtr[CacheWarmingDecisionEventResult](res); ok && dr != nil && dr.Action != nil {
				action = *dr.Action
			}
		}
	}
	return action
}

// EmitMessageEnd dispatches message_end handlers, chaining same-role message replacements.
func (r *ExtensionRunner) EmitMessageEnd(ctx context.Context, event MessageEndEvent) agent.AgentMessage {
	if r == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	currentMessage := event.Message
	modified := false
	ectx := r.CreateContext()

	for _, snap := range r.snapshotHandlers("message_end") {
		for _, handler := range snap.handlers {
			curEvent := event
			curEvent.Type = "message_end"
			curEvent.Message = currentMessage
			res, err := erCallHandlerIsolated(ctx, curEvent, ectx, handler)
			if err != nil {
				r.EmitError(ExtensionError{
					ExtensionPath: snap.ext.Path,
					Event:         "message_end",
					Error:         err.message,
					Stack:         err.stack,
				})
				continue
			}
			mr, ok := erAsPtr[MessageEndEventResult](res)
			if !ok || mr == nil || mr.Message == nil {
				continue
			}
			if currentMessage != nil && mr.Message.GetRole() != currentMessage.GetRole() {
				r.EmitError(ExtensionError{
					ExtensionPath: snap.ext.Path,
					Event:         "message_end",
					Error:         "message_end handlers must return a message with the same role",
				})
				continue
			}
			currentMessage = mr.Message
			modified = true
		}
	}
	if !modified {
		return nil
	}
	return currentMessage
}

// EmitToolResult dispatches tool_result handlers, chaining content, details, structuredContent,
// isError, and usage overrides across handlers.
func (r *ExtensionRunner) EmitToolResult(ctx context.Context, event ToolResultEvent) *ToolResultEventResult {
	if r == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	current := event
	if current.Type == "" {
		current.Type = "tool_result"
	}
	modified := false
	ectx := r.CreateContext()

	for _, snap := range r.snapshotHandlers("tool_result") {
		for _, handler := range snap.handlers {
			res, err := erCallHandlerIsolated(ctx, current, ectx, handler)
			if err != nil {
				r.EmitError(ExtensionError{
					ExtensionPath: snap.ext.Path,
					Event:         "tool_result",
					Error:         err.message,
					Stack:         err.stack,
				})
				continue
			}
			tr, ok := erAsPtr[ToolResultEventResult](res)
			if !ok || tr == nil {
				continue
			}
			if tr.Content != nil {
				current.Content = tr.Content
				if tr.StructuredContent == nil {
					current.StructuredContent = nil
				}
				modified = true
			}
			if tr.Details != nil {
				current.Details = tr.Details
				modified = true
			}
			if tr.StructuredContent != nil {
				current.StructuredContent = tr.StructuredContent
				modified = true
			}
			if tr.IsError != nil {
				current.IsError = *tr.IsError
				modified = true
			}
			if tr.Usage != nil {
				current.Usage = tr.Usage
				modified = true
			}
		}
	}
	if !modified {
		return nil
	}
	isErr := current.IsError
	return &ToolResultEventResult{
		Content:           current.Content,
		Details:           current.Details,
		StructuredContent: current.StructuredContent,
		IsError:           &isErr,
		Usage:             current.Usage,
	}
}

// EmitToolCall dispatches tool_call handlers sequentially until a handler blocks execution.
func (r *ExtensionRunner) EmitToolCall(ctx context.Context, event ToolCallEvent) *ToolCallEventResult {
	if r == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if event.Type == "" {
		event.Type = "tool_call"
	}
	ectx := r.CreateContext()
	var result *ToolCallEventResult
	ev := event
	for _, snap := range r.snapshotHandlers("tool_call") {
		for _, handler := range snap.handlers {
			res, err := erCallHandlerIsolated(ctx, &ev, ectx, handler)
			if err != nil {
				r.EmitError(ExtensionError{
					ExtensionPath: snap.ext.Path,
					Event:         "tool_call",
					Error:         err.message,
					Stack:         err.stack,
				})
				continue
			}
			if tc, ok := erAsPtr[ToolCallEventResult](res); ok && tc != nil {
				result = tc
				if tc.Block != nil && *tc.Block {
					return result
				}
			}
		}
	}
	return result
}

// EmitUserBashWithError dispatches user_bash handlers, validating that any non-nil result sets
// exactly one of Operations or Result. Errors and panics emit an ExtensionError and return an error.
func (r *ExtensionRunner) EmitUserBashWithError(ctx context.Context, event UserBashEvent) (*UserBashEventResult, error) {
	if r == nil {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if event.Type == "" {
		event.Type = "user_bash"
	}
	ectx := r.CreateContext()
	for _, snap := range r.snapshotHandlers("user_bash") {
		for _, handler := range snap.handlers {
			res, err := erCallHandlerIsolated(ctx, event, ectx, handler)
			if err != nil {
				r.EmitError(ExtensionError{
					ExtensionPath: snap.ext.Path,
					Event:         "user_bash",
					Error:         err.message,
					Stack:         err.stack,
				})
				return nil, errors.New(err.message)
			}
			if erIsTypedNil(res) {
				continue
			}
			ub, ok := erAsPtr[UserBashEventResult](res)
			if !ok || !erIsValidUserBashResult(ub) {
				const msg = "Invalid user_bash handler result: return nil for local execution or exactly one valid Operations or Result"
				r.EmitError(ExtensionError{
					ExtensionPath: snap.ext.Path,
					Event:         "user_bash",
					Error:         msg,
				})
				return nil, errors.New(msg)
			}
			return ub, nil
		}
	}
	return nil, nil
}

// EmitUserBash dispatches user_bash handlers and fails closed when any handler errors or returns
// an invalid result.
func (r *ExtensionRunner) EmitUserBash(ctx context.Context, event UserBashEvent) *UserBashEventResult {
	res, err := r.EmitUserBashWithError(ctx, event)
	if err != nil {
		exitCode := 1
		return &UserBashEventResult{
			Result: &BashResult{
				Output:    err.Error(),
				ExitCode:  &exitCode,
				Cancelled: true,
			},
		}
	}
	return res
}

// EmitContext executes request-time transcript transforms in two phases: `context` handlers receive
// non-system messages with Pi's leading system state restored afterward, then `context_with_system`
// handlers inspect and may transform the full message slice.
func (r *ExtensionRunner) EmitContext(ctx context.Context, messages []agent.AgentMessage, _ ...any) []agent.AgentMessage {
	if r == nil {
		return messages
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ectx := r.CreateContext()
	currentMessages := erCloneAgentMessages(messages)

	for _, snap := range r.snapshotHandlers("context") {
		for _, handler := range snap.handlers {
			visible := erFilterNonSystemMessages(currentMessages)
			visibleSnapshot := slices.Clone(visible)
			ev := ContextEvent{Type: "context", Messages: visible}
			res, err := erCallHandlerIsolated(ctx, &ev, ectx, handler)
			if err != nil {
				r.EmitError(ExtensionError{
					ExtensionPath: snap.ext.Path,
					Event:         "context",
					Error:         err.message,
					Stack:         err.stack,
				})
				continue
			}
			var returned []agent.AgentMessage
			if cr, ok := erAsPtr[ContextEventResult](res); ok && cr != nil && cr.Messages != nil {
				returned = cr.Messages
			} else if !erSameAgentMessages(ev.Messages, visibleSnapshot) {
				returned = ev.Messages
			}
			if returned == nil {
				continue
			}
			currentMessages = erRestoreSystemMessages(currentMessages, visibleSnapshot, returned)
		}
	}

	for _, snap := range r.snapshotHandlers("context_with_system") {
		for _, handler := range snap.handlers {
			hadLeadingSystem := len(currentMessages) > 0 && currentMessages[0] != nil && currentMessages[0].GetRole() == "system"
			before := slices.Clone(currentMessages)
			ev := ContextWithSystemEvent{Type: "context_with_system", Messages: currentMessages}
			res, err := erCallHandlerIsolated(ctx, &ev, ectx, handler)
			if err != nil {
				r.EmitError(ExtensionError{
					ExtensionPath: snap.ext.Path,
					Event:         "context_with_system",
					Error:         err.message,
					Stack:         err.stack,
				})
				continue
			}
			if cr, ok := erAsPtr[ContextEventResult](res); ok && cr != nil && cr.Messages != nil {
				currentMessages = cr.Messages
			} else if !erSameAgentMessages(ev.Messages, before) {
				currentMessages = ev.Messages
			}
			if hadLeadingSystem && (len(currentMessages) == 0 || currentMessages[0] == nil || currentMessages[0].GetRole() != "system") {
				r.EmitError(ExtensionError{
					ExtensionPath: snap.ext.Path,
					Event:         "context_with_system",
					Error:         "Handler removed the leading system message; the request has no prompt or initial tool declarations. Keep it at index 0 or replace a dropped prefix with getCurrentSystemMessage().",
				})
			}
		}
	}

	return currentMessages
}

// EmitBeforeProviderRequest chains payload transformations across before_provider_request handlers.
func (r *ExtensionRunner) EmitBeforeProviderRequest(ctx context.Context, payload any) any {
	if r == nil {
		return payload
	}
	if ctx == nil {
		ctx = context.Background()
	}
	currentPayload := payload
	ectx := r.CreateContext()
	for _, snap := range r.snapshotHandlers("before_provider_request") {
		for _, handler := range snap.handlers {
			res, err := erCallHandlerIsolated(ctx, BeforeProviderRequestEvent{
				Type:    "before_provider_request",
				Payload: currentPayload,
			}, ectx, handler)
			if err != nil {
				r.EmitError(ExtensionError{
					ExtensionPath: snap.ext.Path,
					Event:         "before_provider_request",
					Error:         err.message,
					Stack:         err.stack,
				})
				continue
			}
			if !erIsTypedNil(res) {
				currentPayload = res
			}
		}
	}
	return currentPayload
}

// EmitBeforeProviderHeaders lets before_provider_headers handlers mutate provider HTTP headers in place.
func (r *ExtensionRunner) EmitBeforeProviderHeaders(ctx context.Context, headers ai.ProviderHeaders) ai.ProviderHeaders {
	if r == nil {
		return headers
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ectx := r.CreateContext()
	for _, snap := range r.snapshotHandlers("before_provider_headers") {
		for _, handler := range snap.handlers {
			_, err := erCallHandlerIsolated(ctx, BeforeProviderHeadersEvent{
				Type:    "before_provider_headers",
				Headers: headers,
			}, ectx, handler)
			if err != nil {
				r.EmitError(ExtensionError{
					ExtensionPath: snap.ext.Path,
					Event:         "before_provider_headers",
					Error:         err.message,
					Stack:         err.stack,
				})
			}
		}
	}
	return headers
}

// EmitBeforeAgentStart collects injected messages and system prompt modifications before an agent run.
func (r *ExtensionRunner) EmitBeforeAgentStart(
	ctx context.Context,
	trigger BeforeAgentStartTrigger,
	systemPromptOptions BuildSystemPromptOptions,
) BeforeAgentStartCombinedResult {
	currentOptions := NormalizeBuildSystemPromptOptions(systemPromptOptions)
	if r == nil {
		return BeforeAgentStartCombinedResult{
			Messages:            nil,
			SystemPromptOptions: currentOptions,
		}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	renderPrompt := func() string {
		s, _ := BuildSystemPrompt(erNormalizedToBuildSystemPromptOptions(currentOptions))
		return s
	}
	ectx := &erContext{
		runner:                  r,
		getSystemPromptOverride: renderPrompt,
	}

	var messages []BeforeAgentStartCustomMessage
	for _, snap := range r.snapshotHandlers("before_agent_start") {
		for _, handler := range snap.handlers {
			ev := BeforeAgentStartEvent{
				Type:                "before_agent_start",
				SystemPrompt:        renderPrompt(),
				SystemPromptOptions: currentOptions,
				MessageType:         trigger.MessageType,
				Prompt:              trigger.Prompt,
				Images:              slices.Clone(trigger.Images),
				Message:             trigger.Message,
			}
			res, err := erCallHandlerIsolated(ctx, &ev, ectx, handler)
			if err != nil {
				r.EmitError(ExtensionError{
					ExtensionPath: snap.ext.Path,
					Event:         "before_agent_start",
					Error:         err.message,
					Stack:         err.stack,
				})
				continue
			}
			currentOptions = ev.SystemPromptOptions
			if br, ok := erAsPtr[BeforeAgentStartEventResult](res); ok && br != nil {
				if br.Message != nil {
					messages = append(messages, *br.Message)
				}
				if br.SystemPrompt != nil {
					sp := *br.SystemPrompt
					currentOptions.ForceSystemPrompt = &sp
				}
			}
		}
	}

	return BeforeAgentStartCombinedResult{
		Messages:            messages,
		SystemPromptOptions: currentOptions,
	}
}

// EmitResourcesDiscover aggregates skill, prompt, and theme paths contributed by extensions.
func (r *ExtensionRunner) EmitResourcesDiscover(
	ctx context.Context,
	cwd string,
	reason ResourcesDiscoverReason,
) ResourcesDiscoverCombinedResult {
	var out ResourcesDiscoverCombinedResult
	if r == nil {
		return out
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ectx := r.CreateContext()
	for _, snap := range r.snapshotHandlers("resources_discover") {
		for _, handler := range snap.handlers {
			res, err := erCallHandlerIsolated(ctx, ResourcesDiscoverEvent{
				Type:   "resources_discover",
				Cwd:    cwd,
				Reason: reason,
			}, ectx, handler)
			if err != nil {
				r.EmitError(ExtensionError{
					ExtensionPath: snap.ext.Path,
					Event:         "resources_discover",
					Error:         err.message,
					Stack:         err.stack,
				})
				continue
			}
			if dr, ok := erAsPtr[ResourcesDiscoverResult](res); ok && dr != nil {
				for _, p := range dr.SkillPaths {
					out.SkillPaths = append(out.SkillPaths, DiscoveredResourcePath{Path: p, ExtensionPath: snap.ext.Path})
				}
				for _, p := range dr.PromptPaths {
					out.PromptPaths = append(out.PromptPaths, DiscoveredResourcePath{Path: p, ExtensionPath: snap.ext.Path})
				}
				for _, p := range dr.ThemePaths {
					out.ThemePaths = append(out.ThemePaths, DiscoveredResourcePath{Path: p, ExtensionPath: snap.ext.Path})
				}
			}
		}
	}
	return out
}

// EmitInput dispatches input handlers, chaining transforms and short-circuiting on "handled".
func (r *ExtensionRunner) EmitInput(
	ctx context.Context,
	text string,
	images []ai.ImageContent,
	source InputSource,
	streamingBehavior ...string,
) InputEventResult {
	if r == nil {
		return InputEventResult{Action: "continue"}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var sbPtr *string
	if len(streamingBehavior) > 0 && streamingBehavior[0] != "" {
		sb := streamingBehavior[0]
		sbPtr = &sb
	}
	currentText := text
	currentImages := images
	transformed := false
	ectx := r.CreateContext()

	for _, snap := range r.snapshotHandlers("input") {
		for _, handler := range snap.handlers {
			res, err := erCallHandlerIsolated(ctx, InputEvent{
				Type:              "input",
				Text:              currentText,
				Images:            currentImages,
				Source:            source,
				StreamingBehavior: sbPtr,
			}, ectx, handler)
			if err != nil {
				r.EmitError(ExtensionError{
					ExtensionPath: snap.ext.Path,
					Event:         "input",
					Error:         err.message,
					Stack:         err.stack,
				})
				continue
			}
			ir, ok := erAsPtr[InputEventResult](res)
			if !ok || ir == nil {
				continue
			}
			if ir.Action == "handled" {
				return *ir
			}
			if ir.Action == "transform" {
				currentText = ir.Text
				if ir.Images != nil {
					currentImages = ir.Images
				}
				transformed = true
			}
		}
	}

	if transformed || currentText != text || !erSameImages(currentImages, images) {
		return InputEventResult{
			Action: "transform",
			Text:   currentText,
			Images: currentImages,
		}
	}
	return InputEventResult{Action: "continue"}
}

// EmitSessionShutdownEvent emits session_shutdown when handlers exist and reports whether it ran.
func EmitSessionShutdownEvent(ctx context.Context, runner *ExtensionRunner, event SessionShutdownEvent) bool {
	if runner == nil || !runner.HasHandlers("session_shutdown") {
		return false
	}
	if event.Type == "" {
		event.Type = "session_shutdown"
	}
	_ = runner.Emit(ctx, event)
	return true
}

// EmitProjectTrustEvent runs pre-session project_trust handlers; first yes/no decision wins.
func EmitProjectTrustEvent(
	extensionsResult *LoadExtensionsResult,
	event ProjectTrustEvent,
	ptctx ProjectTrustContext,
) (*ProjectTrustEventResult, []ExtensionError) {
	if extensionsResult == nil {
		return nil, nil
	}
	if event.Type == "" {
		event.Type = "project_trust"
	}
	var errs []ExtensionError
	for i := range extensionsResult.Extensions {
		ext := extensionsResult.Extensions[i]
		handlers := slices.Clone(ext.Handlers["project_trust"])
		for _, rawHandler := range handlers {
			res, callErr := erCallProjectTrustIsolated(event, ptctx, rawHandler)
			if callErr != nil {
				errs = append(errs, ExtensionError{
					ExtensionPath: ext.Path,
					Event:         "project_trust",
					Error:         callErr.message,
					Stack:         callErr.stack,
				})
				continue
			}
			if res == nil || res.Trusted == "" || res.Trusted == ProjectTrustEventDecisionUndecided {
				continue
			}
			return res, errs
		}
	}
	return nil, errs
}

type erContext struct {
	runner                  *ExtensionRunner
	getSystemPromptOverride func() string
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
	if c.getSystemPromptOverride != nil {
		return c.getSystemPromptOverride()
	}
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

// ============================================================================
// ExtensionRunner Internal Helpers (prefix: er)
// ============================================================================

type erHandlerSnap struct {
	ext      Extension
	handlers []any
}

type erIsolatedError struct {
	message string
	stack   *string
}

type erBuiltinKeybinding struct {
	keybinding       string
	restrictOverride bool
}

var erReservedKeybindingsForExtensionConflicts = map[string]struct{}{
	"app.interrupt":              {},
	"app.clear":                  {},
	"app.exit":                   {},
	"app.suspend":                {},
	"app.thinking.cycle":         {},
	"app.model.cycleForward":     {},
	"app.model.cycleBackward":    {},
	"app.model.select":           {},
	"app.tools.expand":           {},
	"app.thinking.toggle":        {},
	"app.editor.external":        {},
	"app.message.copy":           {},
	"app.message.followUp":       {},
	"tui.input.submit":           {},
	"tui.select.confirm":         {},
	"tui.select.cancel":          {},
	"tui.input.copy":             {},
	"tui.editor.deleteToLineEnd": {},
}

func erSortedKeys[K ~string, V any](m map[K]V) []K {
	if len(m) == 0 {
		return nil
	}
	keys := make([]K, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func erBuildBuiltinKeybindings(resolvedKeybindings tui.KeybindingsConfig) map[tui.KeyId]erBuiltinKeybinding {
	out := make(map[tui.KeyId]erBuiltinKeybinding)
	if resolvedKeybindings == nil {
		return out
	}
	for _, keybinding := range resolvedKeybindings.Keys() {
		keysVal, ok := resolvedKeybindings.Get(keybinding)
		if !ok || keysVal == nil {
			continue
		}
		_, restrict := erReservedKeybindingsForExtensionConflicts[keybinding]
		var keyList []tui.KeyId
		if keysVal.Single != nil {
			keyList = []tui.KeyId{*keysVal.Single}
		} else {
			keyList = keysVal.Keys
		}
		for _, k := range keyList {
			norm := tui.KeyId(strings.ToLower(string(k)))
			if existing, exists := out[norm]; exists && existing.restrictOverride && !restrict {
				continue
			}
			out[norm] = erBuiltinKeybinding{
				keybinding:       keybinding,
				restrictOverride: restrict,
			}
		}
	}
	return out
}

func erExtensionRegistrationKeys(ext Extension) []string {
	var keys []string
	for _, name := range erSortedKeys(ext.Tools) {
		keys = append(keys, "tool:"+name)
	}
	for _, name := range erSortedKeys(ext.Commands) {
		keys = append(keys, "command:"+name)
	}
	for _, name := range erSortedKeys(ext.Flags) {
		keys = append(keys, "flag:"+name)
	}
	return keys
}

func erFilterReplacedExtensions(extensions []Extension) ([]Extension, []ExtensionLoadWarning) {
	if len(extensions) == 0 {
		return nil, nil
	}
	taken := make(map[string]Extension)
	for _, ext := range extensions {
		if ext.Replaceable {
			continue
		}
		for _, k := range erExtensionRegistrationKeys(ext) {
			if _, exists := taken[k]; !exists {
				taken[k] = ext
			}
		}
	}
	if len(taken) == 0 {
		return slices.Clone(extensions), nil
	}

	const builtinPrefix = "builtin:"
	out := make([]Extension, 0, len(extensions))
	var warnings []ExtensionLoadWarning
	for _, ext := range extensions {
		if !ext.Replaceable {
			out = append(out, ext)
			continue
		}
		var replacedByKey string
		var replacedByExt Extension
		for _, k := range erExtensionRegistrationKeys(ext) {
			if owner, ok := taken[k]; ok {
				replacedByKey = k
				replacedByExt = owner
				break
			}
		}
		if replacedByKey == "" {
			out = append(out, ext)
			continue
		}
		if strings.HasPrefix(ext.Path, builtinPrefix) {
			builtinName := strings.TrimPrefix(ext.Path, builtinPrefix)
			parts := strings.SplitN(replacedByKey, ":", 2)
			registeredName := replacedByKey
			if len(parts) == 2 {
				switch parts[0] {
				case "command":
					registeredName = "/" + parts[1]
				case "flag":
					registeredName = "--" + parts[1]
				default:
					registeredName = parts[1]
				}
			}
			warnings = append(warnings, ExtensionLoadWarning{
				Path:    ext.Path,
				Warning: fmt.Sprintf("Skipped built-in extension %q because %s already registered %s", builtinName, replacedByExt.Path, registeredName),
			})
		}
	}
	return out, warnings
}

func erResolveRegisteredCommands(extensions []Extension) []ResolvedCommand {
	var commands []RegisteredCommand
	counts := make(map[string]int)
	for _, ext := range extensions {
		for _, key := range erSortedKeys(ext.Commands) {
			cmd := ext.Commands[key]
			commands = append(commands, cmd)
			counts[cmd.Name]++
		}
	}
	seen := make(map[string]int)
	taken := make(map[string]struct{})
	out := make([]ResolvedCommand, 0, len(commands))
	for _, cmd := range commands {
		seen[cmd.Name]++
		occurrence := seen[cmd.Name]
		invocationName := cmd.Name
		if counts[cmd.Name] > 1 {
			invocationName = fmt.Sprintf("%s:%d", cmd.Name, occurrence)
		}
		if _, exists := taken[invocationName]; exists {
			suffix := occurrence
			for {
				suffix++
				candidate := fmt.Sprintf("%s:%d", cmd.Name, suffix)
				if _, dup := taken[candidate]; !dup {
					invocationName = candidate
					break
				}
			}
		}
		taken[invocationName] = struct{}{}
		out = append(out, ResolvedCommand{
			RegisteredCommand: cmd,
			InvocationName:    invocationName,
		})
	}
	return out
}

func (r *ExtensionRunner) snapshotHandlers(eventType string) []erHandlerSnap {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	exts := slices.Clone(r.extensions)
	r.mu.RUnlock()
	snaps := make([]erHandlerSnap, 0, len(exts))
	for _, ext := range exts {
		if len(ext.Handlers) == 0 {
			continue
		}
		hs := ext.Handlers[eventType]
		if len(hs) == 0 {
			continue
		}
		snaps = append(snaps, erHandlerSnap{
			ext:      ext,
			handlers: slices.Clone(hs),
		})
	}
	return snaps
}

func (r *ExtensionRunner) buildBoundaryEvent(
	base BoundaryBaseEvent,
	entries []SessionBoundaryDraft,
	shouldContinue bool,
	preview BoundaryContextPreview,
) ExtensionEvent {
	state := BoundaryState{
		Entries:  entries,
		Continue: shouldContinue,
		Context:  preview,
		Outcome:  base.Outcome,
	}
	if base.Type == "agent_before_settle" {
		return AgentBeforeSettleEvent{
			Type:          "agent_before_settle",
			BoundaryState: state,
		}
	}
	toolResults := make([]ai.ToolResultMessage, 0, len(base.ToolResults))
	toolResultEntryIds := make([]string, 0, len(base.ToolResults))
	for _, tr := range base.ToolResults {
		if tr == nil {
			continue
		}
		toolResults = append(toolResults, *tr)
		if entryId := erFindToolResultEntryId(preview.ContextEntries, tr.ToolCallId); entryId != "" {
			toolResultEntryIds = append(toolResultEntryIds, entryId)
		}
	}
	msgEntryId := erFindMessageEntryId(preview.ContextEntries, base.Message)
	return TurnEndEvent{
		Type:               "turn_end",
		BoundaryState:      state,
		TurnIndex:          base.TurnIndex,
		Message:            base.Message,
		ToolResults:        toolResults,
		MessageEntryId:     msgEntryId,
		ToolResultEntryIds: toolResultEntryIds,
	}
}

func erFindMessageEntryId(entries []ProjectedSessionEntry, msg agent.AgentMessage) string {
	if msg == nil {
		return ""
	}
	for i := len(entries) - 1; i >= 0; i-- {
		sm, ok := entries[i].SourceEntry.(*SessionMessageEntry)
		if !ok || sm == nil || sm.Message == nil {
			continue
		}
		if sm.Message == msg {
			return sm.Id
		}
	}
	wantRole := msg.GetRole()
	for i := len(entries) - 1; i >= 0; i-- {
		sm, ok := entries[i].SourceEntry.(*SessionMessageEntry)
		if ok && sm != nil && sm.Message != nil && sm.Message.GetRole() == wantRole {
			return sm.Id
		}
	}
	return ""
}

func erFindToolResultEntryId(entries []ProjectedSessionEntry, toolCallId string) string {
	if toolCallId == "" {
		return ""
	}
	for i := len(entries) - 1; i >= 0; i-- {
		sm, ok := entries[i].SourceEntry.(*SessionMessageEntry)
		if !ok || sm == nil || sm.Message == nil {
			continue
		}
		if tr, ok := sm.Message.(*ai.ToolResultMessage); ok && tr != nil && tr.ToolCallId == toolCallId {
			return sm.Id
		}
	}
	return ""
}

func erIsSessionBeforeEvent(eventType string) bool {
	switch eventType {
	case "session_before_switch", "session_before_fork", "session_before_compact", "session_before_tree":
		return true
	default:
		return false
	}
}

func erIsTypedNil(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Slice, reflect.Map, reflect.Func, reflect.Chan:
		return rv.IsNil()
	default:
		return false
	}
}

func erResultCancels(res any) bool {
	switch v := res.(type) {
	case *SessionBeforeSwitchResult:
		return v != nil && v.Cancel
	case SessionBeforeSwitchResult:
		return v.Cancel
	case *SessionBeforeForkResult:
		return v != nil && v.Cancel
	case SessionBeforeForkResult:
		return v.Cancel
	case *SessionBeforeCompactResult:
		return v != nil && v.Cancel
	case SessionBeforeCompactResult:
		return v.Cancel
	case *SessionBeforeTreeResult:
		return v != nil && v.Cancel
	case SessionBeforeTreeResult:
		return v.Cancel
	default:
		return false
	}
}

func erAsBoundaryResult(res any) (*BoundaryResult, bool) {
	switch v := res.(type) {
	case *BoundaryResult:
		return v, true
	case BoundaryResult:
		return &v, true
	default:
		return nil, false
	}
}

func erAsPtr[T any](res any) (*T, bool) {
	switch v := res.(type) {
	case *T:
		return v, true
	case T:
		return &v, true
	default:
		return nil, false
	}
}

func erIsValidUserBashResult(res *UserBashEventResult) bool {
	if res == nil {
		return false
	}
	hasOps := res.Operations != nil
	hasResult := res.Result != nil
	return hasOps != hasResult
}

func erEventPtr[E any](event any) *E {
	switch v := event.(type) {
	case *E:
		return v
	case E:
		return &v
	default:
		return nil
	}
}

func erInvoke[E any, R any](ctx context.Context, event any, ectx ExtensionContext, h ExtensionHandler[E, R]) (any, error) {
	ep := erEventPtr[E](event)
	if ep == nil {
		return nil, fmt.Errorf("unexpected event type %T for %T", event, (*E)(nil))
	}
	res, err := h(ctx, ep, ectx)
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, nil
	}
	return res, nil
}

func erCallHandlerIsolated(ctx context.Context, event any, ectx ExtensionContext, handler any) (res any, isoErr *erIsolatedError) {
	if handler == nil {
		return nil, nil
	}
	defer func() {
		if r := recover(); r != nil {
			stackStr := string(debug.Stack())
			isoErr = &erIsolatedError{
				message: fmt.Sprint(r),
				stack:   &stackStr,
			}
			res = nil
		}
	}()

	var err error
	switch h := handler.(type) {
	case func(context.Context, any, ExtensionContext) (any, error):
		res, err = h(ctx, event, ectx)
	case ExtensionHandler[ResourcesDiscoverEvent, ResourcesDiscoverResult]:
		res, err = erInvoke(ctx, event, ectx, h)
	case ExtensionHandler[SessionStartEvent, NoResult]:
		res, err = erInvoke(ctx, event, ectx, h)
	case ExtensionHandler[SessionInfoChangedEvent, NoResult]:
		res, err = erInvoke(ctx, event, ectx, h)
	case ExtensionHandler[SessionBeforeSwitchEvent, SessionBeforeSwitchResult]:
		res, err = erInvoke(ctx, event, ectx, h)
	case ExtensionHandler[SessionBeforeForkEvent, SessionBeforeForkResult]:
		res, err = erInvoke(ctx, event, ectx, h)
	case ExtensionHandler[SessionBeforeCompactEvent, SessionBeforeCompactResult]:
		res, err = erInvoke(ctx, event, ectx, h)
	case ExtensionHandler[SessionCompactEvent, NoResult]:
		res, err = erInvoke(ctx, event, ectx, h)
	case ExtensionHandler[SessionCompactFailedEvent, NoResult]:
		res, err = erInvoke(ctx, event, ectx, h)
	case ExtensionHandler[SessionShutdownEvent, NoResult]:
		res, err = erInvoke(ctx, event, ectx, h)
	case ExtensionHandler[McpServersChangeEvent, NoResult]:
		res, err = erInvoke(ctx, event, ectx, h)
	case ExtensionHandler[SessionBeforeTreeEvent, SessionBeforeTreeResult]:
		res, err = erInvoke(ctx, event, ectx, h)
	case ExtensionHandler[SessionTreeEvent, NoResult]:
		res, err = erInvoke(ctx, event, ectx, h)
	case ExtensionHandler[ContextEvent, ContextEventResult]:
		res, err = erInvoke(ctx, event, ectx, h)
	case ExtensionHandler[ContextWithSystemEvent, ContextEventResult]:
		res, err = erInvoke(ctx, event, ectx, h)
	case ExtensionHandler[CacheWarmingDecisionEvent, CacheWarmingDecisionEventResult]:
		res, err = erInvoke(ctx, event, ectx, h)
	case ExtensionHandler[BeforeProviderRequestEvent, any]:
		rawRes, callErr := erInvoke(ctx, event, ectx, h)
		err = callErr
		if ptr, ok := rawRes.(*any); ok && ptr != nil {
			res = *ptr
		}
	case ExtensionHandler[BeforeProviderHeadersEvent, NoResult]:
		res, err = erInvoke(ctx, event, ectx, h)
	case ExtensionHandler[AfterProviderResponseEvent, NoResult]:
		res, err = erInvoke(ctx, event, ectx, h)
	case ExtensionHandler[ProviderStreamEvent, NoResult]:
		res, err = erInvoke(ctx, event, ectx, h)
	case ExtensionHandler[BeforeAgentStartEvent, BeforeAgentStartEventResult]:
		res, err = erInvoke(ctx, event, ectx, h)
	case ExtensionHandler[AgentStartEvent, NoResult]:
		res, err = erInvoke(ctx, event, ectx, h)
	case ExtensionHandler[AgentEndEvent, NoResult]:
		res, err = erInvoke(ctx, event, ectx, h)
	case ExtensionHandler[AgentBeforeSettleEvent, BoundaryResult]:
		res, err = erInvoke(ctx, event, ectx, h)
	case ExtensionHandler[AgentSettledEvent, NoResult]:
		res, err = erInvoke(ctx, event, ectx, h)
	case ExtensionHandler[UIPromptStartEvent, NoResult]:
		res, err = erInvoke(ctx, event, ectx, h)
	case ExtensionHandler[UIPromptEndEvent, NoResult]:
		res, err = erInvoke(ctx, event, ectx, h)
	case ExtensionHandler[TurnStartEvent, NoResult]:
		res, err = erInvoke(ctx, event, ectx, h)
	case ExtensionHandler[TurnEndEvent, BoundaryResult]:
		res, err = erInvoke(ctx, event, ectx, h)
	case ExtensionHandler[MessageStartEvent, NoResult]:
		res, err = erInvoke(ctx, event, ectx, h)
	case ExtensionHandler[MessageUpdateEvent, NoResult]:
		res, err = erInvoke(ctx, event, ectx, h)
	case ExtensionHandler[MessageEndEvent, MessageEndEventResult]:
		res, err = erInvoke(ctx, event, ectx, h)
	case ExtensionHandler[ToolExecutionStartEvent, NoResult]:
		res, err = erInvoke(ctx, event, ectx, h)
	case ExtensionHandler[ToolExecutionUpdateEvent, NoResult]:
		res, err = erInvoke(ctx, event, ectx, h)
	case ExtensionHandler[ToolExecutionEndEvent, NoResult]:
		res, err = erInvoke(ctx, event, ectx, h)
	case ExtensionHandler[ModelSelectEvent, NoResult]:
		res, err = erInvoke(ctx, event, ectx, h)
	case ExtensionHandler[ThinkingLevelSelectEvent, NoResult]:
		res, err = erInvoke(ctx, event, ectx, h)
	case ExtensionHandler[ToolCallEvent, ToolCallEventResult]:
		res, err = erInvoke(ctx, event, ectx, h)
	case ExtensionHandler[ToolResultEvent, ToolResultEventResult]:
		res, err = erInvoke(ctx, event, ectx, h)
	case ExtensionHandler[UserBashEvent, UserBashEventResult]:
		res, err = erInvoke(ctx, event, ectx, h)
	case ExtensionHandler[InputEvent, InputEventResult]:
		res, err = erInvoke(ctx, event, ectx, h)
	default:
		return nil, &erIsolatedError{
			message: fmt.Sprintf("unsupported extension handler type %T", handler),
		}
	}

	if err != nil {
		return nil, &erIsolatedError{message: err.Error()}
	}
	return res, nil
}

func erCallProjectTrustIsolated(
	event ProjectTrustEvent,
	ptctx ProjectTrustContext,
	rawHandler any,
) (res *ProjectTrustEventResult, isoErr *erIsolatedError) {
	if rawHandler == nil {
		return nil, nil
	}
	defer func() {
		if r := recover(); r != nil {
			stackStr := string(debug.Stack())
			isoErr = &erIsolatedError{
				message: fmt.Sprint(r),
				stack:   &stackStr,
			}
			res = nil
		}
	}()

	switch h := rawHandler.(type) {
	case ProjectTrustHandler:
		r, err := h(event, ptctx)
		if err != nil {
			return nil, &erIsolatedError{message: err.Error()}
		}
		return &r, nil
	case func(ProjectTrustEvent, ProjectTrustContext) (ProjectTrustEventResult, error):
		r, err := h(event, ptctx)
		if err != nil {
			return nil, &erIsolatedError{message: err.Error()}
		}
		return &r, nil
	case func(context.Context, any, ExtensionContext) (any, error):
		rawRes, err := h(context.Background(), event, nil)
		if err != nil {
			return nil, &erIsolatedError{message: err.Error()}
		}
		if r, ok := erAsPtr[ProjectTrustEventResult](rawRes); ok {
			return r, nil
		}
		return nil, nil
	default:
		return nil, &erIsolatedError{
			message: fmt.Sprintf("unsupported project_trust handler type %T", rawHandler),
		}
	}
}

func erFilterNonSystemMessages(messages []agent.AgentMessage) []agent.AgentMessage {
	out := make([]agent.AgentMessage, 0, len(messages))
	for _, m := range messages {
		if m != nil && m.GetRole() != "system" {
			out = append(out, m)
		}
	}
	return out
}

func erSameAgentMessages(left, right []agent.AgentMessage) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if !erSameSingleMessage(left[i], right[i]) {
			return false
		}
	}
	return true
}

func erSameSingleMessage(a, b agent.AgentMessage) bool {
	if a == nil || b == nil {
		return a == b
	}
	ra := reflect.ValueOf(a)
	rb := reflect.ValueOf(b)
	if ra.Type() != rb.Type() {
		return false
	}
	if ra.Kind() == reflect.Ptr {
		return ra.Pointer() == rb.Pointer()
	}
	return reflect.DeepEqual(a, b)
}

func erCloneAgentMessages(messages []agent.AgentMessage) []agent.AgentMessage {
	if messages == nil {
		return nil
	}
	out := make([]agent.AgentMessage, len(messages))
	for i, msg := range messages {
		out[i] = erCloneSingleMessage(msg)
	}
	return out
}

func erCloneUserMessageContent(c ai.UserMessageContent) ai.UserMessageContent {
	var txt *string
	if c.Text != nil {
		s := *c.Text
		txt = &s
	}
	return ai.UserMessageContent{
		Text:   txt,
		Blocks: erCloneContentList(c.Blocks),
	}
}

func erCloneSystemMessageContent(c ai.SystemMessageContent) ai.SystemMessageContent {
	var txt *string
	if c.Text != nil {
		s := *c.Text
		txt = &s
	}
	return ai.SystemMessageContent{
		Text:   txt,
		Blocks: erCloneContentList(c.Blocks),
	}
}

func erCloneSingleMessage(msg agent.AgentMessage) agent.AgentMessage {
	if msg == nil {
		return nil
	}
	switch m := msg.(type) {
	case *ai.UserMessage:
		if m == nil {
			return nil
		}
		cp := *m
		cp.Content = erCloneUserMessageContent(m.Content)
		return &cp
	case *ai.AssistantMessage:
		if m == nil {
			return nil
		}
		return &ai.AssistantMessage{
			Role:                  m.Role,
			Content:               erCloneContentList(m.Content),
			Api:                   m.Api,
			Provider:              m.Provider,
			Model:                 m.Model,
			ResponseModel:         m.ResponseModel,
			ResponseId:            m.ResponseId,
			ProviderThinkingLevel: m.ProviderThinkingLevel,
			ThinkingLevel:         m.ThinkingLevel,
			Diagnostics:           slices.Clone(m.Diagnostics),
			Usage:                 m.Usage,
			StopReason:            m.StopReason,
			Deferred:              m.Deferred,
			ErrorMessage:          m.ErrorMessage,
			RawStopReason:         m.RawStopReason,
			EndTurn:               m.EndTurn,
			Timestamp:             m.Timestamp,
			DurationMs:            m.DurationMs,
		}
	case *ai.ToolResultMessage:
		if m == nil {
			return nil
		}
		cp := *m
		cp.Content = erCloneContentList(m.Content)
		return &cp
	case *ai.SystemMessage:
		if m == nil {
			return nil
		}
		cp := *m
		cp.Content = erCloneSystemMessageContent(m.Content)
		if m.Sections != nil {
			cp.Sections = m.Sections.Clone()
		}
		cp.ToolsAdded = slices.Clone(m.ToolsAdded)
		cp.ToolsRemoved = slices.Clone(m.ToolsRemoved)
		return &cp
	case *CustomMessage:
		if m == nil {
			return nil
		}
		cp := *m
		cp.Content = erCloneUserMessageContent(m.Content)
		return &cp
	case *BashExecutionMessage:
		if m == nil {
			return nil
		}
		cp := *m
		return &cp
	case *BranchSummaryMessage:
		if m == nil {
			return nil
		}
		cp := *m
		return &cp
	case *CompactionSummaryMessage:
		if m == nil {
			return nil
		}
		cp := *m
		return &cp
	default:
		// Preserve unknown/external AgentMessage implementations round-tripping through JSON when possible.
		raw, err := jsonx.Marshal(msg)
		if err != nil {
			return msg
		}
		parsed, err := jsonx.ParseString(string(raw))
		if err != nil {
			return msg
		}
		decoded, err := agent.DecodeAgentMessage(parsed)
		if err != nil {
			return msg
		}
		return decoded
	}
}

func erCloneContentList(in ai.ContentList) ai.ContentList {
	if in == nil {
		return nil
	}
	out := make(ai.ContentList, len(in))
	for i, block := range in {
		switch b := block.(type) {
		case *ai.TextContent:
			if b != nil {
				cp := *b
				out[i] = &cp
			}
		case *ai.ThinkingContent:
			if b != nil {
				cp := *b
				out[i] = &cp
			}
		case *ai.ImageContent:
			if b != nil {
				cp := *b
				out[i] = &cp
			}
		case *ai.ToolCall:
			if b != nil {
				cp := *b
				out[i] = &cp
			}
		default:
			out[i] = block
		}
	}
	return out
}

func erGetCurrentSystemMessage(messages []agent.AgentMessage) *ai.SystemMessage {
	aiMsgs := make([]ai.Message, 0, len(messages))
	for _, m := range messages {
		if am, ok := m.(ai.Message); ok && am != nil {
			aiMsgs = append(aiMsgs, am)
		}
	}
	return ai.GetCurrentSystemMessage(aiMsgs)
}

func erRestoreSystemMessages(current, visible, returned []agent.AgentMessage) []agent.AgentMessage {
	if erSameAgentMessages(returned, visible) {
		return current
	}
	head := erGetCurrentSystemMessage(current)
	if head == nil {
		return returned
	}
	out := make([]agent.AgentMessage, 0, 1+len(returned))
	out = append(out, head)
	out = append(out, returned...)
	return out
}

func erSameImages(left, right []ai.ImageContent) bool {
	if len(left) != len(right) {
		return false
	}
	if (left == nil) != (right == nil) {
		return false
	}
	return reflect.DeepEqual(left, right)
}

func erNormalizedToBuildSystemPromptOptions(opts NormalizedBuildSystemPromptOptions) BuildSystemPromptOptions {
	var appendPtr *string
	if opts.AppendSystemPrompt != "" {
		s := opts.AppendSystemPrompt
		appendPtr = &s
	}
	return BuildSystemPromptOptions{
		CustomPrompt:       opts.CustomPrompt,
		ForceSystemPrompt:  opts.ForceSystemPrompt,
		SelectedTools:      opts.SelectedTools,
		HiddenTools:        opts.HiddenTools,
		ToolSnippets:       opts.ToolSnippets,
		ToolGuidelines:     opts.ToolGuidelines,
		PromptGuidelines:   opts.PromptGuidelines,
		AppendSystemPrompt: appendPtr,
		Sections:           opts.Sections,
		Cwd:                opts.Cwd,
		ContextFiles:       opts.ContextFiles,
		Skills:             opts.Skills,
	}
}

// erUIPromptWrapper wraps blocking ExtensionUIContext prompts to emit ui_prompt_start / ui_prompt_end.
type erUIPromptWrapper struct {
	runner *ExtensionRunner
	inner  ExtensionUIContext
}

func (w *erUIPromptWrapper) withUIPrompt(kind UIPromptKind, title *string, fn func() error) {
	if w.runner == nil {
		_ = fn()
		return
	}
	var startEv *UIPromptStartEvent
	w.runner.mu.Lock()
	outer := w.runner.uiPromptDepth == 0
	w.runner.uiPromptDepth++
	if outer {
		w.runner.activeUIPrompt = &erActiveUIPrompt{kind: kind, title: title}
		startEv = &UIPromptStartEvent{
			Type:   "ui_prompt_start",
			Reason: "ui_prompt",
			Kind:   kind,
			Title:  title,
		}
	}
	w.runner.mu.Unlock()

	if startEv != nil {
		_ = w.runner.Emit(context.Background(), *startEv)
	}

	defer func() {
		var endEv *UIPromptEndEvent
		w.runner.mu.Lock()
		w.runner.uiPromptDepth--
		if w.runner.uiPromptDepth <= 0 {
			w.runner.uiPromptDepth = 0
			active := w.runner.activeUIPrompt
			if active == nil {
				active = &erActiveUIPrompt{kind: kind, title: title}
			}
			w.runner.activeUIPrompt = nil
			endEv = &UIPromptEndEvent{
				Type:   "ui_prompt_end",
				Reason: "ui_prompt",
				Kind:   active.kind,
				Title:  active.title,
			}
		}
		w.runner.mu.Unlock()
		if endEv != nil {
			_ = w.runner.Emit(context.Background(), *endEv)
		}
	}()

	_ = fn()
}

func erNonEmptyTitlePtr(title string) *string {
	if title == "" {
		return nil
	}
	t := title
	return &t
}

func (w *erUIPromptWrapper) Select(ctx context.Context, title string, options []string, opts ...*ExtensionUIDialogOptions) (*string, error) {
	var res *string
	var err error
	w.withUIPrompt(UIPromptKindSelect, erNonEmptyTitlePtr(title), func() error {
		res, err = w.inner.Select(ctx, title, options, opts...)
		return err
	})
	return res, err
}

func (w *erUIPromptWrapper) Confirm(ctx context.Context, title, message string, opts ...*ExtensionUIDialogOptions) (bool, error) {
	var res bool
	var err error
	w.withUIPrompt(UIPromptKindConfirm, erNonEmptyTitlePtr(title), func() error {
		res, err = w.inner.Confirm(ctx, title, message, opts...)
		return err
	})
	return res, err
}

func (w *erUIPromptWrapper) Input(ctx context.Context, title string, placeholder *string, opts ...*ExtensionUIDialogOptions) (*string, error) {
	var res *string
	var err error
	w.withUIPrompt(UIPromptKindInput, erNonEmptyTitlePtr(title), func() error {
		res, err = w.inner.Input(ctx, title, placeholder, opts...)
		return err
	})
	return res, err
}

func (w *erUIPromptWrapper) Editor(ctx context.Context, title string, prefill ...string) (*string, error) {
	var res *string
	var err error
	w.withUIPrompt(UIPromptKindEditor, erNonEmptyTitlePtr(title), func() error {
		res, err = w.inner.Editor(ctx, title, prefill...)
		return err
	})
	return res, err
}

func (w *erUIPromptWrapper) Custom(
	ctx context.Context,
	factory func(t tui.TUI, th *theme.Theme, keybindings *tui.KeybindingsManager, done func(any)) (tui.Component, error),
	options ...*ExtensionCustomOptions,
) (any, error) {
	var res any
	var err error
	w.withUIPrompt(UIPromptKindCustom, nil, func() error {
		res, err = w.inner.Custom(ctx, factory, options...)
		return err
	})
	return res, err
}

func (w *erUIPromptWrapper) Notify(message string, notifyType ...string) {
	w.inner.Notify(message, notifyType...)
}
func (w *erUIPromptWrapper) OnTerminalInput(handler TerminalInputHandler) func() {
	return w.inner.OnTerminalInput(handler)
}
func (w *erUIPromptWrapper) SetStatus(key string, text *string) { w.inner.SetStatus(key, text) }
func (w *erUIPromptWrapper) SetWorkingMessage(message ...string) {
	w.inner.SetWorkingMessage(message...)
}
func (w *erUIPromptWrapper) SetWorkingVisible(visible bool) { w.inner.SetWorkingVisible(visible) }
func (w *erUIPromptWrapper) SetWorkingIndicator(options *WorkingIndicatorOptions) {
	w.inner.SetWorkingIndicator(options)
}
func (w *erUIPromptWrapper) SetHiddenThinkingLabel(label ...string) {
	w.inner.SetHiddenThinkingLabel(label...)
}
func (w *erUIPromptWrapper) SetWidgetLines(key string, content []string, options ...*ExtensionWidgetOptions) {
	w.inner.SetWidgetLines(key, content, options...)
}
func (w *erUIPromptWrapper) SetWidgetFactory(key string, factory func(t tui.TUI, th *theme.Theme) tui.Component, options ...*ExtensionWidgetOptions) {
	w.inner.SetWidgetFactory(key, factory, options...)
}
func (w *erUIPromptWrapper) SetFooter(factory func(t tui.TUI, th *theme.Theme, footerData ReadonlyFooterDataProvider) tui.Component) {
	w.inner.SetFooter(factory)
}
func (w *erUIPromptWrapper) SetHeader(factory func(t tui.TUI, th *theme.Theme) tui.Component) {
	w.inner.SetHeader(factory)
}
func (w *erUIPromptWrapper) SetTitle(title string)     { w.inner.SetTitle(title) }
func (w *erUIPromptWrapper) PasteToEditor(text string) { w.inner.PasteToEditor(text) }
func (w *erUIPromptWrapper) SetEditorText(text string) { w.inner.SetEditorText(text) }
func (w *erUIPromptWrapper) GetEditorText() string     { return w.inner.GetEditorText() }
func (w *erUIPromptWrapper) AddAutocompleteProvider(factory AutocompleteProviderFactory) {
	w.inner.AddAutocompleteProvider(factory)
}
func (w *erUIPromptWrapper) SetEditorComponent(factory EditorFactory) {
	w.inner.SetEditorComponent(factory)
}
func (w *erUIPromptWrapper) GetEditorComponent() EditorFactory { return w.inner.GetEditorComponent() }
func (w *erUIPromptWrapper) Theme() *theme.Theme               { return w.inner.Theme() }
func (w *erUIPromptWrapper) GetAllThemes() []ThemeInfo         { return w.inner.GetAllThemes() }
func (w *erUIPromptWrapper) GetTheme(name string) *theme.Theme { return w.inner.GetTheme(name) }
func (w *erUIPromptWrapper) SetThemeByName(name string) SetThemeResult {
	return w.inner.SetThemeByName(name)
}
func (w *erUIPromptWrapper) SetTheme(th *theme.Theme) SetThemeResult { return w.inner.SetTheme(th) }
func (w *erUIPromptWrapper) GetToolsExpanded() bool                  { return w.inner.GetToolsExpanded() }
func (w *erUIPromptWrapper) SetToolsExpanded(expanded bool)          { w.inner.SetToolsExpanded(expanded) }

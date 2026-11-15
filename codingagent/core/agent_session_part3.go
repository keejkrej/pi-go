// Ported from packages/coding-agent/src/core/agent-session.ts (part 3: extension/tool wiring,
// skill expansion, stats/export, and lifecycle disposal; pi v1.5.2 barebone parity).

package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/compat"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

type as3BuildRuntimeOptions struct {
	ActiveToolNames          []string
	HasActiveToolNames       bool
	FlagValues               map[string]any
	IncludeAllExtensionTools bool
}

type as3RefreshToolRegistryOptions struct {
	ActiveToolNames          []string
	HasActiveToolNames       bool
	IncludeAllExtensionTools bool
}

// ReloadOptions configures AgentSession.Reload.
type ReloadOptions struct {
	BeforeSessionStart func(ctx context.Context) error
}

// ExportToHtmlOptions configures AgentSession.ExportToHtml.
type ExportToHtmlOptions struct {
	ThemeName *string
}

// SummarizeForBugReportOptions configures AgentSession.SummarizeForBugReport.
type SummarizeForBugReportOptions struct {
	Hint *string
}

// =========================================================================
// Extension Binding & Resource Extension
// =========================================================================

// BindExtensions binds host UI, command actions, shutdown/abort handlers, and
// error listeners onto the active ExtensionRunner, then emits session_start
// and runs resources_discover. In barebone 0-extension builds runner emissions
// are no-ops.
func (s *AgentSession) BindExtensions(ctx context.Context, bindings ExtensionBindings) error {
	if ctx == nil {
		ctx = context.Background()
	}

	s.mu.Lock()
	if bindings.UIContext != nil {
		s.extUIContext = bindings.UIContext
	}
	if bindings.Mode != "" {
		s.extMode = bindings.Mode
	}
	if bindings.CommandContextActions != nil {
		s.extCmdActions = bindings.CommandContextActions
	}
	if bindings.AbortHandler != nil {
		s.extAbortHandler = bindings.AbortHandler
	}
	if bindings.ShutdownHandler != nil {
		s.extShutdown = bindings.ShutdownHandler
	}
	if bindings.OnError != nil {
		s.extErrListener = bindings.OnError
	}
	runner := s.extensionRunner
	startEv := s.sessionStartEv
	s.mu.Unlock()

	s.as3ApplyExtensionBindings(runner)
	if runner != nil {
		runner.Emit(ctx, startEv)
		runner.ReportUnhandledMcpServers()
	}

	reason := ResourcesDiscoverReasonStartup
	if startEv.Reason == "reload" {
		reason = ResourcesDiscoverReasonReload
	}
	return s.as3ExtendResourcesFromExtensions(ctx, reason)
}

func (s *AgentSession) as3ExtendResourcesFromExtensions(ctx context.Context, reason ResourcesDiscoverReason) error {
	runner := s.currentExtensionRunner()
	if runner == nil || !runner.HasHandlers("resources_discover") || s.resourceLoader == nil {
		return nil
	}

	discovered := runner.EmitResourcesDiscover(ctx, s.cwd, reason)
	if len(discovered.SkillPaths) == 0 && len(discovered.PromptPaths) == 0 && len(discovered.ThemePaths) == 0 {
		return nil
	}

	s.resourceLoader.ExtendResources(ResourceExtensionPaths{
		SkillPaths:  as3BuildExtensionResourcePaths(discovered.SkillPaths),
		PromptPaths: as3BuildExtensionResourcePaths(discovered.PromptPaths),
		ThemePaths:  as3BuildExtensionResourcePaths(discovered.ThemePaths),
	})
	s.as3RebuildSystemPrompt(s.GetActiveToolNames())
	return nil
}

func as3BuildExtensionResourcePaths(entries []DiscoveredResourcePath) []ResourceExtensionPath {
	out := make([]ResourceExtensionPath, 0, len(entries))
	for _, entry := range entries {
		var baseDir string
		if !utils.IsSyntheticPath(entry.ExtensionPath) {
			baseDir = filepath.Dir(entry.ExtensionPath)
		}
		out = append(out, ResourceExtensionPath{
			Path: entry.Path,
			Metadata: utils.PathMetadata{
				Source:  as3ExtensionSourceLabel(entry.ExtensionPath),
				Scope:   utils.SourceScopeTemporary,
				Origin:  utils.SourceOriginTopLevel,
				BaseDir: baseDir,
			},
		})
	}
	return out
}

func as3ExtensionSourceLabel(extensionPath string) string {
	if utils.IsSyntheticPath(extensionPath) {
		cleaned := strings.NewReplacer("<", "", ">", "").Replace(extensionPath)
		return "extension:" + cleaned
	}
	base := filepath.Base(extensionPath)
	name := strings.TrimSuffix(strings.TrimSuffix(base, ".ts"), ".js")
	return "extension:" + name
}

func (s *AgentSession) as3ApplyExtensionBindings(runner *ExtensionRunner) {
	s.mu.Lock()
	uiCtx := s.extUIContext
	mode := s.extMode
	cmdActions := s.extCmdActions
	errListener := s.extErrListener
	oldUnsub := s.extErrUnsub
	s.extErrUnsub = nil
	s.mu.Unlock()

	if oldUnsub != nil {
		oldUnsub()
	}
	if runner == nil {
		return
	}
	runner.SetUIContext(uiCtx, mode)
	runner.BindCommandContext(cmdActions)
	if errListener == nil {
		return
	}
	unsub := runner.OnError(errListener)

	s.mu.Lock()
	if s.extensionRunner == runner && s.extErrListener != nil {
		s.extErrUnsub = unsub
		unsub = nil
	}
	s.mu.Unlock()
	if unsub != nil {
		unsub()
	}
}

func (s *AgentSession) as3RefreshCurrentModelFromRegistry() {
	currentModel := s.Model()
	if currentModel == nil || s.modelRuntime == nil {
		return
	}
	refreshedModel := s.modelRuntime.GetModel(string(currentModel.Provider), currentModel.Id)
	if refreshedModel == nil || refreshedModel == currentModel || s.Agent == nil {
		return
	}
	s.Agent.SetModel(refreshedModel)
}

func (s *AgentSession) as3BindExtensionCore(runner *ExtensionRunner) {
	if runner == nil {
		return
	}

	getCommands := func() []SlashCommandInfo {
		var cmds []SlashCommandInfo
		for _, command := range runner.GetRegisteredCommands() {
			cmds = append(cmds, SlashCommandInfo{
				Name:        command.InvocationName,
				Description: command.Description,
				Source:      SlashCommandSourceExtension,
				SourceInfo:  command.SourceInfo,
			})
		}
		for _, template := range s.PromptTemplates() {
			var desc *string
			if template.Description != "" {
				d := template.Description
				desc = &d
			}
			cmds = append(cmds, SlashCommandInfo{
				Name:        template.Name,
				Description: desc,
				Source:      SlashCommandSourcePrompt,
				SourceInfo:  template.SourceInfo,
			})
		}
		if s.resourceLoader != nil {
			for _, skill := range s.resourceLoader.GetSkills().Skills {
				var desc *string
				if skill.Description != "" {
					d := skill.Description
					desc = &d
				}
				cmds = append(cmds, SlashCommandInfo{
					Name:        "skill:" + skill.Name,
					Description: desc,
					Source:      SlashCommandSourceSkill,
					SourceInfo:  skill.SourceInfo,
				})
			}
		}
		return cmds
	}

	runner.BindCore(
		ExtensionActions{
			SendMessage: func(message CustomMessageInput, options ...*SendMessageOptions) {
				var customOpts *SendCustomMessageOptions
				if len(options) > 0 && options[0] != nil {
					customOpts = &SendCustomMessageOptions{
						TriggerTurn: options[0].TriggerTurn,
						DeliverAs:   options[0].DeliverAs,
					}
				}
				if err := s.SendCustomMessage(context.Background(), message.CustomType, message.Content, message.Display, message.Details, customOpts); err != nil {
					runner.EmitError(ExtensionError{
						ExtensionPath: "<runtime>",
						Event:         "send_message",
						Error:         err.Error(),
					})
				}
			},
			SendUserMessage: func(content any, options ...*SendUserMessageOptions) {
				text, images := as3SplitUserContent(content)
				if err := s.SendUserMessage(context.Background(), text, images, options...); err != nil {
					runner.EmitError(ExtensionError{
						ExtensionPath: "<runtime>",
						Event:         "send_user_message",
						Error:         err.Error(),
					})
				}
			},
			AppendEntry: func(customType string, data any) {
				if s.SessionManager == nil {
					return
				}
				entryId, err := s.SessionManager.AppendCustomEntry(customType, data)
				if err != nil {
					return
				}
				if entry := s.SessionManager.GetEntry(entryId); entry != nil {
					s.as1Emit(&AgentSessionEventEntryAppended{
						Type:  "entry_appended",
						Entry: entry,
					})
				}
			},
			SetSessionName: func(name string) {
				s.SetSessionName(name)
			},
			GetSessionName: func() *string {
				return s.SessionName()
			},
			SetLabel: func(entryId string, label *string) {
				if s.SessionManager != nil {
					_, _ = s.SessionManager.AppendLabelChange(entryId, label)
				}
			},
			GetActiveTools: func() []string {
				return s.GetActiveToolNames()
			},
			GetAllTools: func() []ToolInfo {
				return s.GetAllTools()
			},
			GetSettings: func() Settings {
				if s.SettingsManager == nil {
					return Settings{}
				}
				return s.SettingsManager.GetSettings()
			},
			SetActiveTools: func(toolNames []string) {
				s.SetActiveToolsByName(toolNames)
			},
			RefreshTools: func() {
				s.as3RefreshToolRegistry()
			},
			GetCommands: getCommands,
			SetModel: func(ctx context.Context, model *ai.Model) (bool, error) {
				if model == nil || s.modelRuntime == nil || !s.modelRuntime.HasConfiguredAuth(string(model.Provider)) {
					return false, nil
				}
				if err := s.SetModel(ctx, model); err != nil {
					return false, err
				}
				return true, nil
			},
			GetThinkingLevel: func() agent.ThinkingLevel {
				return s.ThinkingLevel()
			},
			SetThinkingLevel: func(level agent.ThinkingLevel) {
				s.SetThinkingLevel(level)
			},
		},
		ExtensionContextActions{
			GetModel: func() *ai.Model {
				return s.Model()
			},
			GetScopedModels: func() []ScopedModel {
				return s.ScopedModels()
			},
			GetThinkingLevel: func() agent.ThinkingLevel {
				return s.ThinkingLevel()
			},
			IsIdle: func() bool {
				return s.IsIdle()
			},
			IsProjectTrusted: func() bool {
				return s.SettingsManager != nil && s.SettingsManager.IsProjectTrusted()
			},
			GetSignal: func() context.Context {
				if s.Agent == nil {
					return nil
				}
				return s.Agent.Signal()
			},
			Abort: func() {
				s.mu.Lock()
				handler := s.extAbortHandler
				s.mu.Unlock()
				if handler != nil {
					handler()
					return
				}
				_ = s.Abort(context.Background())
			},
			HasPendingMessages: func() bool {
				return s.PendingMessageCount() > 0
			},
			Shutdown: func() {
				s.mu.Lock()
				handler := s.extShutdown
				s.mu.Unlock()
				if handler != nil {
					handler()
				}
			},
			GetContextUsage: func() *ContextUsage {
				return s.GetContextUsage()
			},
			Compact: func(options ...*CompactOptions) {
				var opt *CompactOptions
				if len(options) > 0 {
					opt = options[0]
				}
				go func() {
					var args []string
					if opt != nil && opt.CustomInstructions != "" {
						args = []string{opt.CustomInstructions}
					}
					res, err := s.Compact(context.Background(), args...)
					if err != nil {
						if opt != nil && opt.OnError != nil {
							opt.OnError(err)
						}
						return
					}
					if opt != nil && opt.OnComplete != nil && res != nil {
						opt.OnComplete(*res)
					}
				}()
			},
			GetSystemPrompt: func() string {
				return s.SystemPrompt()
			},
			GetSystemPromptOptions: func() BuildSystemPromptOptions {
				s.mu.Lock()
				opts := s.baseSystemPromptOpts
				s.mu.Unlock()
				return as1NormalizedToBuildSystemPromptOptions(opts)
			},
			ExecuteTool: func(ctx context.Context, callerId, name string, args any, options *ExecuteToolOptions) (agent.AgentToolCallOutcome, error) {
				return s.as1ExecuteNestedToolCall(ctx, callerId, name, args, options)
			},
			GetCallableTools: func() []*agent.AgentTool {
				return s.as3GetCallableTools()
			},
		},
		&ExtensionProviderActions{
			RegisterProvider: func(name string, config ProviderConfig) {
				if s.modelRuntime != nil {
					_ = s.modelRuntime.RegisterProvider(name, config)
					s.as3RefreshCurrentModelFromRegistry()
				}
			},
			RegisterNativeProvider: func(provider *ai.Provider) {
				if s.modelRuntime != nil && provider != nil {
					_ = s.modelRuntime.RegisterNativeProvider(provider)
					s.as3RefreshCurrentModelFromRegistry()
				}
			},
			UnregisterProvider: func(name string) {
				if s.modelRuntime != nil {
					s.modelRuntime.UnregisterProvider(name)
					s.as3RefreshCurrentModelFromRegistry()
				}
			},
			RegisterVirtualModel: func(definition VirtualModelDefinition) {
				if s.modelRuntime != nil {
					_ = s.modelRuntime.RegisterVirtualModel(definition)
					s.as3RefreshCurrentModelFromRegistry()
				}
			},
			UnregisterVirtualModel: func(provider, id string) {
				if s.modelRuntime != nil {
					s.modelRuntime.UnregisterVirtualModel(provider, id)
					s.as3RefreshCurrentModelFromRegistry()
				}
			},
		},
	)
}

func as3SplitUserContent(content any) (string, []*ai.ImageContent) {
	switch v := content.(type) {
	case string:
		return v, nil
	case *string:
		if v != nil {
			return *v, nil
		}
	case ai.ContentList:
		return as3SplitContentBlocks(v)
	case []ai.Content:
		return as3SplitContentBlocks(v)
	case ai.UserMessageContent:
		if v.Text != nil {
			return *v.Text, nil
		}
		return as3SplitContentBlocks(v.Blocks)
	}
	return fmt.Sprint(content), nil
}

func as3SplitContentBlocks(blocks []ai.Content) (string, []*ai.ImageContent) {
	var parts []string
	var images []*ai.ImageContent
	for _, b := range blocks {
		switch blk := b.(type) {
		case *ai.TextContent:
			if blk != nil {
				parts = append(parts, blk.Text)
			}
		case *ai.ImageContent:
			if blk != nil {
				img := *blk
				images = append(images, &img)
			}
		}
	}
	return strings.Join(parts, ""), images
}

// =========================================================================
// Tool Registry, Runtime Construction & Reload
// =========================================================================

func (s *AgentSession) as3RefreshToolRegistry(options ...as3RefreshToolRegistryOptions) {
	var opts as3RefreshToolRegistryOptions
	if len(options) > 0 {
		opts = options[0]
	}

	previousActiveToolNames := s.GetActiveToolNames()

	s.mu.Lock()
	runner := s.extensionRunner
	allowedTools := s.allowedTools
	customTools := slices.Clone(s.customTools)
	baseToolDefs := make(map[string]ToolDefinition, len(s.baseToolDefinitions))
	for k, v := range s.baseToolDefinitions {
		baseToolDefs[k] = v
	}
	baseToolOrder := slices.Clone(s.baseToolOrder)
	previousActivatedOnRegistration := make(map[string]struct{}, len(s.toolRegistry))
	for name := range s.toolRegistry {
		if s.as3IsActivatedOnRegistrationLocked(name) {
			previousActivatedOnRegistration[name] = struct{}{}
		}
	}
	s.mu.Unlock()

	var registeredTools []RegisteredTool
	if runner != nil {
		registeredTools = runner.GetAllRegisteredTools()
	}

	allCustomTools := make([]RegisteredTool, 0, len(registeredTools)+len(customTools))
	for _, rt := range registeredTools {
		if s.as3IsAllowedTool(rt.Definition.Name) {
			allCustomTools = append(allCustomTools, rt)
		}
	}
	for _, def := range customTools {
		if s.as3IsAllowedTool(def.Name) {
			allCustomTools = append(allCustomTools, RegisteredTool{
				Definition: def,
				SourceInfo: utils.CreateSyntheticSourceInfo("<sdk:"+def.Name+">", utils.SyntheticSourceOptions{Source: "sdk"}),
			})
		}
	}

	orderedBaseNames := make([]string, 0, len(baseToolDefs))
	seenBase := make(map[string]struct{}, len(baseToolDefs))
	for _, name := range baseToolOrder {
		if _, ok := baseToolDefs[name]; ok {
			if _, dup := seenBase[name]; !dup {
				seenBase[name] = struct{}{}
				orderedBaseNames = append(orderedBaseNames, name)
			}
		}
	}
	if len(seenBase) < len(baseToolDefs) {
		var extraBase []string
		for name := range baseToolDefs {
			if _, dup := seenBase[name]; !dup {
				extraBase = append(extraBase, name)
			}
		}
		sort.Strings(extraBase)
		orderedBaseNames = append(orderedBaseNames, extraBase...)
	}

	definitionRegistry := make(map[string]as1ToolDefinitionEntry, len(baseToolDefs)+len(allCustomTools))
	var builtInEntries []RegisteredTool
	for _, name := range orderedBaseNames {
		if !s.as3IsAllowedTool(name) {
			continue
		}
		def := baseToolDefs[name]
		srcInfo := utils.CreateSyntheticSourceInfo(utils.BuiltinPathPrefix+name, utils.SyntheticSourceOptions{Source: "builtin"})
		definitionRegistry[name] = as1ToolDefinitionEntry{
			definition: def,
			sourceInfo: srcInfo,
		}
		builtInEntries = append(builtInEntries, RegisteredTool{
			Definition: def,
			SourceInfo: srcInfo,
		})
	}
	for _, rt := range allCustomTools {
		definitionRegistry[rt.Definition.Name] = as1ToolDefinitionEntry{
			definition: rt.Definition,
			sourceInfo: rt.SourceInfo,
		}
	}

	promptSnippets := make(map[string]string, len(definitionRegistry))
	promptGuidelines := make(map[string][]string, len(definitionRegistry))
	for name, entry := range definitionRegistry {
		if snippet := as3NormalizePromptSnippet(entry.definition.PromptSnippet); snippet != "" {
			promptSnippets[name] = snippet
		}
		if guidelines := as3NormalizePromptGuidelines(entry.definition.PromptGuidelines); len(guidelines) > 0 {
			promptGuidelines[name] = guidelines
		}
	}

	wrappedBuiltInTools := WrapRegisteredTools(builtInEntries, runner)
	wrappedExtensionTools := WrapRegisteredTools(allCustomTools, runner)

	newToolRegistry := make(map[string]*agent.AgentTool, len(wrappedBuiltInTools)+len(wrappedExtensionTools))
	newToolRegistryOrder := make([]string, 0, len(wrappedBuiltInTools)+len(wrappedExtensionTools))
	seenRegistryOrder := make(map[string]struct{}, len(wrappedBuiltInTools)+len(wrappedExtensionTools))
	appendRegistryTool := func(tool *agent.AgentTool) {
		if tool == nil {
			return
		}
		newToolRegistry[tool.Name] = tool
		if _, dup := seenRegistryOrder[tool.Name]; !dup {
			seenRegistryOrder[tool.Name] = struct{}{}
			newToolRegistryOrder = append(newToolRegistryOrder, tool.Name)
		}
	}
	for _, tool := range wrappedBuiltInTools {
		appendRegistryTool(tool)
	}
	for _, tool := range wrappedExtensionTools {
		appendRegistryTool(tool)
	}

	s.mu.Lock()
	s.toolDefinitions = definitionRegistry
	s.toolPromptSnippets = promptSnippets
	s.toolPromptGuidelines = promptGuidelines
	s.toolRegistry = newToolRegistry
	s.toolRegistryOrder = newToolRegistryOrder

	var nextActiveToolNames []string
	var sourceNames []string
	if opts.HasActiveToolNames {
		sourceNames = opts.ActiveToolNames
	} else {
		sourceNames = previousActiveToolNames
	}
	for _, name := range sourceNames {
		if s.as3IsAllowedToolLocked(name) {
			nextActiveToolNames = append(nextActiveToolNames, name)
		}
	}

	if allowedTools != nil {
		for _, toolName := range s.toolRegistryOrder {
			if allowedTools(toolName) && s.as3IsDeclarableLocked(toolName) {
				nextActiveToolNames = append(nextActiveToolNames, toolName)
			}
		}
	} else if opts.IncludeAllExtensionTools {
		for _, tool := range wrappedExtensionTools {
			if tool != nil && s.as3IsActivatedOnRegistrationLocked(tool.Name) {
				nextActiveToolNames = append(nextActiveToolNames, tool.Name)
			}
		}
	} else if !opts.HasActiveToolNames {
		for _, toolName := range s.toolRegistryOrder {
			if _, existed := previousActivatedOnRegistration[toolName]; !existed && s.as3IsActivatedOnRegistrationLocked(toolName) {
				nextActiveToolNames = append(nextActiveToolNames, toolName)
			}
		}
	}

	for _, name := range s.pendingToolOrder {
		if _, ok := s.pendingToolNames[name]; ok {
			nextActiveToolNames = append(nextActiveToolNames, name)
		}
	}
	s.mu.Unlock()

	s.as3SetActiveTools(as3DedupeStrings(nextActiveToolNames))
}

func (s *AgentSession) as3BuildRuntime(options as3BuildRuntimeOptions) {
	var autoResizeImages bool
	var commandPrefix string
	var shellPath string
	if s.SettingsManager != nil {
		autoResizeImages = s.SettingsManager.GetImageAutoResize()
		if p := s.SettingsManager.GetShellCommandPrefix(); p != nil {
			commandPrefix = *p
		}
		if sp := s.SettingsManager.GetShellPath(); sp != nil {
			shellPath = *sp
		}
	} else {
		autoResizeImages = true
	}

	baseDefs := make(map[string]ToolDefinition)
	var baseOrder []string
	var defaultOverrideNames []string
	if s.baseToolsOverride != nil {
		for name, tool := range s.baseToolsOverride {
			if tool != nil {
				baseDefs[name] = CreateToolDefinitionFromAgentTool(tool)
				defaultOverrideNames = append(defaultOverrideNames, name)
			}
		}
		sort.Strings(defaultOverrideNames)
		baseOrder = slices.Clone(defaultOverrideNames)
	} else {
		allDefs := CreateAllToolDefinitions(s.cwd, &ToolsOptions{
			Read: &ReadToolOptions{AutoResizeImages: &autoResizeImages},
			Bash: &BashToolOptions{
				CommandPrefix: commandPrefix,
				ShellPath:     shellPath,
			},
		})
		for name, def := range allDefs {
			baseDefs[string(name)] = def
		}
		canonicalBuiltins := []string{"read", "bash", "powershell", "edit", "write", "grep", "find", "ls"}
		seen := make(map[string]struct{}, len(baseDefs))
		for _, name := range canonicalBuiltins {
			if _, ok := baseDefs[name]; ok {
				seen[name] = struct{}{}
				baseOrder = append(baseOrder, name)
			}
		}
		if len(seen) < len(baseDefs) {
			var extras []string
			for name := range baseDefs {
				if _, ok := seen[name]; !ok {
					extras = append(extras, name)
				}
			}
			sort.Strings(extras)
			baseOrder = append(baseOrder, extras...)
		}
	}

	var extensionsResult LoadExtensionsResult
	if s.resourceLoader != nil {
		extensionsResult = s.resourceLoader.GetExtensions()
	}
	if extensionsResult.Runtime == nil {
		extensionsResult.Runtime = &ExtensionRuntime{
			FlagValues: map[string]any{},
		}
	} else if extensionsResult.Runtime.FlagValues == nil {
		extensionsResult.Runtime.FlagValues = map[string]any{}
	}
	for name, val := range options.FlagValues {
		extensionsResult.Runtime.FlagValues[name] = val
	}

	runner := NewExtensionRunner(
		extensionsResult.Extensions,
		extensionsResult.Runtime,
		s.cwd,
		s.SessionManager,
		s.modelRegistry,
	)

	s.mu.Lock()
	s.baseToolDefinitions = baseDefs
	s.baseToolOrder = baseOrder
	s.extensionRunner = runner
	if s.extensionRunRef != nil {
		s.extensionRunRef.Current = runner
	}
	s.mu.Unlock()

	s.as3BindExtensionCore(runner)
	s.as3ApplyExtensionBindings(runner)

	var baseActiveToolNames []string
	hasBaseActive := false
	if options.HasActiveToolNames || options.ActiveToolNames != nil {
		baseActiveToolNames = slices.Clone(options.ActiveToolNames)
		hasBaseActive = true
	} else if s.baseToolsOverride != nil {
		baseActiveToolNames = defaultOverrideNames
		hasBaseActive = true
	} else {
		baseActiveToolNames = []string{"read", "bash", "edit", "write"}
		hasBaseActive = true
	}

	s.as3RefreshToolRegistry(as3RefreshToolRegistryOptions{
		ActiveToolNames:          baseActiveToolNames,
		HasActiveToolNames:       hasBaseActive,
		IncludeAllExtensionTools: options.IncludeAllExtensionTools,
	})
}

// Reload reloads settings, resets API providers, reloads resource files, and
// rebuilds the tool registry and system prompt.
func (s *AgentSession) Reload(ctx context.Context, options ...*ReloadOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if s.IsStreaming() {
		return errors.New("Wait for the current response to finish before reloading.")
	}
	if s.IsCompacting() {
		return errors.New("Wait for compaction to finish before reloading.")
	}

	var opts *ReloadOptions
	if len(options) > 0 {
		opts = options[0]
	}

	oldRunner := s.currentExtensionRunner()
	var previousFlagValues map[string]any
	if oldRunner != nil {
		previousFlagValues = oldRunner.GetFlagValues()
		EmitSessionShutdownEvent(ctx, oldRunner, SessionShutdownEvent{
			Type:   "session_shutdown",
			Reason: "reload",
		})
		oldRunner.Invalidate("Extension context invalidated by session reload.")
	}

	getDefaultTools := func() []string {
		s.mu.Lock()
		usesDefault := s.usesDefaultTools
		mods := slices.Clone(s.defaultToolModifiers)
		s.mu.Unlock()
		if !usesDefault {
			return nil
		}
		var base []string
		if s.SettingsManager != nil {
			base = s.SettingsManager.GetDefaultTools()
		}
		if base == nil {
			base = sdkDefaultToolNames()
		}
		return sdkApplyToolModifiers(base, mods)
	}

	previousDefaultTools := make(map[string]struct{})
	for _, name := range getDefaultTools() {
		previousDefaultTools[name] = struct{}{}
	}

	if s.SettingsManager != nil {
		if err := s.SettingsManager.Reload(); err != nil {
			return err
		}
	}
	s.as2SyncQueueModesFromSettings()
	compat.ResetApiProviders()
	if s.resourceLoader != nil {
		if err := s.resourceLoader.Reload(ctx); err != nil {
			return err
		}
	}

	var addedDefaultTools []string
	for _, name := range getDefaultTools() {
		if _, existed := previousDefaultTools[name]; !existed {
			addedDefaultTools = append(addedDefaultTools, name)
		}
	}

	activeBefore := s.GetActiveToolNames()
	s.mu.Lock()
	for _, name := range activeBefore {
		s.pendingToolNames[name] = struct{}{}
	}
	s.restoredToolNames = make(map[string]struct{}, len(activeBefore))
	for _, name := range activeBefore {
		s.restoredToolNames[name] = struct{}{}
	}
	s.mu.Unlock()

	combinedActive := make([]string, 0, len(activeBefore)+len(addedDefaultTools))
	combinedActive = append(combinedActive, activeBefore...)
	combinedActive = append(combinedActive, addedDefaultTools...)

	s.as3BuildRuntime(as3BuildRuntimeOptions{
		ActiveToolNames:          combinedActive,
		HasActiveToolNames:       true,
		FlagValues:               previousFlagValues,
		IncludeAllExtensionTools: true,
	})

	s.mu.Lock()
	hasBindings := s.extUIContext != nil || s.extCmdActions != nil || s.extShutdown != nil || s.extErrListener != nil
	runner := s.extensionRunner
	s.mu.Unlock()

	if hasBindings {
		if opts != nil && opts.BeforeSessionStart != nil {
			if err := opts.BeforeSessionStart(ctx); err != nil {
				return err
			}
		}
		if runner != nil {
			runner.Emit(ctx, SessionStartEvent{Type: "session_start", Reason: "reload"})
			runner.ReportUnhandledMcpServers()
		}
		if err := s.as3ExtendResourcesFromExtensions(ctx, ResourcesDiscoverReasonReload); err != nil {
			return err
		}
	}
	return nil
}

// =========================================================================
// Tool Introspection & Loadout Management
// =========================================================================

// GetActiveToolNames returns the names of tools currently active/declared to the model.
func (s *AgentSession) GetActiveToolNames() []string {
	if s.Agent == nil {
		return nil
	}
	tools := s.Agent.Tools()
	out := make([]string, 0, len(tools))
	for _, tool := range tools {
		if tool != nil {
			out = append(out, tool.Name)
		}
	}
	return out
}

// GetCallableToolNames returns the names of tools callable through nested tool execution.
func (s *AgentSession) GetCallableToolNames() []string {
	callable := s.as3GetCallableTools()
	out := make([]string, 0, len(callable))
	for _, tool := range callable {
		if tool != nil {
			out = append(out, tool.Name)
		}
	}
	return out
}

// GetAllTools returns all configured tools with metadata and source information.
func (s *AgentSession) GetAllTools() []ToolInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ToolInfo, 0, len(s.toolDefinitions))
	for _, entry := range s.toolDefinitions {
		def := entry.definition
		var annotations *ToolAnnotations
		if def.Annotations != nil {
			copyAnn := *def.Annotations
			annotations = &copyAnn
		}
		out = append(out, ToolInfo{
			Name:             def.Name,
			Description:      def.Description,
			Parameters:       def.Parameters,
			PromptGuidelines: slices.Clone(def.PromptGuidelines),
			ReachesTools:     def.ReachesTools,
			Exposure:         s.as3GetToolExposureLocked(def.Name),
			Namespace:        def.Namespace,
			Annotations:      annotations,
			SourceInfo:       entry.sourceInfo,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

// GetToolDefinition returns the ToolDefinition for name if registered, or nil.
func (s *AgentSession) GetToolDefinition(name string) *ToolDefinition {
	s.mu.Lock()
	entry, ok := s.toolDefinitions[name]
	s.mu.Unlock()
	if !ok {
		return nil
	}
	def := entry.definition
	return &def
}

// SetActiveToolsByName activates the specified registered tools and rebuilds the system prompt.
func (s *AgentSession) SetActiveToolsByName(toolNames []string) {
	previous := s.GetActiveToolNames()
	s.as3SetActiveTools(toolNames)

	activeSet := make(map[string]struct{})
	for _, name := range s.GetActiveToolNames() {
		activeSet[name] = struct{}{}
	}
	s.mu.Lock()
	for _, name := range previous {
		if _, active := activeSet[name]; !active {
			if _, restored := s.restoredToolNames[name]; restored {
				clear(s.pendingToolNames)
				break
			}
		}
	}
	s.mu.Unlock()
}

func (s *AgentSession) as3SetActiveTools(toolNames []string) {
	tools := s.as3ApplyToolLoadout(toolNames)
	s.mu.Lock()
	for _, tool := range tools {
		if tool != nil {
			delete(s.pendingToolNames, tool.Name)
		}
	}
	if len(s.pendingToolOrder) > 0 {
		filtered := s.pendingToolOrder[:0]
		for _, name := range s.pendingToolOrder {
			if _, keep := s.pendingToolNames[name]; keep {
				filtered = append(filtered, name)
			}
		}
		s.pendingToolOrder = filtered
	}
	s.mu.Unlock()

	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		if tool != nil {
			names = append(names, tool.Name)
		}
	}
	s.as3RebuildSystemPrompt(names)
}

func (s *AgentSession) as3IsAllowedTool(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.as3IsAllowedToolLocked(name)
}

func (s *AgentSession) as3IsAllowedToolLocked(name string) bool {
	if s.excludedTools != nil && s.excludedTools(name) {
		return false
	}
	if s.allowedTools == nil || s.allowedTools(name) {
		return true
	}
	return !s.allowlistFiltersMcp && IsMcpToolName(name)
}

func (s *AgentSession) as3IsActivatableLocked(name string) bool {
	if s.allowedTools == nil || s.allowedTools(name) || !IsMcpToolName(name) {
		return true
	}
	_, hasToolSearch := s.toolRegistry["tool_search"]
	return s.as3GetToolExposureLocked(name) != ToolExposureDirect && hasToolSearch
}

func (s *AgentSession) as3GetToolExposureLocked(name string) ToolExposure {
	if entry, ok := s.toolDefinitions[name]; ok && entry.definition.Exposure != "" {
		return entry.definition.Exposure
	}
	return ToolExposureDirect
}

func (s *AgentSession) as3IsDeclarableLocked(name string) bool {
	exposure := s.as3GetToolExposureLocked(name)
	return exposure == ToolExposureDirect || exposure == ToolExposureModelOnly
}

func (s *AgentSession) as3IsActivatedOnRegistrationLocked(name string) bool {
	if !s.as3IsDeclarableLocked(name) {
		return false
	}
	entry, ok := s.toolDefinitions[name]
	if ok && entry.definition.DefaultActive != nil && !*entry.definition.DefaultActive {
		return false
	}
	return true
}

func (s *AgentSession) as3GetCallableTools(active ...map[string]struct{}) []*agent.AgentTool {
	var activeSet map[string]struct{}
	if len(active) > 0 && active[0] != nil {
		activeSet = active[0]
	} else {
		activeSet = make(map[string]struct{})
		for _, name := range s.GetActiveToolNames() {
			activeSet[name] = struct{}{}
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	var callable []*agent.AgentTool
	for _, tool := range s.toolRegistry {
		if tool == nil {
			continue
		}
		exposure := s.as3GetToolExposureLocked(tool.Name)
		_, isActive := activeSet[tool.Name]
		if exposure == ToolExposureCodemode || exposure == ToolExposureDeferred || (exposure == ToolExposureDirect && isActive) {
			callable = append(callable, tool)
		}
	}
	sort.Slice(callable, func(i, j int) bool {
		return callable[i].Name < callable[j].Name
	})
	return callable
}

func (s *AgentSession) as3ApplyToolLoadout(toolNames []string) []*agent.AgentTool {
	requested := make(map[string]struct{}, len(toolNames))
	for _, name := range toolNames {
		requested[name] = struct{}{}
	}

	var ordered []string
	seen := make(map[string]struct{}, len(toolNames))
	for _, name := range s.GetActiveToolNames() {
		if _, req := requested[name]; req {
			if _, ok := seen[name]; !ok {
				seen[name] = struct{}{}
				ordered = append(ordered, name)
			}
		}
	}
	for _, name := range toolNames {
		if _, ok := seen[name]; !ok {
			seen[name] = struct{}{}
			ordered = append(ordered, name)
		}
	}

	s.mu.Lock()
	var tools []*agent.AgentTool
	var hooks []as1ToolDefinitionEntry
	for _, name := range ordered {
		tool := s.toolRegistry[name]
		if tool != nil && s.as3GetToolExposureLocked(name) != ToolExposureHidden && s.as3IsActivatableLocked(name) {
			tools = append(tools, tool)
			if entry, ok := s.toolDefinitions[name]; ok && entry.definition.PrepareLoadout != nil {
				hooks = append(hooks, entry)
			}
		}
	}
	runner := s.extensionRunner
	s.mu.Unlock()

	hidden := make(map[string]struct{})
	declared := tools
	if len(hooks) > 0 {
		declaredSet := make(map[string]struct{}, len(tools))
		for _, tool := range tools {
			declaredSet[tool.Name] = struct{}{}
		}
		available := s.as3GetCallableTools(declaredSet)
		descriptions := make(map[string]string)

		loadout := ToolLoadout{
			Declared:  slices.Clone(tools),
			Available: available,
			GetExposure: func(name string) ToolExposure {
				s.mu.Lock()
				defer s.mu.Unlock()
				return s.as3GetToolExposureLocked(name)
			},
			GetNamespace: func(name string) *ToolNamespace {
				s.mu.Lock()
				defer s.mu.Unlock()
				if entry, ok := s.toolDefinitions[name]; ok {
					return entry.definition.Namespace
				}
				return nil
			},
			GetPromptGuidelines: func(name string) []string {
				s.mu.Lock()
				defer s.mu.Unlock()
				return slices.Clone(s.toolPromptGuidelines[name])
			},
		}

		for _, hook := range hooks {
			changes := func() (res *ToolLoadoutChanges) {
				defer func() {
					if r := recover(); r != nil && runner != nil {
						runner.EmitError(ExtensionError{
							ExtensionPath: hook.sourceInfo.Path,
							Event:         "prepare_loadout",
							Error:         fmt.Sprint(r),
						})
					}
				}()
				return hook.definition.PrepareLoadout(loadout)
			}()
			if changes == nil {
				continue
			}
			for name, desc := range changes.Descriptions {
				descriptions[name] = desc
			}
			for _, name := range changes.HideDeclarations {
				hidden[name] = struct{}{}
			}
		}

		if len(descriptions) > 0 {
			updated := make([]*agent.AgentTool, len(tools))
			for i, tool := range tools {
				if desc, ok := descriptions[tool.Name]; ok {
					cloned := *tool
					cloned.Description = desc
					updated[i] = &cloned
				} else {
					updated[i] = tool
				}
			}
			declared = updated
		}
	}

	s.mu.Lock()
	s.hiddenDeclarations = hidden
	s.mu.Unlock()

	if s.Agent != nil {
		s.Agent.SetTools(declared)
	}
	return declared
}

func as3NormalizePromptSnippet(text *string) string {
	if text == nil || *text == "" {
		return ""
	}
	return strings.Join(strings.Fields(*text), " ")
}

func as3NormalizePromptGuidelines(guidelines []string) []string {
	if len(guidelines) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(guidelines))
	var out []string
	for _, g := range guidelines {
		trimmed := strings.TrimSpace(g)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; !ok {
			seen[trimmed] = struct{}{}
			out = append(out, trimmed)
		}
	}
	return out
}

func (s *AgentSession) as3RebuildSystemPrompt(toolNames []string) {
	s.mu.Lock()
	validToolNames := make([]string, 0, len(toolNames))
	for _, name := range toolNames {
		if _, ok := s.toolRegistry[name]; ok {
			validToolNames = append(validToolNames, name)
		}
	}
	toolSnippets := make(map[string]string, len(s.toolRegistry))
	for name := range s.toolRegistry {
		if snippet := s.toolPromptSnippets[name]; snippet != "" {
			toolSnippets[name] = snippet
		}
	}
	toolGuidelines := make(map[string][]string, len(s.toolPromptGuidelines))
	for name, gl := range s.toolPromptGuidelines {
		toolGuidelines[name] = slices.Clone(gl)
	}
	hiddenTools := make([]string, 0, len(s.hiddenDeclarations))
	for name := range s.hiddenDeclarations {
		hiddenTools = append(hiddenTools, name)
	}
	sort.Strings(hiddenTools)
	cwd := s.cwd
	loader := s.resourceLoader
	s.mu.Unlock()

	var customPrompt *string
	var appendPromptPtr *string
	var skills []Skill
	var contextFiles []ContextFile
	if loader != nil {
		customPrompt = loader.GetSystemPrompt()
		appendParts := loader.GetAppendSystemPrompt()
		if len(appendParts) > 0 {
			joined := strings.Join(appendParts, "\n\n")
			appendPromptPtr = &joined
		}
		skills = loader.GetSkills().Skills
		contextFiles = loader.GetAgentsFiles().AgentsFiles
	}

	norm := NormalizeBuildSystemPromptOptions(BuildSystemPromptOptions{
		Cwd:                cwd,
		Skills:             skills,
		ContextFiles:       contextFiles,
		CustomPrompt:       customPrompt,
		AppendSystemPrompt: appendPromptPtr,
		SelectedTools:      validToolNames,
		HiddenTools:        hiddenTools,
		ToolSnippets:       toolSnippets,
		ToolGuidelines:     toolGuidelines,
	})

	s.mu.Lock()
	s.baseSystemPromptOpts = norm
	s.mu.Unlock()
}

func (s *AgentSession) as3PreparePromptAndToolLoadout(
	options *NormalizedBuildSystemPromptOptions,
	messages agent.AgentMessageList,
) *ai.SystemMessage {
	if options == nil {
		return nil
	}
	tools := s.as3ApplyToolLoadout(options.SelectedTools)
	selected := make([]string, 0, len(tools))
	for _, tool := range tools {
		if tool != nil {
			selected = append(selected, tool.Name)
		}
	}
	options.SelectedTools = selected

	s.mu.Lock()
	hiddenTools := make([]string, 0, len(s.hiddenDeclarations))
	for name := range s.hiddenDeclarations {
		hiddenTools = append(hiddenTools, name)
	}
	s.mu.Unlock()
	sort.Strings(hiddenTools)
	options.HiddenTools = hiddenTools

	if messages == nil {
		messages = s.Messages()
	}
	curSys := ai.GetCurrentSystemMessage(as1AgentMessagesToAiMessages(messages))
	var prevSections *omap.Map[string, *string]
	if curSys != nil {
		prevSections = curSys.Sections
	}

	builtSections, err := BuildSystemPromptSections(as1NormalizedToBuildSystemPromptOptions(*options))
	if err != nil {
		return nil
	}
	diff := DiffSystemPromptSections(prevSections, builtSections)
	if diff == nil {
		return nil
	}
	empty := ""
	return &ai.SystemMessage{
		Role:      "system",
		Content:   ai.SystemMessageContent{Text: &empty},
		Sections:  diff,
		Timestamp: time.Now().UnixMilli(),
	}
}

func (s *AgentSession) as3RestoreToolsFromTranscript(modifiers []string) {
	s.mu.Lock()
	clear(s.pendingToolNames)
	s.pendingToolOrder = nil
	clear(s.restoredToolNames)
	s.mu.Unlock()

	if s.SessionManager == nil {
		return
	}
	branch := s.SessionManager.GetBranch()
	var systemMessages []*ai.SystemMessage
	for _, entry := range branch {
		me, ok := entry.(*SessionMessageEntry)
		if !ok || me == nil || me.Message == nil {
			continue
		}
		if sm, ok := me.Message.(*ai.SystemMessage); ok && sm != nil {
			systemMessages = append(systemMessages, sm)
		}
	}
	if len(systemMessages) == 0 {
		return
	}

	mentioned := make(map[string]struct{})
	aiSys := make([]ai.Message, len(systemMessages))
	for i, sm := range systemMessages {
		aiSys[i] = sm
		for _, tool := range sm.ToolsAdded {
			if tool != nil {
				mentioned[tool.Name] = struct{}{}
			}
		}
		for _, ref := range sm.ToolsRemoved {
			mentioned[ref.Name] = struct{}{}
		}
	}

	currentTools := ai.GetCurrentTools(aiSys)
	currentNames := make([]string, 0, len(currentTools))
	for _, t := range currentTools {
		if t != nil {
			currentNames = append(currentNames, t.Name)
		}
	}

	s.mu.Lock()
	var newTools []string
	for _, name := range s.toolRegistryOrder {
		if _, wasMentioned := mentioned[name]; wasMentioned {
			continue
		}
		if _, isBuiltin := s.baseToolDefinitions[name]; isBuiltin {
			continue
		}
		if s.as3IsActivatedOnRegistrationLocked(name) {
			newTools = append(newTools, name)
		}
	}
	s.mu.Unlock()

	combined := make([]string, 0, len(currentNames)+len(newTools))
	combined = append(combined, currentNames...)
	combined = append(combined, newTools...)

	names := sdkApplyToolModifiers(combined, modifiers)
	restored := sdkApplyToolModifiers(currentNames, modifiers)

	s.mu.Lock()
	s.pendingToolNames = make(map[string]struct{})
	s.pendingToolOrder = nil
	for _, name := range names {
		if s.as3IsAllowedToolLocked(name) {
			s.pendingToolNames[name] = struct{}{}
			s.pendingToolOrder = append(s.pendingToolOrder, name)
		}
	}
	s.restoredToolNames = make(map[string]struct{}, len(restored))
	for _, name := range restored {
		s.restoredToolNames[name] = struct{}{}
	}
	s.mu.Unlock()

	s.as3SetActiveTools(names)
}

// =========================================================================
// Skill Expansion
// =========================================================================

func (s *AgentSession) as3ExpandSkillCommand(text string) string {
	if !strings.HasPrefix(text, "/skill:") || s.resourceLoader == nil {
		return text
	}

	rest := text[len("/skill:"):]
	var skillName, args string
	if idx := strings.IndexByte(rest, ' '); idx == -1 {
		skillName = rest
	} else {
		skillName = rest[:idx]
		args = strings.TrimSpace(rest[idx+1:])
	}

	var matched *Skill
	skills := s.resourceLoader.GetSkills().Skills
	for i := range skills {
		if skills[i].Name == skillName {
			matched = &skills[i]
			break
		}
	}
	if matched == nil {
		return text
	}

	raw, err := os.ReadFile(matched.FilePath)
	if err != nil {
		if runner := s.currentExtensionRunner(); runner != nil {
			runner.EmitError(ExtensionError{
				ExtensionPath: matched.FilePath,
				Event:         "skill_expansion",
				Error:         err.Error(),
			})
		}
		return text
	}

	stripped, _ := utils.StripFrontmatter(string(raw))
	body := strings.TrimSpace(stripped)
	block := fmt.Sprintf(
		"<skill name=%q location=%q>\nReferences are relative to %s.\n\n%s\n</skill>",
		matched.Name,
		matched.FilePath,
		matched.BaseDir,
		body,
	)
	if args != "" {
		return block + "\n\n" + args
	}
	return block
}

// =========================================================================
// Statistics, Context Usage, Exports & Bug Reports
// =========================================================================

// GetSessionStats aggregates message counts, token totals, cost, and context usage
// across all entries in the session.
func (s *AgentSession) GetSessionStats() SessionStats {
	var userMessages, assistantMessages, toolResults, totalMessages, toolCalls int
	usageTotals := CreateUsageTotals()

	var entries []SessionEntry
	if s.SessionManager != nil {
		entries = s.SessionManager.GetEntries()
	}

	for _, entry := range entries {
		switch e := entry.(type) {
		case *UsageEntry:
			if e != nil {
				AddUsageToTotals(&usageTotals, e.Usage)
			}
		case *BranchSummaryEntry:
			if e != nil && e.Usage != nil {
				AddUsageToTotals(&usageTotals, *e.Usage)
			}
		case *CompactionEntry:
			if e != nil && e.Usage != nil {
				AddUsageToTotals(&usageTotals, *e.Usage)
			}
		case *SessionMessageEntry:
			if e == nil || e.Message == nil {
				continue
			}
			totalMessages++
			switch msg := e.Message.(type) {
			case *ai.UserMessage:
				userMessages++
			case *ai.ToolResultMessage:
				toolResults++
				if msg.Usage != nil {
					AddUsageToTotals(&usageTotals, *msg.Usage)
				}
			case *ai.AssistantMessage:
				assistantMessages++
				for _, block := range msg.Content {
					if _, isToolCall := block.(*ai.ToolCall); isToolCall {
						toolCalls++
					}
				}
				AddUsageToTotals(&usageTotals, msg.Usage)
			}
		}
	}

	return SessionStats{
		SessionFile:       s.SessionFile(),
		SessionId:         s.SessionId(),
		UserMessages:      userMessages,
		AssistantMessages: assistantMessages,
		ToolCalls:         toolCalls,
		ToolResults:       toolResults,
		TotalMessages:     totalMessages,
		Tokens: SessionStatsTokens{
			Input:      usageTotals.Input,
			Output:     usageTotals.Output,
			CacheRead:  usageTotals.CacheRead,
			CacheWrite: usageTotals.CacheWrite,
			Total:      usageTotals.Input + usageTotals.Output + usageTotals.CacheRead + usageTotals.CacheWrite,
		},
		Cost:         usageTotals.Cost,
		ContextUsage: s.GetContextUsage(),
	}
}

// GetContextUsage reports estimated context token consumption against the current
// limits model's context window, or nil when no model with a positive window is selected.
func (s *AgentSession) GetContextUsage() *ContextUsage {
	model := s.as1LimitsModel()
	if model == nil || model.ContextWindow <= 0 {
		return nil
	}
	if s.SessionManager == nil {
		return nil
	}

	projection := s.SessionManager.BuildSessionProjection()
	branch := s.SessionManager.GetBranch()
	latestCompaction := GetLatestCompactionEntry(branch)

	if latestCompaction != nil {
		projectedAssistants := make(map[string]struct{})
		for _, pe := range projection.Entries {
			for _, msg := range pe.Messages {
				if am, ok := msg.(*ai.AssistantMessage); ok && am != nil &&
					am.StopReason != ai.StopReasonAborted &&
					am.StopReason != ai.StopReasonError &&
					CalculateContextTokens(am.Usage) > 0 {
					projectedAssistants[pe.SourceEntry.GetId()] = struct{}{}
					break
				}
			}
		}
		compIdx := -1
		for i, entry := range branch {
			if entry.GetId() == latestCompaction.GetId() {
				compIdx = i
				break
			}
		}
		hasPostCompactionUsage := false
		if compIdx >= 0 {
			for _, entry := range branch[compIdx+1:] {
				if _, ok := projectedAssistants[entry.GetId()]; ok {
					hasPostCompactionUsage = true
					break
				}
			}
		}
		if !hasPostCompactionUsage {
			return &ContextUsage{
				Tokens:        jsonx.Null[int](),
				ContextWindow: model.ContextWindow,
				Percent:       jsonx.Null[float64](),
			}
		}
	}

	estimate := EstimateProjectedContextTokens(projection, branch)
	pct := (float64(estimate.Tokens) / float64(model.ContextWindow)) * 100.0
	return &ContextUsage{
		Tokens:        jsonx.Some(estimate.Tokens),
		ContextWindow: model.ContextWindow,
		Percent:       jsonx.Some(pct),
	}
}

// ExportToHtml exports the session to an HTML file and returns its output path.
func (s *AgentSession) ExportToHtml(outputPath string, options ...*ExportToHtmlOptions) (string, error) {
	var requestedTheme *string
	if len(options) > 0 && options[0] != nil {
		requestedTheme = options[0].ThemeName
	}

	var resolvedTheme *string
	var candidates []*string
	if requestedTheme != nil {
		candidates = append(candidates, requestedTheme)
	}
	if s.SettingsManager != nil {
		candidates = append(candidates, s.SettingsManager.GetTheme())
	}
	toolTheme := theme.CurrentTheme()
	for _, candidate := range candidates {
		if candidate != nil && *candidate != "" {
			if loadedTheme, err := theme.GetThemeByName(*candidate); err == nil {
				resolvedTheme = candidate
				if loadedTheme != nil {
					toolTheme = loadedTheme
				}
				break
			}
		}
	}

	runner := s.currentExtensionRunner()
	toolRenderer := CreateToolHtmlRenderer(ToolHtmlRendererDeps{
		GetToolRenderers: func(name string) *ToolRenderers {
			if runner == nil {
				if def := s.GetToolDefinition(name); def != nil {
					return &ToolRenderers{
						RenderShell:  def.RenderShell,
						RenderCall:   def.RenderCall,
						RenderResult: def.RenderResult,
					}
				}
				return nil
			}
			return runner.ResolveToolRenderers(name, func() *ToolRenderers {
				def := s.GetToolDefinition(name)
				if def == nil {
					return nil
				}
				return &ToolRenderers{
					RenderShell:  def.RenderShell,
					RenderCall:   def.RenderCall,
					RenderResult: def.RenderResult,
				}
			})
		},
		Theme: toolTheme,
		Cwd:   s.cwd,
	})

	var stateCopy *agent.AgentState
	if s.Agent != nil {
		st := s.Agent.State()
		stateCopy = &st
	}

	exportOpts := &ExportOptions{
		ThemeName:    resolvedTheme,
		ToolRenderer: toolRenderer,
	}
	if outputPath != "" {
		exportOpts.OutputPath = &outputPath
	}
	return ExportSessionToHtml(s.SessionManager, stateCopy, exportOpts)
}

// ExportToJsonl exports the active session branch to a JSONL file and returns its path.
func (s *AgentSession) ExportToJsonl(outputPath ...string) (string, error) {
	return ExportSessionToJsonl(s.SessionManager, outputPath...)
}

// SummarizeForBugReport asks the current session model to summarize what went wrong.
func (s *AgentSession) SummarizeForBugReport(ctx context.Context, options ...*SummarizeForBugReportOptions) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	model := s.Model()
	if model == nil {
		return "", errors.New("No model selected")
	}
	auth, err := s.as1GetSummarizationRequestAuth(ctx, model)
	if err != nil {
		return "", err
	}
	var hint *string
	if len(options) > 0 && options[0] != nil {
		hint = options[0].Hint
	}
	var streamFn agent.StreamFn
	if s.Agent != nil {
		streamFn = s.Agent.StreamFn()
	}
	var retryPolicy *ai.RetryPolicy
	if s.SettingsManager != nil {
		rs := s.SettingsManager.GetRetrySettings()
		rp := as2RetryPolicyFromSettings(rs)
		retryPolicy = &rp
	}
	sessionId := s.SessionId()
	return GenerateBugReportSummary(ctx, GenerateBugReportSummaryOptions{
		Messages:      s.Messages(),
		Hint:          hint,
		Model:         auth.model,
		ApiKey:        auth.apiKey,
		Headers:       auth.headers,
		Env:           auth.env,
		ThinkingLevel: &auth.thinkingLevel,
		StreamFn:      streamFn,
		Retry:         retryPolicy,
		SessionId:     &sessionId,
	})
}

// GetLastAssistantText returns the trimmed text content of the latest assistant reply,
// or nil if no qualifying assistant message has non-empty text.
func (s *AgentSession) GetLastAssistantText() *string {
	msgs := s.Messages()
	var lastAssistant *ai.AssistantMessage
	for i := len(msgs) - 1; i >= 0; i-- {
		am, ok := msgs[i].(*ai.AssistantMessage)
		if !ok || am == nil {
			continue
		}
		hasNonEmptyText := false
		for _, block := range am.Content {
			if tc, ok := block.(*ai.TextContent); ok && tc != nil && strings.TrimSpace(tc.Text) != "" {
				hasNonEmptyText = true
				break
			}
		}
		if am.StopReason != ai.StopReasonStop && !hasNonEmptyText {
			continue
		}
		lastAssistant = am
		break
	}
	if lastAssistant == nil {
		return nil
	}
	var b strings.Builder
	for _, block := range lastAssistant.Content {
		if tc, ok := block.(*ai.TextContent); ok && tc != nil {
			b.WriteString(tc.Text)
		}
	}
	trimmed := strings.TrimSpace(b.String())
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

// CreateReplacedSessionContext clones the current extension command context with
// SendMessage and SendUserMessage bound to this session instance.
func (s *AgentSession) CreateReplacedSessionContext() ReplacedSessionContext {
	runner := s.currentExtensionRunner()
	if runner == nil {
		return nil
	}
	return runner.CreateCommandContext()
}

// Dispose aborts in-flight operations, invalidates extension contexts, disconnects from
// the agent, clears listeners, stops the cache warmer, and releases session resources.
func (s *AgentSession) Dispose() {
	func() {
		defer func() { _ = recover() }()
		s.AbortRetry()
		s.AbortCompaction()
		s.AbortBranchSummary()
		s.AbortBash()
		if s.Agent != nil {
			s.Agent.Abort()
		}
	}()

	if runner := s.currentExtensionRunner(); runner != nil {
		runner.Invalidate(
			"This extension ctx is stale after session replacement or reload. Do not use a captured pi or command ctx after ctx.newSession(), ctx.fork(), ctx.switchSession(), or ctx.reload(). For newSession, fork, and switchSession, move post-replacement work into withSession and use the ctx passed to withSession. For reload, do not use the old ctx after await ctx.reload().",
		)
	}
	s.as1DisconnectFromAgent()

	s.mu.Lock()
	s.eventListeners = nil
	warmer := s.cacheWarmer
	s.mu.Unlock()

	if warmer != nil {
		warmer.OnWarmed = nil
		warmer.Cancel()
	}
	if sid := s.SessionId(); sid != "" {
		_ = ai.CleanupSessionResources(&sid)
	}
}

func as3DedupeStrings(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

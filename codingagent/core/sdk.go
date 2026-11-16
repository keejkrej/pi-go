package core

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strings"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
)

// NoToolsOption restricts default tool exposure when creating an AgentSession.
type NoToolsOption string

const (
	// NoToolsNone leaves default built-in tools enabled.
	NoToolsNone NoToolsOption = ""
	// NoToolsAll suppresses all built-in and extension tools unless explicitly added via "+name".
	NoToolsAll NoToolsOption = "all"
	// NoToolsBuiltin suppresses built-in coding tools while preserving custom/extension tools.
	NoToolsBuiltin NoToolsOption = "builtin"
)

// CreateAgentSessionOptions configures standalone CreateAgentSession.
type CreateAgentSessionOptions struct {
	Cwd                           string
	AgentDir                      string
	AuthStorage                   *AuthStorage
	ModelRegistry                 *ModelRegistry
	ModelRuntime                  *ModelRuntime
	SettingsManager               *SettingsManager
	ResourceLoader                ResourceLoader
	SessionManager                *SessionManager
	SessionStartEvent             *SessionStartEvent
	Model                         *ai.Model
	ThinkingLevel                 agent.ThinkingLevel
	ScopedModels                  []ScopedModel
	Tools                         []*agent.AgentTool
	ToolNames                     []string
	ExcludeTools                  []string
	NoTools                       NoToolsOption
	CustomTools                   []ToolDefinition
	FlagValues                    map[string]any
	EventBus                      EventBus
	AdditionalExtensionPaths      []string
	AdditionalSkillPaths          []string
	AdditionalPromptTemplatePaths []string
	AdditionalThemePaths          []string
	NoExtensions                  bool
	DisabledBuiltinExtensions     []string
	NoSkills                      bool
	NoPromptTemplates             bool
	NoThemes                      bool
	NoContextFiles                bool
	SystemPrompt                  *string
	AppendSystemPrompt            []string
	InitialActiveToolNames        []string
	BaseToolsOverride             map[string]*agent.AgentTool
}

// CreateAgentSessionResult returns the created AgentSession and metadata.
type CreateAgentSessionResult struct {
	Session              *AgentSession
	ExtensionsResult     *LoadExtensionsResult
	ModelFallbackMessage *string
	Diagnostics          []AgentSessionRuntimeDiagnostic
}

type sdkToolSelection struct {
	usesDefaultTools     bool
	defaultToolModifiers []string
	initialActiveNames   []string
	allowedToolNames     []string // non-nil means allowlist active
	excludedToolNames    []string
	suppressBuiltinTools bool
}

// CreateAgentSession builds a ready-to-use AgentSession using provided overrides or
// defaults under cwd and agentDir.
func CreateAgentSession(ctx context.Context, options ...*CreateAgentSessionOptions) (*CreateAgentSessionResult, error) {
	var opts CreateAgentSessionOptions
	if len(options) > 0 && options[0] != nil {
		opts = *options[0]
	}

	cwd := opts.Cwd
	if strings.TrimSpace(cwd) == "" && opts.SessionManager != nil {
		cwd = opts.SessionManager.GetCwd()
	}
	if strings.TrimSpace(cwd) == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		cwd = wd
	} else {
		cwd = assResolvePath(cwd)
	}

	services, err := CreateAgentSessionServices(ctx, &CreateAgentSessionServicesOptions{
		Cwd:                           cwd,
		AgentDir:                      opts.AgentDir,
		AuthStorage:                   opts.AuthStorage,
		SettingsManager:               opts.SettingsManager,
		ModelRegistry:                 opts.ModelRegistry,
		ModelRuntime:                  opts.ModelRuntime,
		ResourceLoader:                opts.ResourceLoader,
		FlagValues:                    opts.FlagValues,
		EventBus:                      opts.EventBus,
		AdditionalExtensionPaths:      opts.AdditionalExtensionPaths,
		AdditionalSkillPaths:          opts.AdditionalSkillPaths,
		AdditionalPromptTemplatePaths: opts.AdditionalPromptTemplatePaths,
		AdditionalThemePaths:          opts.AdditionalThemePaths,
		NoExtensions:                  opts.NoExtensions,
		DisabledBuiltinExtensions:     opts.DisabledBuiltinExtensions,
		NoSkills:                      opts.NoSkills,
		NoPromptTemplates:             opts.NoPromptTemplates,
		NoThemes:                      opts.NoThemes,
		NoContextFiles:                opts.NoContextFiles,
		SystemPrompt:                  opts.SystemPrompt,
		AppendSystemPrompt:            opts.AppendSystemPrompt,
	})
	if err != nil {
		return nil, err
	}

	sessionManager := opts.SessionManager
	if sessionManager == nil {
		sessionDir := GetDefaultSessionDir(services.Cwd, services.AgentDir)
		created, err := SessionManagerCreate(services.Cwd, sessionDir)
		if err != nil {
			return nil, err
		}
		sessionManager = created
	}

	sessionCwd := sessionManager.GetCwd()
	if strings.TrimSpace(sessionCwd) == "" {
		sessionCwd = services.Cwd
	}

	toolSelection, baseOverride, err := sdkResolveToolSelection(services.SettingsManager, &opts)
	if err != nil {
		return nil, err
	}

	existingContext := sessionManager.BuildSessionContext()
	hasExistingSession := len(existingContext.Messages) > 0 || existingContext.Model != nil

	var model *ai.Model
	var modelFallbackMessage *string
	thinkingLevel := opts.ThinkingLevel

	switch {
	case opts.Model != nil:
		model = opts.Model
	case existingContext.Model != nil:
		restored := RestoreModelFromSession(
			existingContext.Model.Provider,
			existingContext.Model.ModelId,
			nil,
			false,
			services.ModelRuntime,
		)
		model = restored.Model
		modelFallbackMessage = restored.FallbackMessage
	default:
		defaultProvider := services.SettingsManager.GetDefaultProvider()
		defaultModelId := services.SettingsManager.GetDefaultModel()
		defaultThinking := services.SettingsManager.GetDefaultThinkingLevel()
		initial, initErr := FindInitialModel(ctx, FindInitialModelOptions{
			ScopedModels:         opts.ScopedModels,
			IsContinuing:         len(existingContext.Messages) > 0,
			DefaultProvider:      defaultProvider,
			DefaultModelId:       defaultModelId,
			DefaultThinkingLevel: defaultThinking,
			ModelThinkingLevels:  services.SettingsManager.GetAllModelThinkingLevels(),
			ModelRuntime:         services.ModelRuntime,
		})
		if initErr != nil {
			return nil, initErr
		}
		model = initial.Model
		if thinkingLevel == "" && initial.ThinkingLevel != "" {
			thinkingLevel = initial.ThinkingLevel
		}
		modelFallbackMessage = initial.FallbackMessage
	}

	if thinkingLevel == "" {
		if hasExistingSession && existingContext.ThinkingLevel != "" {
			thinkingLevel = agent.ThinkingLevel(existingContext.ThinkingLevel)
		} else if defThinking := services.SettingsManager.GetDefaultThinkingLevel(); defThinking != nil {
			thinkingLevel = *defThinking
		} else {
			thinkingLevel = DefaultThinkingLevel
		}
	}
	if model == nil || !model.Reasoning {
		thinkingLevel = agent.ThinkingLevelOff
	}

	var extensionRunnerRef ExtensionRunnerRef
	var ag *agent.Agent
	cacheWarmer := sdkCreateCacheWarmer(services.SettingsManager, services.ModelRuntime, sessionManager, &extensionRunnerRef)
	runtimeStreamFn := sdkCreateRuntimeStreamFn(
		services.ModelRuntime,
		services.SettingsManager,
		sessionManager,
		cacheWarmer,
		func() *agent.Agent { return ag },
		&extensionRunnerRef,
	)
	MarkRuntimeStreamFunction(runtimeStreamFn, services.ModelRuntime)

	var transportPtr *ai.Transport
	if t := services.SettingsManager.GetTransport(); t != "" {
		tt := ai.Transport(t)
		transportPtr = &tt
	}
	var budgetsPtr *ai.ThinkingBudgets
	if b := services.SettingsManager.GetThinkingBudgets(); b != nil {
		budgetsPtr = b
	}

	sessionId := sessionManager.GetSessionId()
	ag = agent.NewAgent(&agent.AgentOptions{
		InitialState: &agent.AgentInitialState{
			Model:         model,
			ThinkingLevel: thinkingLevel,
		},
		SteeringMode:    agent.QueueMode(services.SettingsManager.GetSteeringMode()),
		FollowUpMode:    agent.QueueMode(services.SettingsManager.GetFollowUpMode()),
		Transport:       transportPtr,
		ThinkingBudgets: budgetsPtr,
		ConvertToLlm: func(_ context.Context, messages []agent.AgentMessage) ([]ai.Message, error) {
			return ConvertToLlm(messages), nil
		},
		TransformContext: func(reqCtx context.Context, messages []agent.AgentMessage) ([]agent.AgentMessage, error) {
			if runner := extensionRunnerRef.currentOrNil(); runner != nil {
				return runner.EmitContext(reqCtx, messages), nil
			}
			return messages, nil
		},
		OnPayload: func(payload any, _ *ai.Model) (any, error) {
			if runner := extensionRunnerRef.currentOrNil(); runner != nil {
				return runner.EmitBeforeProviderRequest(context.Background(), payload), nil
			}
			return payload, nil
		},
		OnResponse: func(response *ai.ProviderResponse, _ *ai.Model) error {
			if response == nil {
				return nil
			}
			if runner := extensionRunnerRef.currentOrNil(); runner != nil && runner.HasHandlers("after_provider_response") {
				_ = runner.Emit(context.Background(), AfterProviderResponseEvent{
					Type:    "after_provider_response",
					Status:  response.Status,
					Headers: response.Headers,
				})
			}
			return nil
		},
		StreamFn:  runtimeStreamFn,
		SessionId: &sessionId,
		GetApiKey: func(ctx context.Context, provider string) (string, error) {
			if agModel := sdkAgModelFromRuntime(services.ModelRuntime, model, provider); agModel != nil {
				authRes, aErr := services.ModelRuntime.GetAuthModel(ctx, agModel, nil)
				if aErr == nil && authRes != nil && authRes.Auth.ApiKey != nil {
					return *authRes.Auth.ApiKey, nil
				}
			}
			return "", nil
		},
	})
	if len(existingContext.Messages) > 0 {
		ag.ReplaceMessages(existingContext.Messages)
	}

	session := NewAgentSession(AgentSessionConfig{
		Agent:                  ag,
		SessionManager:         sessionManager,
		SettingsManager:        services.SettingsManager,
		Cwd:                    sessionCwd,
		ScopedModels:           opts.ScopedModels,
		ResourceLoader:         services.ResourceLoader,
		CustomTools:            opts.CustomTools,
		ModelRuntime:           services.ModelRuntime,
		CacheWarmer:            cacheWarmer,
		InitialActiveToolNames: toolSelection.initialActiveNames,
		UsesDefaultTools:       toolSelection.usesDefaultTools,
		DefaultToolModifiers:   toolSelection.defaultToolModifiers,
		AllowedToolNames:       toolSelection.allowedToolNames,
		ExcludedToolNames:      toolSelection.excludedToolNames,
		SuppressBuiltinTools:   toolSelection.suppressBuiltinTools,
		BaseToolsOverride:      baseOverride,
		ExtensionRunnerRef:     &extensionRunnerRef,
		SessionStartEvent:      opts.SessionStartEvent,
		FlagValues:             opts.FlagValues,
	})

	extResult := services.ResourceLoader.GetExtensions()

	return &CreateAgentSessionResult{
		Session:              session,
		ExtensionsResult:     &extResult,
		ModelFallbackMessage: modelFallbackMessage,
		Diagnostics:          services.Diagnostics,
	}, nil
}

func sdkSameAgentMessage(a, b agent.AgentMessage) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	ta := reflect.TypeOf(a)
	tb := reflect.TypeOf(b)
	if ta != tb {
		return false
	}
	if ta.Comparable() {
		return a == b
	}
	return reflect.DeepEqual(a, b)
}

func sdkCacheContextIsCurrent(getAgent func() *agent.Agent, requestModel *ai.Model) func() bool {
	if getAgent == nil || requestModel == nil {
		return func() bool { return false }
	}
	ag := getAgent()
	if ag == nil {
		return func() bool { return false }
	}
	prefix := ag.Messages()
	reqProvider := requestModel.Provider
	reqModelId := requestModel.Id
	return func() bool {
		curAg := getAgent()
		if curAg == nil {
			return false
		}
		currentModel := curAg.Model()
		if currentModel == nil || currentModel.Provider != reqProvider || currentModel.Id != reqModelId {
			return false
		}
		currentMessages := curAg.Messages()
		if len(prefix) > len(currentMessages) {
			return false
		}
		for i, message := range prefix {
			if !sdkSameAgentMessage(currentMessages[i], message) {
				return false
			}
		}
		return true
	}
}

func sdkCreateRuntimeStreamFn(
	modelRuntime *ModelRuntime,
	settingsManager *SettingsManager,
	sessionManager *SessionManager,
	cacheWarmer *CacheWarmer,
	getAgent func() *agent.Agent,
	extensionRunnerRef *ExtensionRunnerRef,
) agent.StreamFn {
	return func(ctx context.Context, reqModel *ai.Model, transcript *ai.TranscriptContext, options *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
		var base ai.SimpleStreamOptions
		if options != nil {
			base = *options
		}
		isSessionRequest := cacheWarmer != nil && sessionManager != nil &&
			options != nil && options.SessionId != nil && *options.SessionId == sessionManager.GetSessionId()

		if base.TimeoutMs == nil {
			if idleTimeout, err := settingsManager.GetHttpIdleTimeoutMs(); err == nil {
				base.TimeoutMs = &idleTimeout
			}
		}
		if base.WebsocketConnectTimeoutMs == nil {
			if wsTimeout, err := settingsManager.GetWebSocketConnectTimeoutMs(); err == nil && wsTimeout != nil {
				base.WebsocketConnectTimeoutMs = wsTimeout
			}
		}

		retrySettings := settingsManager.GetProviderRetrySettings()
		if base.MaxRetries == nil && retrySettings.MaxRetries != nil {
			mr := *retrySettings.MaxRetries
			base.MaxRetries = &mr
		}
		if base.MaxRetryDelayMs == nil && retrySettings.MaxRetryDelayMs > 0 {
			mrd := int64(retrySettings.MaxRetryDelayMs)
			base.MaxRetryDelayMs = &mrd
		}

		ci := GetClientIdentity(settingsManager)
		if base.Identity == nil {
			base.Identity = &ci
		} else {
			base.Identity = ai.MergeClientIdentity(&ci, base.Identity)
		}

		if base.SessionId == nil && sessionManager != nil {
			sid := sessionManager.GetSessionId()
			base.SessionId = &sid
		}

		if runner := extensionRunnerRef.currentOrNil(); runner != nil && runner.HasHandlers("provider_stream_event") {
			origCallback := base.OnProviderStreamEvent
			base.OnProviderStreamEvent = func(data any, m *ai.Model) error {
				if origCallback != nil {
					if err := origCallback(data, m); err != nil {
						return err
					}
				}
				if m != nil {
					_ = runner.Emit(ctx, ProviderStreamEvent{
						Type:     "provider_stream_event",
						Provider: m.Provider,
						Api:      ai.Api(m.Api),
						Model:    m.Id,
						Data:     data,
					})
				}
				return nil
			}
		}

		modelOpts := &ai.ModelsSimpleStreamOptions{
			SimpleStreamOptions: base,
		}
		if runner := extensionRunnerRef.currentOrNil(); runner != nil && runner.HasHandlers("before_provider_headers") {
			modelOpts.TransformHeaders = func(headers ai.ProviderHeaders) (ai.ProviderHeaders, error) {
				return runner.EmitBeforeProviderHeaders(ctx, headers), nil
			}
		}

		var messages []ai.Message
		if transcript != nil {
			messages = transcript.Messages
		}
		reqCtx := &ai.Context{Messages: messages}
		if isSessionRequest {
			cacheWarmer.Start(
				CacheWarmRequest{
					Model:   reqModel,
					Context: reqCtx,
					Options: modelOpts,
				},
				sdkCacheContextIsCurrent(getAgent, reqModel),
			)
		}
		return modelRuntime.StreamSimple(ctx, reqModel, reqCtx, modelOpts), nil
	}
}

func sdkCreateCacheWarmer(
	settingsManager *SettingsManager,
	modelRuntime *ModelRuntime,
	sessionManager *SessionManager,
	extensionRunnerRef *ExtensionRunnerRef,
) *CacheWarmer {
	return NewCacheWarmer(
		modelRuntime,
		sessionManager,
		settingsManager.GetCacheWarmingMode,
		func(ctx context.Context, event CacheWarmingDecisionEvent) (ai.CacheWarmingAction, error) {
			runner := extensionRunnerRef.currentOrNil()
			if runner == nil || !runner.HasHandlers("cache_warming_decision") {
				return "", nil
			}
			action := runner.EmitCacheWarmingDecision(ctx, event)
			return action, nil
		},
	)
}

func sdkResolveToolSelection(settingsManager *SettingsManager, opts *CreateAgentSessionOptions) (sdkToolSelection, map[string]*agent.AgentTool, error) {
	var sel sdkToolSelection
	baseOverride := opts.BaseToolsOverride

	// Explicit *agent.AgentTool slice overrides built-in tool registry unless BaseToolsOverride was also set.
	if opts.Tools != nil {
		if baseOverride == nil {
			baseOverride = make(map[string]*agent.AgentTool, len(opts.Tools))
		}
		names := make([]string, 0, len(opts.Tools))
		for _, t := range opts.Tools {
			if t == nil {
				continue
			}
			baseOverride[t.Name] = t
			names = append(names, t.Name)
		}
		if opts.InitialActiveToolNames != nil {
			sel.initialActiveNames = append([]string(nil), opts.InitialActiveToolNames...)
		} else {
			sel.initialActiveNames = names
		}
		sel.excludedToolNames = append([]string(nil), opts.ExcludeTools...)
		return sel, baseOverride, nil
	}

	if err := sdkValidateToolList(opts.ToolNames); err != nil {
		return sel, nil, fmt.Errorf("Invalid tools option: %s", err.Error())
	}

	hasModifiers := len(opts.ToolNames) > 0 && sdkIsToolModifier(opts.ToolNames[0])
	hasAllowlist := len(opts.ToolNames) > 0 && !hasModifiers

	if opts.NoTools == NoToolsAll {
		if hasAllowlist {
			sel.allowedToolNames = append([]string(nil), opts.ToolNames...)
			sel.initialActiveNames = []string{}
		} else if hasModifiers {
			added := sdkApplyToolModifiers([]string{}, opts.ToolNames)
			sel.allowedToolNames = append([]string(nil), added...)
			sel.initialActiveNames = []string{}
		} else {
			sel.allowedToolNames = []string{}
			sel.initialActiveNames = []string{}
		}
		sel.excludedToolNames = append([]string(nil), opts.ExcludeTools...)
		return sel, baseOverride, nil
	}

	if opts.NoTools == NoToolsBuiltin {
		sel.suppressBuiltinTools = true
	}

	defaultTools := settingsManager.GetDefaultTools()
	baseDefaults := sdkDefaultToolNames()
	if len(defaultTools) > 0 {
		baseDefaults = sdkResolveDefaultTools(defaultTools)
	}

	switch {
	case opts.InitialActiveToolNames != nil:
		sel.initialActiveNames = append([]string(nil), opts.InitialActiveToolNames...)
	case hasAllowlist:
		sel.allowedToolNames = append([]string(nil), opts.ToolNames...)
		sel.initialActiveNames = []string{}
	case hasModifiers:
		sel.usesDefaultTools = true
		sel.defaultToolModifiers = append([]string(nil), opts.ToolNames...)
		sel.initialActiveNames = sdkApplyToolModifiers(baseDefaults, opts.ToolNames)
	case opts.NoTools == NoToolsBuiltin:
		sel.initialActiveNames = []string{}
	default:
		sel.usesDefaultTools = true
		sel.initialActiveNames = baseDefaults
	}

	sel.excludedToolNames = append([]string(nil), opts.ExcludeTools...)
	return sel, baseOverride, nil
}

func sdkDefaultToolNames() []string {
	return []string{"read", "bash", "edit", "write"}
}

func sdkIsToolModifier(entry string) bool {
	return strings.HasPrefix(entry, "+") || strings.HasPrefix(entry, "-")
}

func sdkValidateToolList(entries []string) error {
	if len(entries) == 0 {
		return nil
	}
	var modifiers []string
	for _, entry := range entries {
		if sdkIsToolModifier(entry) {
			modifiers = append(modifiers, entry)
		}
	}
	if len(modifiers) == 0 {
		return nil
	}
	if len(modifiers) < len(entries) {
		return fmt.Errorf("tool names cannot be mixed with +name or -name entries")
	}
	for _, entry := range modifiers {
		if strings.Contains(entry, "*") {
			return fmt.Errorf("+name and -name entries take exact tool names, not patterns: %s", entry)
		}
	}
	return nil
}

func sdkApplyToolModifiers(base []string, entries []string) []string {
	out := append([]string(nil), base...)
	for _, entry := range entries {
		if !sdkIsToolModifier(entry) {
			continue
		}
		name := entry[1:]
		if name == "" {
			continue
		}
		idx := -1
		for i, existing := range out {
			if existing == name {
				idx = i
				break
			}
		}
		if strings.HasPrefix(entry, "+") && idx == -1 {
			out = append(out, name)
		} else if strings.HasPrefix(entry, "-") && idx != -1 {
			out = append(out[:idx], out[idx+1:]...)
		}
	}
	return out
}

func sdkResolveDefaultTools(entries []string) []string {
	allModifiers := len(entries) > 0
	for _, entry := range entries {
		if !sdkIsToolModifier(entry) {
			allModifiers = false
			break
		}
	}
	if allModifiers {
		return sdkApplyToolModifiers(sdkDefaultToolNames(), entries)
	}
	var plain []string
	var mods []string
	for _, entry := range entries {
		if sdkIsToolModifier(entry) {
			mods = append(mods, entry)
		} else {
			plain = append(plain, entry)
		}
	}
	if len(plain) == 0 {
		return sdkDefaultToolNames()
	}
	return sdkApplyToolModifiers(plain, mods)
}

func sdkAgModelFromRuntime(mr *ModelRuntime, current *ai.Model, provider string) *ai.Model {
	if current != nil && current.Provider == ai.ProviderId(provider) {
		return current
	}
	if mr != nil {
		for _, anyM := range mr.GetAllModels(nil) {
			if chatM, ok := anyM.(*ai.Model); ok && chatM != nil && chatM.Provider == ai.ProviderId(provider) {
				return chatM
			}
		}
	}
	return nil
}

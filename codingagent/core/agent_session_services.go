package core

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/utils"
)

// Diagnostic severity levels for AgentSessionRuntimeDiagnostic.
const (
	AgentSessionRuntimeDiagnosticInfo    = "info"
	AgentSessionRuntimeDiagnosticWarning = "warning"
	AgentSessionRuntimeDiagnosticError   = "error"
)

// AgentSessionRuntimeDiagnostic reports a non-fatal warning or error discovered
// when initializing runtime services or reloading resources.
type AgentSessionRuntimeDiagnostic struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// CreateAgentSessionServicesOptions configures shared services for one or more
// AgentSession instances created under the same runtime.
type CreateAgentSessionServicesOptions struct {
	Cwd                           string
	AgentDir                      string
	AuthStorage                   *AuthStorage
	SettingsManager               *SettingsManager
	ModelRegistry                 *ModelRegistry
	ModelRuntime                  *ModelRuntime
	ResourceLoader                ResourceLoader
	ResourceLoaderOptions         *DefaultResourceLoaderOptions
	FlagValues                    map[string]any
	EventBus                      EventBus
	ClientIdentity                *ai.ClientIdentity
	HttpClient                    *http.Client
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
}

// AgentSessionServices holds the long-lived registries, stores, loaders, and
// startup diagnostics shared across session switches within a runtime.
type AgentSessionServices struct {
	Cwd             string
	AgentDir        string
	AuthStorage     *AuthStorage
	SettingsManager *SettingsManager
	ModelRegistry   *ModelRegistry
	ModelRuntime    *ModelRuntime
	ResourceLoader  ResourceLoader
	EventBus        EventBus
	FlagValues      map[string]any
	Diagnostics     []AgentSessionRuntimeDiagnostic
}

// CreateAgentSessionFromServicesOptions configures an AgentSession created from
// an initialized AgentSessionServices bundle.
type CreateAgentSessionFromServicesOptions struct {
	Services               *AgentSessionServices
	SessionManager         *SessionManager
	SessionStartEvent      *SessionStartEvent
	Model                  *ai.Model
	ThinkingLevel          agent.ThinkingLevel
	ScopedModels           []ScopedModel
	Tools                  []*agent.AgentTool
	ToolNames              []string
	ExcludeTools           []string
	NoTools                NoToolsOption
	CustomTools            []ToolDefinition
	FlagValues             map[string]any
	InitialActiveToolNames []string
	BaseToolsOverride      map[string]*agent.AgentTool
}

// Settle flushes any pending asynchronous credential writes held by the services.
func (s *AgentSessionServices) Settle(ctx context.Context) error {
	if s == nil || s.ModelRuntime == nil {
		return nil
	}
	return s.ModelRuntime.SettleCredentialWrites(ctx)
}

// CreateAgentSessionServices initializes SettingsManager, AuthStorage, ModelRuntime,
// ModelRegistry, ResourceLoader, EventBus, and collects startup diagnostics.
func CreateAgentSessionServices(ctx context.Context, options ...*CreateAgentSessionServicesOptions) (*AgentSessionServices, error) {
	var opts CreateAgentSessionServicesOptions
	if len(options) > 0 && options[0] != nil {
		opts = *options[0]
	}

	cwd := opts.Cwd
	if strings.TrimSpace(cwd) == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		cwd = wd
	} else {
		cwd = assResolvePath(cwd)
	}

	agentDir := opts.AgentDir
	if strings.TrimSpace(agentDir) == "" {
		agentDir = utils.GetAgentDir()
	} else {
		agentDir = assResolvePath(agentDir)
	}

	settingsManager := opts.SettingsManager
	if settingsManager == nil {
		settingsManager = SettingsManagerCreate(cwd, agentDir)
	}

	if timeoutMs, err := settingsManager.GetHttpIdleTimeoutMs(); err == nil {
		ConfigureHttpDispatcher(timeoutMs)
	}

	diagnostics := append([]AgentSessionRuntimeDiagnostic(nil), CollectSettingsDiagnostics(settingsManager)...)

	authStorage := opts.AuthStorage
	if authStorage == nil {
		authStorage = AuthStorageCreate(filepath.Join(agentDir, "auth.json"))
	}

	eventBus := opts.EventBus
	if eventBus == nil {
		eventBus = CreateEventBus()
	}

	clientIdentity := opts.ClientIdentity
	if clientIdentity == nil {
		id := GetClientIdentity(settingsManager)
		clientIdentity = &id
	}

	modelRuntime := opts.ModelRuntime
	modelRegistry := opts.ModelRegistry
	if modelRuntime == nil && modelRegistry != nil {
		modelRuntime = modelRegistry.GetRuntime()
	}
	if modelRuntime == nil {
		modelsPath := filepath.Join(agentDir, "models.json")
		createdRuntime, err := CreateModelRuntime(ctx, &CreateModelRuntimeOptions{
			Credentials: authStorage,
			ModelsPath:  &modelsPath,
			Identity:    clientIdentity,
		})
		if err != nil {
			return nil, err
		}
		modelRuntime = createdRuntime
	}
	if modelRegistry == nil {
		modelRegistry = NewModelRegistry(modelRuntime)
	}
	if errMsg := modelRuntime.GetError(); errMsg != nil && *errMsg != "" {
		diagnostics = append(diagnostics, AgentSessionRuntimeDiagnostic{
			Type:    AgentSessionRuntimeDiagnosticError,
			Message: *errMsg,
		})
	}

	flagValues := assCloneFlagValues(opts.FlagValues)

	resourceLoader := opts.ResourceLoader
	if resourceLoader == nil {
		rlOpts := assMergeResourceLoaderOptions(cwd, agentDir, settingsManager, eventBus, &opts)
		loader := NewDefaultResourceLoader(rlOpts)
		if err := loader.Reload(ctx); err != nil {
			return nil, err
		}
		resourceLoader = loader
	}

	extResult := resourceLoader.GetExtensions()
	assApplyExtensionsToRuntime(ctx, &extResult, flagValues, modelRuntime, &diagnostics)

	diagnostics = append(diagnostics, assCollectResourceDiagnostics(resourceLoader)...)
	diagnostics = DeduplicateDiagnostics(diagnostics)

	return &AgentSessionServices{
		Cwd:             cwd,
		AgentDir:        agentDir,
		AuthStorage:     authStorage,
		SettingsManager: settingsManager,
		ModelRegistry:   modelRegistry,
		ModelRuntime:    modelRuntime,
		ResourceLoader:  resourceLoader,
		EventBus:        eventBus,
		FlagValues:      flagValues,
		Diagnostics:     diagnostics,
	}, nil
}

// CreateAgentSessionFromServices creates an AgentSession using pre-initialized services.
func CreateAgentSessionFromServices(ctx context.Context, options CreateAgentSessionFromServicesOptions) (*CreateAgentSessionResult, error) {
	if options.Services == nil {
		return nil, fmt.Errorf("services are required to create an agent session")
	}
	services := options.Services
	flagValues := assMergeFlagValues(services.FlagValues, options.FlagValues)

	res, err := CreateAgentSession(ctx, &CreateAgentSessionOptions{
		Cwd:                    services.Cwd,
		AgentDir:               services.AgentDir,
		AuthStorage:            services.AuthStorage,
		ModelRegistry:          services.ModelRegistry,
		ModelRuntime:           services.ModelRuntime,
		SettingsManager:        services.SettingsManager,
		ResourceLoader:         services.ResourceLoader,
		SessionManager:         options.SessionManager,
		SessionStartEvent:      options.SessionStartEvent,
		Model:                  options.Model,
		ThinkingLevel:          options.ThinkingLevel,
		ScopedModels:           options.ScopedModels,
		Tools:                  options.Tools,
		ToolNames:              options.ToolNames,
		ExcludeTools:           options.ExcludeTools,
		NoTools:                options.NoTools,
		CustomTools:            options.CustomTools,
		FlagValues:             flagValues,
		EventBus:               services.EventBus,
		InitialActiveToolNames: options.InitialActiveToolNames,
		BaseToolsOverride:      options.BaseToolsOverride,
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

func assMergeResourceLoaderOptions(
	cwd string,
	agentDir string,
	settingsManager *SettingsManager,
	eventBus EventBus,
	opts *CreateAgentSessionServicesOptions,
) DefaultResourceLoaderOptions {
	var rlOpts DefaultResourceLoaderOptions
	if opts.ResourceLoaderOptions != nil {
		rlOpts = *opts.ResourceLoaderOptions
	}
	rlOpts.Cwd = cwd
	rlOpts.AgentDir = agentDir
	if rlOpts.SettingsManager == nil {
		rlOpts.SettingsManager = settingsManager
	}
	if rlOpts.EventBus == nil {
		rlOpts.EventBus = eventBus
	}
	if len(opts.AdditionalExtensionPaths) > 0 {
		rlOpts.AdditionalExtensionPaths = append([]string(nil), opts.AdditionalExtensionPaths...)
	}
	if len(opts.AdditionalSkillPaths) > 0 {
		rlOpts.AdditionalSkillPaths = append([]string(nil), opts.AdditionalSkillPaths...)
	}
	if len(opts.AdditionalPromptTemplatePaths) > 0 {
		rlOpts.AdditionalPromptTemplatePaths = append([]string(nil), opts.AdditionalPromptTemplatePaths...)
	}
	if len(opts.AdditionalThemePaths) > 0 {
		rlOpts.AdditionalThemePaths = append([]string(nil), opts.AdditionalThemePaths...)
	}
	if opts.NoExtensions {
		rlOpts.NoExtensions = true
	}
	if len(opts.DisabledBuiltinExtensions) > 0 {
		rlOpts.DisabledBuiltinExtensions = append([]string(nil), opts.DisabledBuiltinExtensions...)
	}
	if opts.NoSkills {
		rlOpts.NoSkills = true
	}
	if opts.NoPromptTemplates {
		rlOpts.NoPromptTemplates = true
	}
	if opts.NoThemes {
		rlOpts.NoThemes = true
	}
	if opts.NoContextFiles {
		rlOpts.NoContextFiles = true
	}
	if opts.SystemPrompt != nil {
		rlOpts.SystemPrompt = opts.SystemPrompt
	}
	if len(opts.AppendSystemPrompt) > 0 {
		rlOpts.AppendSystemPrompt = append([]string(nil), opts.AppendSystemPrompt...)
	}
	return rlOpts
}

func assApplyExtensionsToRuntime(
	ctx context.Context,
	extResult *LoadExtensionsResult,
	flagValues map[string]any,
	modelRuntime *ModelRuntime,
	diagnostics *[]AgentSessionRuntimeDiagnostic,
) {
	if extResult == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for _, extErr := range extResult.Errors {
		*diagnostics = append(*diagnostics, AgentSessionRuntimeDiagnostic{
			Type:    AgentSessionRuntimeDiagnosticError,
			Message: fmt.Sprintf("Failed to load extension %q: %s", extErr.Path, extErr.Error),
		})
	}
	for _, extWarn := range extResult.Warnings {
		*diagnostics = append(*diagnostics, AgentSessionRuntimeDiagnostic{
			Type:    AgentSessionRuntimeDiagnosticWarning,
			Message: fmt.Sprintf("Extension %q: %s", extWarn.Path, extWarn.Warning),
		})
	}

	if extResult.Runtime == nil {
		return
	}
	if extResult.Runtime.FlagValues == nil {
		extResult.Runtime.FlagValues = make(map[string]any)
	}
	registeredFlags := make(map[string]ExtensionFlag)
	for _, ext := range extResult.Extensions {
		for _, flag := range ext.Flags {
			registeredFlags[flag.Name] = flag
			if _, exists := extResult.Runtime.FlagValues[flag.Name]; !exists && flag.Default != nil {
				extResult.Runtime.FlagValues[flag.Name] = flag.Default
			}
		}
	}
	if len(flagValues) > 0 {
		flagNames := make([]string, 0, len(flagValues))
		for name := range flagValues {
			flagNames = append(flagNames, name)
		}
		sort.Strings(flagNames)
		for _, name := range flagNames {
			val := flagValues[name]
			flag, known := registeredFlags[name]
			if !known {
				*diagnostics = append(*diagnostics, AgentSessionRuntimeDiagnostic{
					Type:    AgentSessionRuntimeDiagnosticError,
					Message: fmt.Sprintf("Unknown option --%s. Use --help for available options.", name),
				})
				continue
			}
			if flag.Type == "string" {
				if b, isBool := val.(bool); isBool && b {
					*diagnostics = append(*diagnostics, AgentSessionRuntimeDiagnostic{
						Type:    AgentSessionRuntimeDiagnosticError,
						Message: fmt.Sprintf("Option --%s requires a value.", name),
					})
					continue
				}
			}
			extResult.Runtime.FlagValues[name] = val
		}
	}

	if modelRuntime == nil {
		return
	}
	appliedAny := false
	for _, reg := range extResult.Runtime.PendingProviderRegistrations {
		if err := modelRuntime.RegisterProvider(reg.Name, reg.Config); err != nil {
			*diagnostics = append(*diagnostics, AgentSessionRuntimeDiagnostic{
				Type:    AgentSessionRuntimeDiagnosticError,
				Message: fmt.Sprintf("Extension %q failed to register provider %q: %s", reg.ExtensionPath, reg.Name, err.Error()),
			})
			continue
		}
		appliedAny = true
	}
	for _, reg := range extResult.Runtime.PendingNativeProviderRegistrations {
		if err := modelRuntime.RegisterNativeProvider(reg.Provider); err != nil {
			provID := ""
			if reg.Provider != nil {
				provID = reg.Provider.Id
			}
			*diagnostics = append(*diagnostics, AgentSessionRuntimeDiagnostic{
				Type:    AgentSessionRuntimeDiagnosticError,
				Message: fmt.Sprintf("Extension %q failed to register provider %q: %s", reg.ExtensionPath, provID, err.Error()),
			})
			continue
		}
		appliedAny = true
	}
	for _, vreg := range extResult.Runtime.PendingVirtualModelRegistrations {
		if err := modelRuntime.RegisterVirtualModel(vreg.Definition); err != nil {
			*diagnostics = append(*diagnostics, AgentSessionRuntimeDiagnostic{
				Type:    AgentSessionRuntimeDiagnosticError,
				Message: fmt.Sprintf("Extension %q failed to register virtual model %s/%s: %s", vreg.ExtensionPath, vreg.Definition.Provider, vreg.Definition.Id, err.Error()),
			})
			continue
		}
		appliedAny = true
	}
	extResult.Runtime.PendingProviderRegistrations = nil
	extResult.Runtime.PendingNativeProviderRegistrations = nil
	extResult.Runtime.PendingVirtualModelRegistrations = nil
	if appliedAny {
		allowNet := false
		_, _ = modelRuntime.Refresh(ctx, &ai.ModelsRefreshOptions{AllowNetwork: &allowNet})
	}
}

func assCollectResourceDiagnostics(loader ResourceLoader) []AgentSessionRuntimeDiagnostic {
	var diagnostics []AgentSessionRuntimeDiagnostic
	if loader == nil {
		return diagnostics
	}
	appendDiags := func(items []ResourceDiagnostic) {
		for _, d := range items {
			typ := AgentSessionRuntimeDiagnosticWarning
			if string(d.Type) == AgentSessionRuntimeDiagnosticError {
				typ = AgentSessionRuntimeDiagnosticError
			} else if string(d.Type) == AgentSessionRuntimeDiagnosticInfo {
				typ = AgentSessionRuntimeDiagnosticInfo
			}
			diagnostics = append(diagnostics, AgentSessionRuntimeDiagnostic{
				Type:    typ,
				Message: d.Message,
			})
		}
	}
	appendDiags(loader.GetSkills().Diagnostics)
	appendDiags(loader.GetPrompts().Diagnostics)
	appendDiags(loader.GetThemes().Diagnostics)
	return diagnostics
}

func assCloneFlagValues(src map[string]any) map[string]any {
	if len(src) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func assMergeFlagValues(base map[string]any, override map[string]any) map[string]any {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	out := assCloneFlagValues(base)
	for k, v := range override {
		out[k] = v
	}
	return out
}

func assResolvePath(p string) string {
	resolved, err := utils.ResolvePath(p, "", nil)
	if err == nil && resolved != "" {
		return resolved
	}
	if abs, absErr := filepath.Abs(p); absErr == nil {
		return abs
	}
	return p
}

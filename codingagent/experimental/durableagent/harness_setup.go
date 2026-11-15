// Ported from packages/coding-agent/src/experimental/durable/harness-setup.ts (pi v1.0.0).

package durableagent

import (
	"context"
	"fmt"
	"strings"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/env"
	"github.com/keejkrej/pi-go/durable/tools"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// HarnessModelConfig captures resolved model configuration for a durable agent harness.
type HarnessModelConfig struct {
	Model         *ai.Model
	ThinkingLevel ai.ModelThinkingLevel
	ModelRef      durable.ModelRef
	FallbackNote  *string
}

// HarnessSetupOptions configures OpenDurableHarness.
type HarnessSetupOptions struct {
	Cwd               string
	AgentDir          string
	Storage           durable.Storage
	Provider          *string
	Model             *string
	Thinking          *agent.ThinkingLevel
	Instructions      *string
	AllowModelNetwork bool
	Extensions        []*durable.Extension
	AdditionalTools   []string
	ShellPath         *string
	OnReport          func(error)
}

// DurableHarnessBundle holds initialized runtime components for durableagent.
type DurableHarnessBundle struct {
	Cwd             string
	AgentDir        string
	SettingsManager *core.SettingsManager
	ModelRuntime    *core.ModelRuntime
	Registry        durable.Registry
	Harness         durable.Harness
	Root            durable.Conversation
	ModelConfig     HarnessModelConfig
}

// Close closes the underlying harness and model runtime cleanly.
func (b *DurableHarnessBundle) Close(ctx chord.Context) error {
	var firstErr error
	if b.Harness != nil {
		if err := b.Harness.Close(ctx); err != nil {
			firstErr = err
		}
	}
	if b.ModelRuntime != nil {
		b.ModelRuntime.Close()
	}
	return firstErr
}

// ApplyHttpSettings configures HTTP proxy and idle dispatcher timeouts from settings.
func ApplyHttpSettings(settingsManager *core.SettingsManager) (int64, error) {
	if settingsManager == nil {
		return core.DefaultHttpIdleTimeoutMs, core.ConfigureHttpDispatcher(core.DefaultHttpIdleTimeoutMs)
	}
	settings := settingsManager.GetSettings()
	core.ApplyHttpProxySettings(settings.HttpProxy)
	timeoutMs, err := settingsManager.GetHttpIdleTimeoutMs()
	if err != nil {
		return 0, err
	}
	if err := core.ConfigureHttpDispatcher(timeoutMs); err != nil {
		return 0, err
	}
	return timeoutMs, nil
}

// ResolveHarnessModel selects the initial model and thinking level using CLI flags, scoped models, or defaults.
func ResolveHarnessModel(
	ctx context.Context,
	modelRuntime *core.ModelRuntime,
	settingsManager *core.SettingsManager,
	cliProvider *string,
	cliModel *string,
	cliThinking *agent.ThinkingLevel,
) (HarnessModelConfig, error) {
	if modelRuntime == nil {
		return HarnessModelConfig{}, fmt.Errorf("model runtime is required")
	}

	var chosen *ai.Model
	var thinking agent.ThinkingLevel
	var fallbackNote *string

	hasCliModel := cliModel != nil && strings.TrimSpace(*cliModel) != ""
	hasCliProvider := cliProvider != nil && strings.TrimSpace(*cliProvider) != ""
	if hasCliModel || hasCliProvider {
		res := core.ResolveCliModel(core.ResolveCliModelOptions{
			CliProvider:  cliProvider,
			CliModel:     cliModel,
			CliThinking:  cliThinking,
			InlineApiKey: false,
			ModelRuntime: modelRuntime,
		})
		if res.Error != nil {
			return HarnessModelConfig{}, fmt.Errorf("%s", *res.Error)
		}
		chosen = res.Model
		if res.ThinkingLevel != nil {
			thinking = *res.ThinkingLevel
		}
		if res.Warning != nil {
			fallbackNote = res.Warning
		}
	}

	if chosen == nil {
		var scopedModels []core.ScopedModel
		if settingsManager != nil {
			enabled := settingsManager.GetEnabledModels()
			if len(enabled) > 0 {
				scopedRes := core.ResolveModelScopeFromModels(enabled, modelRuntime.GetModels(nil))
				scopedModels = scopedRes.ScopedModels
			}
		}

		var defProvider, defModel *string
		var defThinking *agent.ThinkingLevel
		var modelThinking map[string]agent.ThinkingLevel
		if settingsManager != nil {
			defProvider = settingsManager.GetDefaultProvider()
			defModel = settingsManager.GetDefaultModel()
			defThinking = settingsManager.GetDefaultThinkingLevel()
			modelThinking = settingsManager.GetAllModelThinkingLevels()
		}

		initial, err := core.FindInitialModel(ctx, core.FindInitialModelOptions{
			ScopedModels:         scopedModels,
			IsContinuing:         false,
			DefaultProvider:      defProvider,
			DefaultModelId:       defModel,
			DefaultThinkingLevel: defThinking,
			ModelThinkingLevels:  modelThinking,
			ModelRuntime:         modelRuntime,
		})
		if err != nil {
			return HarnessModelConfig{}, err
		}
		chosen = initial.Model
		if thinking == "" {
			thinking = initial.ThinkingLevel
		}
		if fallbackNote == nil {
			fallbackNote = initial.FallbackMessage
		}
	}

	if chosen == nil {
		return HarnessModelConfig{}, fmt.Errorf("no models available; configure provider credentials or specify --provider/--model")
	}

	if cliThinking != nil && *cliThinking != "" {
		thinking = *cliThinking
	}
	if thinking == "" {
		if settingsManager != nil {
			if mt := settingsManager.GetModelThinkingLevel(string(chosen.Provider), chosen.Id); mt != nil {
				thinking = *mt
			} else if dt := settingsManager.GetDefaultThinkingLevel(); dt != nil {
				thinking = *dt
			}
		}
		if thinking == "" {
			thinking = ai.ModelThinkingLevelMedium
		}
	}

	durableThinking := hsToModelThinkingLevel(thinking, chosen)
	return HarnessModelConfig{
		Model:         chosen,
		ThinkingLevel: durableThinking,
		ModelRef: durable.ModelRef{
			Provider: string(chosen.Provider),
			ModelId:  chosen.Id,
		},
		FallbackNote: fallbackNote,
	}, nil
}

// BuildHarnessSettings translates SettingsManager settings into durable.HarnessSettings.
func BuildHarnessSettings(settingsManager *core.SettingsManager, extensions []*durable.Extension) (durable.HarnessSettings, error) {
	hsSettings := durable.HarnessSettings{
		Extensions:    extensions,
		HasExtensions: true,
		ToolExecution: durable.ToolExecutionModeParallel,
	}
	if settingsManager == nil {
		return hsSettings, nil
	}

	comp, err := settingsManager.GetCompactionSettings()
	if err != nil {
		return durable.HarnessSettings{}, err
	}
	retry := settingsManager.GetRetrySettings()
	steering := hsQueueMode(settingsManager.GetSteeringMode())
	followUp := hsQueueMode(settingsManager.GetFollowUpMode())

	baseDelayMs := int64(retry.BaseDelayMs)
	maxDelayMs := int64(retry.MaxAgentDelayMs)
	hsSettings.Compaction = &durable.CompactionPolicyPatch{
		Enabled:          &comp.Enabled,
		ReserveTokens:    &comp.ReserveTokens,
		KeepRecentTokens: &comp.KeepRecentTokens,
	}
	hsSettings.Retry = &durable.ConversationRetryPolicyPatch{
		Enabled:         &retry.Enabled,
		MaxRetries:      &retry.MaxRetries,
		BaseDelayMs:     &baseDelayMs,
		MaxAgentDelayMs: &maxDelayMs,
	}
	hsSettings.SteeringMode = steering
	hsSettings.FollowUpMode = followUp
	return hsSettings, nil
}

// CreateHarnessEnv returns an environment factory bound to defaultCwd and optional shellPath.
func CreateHarnessEnv(defaultCwd string, shellPath *string) func(target durable.EnvTarget, ctx chord.Context) (env.ExecutionEnv, error) {
	shell := ""
	if shellPath != nil {
		shell = *shellPath
	}
	return func(target durable.EnvTarget, _ chord.Context) (env.ExecutionEnv, error) {
		cwd := defaultCwd
		if target.Cwd != nil && strings.TrimSpace(*target.Cwd) != "" {
			cwd = *target.Cwd
		}
		return env.NewNodeExecutionEnv(env.NodeExecutionEnvOptions{
			Cwd:       cwd,
			ShellPath: shell,
		}), nil
	}
}

// OpenDurableHarness sets up SettingsManager, ModelRuntime, durable Registry, Harness, and Root conversation.
func OpenDurableHarness(ctx context.Context, options HarnessSetupOptions) (*DurableHarnessBundle, error) {
	if options.Storage == nil {
		return nil, fmt.Errorf("storage is required")
	}
	cwd := strings.TrimSpace(options.Cwd)
	if cwd == "" {
		cwd = "."
	}
	agentDir := strings.TrimSpace(options.AgentDir)
	if agentDir == "" {
		agentDir = utils.GetAgentDir()
	}

	settingsManager := core.SettingsManagerCreate(cwd, agentDir)
	if _, err := ApplyHttpSettings(settingsManager); err != nil {
		return nil, err
	}

	modelRuntime, err := core.CreateModelRuntime(ctx, &core.CreateModelRuntimeOptions{
		AllowModelNetwork: options.AllowModelNetwork,
	})
	if err != nil {
		return nil, err
	}

	modelCfg, err := ResolveHarnessModel(ctx, modelRuntime, settingsManager, options.Provider, options.Model, options.Thinking)
	if err != nil {
		modelRuntime.Close()
		return nil, err
	}

	extensions := hsCollectExtensions(cwd, agentDir, options.Extensions)
	registry := durable.CreateRegistry()
	for _, ext := range extensions {
		if ext == nil {
			continue
		}
		if err := registry.Install(ext); err != nil {
			modelRuntime.Close()
			return nil, fmt.Errorf("install extension %q: %w", ext.Name, err)
		}
	}

	harnessSettings, err := BuildHarnessSettings(settingsManager, extensions)
	if err != nil {
		modelRuntime.Close()
		return nil, err
	}

	shellPath := options.ShellPath
	if shellPath == nil {
		shellPath = settingsManager.GetShellPath()
	}

	chordCtx := hsChordContext(ctx)
	harness, err := durable.HarnessOpen(options.Storage, durable.HarnessOptions{
		Models:         modelRuntime,
		Registry:       registry,
		StaticSettings: &harnessSettings,
		Env:            CreateHarnessEnv(cwd, shellPath),
		OnReport:       options.OnReport,
	}, chordCtx)
	if err != nil {
		modelRuntime.Close()
		return nil, err
	}

	agentChange := durable.AgentChange{
		Model:         jsonx.Some(modelCfg.ModelRef),
		ThinkingLevel: jsonx.Some(modelCfg.ThinkingLevel),
		Cwd:           jsonx.Some(cwd),
	}
	if options.Instructions != nil {
		agentChange.Instructions = jsonx.Some(*options.Instructions)
	}

	root, err := harness.Root(chordCtx, durable.RootOptions{
		Agent: &agentChange,
	})
	if err != nil {
		_ = harness.Close(chordCtx)
		modelRuntime.Close()
		return nil, err
	}

	return &DurableHarnessBundle{
		Cwd:             cwd,
		AgentDir:        agentDir,
		SettingsManager: settingsManager,
		ModelRuntime:    modelRuntime,
		Registry:        registry,
		Harness:         harness,
		Root:            root,
		ModelConfig:     modelCfg,
	}, nil
}

func hsCollectExtensions(cwd, agentDir string, extra []*durable.Extension) []*durable.Extension {
	promptExt := CreateDurablePromptExtension(DurablePromptExtensionOptions{
		Cwd:      cwd,
		AgentDir: agentDir,
	})
	subagentExt := CreateSubagentExtension()
	list := []*durable.Extension{
		tools.CodingTools,
		promptExt,
		subagentExt,
	}
	seen := map[string]bool{
		tools.CodingTools.Name: true,
		promptExt.Name:         true,
		subagentExt.Name:       true,
	}
	for _, ext := range extra {
		if ext == nil || seen[ext.Name] {
			continue
		}
		seen[ext.Name] = true
		list = append(list, ext)
	}
	return list
}

func hsChordContext(ctx context.Context) chord.Context {
	if ctx == nil {
		return chord.BackgroundContext
	}
	return chord.WithAbortSignal(ctx, chord.BackgroundContext)
}

func hsQueueMode(mode agent.QueueMode) durable.QueueMode {
	if mode == agent.QueueModeAll {
		return durable.QueueModeAll
	}
	return durable.QueueModeOneAtATime
}

func hsToModelThinkingLevel(level agent.ThinkingLevel, model *ai.Model) ai.ModelThinkingLevel {
	if model != nil && !model.Reasoning {
		return ""
	}
	switch level {
	case ai.ModelThinkingLevelOff,
		ai.ModelThinkingLevelMinimal,
		ai.ModelThinkingLevelLow,
		ai.ModelThinkingLevelMedium,
		ai.ModelThinkingLevelHigh,
		ai.ModelThinkingLevelXhigh:
		return level
	default:
		return ""
	}
}

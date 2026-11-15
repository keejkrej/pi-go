// Ported from packages/coding-agent/src/experimental/durable/harness-setup.ts (pi v1.0.0).

package durableagent

import (
	"context"
	"fmt"
	"math"
	"sync"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/env"
	"github.com/keejkrej/pi-go/durable/tools"
)

// ConfigureHarnessHttp applies pi's HTTP proxy and idle-timeout configuration.
// Without it, some provider streams end early.
func ConfigureHarnessHttp(settingsManager *core.SettingsManager) error {
	if settingsManager == nil {
		return core.ConfigureHttpDispatcher(core.DefaultHttpIdleTimeoutMs)
	}
	core.ApplyHttpProxySettings(settingsManager.GetSettings().HttpProxy)
	idle, err := settingsManager.GetHttpIdleTimeoutMs()
	if err != nil {
		return err
	}
	return core.ConfigureHttpDispatcher(idle)
}

// CreateHarnessSettings returns a dynamic HarnessSettings supplier reading
// pi's settings as loaded at startup at every use.
func CreateHarnessSettings(settingsManager *core.SettingsManager) func() durable.HarnessSettings {
	return func() durable.HarnessSettings {
		if settingsManager == nil {
			return durable.HarnessSettings{}
		}
		provider := settingsManager.GetProviderRetrySettings()
		idle, idleErr := settingsManager.GetHttpIdleTimeoutMs()
		timeoutMs := int64(math.MaxInt32)
		if provider.TimeoutMs != nil {
			timeoutMs = int64(*provider.TimeoutMs)
		} else if idleErr == nil && idle != 0 {
			timeoutMs = idle
		}
		maxRetryDelayMs := int64(provider.MaxRetryDelayMs)
		stream := &durable.ConversationStreamOptions{
			TimeoutMs:       &timeoutMs,
			MaxRetryDelayMs: &maxRetryDelayMs,
			MaxRetries:      provider.MaxRetries,
		}

		var compaction *durable.CompactionPolicyPatch
		if comp, err := settingsManager.GetCompactionSettings(); err == nil {
			compaction = &durable.CompactionPolicyPatch{
				Enabled:          &comp.Enabled,
				ReserveTokens:    &comp.ReserveTokens,
				KeepRecentTokens: &comp.KeepRecentTokens,
			}
		}

		retrySettings := settingsManager.GetRetrySettings()
		baseDelayMs := int64(retrySettings.BaseDelayMs)
		maxDelayMs := int64(retrySettings.MaxAgentDelayMs)
		retry := &durable.ConversationRetryPolicyPatch{
			Enabled:         &retrySettings.Enabled,
			MaxRetries:      &retrySettings.MaxRetries,
			BaseDelayMs:     &baseDelayMs,
			MaxAgentDelayMs: &maxDelayMs,
		}

		return durable.HarnessSettings{
			Stream:       stream,
			Compaction:   compaction,
			Retry:        retry,
			SteeringMode: hsQueueMode(settingsManager.GetSteeringMode()),
			FollowUpMode: hsQueueMode(settingsManager.GetFollowUpMode()),
		}
	}
}

// CreateCodingRegistry returns a registry with pi's coding tools and system prompt.
func CreateCodingRegistry(settingsManager *core.SettingsManager, cwd string) (durable.Registry, error) {
	registry := durable.CreateRegistry()
	if err := registry.Install(tools.CodingTools); err != nil {
		return nil, err
	}
	if err := registry.Install(CreatePiPrompt(settingsManager, cwd)); err != nil {
		return nil, err
	}
	return registry, nil
}

// ExecutionEnvs maintains one execution environment per directory, shared by every conversation in it.
type ExecutionEnvs struct {
	mu         sync.Mutex
	defaultCwd string
	envs       map[string]*env.NodeExecutionEnv
}

// NewExecutionEnvs creates an ExecutionEnvs manager rooted at defaultCwd.
func NewExecutionEnvs(defaultCwd string) *ExecutionEnvs {
	return &ExecutionEnvs{
		defaultCwd: defaultCwd,
		envs:       make(map[string]*env.NodeExecutionEnv),
	}
}

// Env returns the shared execution environment for target's directory.
func (e *ExecutionEnvs) Env(target durable.EnvTarget, _ chord.Context) (env.ExecutionEnv, error) {
	cwd := e.defaultCwd
	if target.Cwd != nil && *target.Cwd != "" {
		cwd = *target.Cwd
	}
	e.mu.Lock()
	found := e.envs[cwd]
	if found == nil {
		found = env.NewNodeExecutionEnv(env.NodeExecutionEnvOptions{
			Cwd: cwd,
		})
		e.envs[cwd] = found
	}
	e.mu.Unlock()
	return found, nil
}

// Cleanup tears down all active execution environments.
func (e *ExecutionEnvs) Cleanup(ctx chord.Context) {
	e.mu.Lock()
	list := make([]*env.NodeExecutionEnv, 0, len(e.envs))
	for _, executionEnv := range e.envs {
		list = append(list, executionEnv)
	}
	e.envs = make(map[string]*env.NodeExecutionEnv)
	e.mu.Unlock()

	for _, executionEnv := range list {
		executionEnv.Cleanup(ctx)
	}
}

// CliModelTarget specifies an explicit provider/model CLI selection.
type CliModelTarget struct {
	Provider *string
	Model    string
}

// InitialModel captures the resolved starting model, thinking level, and optional fallback notice.
type InitialModel struct {
	Model           *durable.ModelRef
	ThinkingLevel   *ai.ModelThinkingLevel
	FallbackMessage *string
}

// FindInitialAgentModel resolves the model a new root conversation starts with:
// an explicit --provider/--model, or pi's default resolution.
func FindInitialAgentModel(
	ctx context.Context,
	settingsManager *core.SettingsManager,
	modelRuntime *core.ModelRuntime,
	cli *CliModelTarget,
) (*InitialModel, error) {
	if modelRuntime == nil {
		return nil, fmt.Errorf("model runtime is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if cli != nil {
		modelArg := cli.Model
		resolved := core.ResolveCliModel(core.ResolveCliModelOptions{
			CliProvider:  cli.Provider,
			CliModel:     &modelArg,
			ModelRuntime: modelRuntime,
		})
		if resolved.Error != nil || resolved.Model == nil {
			msg := cli.Model
			if resolved.Error != nil {
				msg = *resolved.Error
			}
			return nil, fmt.Errorf("Could not resolve model: %s", msg)
		}
		out := &InitialModel{
			Model: &durable.ModelRef{
				Provider: string(resolved.Model.Provider),
				ModelId:  resolved.Model.Id,
			},
		}
		if resolved.ThinkingLevel != nil {
			lvl := ai.ModelThinkingLevel(*resolved.ThinkingLevel)
			out.ThinkingLevel = &lvl
		}
		return out, nil
	}

	var defaultProvider, defaultModel *string
	var defaultThinking *agent.ThinkingLevel
	if settingsManager != nil {
		defaultProvider = settingsManager.GetDefaultProvider()
		defaultModel = settingsManager.GetDefaultModel()
		defaultThinking = settingsManager.GetDefaultThinkingLevel()
	}
	initial, err := core.FindInitialModel(ctx, core.FindInitialModelOptions{
		ScopedModels:         nil,
		IsContinuing:         false,
		DefaultProvider:      defaultProvider,
		DefaultModelId:       defaultModel,
		DefaultThinkingLevel: defaultThinking,
		ModelRuntime:         modelRuntime,
	})
	if err != nil {
		return nil, err
	}
	out := &InitialModel{
		FallbackMessage: initial.FallbackMessage,
	}
	if initial.Model != nil {
		out.Model = &durable.ModelRef{
			Provider: string(initial.Model.Provider),
			ModelId:  initial.Model.Id,
		}
		lvl := ai.ModelThinkingLevel(initial.ThinkingLevel)
		out.ThinkingLevel = &lvl
	}
	return out, nil
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

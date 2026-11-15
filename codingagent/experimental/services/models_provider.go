// Ported from packages/coding-agent/src/experimental/services/models-provider.ts (pi v1.0.0).

package services

import (
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// ModelsServiceRuntime is an assembled Models service bound to a durable conversation's pi.agent document.
type ModelsServiceRuntime interface {
	Service() Models
	WireState() chord.MutableReplicatedState[chord.JsonValue]
	Activate(ctx chord.Context) error
	// SyncConfiguration publishes the agent document's model and thinking level when changed.
	SyncConfiguration(ctx chord.Context)
}

// CreateModelsServiceFacetOptions configures CreateModelsServiceFacet.
type CreateModelsServiceFacetOptions struct {
	Harness         durable.Harness
	Conversation    durable.Conversation
	ModelRuntime    *core.ModelRuntime
	SettingsManager *core.SettingsManager
	Context         chord.Context
}

type mpRuntime struct {
	mu              sync.Mutex
	conversation    durable.Conversation
	agent           durable.DocumentState
	modelRuntime    *core.ModelRuntime
	settingsManager *core.SettingsManager
	stateRaw        chord.MutableReplicatedState[chord.JsonValue]
	stateTyped      chord.ReplicatedState[ModelsState]
	catalogRevision int
}

// CreateModelsService constructs the Models service over one conversation and its replicated pi.agent document.
func CreateModelsService(
	conversation durable.Conversation,
	agent durable.DocumentState,
	modelRuntime *core.ModelRuntime,
	settingsManager *core.SettingsManager,
	createState func(initial chord.JsonValue) chord.MutableReplicatedState[chord.JsonValue],
) ModelsServiceRuntime {
	rt := &mpRuntime{
		conversation:    conversation,
		agent:           agent,
		modelRuntime:    modelRuntime,
		settingsManager: settingsManager,
	}
	initial := ModelsState{
		Catalog:       ModelsCatalogState{Revision: 0, AvailableModels: []ModelSummary{}},
		Configuration: mpConfigurationOf(rt.readAgentState()),
		Refresh:       ModelsRefreshState{Status: "idle"},
	}
	initialJson, err := connEncodeJsonValue(initial)
	if err != nil {
		panic(fmt.Sprintf("services: encode initial ModelsState: %v", err))
	}
	rawState := createState(initialJson)
	rt.stateRaw = rawState
	rt.stateTyped = WrapReplicatedState[ModelsState](rawState)
	return rt
}

// CreateModelsServiceFacet acquires the conversation's pi.agent document and returns the @pi/models facet.
func CreateModelsServiceFacet(options CreateModelsServiceFacetOptions) (chord.Facet, error) {
	ctx := options.Context
	if ctx == nil {
		ctx = chord.BackgroundContext
	}
	agent, err := options.Harness.DocumentState(ctx, durable.AgentDoc, options.Conversation.Id())
	if err != nil {
		return chord.Facet{}, err
	}
	if agent == nil {
		return chord.Facet{}, fmt.Errorf("Conversation %d has no agent document", options.Conversation.Id())
	}
	return chord.DefineFacet(chord.Facet{
		Id: "@pi/models",
		Setup: func(env chord.FacetEnvironment) error {
			env.Own(func() error {
				agent.Dispose()
				return nil
			})
			runtime := CreateModelsService(
				options.Conversation,
				agent,
				options.ModelRuntime,
				options.SettingsManager,
				env.ReplicatedState,
			)
			env.Provide(ModelsService, ProvideModels(runtime.WireState(), runtime.Service()))
			env.OnActivate(func() error {
				if err := runtime.Activate(chord.BackgroundContext); err != nil {
					return err
				}
				unsub := agent.Subscribe(func(_ durable.JsonObject, call chord.Context, _ chord.ReplicatedStateDelivery) error {
					runtime.SyncConfiguration(call)
					return nil
				})
				env.Own(func() error {
					unsub()
					return nil
				})
				return nil
			})
			return nil
		},
	}), nil
}

func (r *mpRuntime) Service() Models {
	return r
}

func (r *mpRuntime) WireState() chord.MutableReplicatedState[chord.JsonValue] {
	return r.stateRaw
}

func (r *mpRuntime) State() chord.ReplicatedState[ModelsState] {
	return r.stateTyped
}

func (r *mpRuntime) readAgentState() *durable.AgentState {
	if r.agent == nil {
		return nil
	}
	obj, ok := r.agent.Value()
	if !ok || obj == nil {
		return nil
	}
	var parsed durable.AgentState
	if err := jsonx.Decode(obj, &parsed); err != nil {
		return nil
	}
	return &parsed
}

func (r *mpRuntime) selectedModel() *ai.Model {
	if r.modelRuntime == nil {
		return nil
	}
	st := r.readAgentState()
	if st == nil || st.Model == nil {
		return nil
	}
	return r.modelRuntime.GetModel(st.Model.Provider, st.Model.ModelId)
}

func (r *mpRuntime) readThinkingLevels() []ai.ModelThinkingLevel {
	selected := r.selectedModel()
	if selected == nil {
		return []ai.ModelThinkingLevel{ai.ModelThinkingLevelOff}
	}
	return ai.GetSupportedThinkingLevels(selected)
}

func (r *mpRuntime) readCatalog() ModelsCatalogState {
	selected := r.selectedModel()
	var available []*ai.Model
	if r.modelRuntime != nil {
		available = r.modelRuntime.GetAvailableSnapshot()
	}
	catalog := make([]*ai.Model, len(available))
	copy(catalog, available)
	if selected != nil && !mpIncludesModel(available, selected) {
		catalog = append(catalog, selected)
	}
	r.mu.Lock()
	r.catalogRevision++
	rev := r.catalogRevision
	r.mu.Unlock()

	summaries := make([]ModelSummary, 0, len(catalog))
	for _, m := range catalog {
		summaries = append(summaries, ModelSummary{
			Provider:  string(m.Provider),
			ModelId:   m.Id,
			Name:      m.Name,
			Reasoning: m.Reasoning,
		})
	}
	return ModelsCatalogState{
		Revision:        rev,
		AvailableModels: summaries,
	}
}

func (r *mpRuntime) Activate(ctx chord.Context) error {
	catalog := r.readCatalog()
	return r.stateRaw.Change(ctx, func(draft chord.Draft[chord.JsonValue]) error {
		root, ok := draft.Value.(*jsonx.Object)
		if !ok {
			return fmt.Errorf("unexpected ModelsState draft %T", draft.Value)
		}
		catJson, err := connEncodeJsonValue(catalog)
		if err != nil {
			return err
		}
		refJson, err := connEncodeJsonValue(ModelsRefreshState{Status: "idle"})
		if err != nil {
			return err
		}
		root.Set("catalog", catJson)
		root.Set("refresh", refJson)
		return nil
	})
}

func (r *mpRuntime) SyncConfiguration(ctx chord.Context) {
	next := mpConfigurationOf(r.readAgentState())
	current, ok := r.stateTyped.Value()
	if ok && mpSameConfiguration(current.Configuration, next) {
		return
	}
	_ = r.stateRaw.Change(ctx, func(draft chord.Draft[chord.JsonValue]) error {
		root, ok := draft.Value.(*jsonx.Object)
		if !ok {
			return fmt.Errorf("unexpected ModelsState draft %T", draft.Value)
		}
		cfgJson, err := connEncodeJsonValue(next)
		if err != nil {
			return err
		}
		root.Set("configuration", cfgJson)
		return nil
	})
}

func (r *mpRuntime) CycleThinking(ctx chord.Context) error {
	levels := r.readThinkingLevels()
	if len(levels) == 0 {
		levels = []ai.ModelThinkingLevel{ai.ModelThinkingLevelOff}
	}
	st := r.readAgentState()
	current := ai.ModelThinkingLevelOff
	if st != nil && st.ThinkingLevel != nil {
		current = *st.ThinkingLevel
	}
	idx := slices.Index(levels, current)
	next := levels[(idx+1)%len(levels)]
	return r.conversation.Configure(durable.AgentChange{
		ThinkingLevel: jsonx.Some(next),
	}, ctx)
}

func (r *mpRuntime) GetThinkingLevels(_ chord.Context) ([]ai.ModelThinkingLevel, error) {
	return r.readThinkingLevels(), nil
}

func (r *mpRuntime) Refresh(ctx chord.Context) error {
	if err := r.stateRaw.Change(ctx, func(draft chord.Draft[chord.JsonValue]) error {
		root, ok := draft.Value.(*jsonx.Object)
		if !ok {
			return fmt.Errorf("unexpected ModelsState draft %T", draft.Value)
		}
		refJson, err := connEncodeJsonValue(ModelsRefreshState{Status: "refreshing"})
		if err != nil {
			return err
		}
		root.Set("refresh", refJson)
		return nil
	}); err != nil {
		return err
	}

	var errs map[string]string
	if r.modelRuntime != nil {
		abortCtx := chord.BackgroundContext.AbortSignal()
		if ctx != nil {
			abortCtx = ctx.AbortSignal()
		}
		res, err := r.modelRuntime.Refresh(abortCtx, nil)
		if err != nil {
			return err
		}
		if res.Errors != nil && res.Errors.Len() > 0 {
			errs = make(map[string]string, res.Errors.Len())
			for k, e := range res.Errors.All() {
				if e != nil {
					errs[k] = e.Error()
				}
			}
		}
	}
	catalog := r.readCatalog()
	return r.stateRaw.Change(ctx, func(draft chord.Draft[chord.JsonValue]) error {
		root, ok := draft.Value.(*jsonx.Object)
		if !ok {
			return fmt.Errorf("unexpected ModelsState draft %T", draft.Value)
		}
		catJson, err := connEncodeJsonValue(catalog)
		if err != nil {
			return err
		}
		refState := ModelsRefreshState{Status: "done"}
		if len(errs) > 0 {
			refState = ModelsRefreshState{Status: "warning", Errors: errs}
		}
		refJson, err := connEncodeJsonValue(refState)
		if err != nil {
			return err
		}
		root.Set("catalog", catJson)
		root.Set("refresh", refJson)
		return nil
	})
}

func (r *mpRuntime) Select(model ModelRef, ctx chord.Context) error {
	var selected *ai.Model
	if r.modelRuntime != nil {
		selected = r.modelRuntime.GetModel(model.Provider, model.ModelId)
	}
	if selected == nil {
		return fmt.Errorf("Unknown model: %s/%s", model.Provider, model.ModelId)
	}
	st := r.readAgentState()
	curThinking := ai.ModelThinkingLevelOff
	if st != nil && st.ThinkingLevel != nil {
		curThinking = *st.ThinkingLevel
	}
	clamped := ai.ClampThinkingLevel(selected, curThinking)
	if err := r.conversation.Configure(durable.AgentChange{
		Model:         jsonx.Some(durable.ModelRef{Provider: string(selected.Provider), ModelId: selected.Id}),
		ThinkingLevel: jsonx.Some(clamped),
	}, ctx); err != nil {
		return err
	}
	if r.settingsManager != nil {
		r.settingsManager.SetDefaultModelAndProvider(string(selected.Provider), selected.Id)
		return r.settingsManager.Flush()
	}
	return nil
}

func (r *mpRuntime) SelectThinking(level ai.ModelThinkingLevel, ctx chord.Context) error {
	levels := r.readThinkingLevels()
	if !slices.Contains(levels, level) {
		names := make([]string, len(levels))
		for i, l := range levels {
			names[i] = string(l)
		}
		return fmt.Errorf("Thinking level %s is unavailable; choose one of: %s", level, strings.Join(names, ", "))
	}
	return r.conversation.Configure(durable.AgentChange{
		ThinkingLevel: jsonx.Some(level),
	}, ctx)
}

func mpConfigurationOf(value *durable.AgentState) ModelsConfigurationState {
	var model *ModelRef
	thinking := ai.ModelThinkingLevelOff
	if value != nil {
		if value.Model != nil {
			model = &ModelRef{Provider: value.Model.Provider, ModelId: value.Model.ModelId}
		}
		if value.ThinkingLevel != nil {
			thinking = *value.ThinkingLevel
		}
	}
	return ModelsConfigurationState{
		Model:         model,
		ThinkingLevel: thinking,
	}
}

func mpSameConfiguration(left, right ModelsConfigurationState) bool {
	if left.ThinkingLevel != right.ThinkingLevel {
		return false
	}
	if left.Model == nil || right.Model == nil {
		return left.Model == right.Model
	}
	return left.Model.Provider == right.Model.Provider && left.Model.ModelId == right.Model.ModelId
}

func mpIncludesModel(models []*ai.Model, selected *ai.Model) bool {
	for _, m := range models {
		if m != nil && m.Provider == selected.Provider && m.Id == selected.Id {
			return true
		}
	}
	return false
}

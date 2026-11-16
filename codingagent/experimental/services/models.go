// Ported from packages/coding-agent/src/experimental/services/models.ts (pi v1.0.0).

package services

import (
	"fmt"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/chord"
)

// ModelRef identifies a provider and model ID.
type ModelRef struct {
	Provider string `json:"provider"`
	ModelId  string `json:"modelId"`
}

// ModelSummary describes an available model exposed by the Models service.
type ModelSummary struct {
	Provider  string `json:"provider"`
	ModelId   string `json:"modelId"`
	Name      string `json:"name"`
	Reasoning bool   `json:"reasoning"`
}

// ModelsCatalogState holds the replicated model catalog snapshot.
type ModelsCatalogState struct {
	Revision        int            `json:"revision"`
	AvailableModels []ModelSummary `json:"availableModels"`
}

// ModelsConfigurationState holds the active model selection and thinking level.
type ModelsConfigurationState struct {
	Model         *ModelRef             `json:"model"`
	ThinkingLevel ai.ModelThinkingLevel `json:"thinkingLevel"`
}

// ModelsRefreshState tracks catalog refresh progress and per-provider warnings.
type ModelsRefreshState struct {
	Status string            `json:"status"`
	Errors map[string]string `json:"errors,omitzero"`
}

// ModelsState is the replicated state document served by ModelsService.
type ModelsState struct {
	Catalog       ModelsCatalogState       `json:"catalog"`
	Configuration ModelsConfigurationState `json:"configuration"`
	Refresh       ModelsRefreshState       `json:"refresh"`
}

// Models exposes session model selection, thinking level controls, and catalog refresh.
type Models interface {
	State() chord.ReplicatedState[ModelsState]
	CycleThinking(ctx chord.Context) error
	GetThinkingLevels(ctx chord.Context) ([]ai.ModelThinkingLevel, error)
	Refresh(ctx chord.Context) error
	Select(model ModelRef, ctx chord.Context) error
	SelectThinking(level ai.ModelThinkingLevel, ctx chord.Context) error
}

// ModelsService is the Chord service token for Models.
var ModelsService = connDefineService[Models]("pi.models")

// ProvideModels builds a Chord provider member table combining wireState and impl methods.
func ProvideModels(wireState any, impl Models) map[string]any {
	return map[string]any{
		"state": wireState,
		"cycleThinking": func(ctx chord.Context) error {
			return impl.CycleThinking(ctx)
		},
		"getThinkingLevels": func(ctx chord.Context) (any, error) {
			levels, err := impl.GetThinkingLevels(ctx)
			if err != nil {
				return nil, err
			}
			return connEncodeJsonValue(levels)
		},
		"refresh": func(ctx chord.Context) error {
			return impl.Refresh(ctx)
		},
		"select": func(rawModel any, ctx chord.Context) error {
			model, err := connDecodeJson[ModelRef](rawModel)
			if err != nil {
				return err
			}
			return impl.Select(model, ctx)
		},
		"selectThinking": func(level ai.ModelThinkingLevel, ctx chord.Context) error {
			return impl.SelectThinking(level, ctx)
		},
	}
}

// AsModels wraps a Chord service handle or concrete implementation as Models.
func AsModels(raw any) Models {
	if raw == nil {
		return nil
	}
	if impl, ok := raw.(Models); ok {
		return impl
	}
	if view, ok := raw.(connServiceView); ok {
		return &modelsClient{
			view:  view,
			state: connWrapServiceStateMember[ModelsState](view, "state"),
		}
	}
	panic(fmt.Sprintf("services: unsupported Models handle %T", raw))
}

type modelsClient struct {
	view  connServiceView
	state chord.ReplicatedState[ModelsState]
}

func (c *modelsClient) State() chord.ReplicatedState[ModelsState] {
	return c.state
}

func (c *modelsClient) CycleThinking(ctx chord.Context) error {
	_, err := c.view.Call("cycleThinking", ctx)
	return err
}

func (c *modelsClient) GetThinkingLevels(ctx chord.Context) ([]ai.ModelThinkingLevel, error) {
	raw, err := c.view.Call("getThinkingLevels", ctx)
	if err != nil {
		return nil, err
	}
	return connDecodeJson[[]ai.ModelThinkingLevel](raw)
}

func (c *modelsClient) Refresh(ctx chord.Context) error {
	_, err := c.view.Call("refresh", ctx)
	return err
}

func (c *modelsClient) Select(model ModelRef, ctx chord.Context) error {
	encoded, err := connEncodeJsonValue(model)
	if err != nil {
		return err
	}
	_, err = c.view.Call("select", encoded, ctx)
	return err
}

func (c *modelsClient) SelectThinking(level ai.ModelThinkingLevel, ctx chord.Context) error {
	_, err := c.view.Call("selectThinking", string(level), ctx)
	return err
}

// Ported from packages/evals/evals/configured-runtime.ts (pi v1.0.0).

package suites

import (
	"context"
	"fmt"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
)

// ModelFieldsCost summarizes token cost rates on an inspected model.
type ModelFieldsCost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
}

// ModelFields holds the JSON-comparable metadata fields inspected from an ai.Model.
type ModelFields struct {
	Id            string          `json:"id"`
	Name          string          `json:"name"`
	Provider      string          `json:"provider"`
	Reasoning     bool            `json:"reasoning"`
	Input         []string        `json:"input"`
	Cost          ModelFieldsCost `json:"cost"`
	ContextWindow int             `json:"contextWindow"`
	MaxTokens     int             `json:"maxTokens"`
}

// ProviderRuntimeResponse summarizes one completion response during provider inspection.
type ProviderRuntimeResponse struct {
	Text         string `json:"text"`
	StopReason   string `json:"stopReason"`
	InputTokens  int    `json:"inputTokens"`
	OutputTokens int    `json:"outputTokens"`
}

// ProviderRuntimeResult represents the union result of InspectProvider.
type ProviderRuntimeResult struct {
	ValidRequestReceived *bool                    `json:"validRequestReceived,omitzero"`
	Model                *ModelFields             `json:"model,omitzero"`
	Response             *ProviderRuntimeResponse `json:"response,omitzero"`
	Error                *string                  `json:"error,omitzero"`
}

// ProviderRuntimeOutput is the top-level output shape returned by InspectProvider.
type ProviderRuntimeOutput struct {
	Result ProviderRuntimeResult `json:"result"`
}

// AddedModelResult represents the union result of InspectAddedModel.
type AddedModelResult struct {
	Model                   *ModelFields `json:"model,omitzero"`
	ExistingModelsPreserved *bool        `json:"existingModelsPreserved,omitzero"`
	Error                   *string      `json:"error,omitzero"`
}

// AddedModelOutput is the top-level output shape returned by InspectAddedModel.
type AddedModelOutput struct {
	Result AddedModelResult `json:"result"`
}

// ProviderScenario describes a single ModelRuntime probe scenario.
type ProviderScenario struct {
	ProviderId           string
	ModelId              string
	CreateContext        func() *ai.Context
	Options              *ai.ModelsSimpleStreamOptions
	ValidRequestReceived func() bool
}

func cfgrtStrPtr(s string) *string { return &s }

func cfgrtBoolPtr(b bool) *bool { return &b }

func cfgrtModelFields(model *ai.Model) ModelFields {
	inputs := make([]string, 0, len(model.Input))
	for _, item := range model.Input {
		inputs = append(inputs, string(item))
	}
	return ModelFields{
		Id:        model.Id,
		Name:      model.Name,
		Provider:  string(model.Provider),
		Reasoning: model.Reasoning,
		Input:     inputs,
		Cost: ModelFieldsCost{
			Input:      model.Cost.Input,
			Output:     model.Cost.Output,
			CacheRead:  model.Cost.CacheRead,
			CacheWrite: model.Cost.CacheWrite,
		},
		ContextWindow: model.ContextWindow,
		MaxTokens:     model.MaxTokens,
	}
}

// InspectProvider reloads runtime configuration, verifies the target model exists, and executes one probe.
func InspectProvider(ctx context.Context, runtime *core.ModelRuntime, scenario ProviderScenario) ProviderRuntimeOutput {
	if runtime == nil {
		return ProviderRuntimeOutput{Result: ProviderRuntimeResult{Error: cfgrtStrPtr("ModelRuntime is nil.")}}
	}
	if _, err := runtime.Refresh(ctx, &ai.ModelsRefreshOptions{AllowNetwork: cfgrtBoolPtr(false)}); err != nil {
		return ProviderRuntimeOutput{Result: ProviderRuntimeResult{Error: cfgrtStrPtr(err.Error())}}
	}
	if configurationError := runtime.GetError(); configurationError != nil {
		return ProviderRuntimeOutput{Result: ProviderRuntimeResult{Error: configurationError}}
	}
	model := runtime.GetModel(scenario.ProviderId, scenario.ModelId)
	if model == nil {
		return ProviderRuntimeOutput{
			Result: ProviderRuntimeResult{
				Error: cfgrtStrPtr(fmt.Sprintf("Model %s/%s is unavailable after reload.", scenario.ProviderId, scenario.ModelId)),
			},
		}
	}
	var msgCtx *ai.Context
	if scenario.CreateContext != nil {
		msgCtx = scenario.CreateContext()
	}
	response, err := runtime.CompleteSimple(ctx, model, msgCtx, scenario.Options)
	if err != nil {
		return ProviderRuntimeOutput{Result: ProviderRuntimeResult{Error: cfgrtStrPtr(err.Error())}}
	}
	valid := false
	if scenario.ValidRequestReceived != nil {
		valid = scenario.ValidRequestReceived()
	}
	fields := cfgrtModelFields(model)
	return ProviderRuntimeOutput{
		Result: ProviderRuntimeResult{
			ValidRequestReceived: cfgrtBoolPtr(valid),
			Model:                &fields,
			Response: &ProviderRuntimeResponse{
				Text:         ai.ContentText(response.Content),
				StopReason:   string(response.StopReason),
				InputTokens:  response.Usage.Input,
				OutputTokens: response.Usage.Output,
			},
		},
	}
}

// InspectAddedModel checks that a newly added model on a built-in provider loads alongside all existing models.
func InspectAddedModel(ctx context.Context, runtime *core.ModelRuntime, providerId string, modelId string) AddedModelOutput {
	if runtime == nil {
		return AddedModelOutput{Result: AddedModelResult{Error: cfgrtStrPtr("ModelRuntime is nil.")}}
	}
	pristineRuntime, err := core.CreateModelRuntime(ctx, &core.CreateModelRuntimeOptions{
		ModelsPath:        cfgrtStrPtr(""),
		AllowModelNetwork: false,
	})
	if err != nil {
		return AddedModelOutput{Result: AddedModelResult{Error: cfgrtStrPtr(err.Error())}}
	}
	defer pristineRuntime.Close()

	pristineProvider := pristineRuntime.GetProvider(providerId)
	if pristineProvider == nil {
		return AddedModelOutput{
			Result: AddedModelResult{
				Error: cfgrtStrPtr(fmt.Sprintf("Built-in provider %s is unavailable.", providerId)),
			},
		}
	}
	var existingModels []*ai.Model
	if pristineProvider.GetModels != nil {
		existingModels = pristineProvider.GetModels()
	}
	if len(existingModels) == 0 {
		return AddedModelOutput{
			Result: AddedModelResult{
				Error: cfgrtStrPtr(fmt.Sprintf("Built-in provider %s has no models.", providerId)),
			},
		}
	}

	if _, err := runtime.Refresh(ctx, &ai.ModelsRefreshOptions{AllowNetwork: cfgrtBoolPtr(false)}); err != nil {
		return AddedModelOutput{Result: AddedModelResult{Error: cfgrtStrPtr(err.Error())}}
	}
	if configurationError := runtime.GetError(); configurationError != nil {
		return AddedModelOutput{Result: AddedModelResult{Error: configurationError}}
	}
	model := runtime.GetModel(providerId, modelId)
	if model == nil {
		return AddedModelOutput{
			Result: AddedModelResult{
				Error: cfgrtStrPtr(fmt.Sprintf("Model %s/%s is unavailable after reload.", providerId, modelId)),
			},
		}
	}
	preserved := true
	for _, existing := range existingModels {
		if existing == nil || runtime.GetModel(providerId, existing.Id) == nil {
			preserved = false
			break
		}
	}
	fields := cfgrtModelFields(model)
	return AddedModelOutput{
		Result: AddedModelResult{
			Model:                   &fields,
			ExistingModelsPreserved: cfgrtBoolPtr(preserved),
		},
	}
}

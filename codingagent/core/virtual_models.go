// Ported from packages/coding-agent/src/core/virtual-models.ts (pi v1.5.2).

package core

import (
	"context"
	"fmt"
	"slices"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

const (
	// VirtualModelApi is the API identifier of virtual catalog entries.
	VirtualModelApi = "pi-virtual"
	// VirtualModelStateEntry is the custom entry type that stores router state on the session branch.
	VirtualModelStateEntry = "pi.virtual-model-state"
)

// VirtualModelStateData is the payload of a pi.virtual-model-state custom entry.
type VirtualModelStateData struct {
	Provider string `json:"provider"`
	ModelId  string `json:"modelId"`
	State    any    `json:"state,omitzero"`
}

// ModelRouteReason describes why a request is being routed.
type ModelRouteReason string

const (
	ModelRouteReasonUser         ModelRouteReason = "user"
	ModelRouteReasonContinuation ModelRouteReason = "continuation"
	ModelRouteReasonRetry        ModelRouteReason = "retry"
	ModelRouteReasonDirect       ModelRouteReason = "direct"
)

// ModelRoutePrevious describes the physical model and thinking level of the latest successful response.
type ModelRoutePrevious struct {
	Model         *ai.Model              `json:"model"`
	ThinkingLevel *ai.ModelThinkingLevel `json:"thinkingLevel,omitzero"`
}

// ModelRouteFailed describes the failed physical request being retried.
type ModelRouteFailed struct {
	Model         *ai.Model              `json:"model"`
	ThinkingLevel *ai.ModelThinkingLevel `json:"thinkingLevel,omitzero"`
	Message       *ai.AssistantMessage   `json:"message"`
}

// ModelRouteRequest is passed to a virtual model's Route function.
type ModelRouteRequest struct {
	Model         *ai.Model             `json:"model"`
	ThinkingLevel ai.ModelThinkingLevel `json:"thinkingLevel"`
	Reason        ModelRouteReason      `json:"reason"`
	Previous      *ModelRoutePrevious   `json:"previous,omitzero"`
	Failed        *ModelRouteFailed     `json:"failed,omitzero"`
	State         any                   `json:"state,omitzero"`
	Messages      []ai.Message          `json:"messages"`
}

// ModelRoute selects the physical model and thinking level for one request.
type ModelRoute struct {
	Model         *ai.Model             `json:"model"`
	ThinkingLevel ai.ModelThinkingLevel `json:"thinkingLevel"`
	State         any                   `json:"state,omitzero"`
}

// VirtualModelDefinition defines a virtual model catalog entry and optional router.
type VirtualModelDefinition struct {
	Provider       string
	Id             string
	Name           string
	ThinkingLevels []ai.ModelThinkingLevel
	ContextWindow  int
	MaxTokens      int
	Input          []ai.ModelModality
	Route          func(ctx context.Context, request ModelRouteRequest) (ModelRoute, error)
}

// BranchModelRef names a (provider, modelId) pair from a session branch.
type BranchModelRef struct {
	Provider string `json:"provider"`
	ModelId  string `json:"modelId"`
}

var vmAllThinkingLevels = []ai.ModelThinkingLevel{
	ai.ModelThinkingLevelOff,
	ai.ModelThinkingLevelMinimal,
	ai.ModelThinkingLevelLow,
	ai.ModelThinkingLevelMedium,
	ai.ModelThinkingLevelHigh,
	ai.ModelThinkingLevelXhigh,
	ai.ModelThinkingLevelMax,
}

// IsVirtualModel reports whether a chat model is a virtual catalog entry.
func IsVirtualModel(model *ai.Model) bool {
	return model != nil && model.Api == VirtualModelApi
}

// IsVirtualAnyModel reports whether any catalog model is a virtual catalog entry.
func IsVirtualAnyModel(model ai.AnyModel) bool {
	if model == nil {
		return false
	}
	if m, ok := model.(*ai.Model); ok {
		return m != nil && m.Api == VirtualModelApi
	}
	return false
}

// IsVirtualModelApi reports whether an API id names virtual models.
func IsVirtualModelApi(api string) bool {
	return api == VirtualModelApi
}

// IsVirtualAssistantMessage reports whether an assistant message records a virtual model.
func IsVirtualAssistantMessage(msg *ai.AssistantMessage) bool {
	return msg != nil && string(msg.Api) == VirtualModelApi
}

// FindLatestResponse returns the latest successful physical assistant message.
func FindLatestResponse(messages []agent.AgentMessage) *ai.AssistantMessage {
	for i := len(messages) - 1; i >= 0; i-- {
		if assistant, ok := messages[i].(*ai.AssistantMessage); ok && assistant != nil {
			if assistant.StopReason != ai.StopReasonError && assistant.StopReason != ai.StopReasonAborted {
				return assistant
			}
		}
	}
	return nil
}

// GetBranchSelection returns the active model selection along a session branch.
func GetBranchSelection(branch []SessionEntry, getModel func(provider, modelId string) *ai.Model) *BranchModelRef {
	for i := len(branch) - 1; i >= 0; i-- {
		entry := branch[i]
		if mc, ok := entry.(*ModelChangeEntry); ok && mc != nil {
			return &BranchModelRef{Provider: mc.Provider, ModelId: mc.ModelId}
		}
		if sm, ok := entry.(*SessionMessageEntry); ok && sm != nil {
			if assistant, ok := sm.Message.(*ai.AssistantMessage); ok && assistant != nil && !IsVirtualAssistantMessage(assistant) {
				resp := BranchModelRef{Provider: string(assistant.Provider), ModelId: assistant.Model}
				change := vmFindLastModelChange(branch, i)
				if change != nil && getModel != nil {
					if model := getModel(change.Provider, change.ModelId); model != nil && IsVirtualModel(model) {
						return change
					}
				}
				return &resp
			}
		}
	}
	return nil
}

func vmFindLastModelChange(branch []SessionEntry, before int) *BranchModelRef {
	for i := before - 1; i >= 0; i-- {
		if mc, ok := branch[i].(*ModelChangeEntry); ok && mc != nil {
			return &BranchModelRef{Provider: mc.Provider, ModelId: mc.ModelId}
		}
	}
	return nil
}

// GetVirtualModelState returns the latest router state stored on branch for (provider, modelId).
func GetVirtualModelState(branch []SessionEntry, provider, modelId string) any {
	for i := len(branch) - 1; i >= 0; i-- {
		custom, ok := branch[i].(*CustomEntry)
		if !ok || custom == nil || custom.CustomType != VirtualModelStateEntry || custom.Data == nil {
			continue
		}
		switch data := custom.Data.(type) {
		case VirtualModelStateData:
			if data.Provider == provider && data.ModelId == modelId {
				return data.State
			}
		case *VirtualModelStateData:
			if data != nil && data.Provider == provider && data.ModelId == modelId {
				return data.State
			}
		case *jsonx.Object:
			p, _ := data.GetString("provider")
			m, _ := data.GetString("modelId")
			if p == provider && m == modelId {
				s, _ := data.Get("state")
				return s
			}
		case map[string]any:
			p, _ := data["provider"].(string)
			m, _ := data["modelId"].(string)
			if p == provider && m == modelId {
				return data["state"]
			}
		}
	}
	return nil
}

// CreateVirtualModel builds the catalog *ai.Model entry of a virtual model definition.
func CreateVirtualModel(definition VirtualModelDefinition) *ai.Model {
	levels := definition.ThinkingLevels
	if len(levels) == 0 {
		levels = []ai.ModelThinkingLevel{ai.ModelThinkingLevelOff}
	}
	thinkingMap := &ai.ThinkingLevelMap{
		Off:     vmThinkingMapEntry(levels, ai.ModelThinkingLevelOff),
		Minimal: vmThinkingMapEntry(levels, ai.ModelThinkingLevelMinimal),
		Low:     vmThinkingMapEntry(levels, ai.ModelThinkingLevelLow),
		Medium:  vmThinkingMapEntry(levels, ai.ModelThinkingLevelMedium),
		High:    vmThinkingMapEntry(levels, ai.ModelThinkingLevelHigh),
		Xhigh:   vmThinkingMapEntry(levels, ai.ModelThinkingLevelXhigh),
		Max:     vmThinkingMapEntry(levels, ai.ModelThinkingLevelMax),
	}
	reasoning := false
	for _, lvl := range levels {
		if lvl != ai.ModelThinkingLevelOff {
			reasoning = true
			break
		}
	}
	input := definition.Input
	if len(input) == 0 {
		input = []ai.ModelModality{ai.ModelModalityText, ai.ModelModalityImage}
	}
	return &ai.Model{
		BaseModel: ai.BaseModel{
			Id:       definition.Id,
			Name:     definition.Name,
			Api:      VirtualModelApi,
			Provider: ai.ProviderId(definition.Provider),
			BaseUrl:  "",
			Input:    slices.Clone(input),
			Cost:     ai.ModelCost{},
		},
		Reasoning:        reasoning,
		ThinkingLevelMap: thinkingMap,
		ContextWindow:    definition.ContextWindow,
		MaxTokens:        definition.MaxTokens,
	}
}

func vmThinkingMapEntry(levels []ai.ModelThinkingLevel, target ai.ModelThinkingLevel) jsonx.Opt[string] {
	if slices.Contains(levels, target) {
		return jsonx.Some(string(target))
	}
	return jsonx.Null[string]()
}

func vmUnroutedStream(ctx context.Context, model *ai.Model) *ai.AssistantMessageEventStream {
	return ai.LazyStream(model, func() (*ai.AssistantMessageEventStream, error) {
		return nil, fmt.Errorf("Virtual model %s/%s must be routed before streaming", model.Provider, model.Id)
	}, nil, ctx)
}

// WithVirtualModels adds virtual models to a provider's catalog. If provider is nil,
// returns a keyless provider that only lists the virtual models.
func WithVirtualModels(providerId string, provider *ai.Provider, virtualModels []*ai.Model) *ai.Provider {
	if provider == nil {
		copied := slices.Clone(virtualModels)
		return &ai.Provider{
			Id:   providerId,
			Name: providerId,
			Auth: ai.ProviderAuth{
				ApiKey: &ai.ApiKeyAuth{
					Name: "Virtual model",
					Resolve: func(_ context.Context, _ ai.ApiKeyResolveInput) (*ai.AuthResult, error) {
						src := "virtual"
						return &ai.AuthResult{Auth: ai.ModelAuth{}, Source: &src}, nil
					},
				},
			},
			GetModels: func() []*ai.Model {
				return slices.Clone(copied)
			},
			Stream: func(ctx context.Context, model *ai.Model, _ *ai.TranscriptContext, _ ai.ProviderStreamOptions) *ai.AssistantMessageEventStream {
				return vmUnroutedStream(ctx, model)
			},
			StreamSimple: func(ctx context.Context, model *ai.Model, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
				return vmUnroutedStream(ctx, model)
			},
		}
	}

	ids := make(map[string]struct{}, len(virtualModels))
	for _, vm := range virtualModels {
		ids[vm.Id] = struct{}{}
	}
	isShadowedPhysicalChat := func(m ai.AnyModel) bool {
		if !ai.IsModelType(m, ai.ModelTypeChat) {
			return false
		}
		if IsVirtualAnyModel(m) {
			return false
		}
		chat, ok := m.(*ai.Model)
		if !ok || chat == nil {
			return false
		}
		_, shadowed := ids[chat.Id]
		return shadowed
	}
	filterPhysicalChat := func(models []*ai.Model) []*ai.Model {
		out := make([]*ai.Model, 0, len(models))
		for _, m := range models {
			if !isShadowedPhysicalChat(m) {
				out = append(out, m)
			}
		}
		return out
	}
	filterVirtualChat := func(models []*ai.Model) []*ai.Model {
		var out []*ai.Model
		for _, m := range models {
			if IsVirtualModel(m) {
				out = append(out, m)
			}
		}
		return out
	}
	filterPhysicalAll := func(models []ai.AnyModel) []ai.AnyModel {
		out := make([]ai.AnyModel, 0, len(models))
		for _, m := range models {
			if !isShadowedPhysicalChat(m) {
				out = append(out, m)
			}
		}
		return out
	}
	filterVirtualAll := func(models []ai.AnyModel) []ai.AnyModel {
		var out []ai.AnyModel
		for _, m := range models {
			if IsVirtualAnyModel(m) {
				out = append(out, m)
			}
		}
		return out
	}

	base := rcpCloneProvider(provider)
	base.GetModels = func() []*ai.Model {
		real := filterPhysicalChat(provider.GetModels())
		return append(real, virtualModels...)
	}
	base.GetAllModels = func() []ai.AnyModel {
		var all []ai.AnyModel
		if provider.GetAllModels != nil {
			all = provider.GetAllModels()
		} else if provider.GetModels != nil {
			for _, m := range provider.GetModels() {
				all = append(all, m)
			}
		}
		real := filterPhysicalAll(all)
		for _, vm := range virtualModels {
			real = append(real, vm)
		}
		return real
	}
	base.FilterModels = func(models []*ai.Model, credential ai.Credential) []*ai.Model {
		real := filterPhysicalChat(models)
		if provider.FilterModels != nil {
			real = provider.FilterModels(real, credential)
		}
		return append(real, filterVirtualChat(models)...)
	}
	if provider.FilterAllModels != nil {
		origFilterAll := provider.FilterAllModels
		base.FilterAllModels = func(models []ai.AnyModel, credential ai.Credential) []ai.AnyModel {
			real := origFilterAll(filterPhysicalAll(models), credential)
			return append(real, filterVirtualAll(models)...)
		}
	}
	base.Stream = func(ctx context.Context, model *ai.Model, transcript *ai.TranscriptContext, opts ai.ProviderStreamOptions) *ai.AssistantMessageEventStream {
		if IsVirtualModel(model) {
			return vmUnroutedStream(ctx, model)
		}
		return provider.Stream(ctx, model, transcript, opts)
	}
	base.StreamSimple = func(ctx context.Context, model *ai.Model, transcript *ai.TranscriptContext, opts *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
		if IsVirtualModel(model) {
			return vmUnroutedStream(ctx, model)
		}
		return provider.StreamSimple(ctx, model, transcript, opts)
	}
	return base
}

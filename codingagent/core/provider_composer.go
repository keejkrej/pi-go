// Ported from packages/coding-agent/src/core/provider-composer.ts (pi v1.5.2).

package core

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/compat"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

// ExtensionOAuthConfig is the legacy extension OAuth provider definition.
type ExtensionOAuthConfig struct {
	Name               string
	IsSubscription     bool
	UsesCallbackServer bool
	Login              func(ctx context.Context, callbacks compat.OAuthLoginCallbacks) (ai.OAuthCredentials, error)
	RefreshToken       func(ctx context.Context, credentials ai.OAuthCredentials) (ai.OAuthCredentials, error)
	GetApiKey          func(credentials ai.OAuthCredentials) string
	ModifyModels       func(models []*ai.Model, credentials ai.OAuthCredentials) []*ai.Model
}

// ProviderModelConfigBase contains common fields shared by all ProviderModelConfig variants.
type ProviderModelConfigBase struct {
	Id          string                    `json:"id"`
	Name        string                    `json:"name"`
	Api         *string                   `json:"api,omitzero"`
	BaseUrl     *string                   `json:"baseUrl,omitzero"`
	Input       []ai.ModelModality        `json:"input,omitzero"`
	InputLimits *ai.ModelInputLimits      `json:"inputLimits,omitzero"`
	Cost        ai.ModelCost              `json:"cost"`
	Headers     *omap.Map[string, string] `json:"headers,omitzero"`
}

// ProviderChatModelConfig defines a chat model contributed by registerProvider.
type ProviderChatModelConfig struct {
	ProviderModelConfigBase
	Type                          *ai.ModelType                     `json:"type,omitzero"`
	Reasoning                     bool                              `json:"reasoning"`
	ThinkingLevelMap              *ai.ThinkingLevelMap              `json:"thinkingLevelMap,omitzero"`
	PromptCache                   *ai.ModelPromptCache              `json:"promptCache,omitzero"`
	ContextWindow                 int                               `json:"contextWindow"`
	MaxTokens                     int                               `json:"maxTokens"`
	SamplingParams                *jsonx.Object                     `json:"samplingParams,omitzero"`
	SamplingParamsByThinkingLevel *ai.SamplingParamsByThinkingLevel `json:"samplingParamsByThinkingLevel,omitzero"`
	Compat                        ai.ModelCompatValue               `json:"compat,omitzero"`
}

// ProviderImageModelConfig defines an image model contributed by registerProvider.
type ProviderImageModelConfig struct {
	ProviderModelConfigBase
	Type   ai.ModelType       `json:"type"`
	Output []ai.ModelModality `json:"output"`
}

// ProviderClassifierModelConfig defines a classifier model contributed by registerProvider.
type ProviderClassifierModelConfig struct {
	ProviderModelConfigBase
	Type          ai.ModelType        `json:"type"`
	ContextWindow int                 `json:"contextWindow"`
	Compat        ai.ModelCompatValue `json:"compat,omitzero"`
}

// ProviderModelConfig is the sealed union of ProviderChatModelConfig,
// ProviderImageModelConfig, and ProviderClassifierModelConfig.
type ProviderModelConfig interface {
	isProviderModelConfig()
	modelConfigType() ai.ModelType
	modelConfigId() string
	modelConfigApi() *string
	modelConfigBaseUrl() *string
	modelConfigHeaders() *omap.Map[string, string]
}

func (*ProviderChatModelConfig) isProviderModelConfig() {}
func (c *ProviderChatModelConfig) modelConfigType() ai.ModelType {
	if c.Type != nil {
		return *c.Type
	}
	return ai.ModelTypeChat
}
func (c *ProviderChatModelConfig) modelConfigId() string                         { return c.Id }
func (c *ProviderChatModelConfig) modelConfigApi() *string                       { return c.Api }
func (c *ProviderChatModelConfig) modelConfigBaseUrl() *string                   { return c.BaseUrl }
func (c *ProviderChatModelConfig) modelConfigHeaders() *omap.Map[string, string] { return c.Headers }

func (*ProviderImageModelConfig) isProviderModelConfig()                          {}
func (*ProviderImageModelConfig) modelConfigType() ai.ModelType                   { return ai.ModelTypeImage }
func (c *ProviderImageModelConfig) modelConfigId() string                         { return c.Id }
func (c *ProviderImageModelConfig) modelConfigApi() *string                       { return c.Api }
func (c *ProviderImageModelConfig) modelConfigBaseUrl() *string                   { return c.BaseUrl }
func (c *ProviderImageModelConfig) modelConfigHeaders() *omap.Map[string, string] { return c.Headers }

func (*ProviderClassifierModelConfig) isProviderModelConfig()        {}
func (*ProviderClassifierModelConfig) modelConfigType() ai.ModelType { return ai.ModelTypeClassifier }
func (c *ProviderClassifierModelConfig) modelConfigId() string       { return c.Id }
func (c *ProviderClassifierModelConfig) modelConfigApi() *string     { return c.Api }
func (c *ProviderClassifierModelConfig) modelConfigBaseUrl() *string { return c.BaseUrl }
func (c *ProviderClassifierModelConfig) modelConfigHeaders() *omap.Map[string, string] {
	return c.Headers
}

// ProviderConfig is the input type for extension/runtime provider registration.
type ProviderConfig struct {
	Name          *string                                                                                                                                       `json:"name,omitzero"`
	BaseUrl       *string                                                                                                                                       `json:"baseUrl,omitzero"`
	ApiKey        *string                                                                                                                                       `json:"apiKey,omitzero"`
	Api           *string                                                                                                                                       `json:"api,omitzero"`
	StreamSimple  func(ctx context.Context, model *ai.Model, transcript *ai.TranscriptContext, options *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream `json:"-"`
	Images        map[string]ai.ProviderImages                                                                                                                  `json:"-"`
	Classifiers   map[string]ai.ProviderClassifier                                                                                                              `json:"-"`
	Headers       *omap.Map[string, string]                                                                                                                     `json:"headers,omitzero"`
	AuthHeader    *bool                                                                                                                                         `json:"authHeader,omitzero"`
	OAuth         *ExtensionOAuthConfig                                                                                                                         `json:"-"`
	Models        []ProviderModelConfig                                                                                                                         `json:"-"`
	RefreshModels func(ctx context.Context, refresh *ai.RefreshModelsContext) ([]ProviderModelConfig, error)                                                    `json:"-"`
}

// ProviderConfigInput aliases ProviderConfig for TS name parity.
type ProviderConfigInput = ProviderConfig

// AuthStatus describes whether and how a provider's credentials are configured.
type AuthStatus struct {
	Configured bool    `json:"configured"`
	Source     *string `json:"source,omitzero"`
	Label      *string `json:"label,omitzero"`
}

// ClearApiKeyCache clears cached resolved config/command values.
func ClearApiKeyCache() {
	ClearConfigValueCache()
}

func pcGetAllProviderModels(provider *ai.Provider) []ai.AnyModel {
	if provider == nil {
		return nil
	}
	if provider.GetAllModels != nil {
		return provider.GetAllModels()
	}
	if provider.GetModels != nil {
		models := provider.GetModels()
		out := make([]ai.AnyModel, 0, len(models))
		for _, m := range models {
			out = append(out, m)
		}
		return out
	}
	return nil
}

func pcStructToJsonObject(v any) *jsonx.Object {
	if v == nil {
		return nil
	}
	if obj, ok := v.(*jsonx.Object); ok {
		return obj.Clone()
	}
	raw, err := jsonx.Marshal(v)
	if err != nil || len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	parsed, err := jsonx.Parse(raw)
	if err != nil {
		return nil
	}
	obj, _ := parsed.(*jsonx.Object)
	return obj
}

func pcMergeCompatValue(api string, base ai.ModelCompatValue, override any) ai.ModelCompatValue {
	if override == nil {
		return base
	}
	overObj := pcStructToJsonObject(override)
	if overObj == nil {
		return base
	}
	merged := jsonx.NewObject()
	if !base.IsZero() && base.V != nil {
		if baseObj := pcStructToJsonObject(base.V); baseObj != nil {
			merged.Assign(baseObj)
		}
	}
	merged.Assign(overObj)
	for _, key := range []string{"openRouterRouting", "vercelGatewayRouting", "chatTemplateKwargs", "chatTemplateArgs"} {
		var baseSub, overSub *jsonx.Object
		if !base.IsZero() && base.V != nil {
			if baseObj := pcStructToJsonObject(base.V); baseObj != nil {
				baseSub, _ = baseObj.GetObject(key)
			}
		}
		overSub, _ = overObj.GetObject(key)
		if baseSub != nil || overSub != nil {
			sub := jsonx.NewObject()
			if baseSub != nil {
				sub.Assign(baseSub)
			}
			if overSub != nil {
				sub.Assign(overSub)
			}
			merged.Set(key, sub)
		}
	}
	if api != "" {
		if decoded, err := ai.DecodeModelCompatForApi(api, merged); err == nil {
			return ai.ModelCompatValue{V: decoded}
		}
	}
	if decoded, err := ai.DecodeModelCompat(merged); err == nil {
		return ai.ModelCompatValue{V: decoded}
	}
	return base
}

func pcMergeClassifierCompatValue(api string, base ai.ModelCompatValue, override *ai.ProviderCompat) ai.ModelCompatValue {
	if override == nil {
		return base
	}
	overObj := pcStructToJsonObject(override)
	if overObj == nil {
		return base
	}
	merged := jsonx.NewObject()
	if !base.IsZero() && base.V != nil {
		if baseObj := pcStructToJsonObject(base.V); baseObj != nil {
			merged.Assign(baseObj)
		}
	}
	merged.Assign(overObj)
	if api != "" {
		if decoded, err := ai.DecodeModelCompatForApi(api, merged); err == nil {
			return ai.ModelCompatValue{V: decoded}
		}
	}
	if decoded, err := ai.DecodeModelCompat(merged); err == nil {
		return ai.ModelCompatValue{V: decoded}
	}
	return base
}

func pcMergeInputLimits(base *ai.ModelInputLimits, override *ai.ModelInputLimits) *ai.ModelInputLimits {
	if override == nil {
		return base
	}
	out := &ai.ModelInputLimits{}
	if base != nil {
		*out = *base
	}
	if override.MaxRequestBytes != nil {
		v := *override.MaxRequestBytes
		out.MaxRequestBytes = &v
	}
	if override.Images != nil || (base != nil && base.Images != nil) {
		img := &ai.ModelImageInputLimits{}
		if base != nil && base.Images != nil {
			*img = *base.Images
			if base.Images.Resize != nil {
				r := *base.Images.Resize
				img.Resize = &r
			}
		}
		if override.Images != nil {
			if override.Images.MaxPerMessage != nil {
				v := *override.Images.MaxPerMessage
				img.MaxPerMessage = &v
			}
			if override.Images.MaxPerRequest != nil {
				v := *override.Images.MaxPerRequest
				img.MaxPerRequest = &v
			}
			if override.Images.Resize != nil {
				res := &ai.ModelImageResizeOptions{}
				if img.Resize != nil {
					*res = *img.Resize
				}
				if override.Images.Resize.MaxWidth != nil {
					v := *override.Images.Resize.MaxWidth
					res.MaxWidth = &v
				}
				if override.Images.Resize.MaxHeight != nil {
					v := *override.Images.Resize.MaxHeight
					res.MaxHeight = &v
				}
				if override.Images.Resize.MaxBytes != nil {
					v := *override.Images.Resize.MaxBytes
					res.MaxBytes = &v
				}
				if override.Images.Resize.JpegQuality != nil {
					v := *override.Images.Resize.JpegQuality
					res.JpegQuality = &v
				}
				img.Resize = res
			}
		}
		out.Images = img
	}
	return out
}

func pcMergeSamplingParams(base, override *jsonx.Object) *jsonx.Object {
	if override == nil {
		return base
	}
	out := jsonx.NewObject()
	if base != nil {
		out.Assign(base)
	}
	out.Assign(override)
	return out
}

func pcMergeSamplingParamsByThinkingLevel(base, override *ai.SamplingParamsByThinkingLevel) *ai.SamplingParamsByThinkingLevel {
	if override == nil {
		return base
	}
	out := &ai.SamplingParamsByThinkingLevel{}
	if base != nil {
		*out = *base
	}
	out.Off = pcMergeSamplingParams(out.Off, override.Off)
	out.Minimal = pcMergeSamplingParams(out.Minimal, override.Minimal)
	out.Low = pcMergeSamplingParams(out.Low, override.Low)
	out.Medium = pcMergeSamplingParams(out.Medium, override.Medium)
	out.High = pcMergeSamplingParams(out.High, override.High)
	out.Xhigh = pcMergeSamplingParams(out.Xhigh, override.Xhigh)
	out.Max = pcMergeSamplingParams(out.Max, override.Max)
	return out
}

func pcMergeThinkingLevelMap(base, override *ai.ThinkingLevelMap) *ai.ThinkingLevelMap {
	if override == nil {
		return base
	}
	out := &ai.ThinkingLevelMap{}
	if base != nil {
		*out = *base
	}
	if !override.Off.IsZero() || override.Off.IsNull() {
		out.Off = override.Off
	}
	if !override.Minimal.IsZero() || override.Minimal.IsNull() {
		out.Minimal = override.Minimal
	}
	if !override.Low.IsZero() || override.Low.IsNull() {
		out.Low = override.Low
	}
	if !override.Medium.IsZero() || override.Medium.IsNull() {
		out.Medium = override.Medium
	}
	if !override.High.IsZero() || override.High.IsNull() {
		out.High = override.High
	}
	if !override.Xhigh.IsZero() || override.Xhigh.IsNull() {
		out.Xhigh = override.Xhigh
	}
	if !override.Max.IsZero() || override.Max.IsNull() {
		out.Max = override.Max
	}
	return out
}

func pcApplyModelOverride(model *ai.Model, override ModelsJsonModelOverride) *ai.Model {
	out := *model
	if override.Name != nil {
		out.Name = *override.Name
	}
	if override.Reasoning != nil {
		out.Reasoning = *override.Reasoning
	}
	if override.ThinkingLevelMap != nil {
		out.ThinkingLevelMap = pcMergeThinkingLevelMap(model.ThinkingLevelMap, override.ThinkingLevelMap)
	}
	if override.Input != nil {
		out.Input = slices.Clone(override.Input)
	}
	out.InputLimits = pcMergeInputLimits(model.InputLimits, override.InputLimits)
	if override.Cost != nil {
		cost := model.Cost
		if override.Cost.Input != nil {
			cost.Input = *override.Cost.Input
		}
		if override.Cost.Output != nil {
			cost.Output = *override.Cost.Output
		}
		if override.Cost.CacheRead != nil {
			cost.CacheRead = *override.Cost.CacheRead
		}
		if override.Cost.CacheWrite != nil {
			cost.CacheWrite = *override.Cost.CacheWrite
		}
		if override.Cost.Tiers != nil {
			cost.Tiers = slices.Clone(override.Cost.Tiers)
		}
		out.Cost = cost
	}
	if override.PromptCache != nil {
		pc := &ai.ModelPromptCache{}
		if model.PromptCache != nil {
			*pc = *model.PromptCache
		}
		if override.PromptCache.Short != nil {
			v := *override.PromptCache.Short
			pc.Short = &v
		}
		if override.PromptCache.Long != nil {
			v := *override.PromptCache.Long
			pc.Long = &v
		}
		out.PromptCache = pc
	}
	if override.ContextWindow != nil {
		out.ContextWindow = *override.ContextWindow
	}
	if override.MaxTokens != nil {
		out.MaxTokens = *override.MaxTokens
	}
	if override.SamplingParams != nil {
		out.SamplingParams = pcMergeSamplingParams(model.SamplingParams, override.SamplingParams)
	}
	out.SamplingParamsByThinkingLevel = pcMergeSamplingParamsByThinkingLevel(model.SamplingParamsByThinkingLevel, override.SamplingParamsByThinkingLevel)
	out.Compat = pcMergeCompatValue(out.Api, model.Compat, override.Compat)
	return &out
}

func pcModelFromJson(providerId string, def ModelsJsonModel, providerConfig *ModelsJsonProvider, defaults *ai.Model) (*ai.Model, error) {
	var api string
	if def.Api != nil {
		api = *def.Api
	} else if providerConfig != nil && providerConfig.Api != nil {
		api = *providerConfig.Api
	} else if defaults != nil {
		api = defaults.Api
	}
	if api == "" {
		return nil, fmt.Errorf("Provider %s, model %s: no \"api\" specified. Set at provider or model level.", providerId, def.Id)
	}

	var baseUrl string
	if def.BaseUrl != nil {
		baseUrl = *def.BaseUrl
	} else if providerConfig != nil && providerConfig.BaseUrl != nil {
		baseUrl = *providerConfig.BaseUrl
	} else if defaults != nil {
		baseUrl = defaults.BaseUrl
	}
	if baseUrl == "" {
		return nil, fmt.Errorf("Provider %s: \"baseUrl\" is required when defining custom models.", providerId)
	}
	if def.ContextWindow != nil && *def.ContextWindow <= 0 {
		return nil, fmt.Errorf("Provider %s, model %s: invalid contextWindow", providerId, def.Id)
	}
	if def.MaxTokens != nil && *def.MaxTokens <= 0 {
		return nil, fmt.Errorf("Provider %s, model %s: invalid maxTokens", providerId, def.Id)
	}

	name := def.Id
	if def.Name != nil {
		name = *def.Name
	}
	reasoning := false
	if def.Reasoning != nil {
		reasoning = *def.Reasoning
	}
	input := []ai.ModelModality{ai.ModelModalityText}
	if def.Input != nil {
		input = slices.Clone(def.Input)
	}
	cost := ai.ModelCost{}
	if def.Cost != nil {
		cost = *def.Cost
	}
	contextWindow := 128000
	if def.ContextWindow != nil {
		contextWindow = *def.ContextWindow
	}
	maxTokens := 16384
	if def.MaxTokens != nil {
		maxTokens = *def.MaxTokens
	}
	var providerCompat *ai.ProviderCompat
	if providerConfig != nil {
		providerCompat = providerConfig.Compat
	}
	compatVal := pcMergeCompatValue(api, ai.ModelCompatValue{}, providerCompat)
	compatVal = pcMergeCompatValue(api, compatVal, def.Compat)

	return &ai.Model{
		BaseModel: ai.BaseModel{
			Id:          def.Id,
			Name:        name,
			Api:         api,
			Provider:    ai.ProviderId(providerId),
			BaseUrl:     baseUrl,
			Input:       input,
			InputLimits: def.InputLimits,
			Cost:        cost,
		},
		Reasoning:                     reasoning,
		ThinkingLevelMap:              def.ThinkingLevelMap,
		PromptCache:                   def.PromptCache,
		ContextWindow:                 contextWindow,
		MaxTokens:                     maxTokens,
		SamplingParams:                def.SamplingParams,
		SamplingParamsByThinkingLevel: def.SamplingParamsByThinkingLevel,
		Compat:                        compatVal,
	}, nil
}

func pcFindModelDefaults(models []ai.AnyModel, modelId string, api string) *ai.Model {
	var chatModels []*ai.Model
	for _, m := range models {
		if ai.IsModelType(m, ai.ModelTypeChat) {
			if cm, ok := m.(*ai.Model); ok && cm != nil {
				chatModels = append(chatModels, cm)
			}
		}
	}
	for _, m := range chatModels {
		if m.Id == modelId {
			return m
		}
	}
	if api != "" {
		for _, m := range chatModels {
			if m.Api == api {
				return m
			}
		}
	}
	for _, m := range chatModels {
		if m.Api == string(ai.KnownApiOpenaiCompletions) {
			return m
		}
	}
	if len(chatModels) > 0 {
		return chatModels[0]
	}
	return nil
}

func pcAnyModelMeta(m ai.AnyModel) (id, api, baseUrl string) {
	switch x := m.(type) {
	case *ai.Model:
		if x != nil {
			return x.Id, x.Api, x.BaseUrl
		}
	case *ai.ImageModel:
		if x != nil {
			return x.Id, x.Api, x.BaseUrl
		}
	case *ai.ClassifierModel:
		if x != nil {
			return x.Id, x.Api, x.BaseUrl
		}
	}
	return "", "", ""
}

func pcWithModelBaseUrl(m ai.AnyModel, baseUrl string) ai.AnyModel {
	switch x := m.(type) {
	case *ai.Model:
		c := *x
		c.BaseUrl = baseUrl
		return &c
	case *ai.ImageModel:
		c := *x
		c.BaseUrl = baseUrl
		return &c
	case *ai.ClassifierModel:
		c := *x
		c.BaseUrl = baseUrl
		return &c
	}
	return m
}

func pcFindExtensionModelDefaults(models []ai.AnyModel, def ProviderModelConfig) (defApi, defBaseUrl string) {
	typ := def.modelConfigType()
	var candidates []ai.AnyModel
	for _, m := range models {
		if ai.IsModelType(m, typ) {
			candidates = append(candidates, m)
		}
	}
	for _, m := range candidates {
		id, api, baseUrl := pcAnyModelMeta(m)
		if id == def.modelConfigId() {
			return api, baseUrl
		}
	}
	if def.modelConfigApi() != nil && *def.modelConfigApi() != "" {
		for _, m := range candidates {
			_, api, baseUrl := pcAnyModelMeta(m)
			if api == *def.modelConfigApi() {
				return api, baseUrl
			}
		}
	}
	if typ == ai.ModelTypeChat {
		for _, m := range candidates {
			_, api, baseUrl := pcAnyModelMeta(m)
			if api == string(ai.KnownApiOpenaiCompletions) {
				return api, baseUrl
			}
		}
	}
	if len(candidates) > 0 {
		_, api, baseUrl := pcAnyModelMeta(candidates[0])
		return api, baseUrl
	}
	return "", ""
}

func pcExtensionModelFromDefinition(providerId string, models []ai.AnyModel, cfg *ProviderConfig, def ProviderModelConfig) (ai.AnyModel, error) {
	typ := def.modelConfigType()
	defApi, defBaseUrl := pcFindExtensionModelDefaults(models, def)

	var api string
	if def.modelConfigApi() != nil && *def.modelConfigApi() != "" {
		api = *def.modelConfigApi()
	} else if typ == ai.ModelTypeChat && cfg != nil && cfg.Api != nil && *cfg.Api != "" {
		api = *cfg.Api
	} else {
		api = defApi
	}
	if api == "" {
		suffix := ""
		if typ == ai.ModelTypeChat {
			suffix = " or provider level"
		}
		return nil, fmt.Errorf("Provider %s, model %s: no \"api\" specified. Set it at model level%s.", providerId, def.modelConfigId(), suffix)
	}

	var baseUrl string
	if def.modelConfigBaseUrl() != nil && *def.modelConfigBaseUrl() != "" {
		baseUrl = *def.modelConfigBaseUrl()
	} else if cfg != nil && cfg.BaseUrl != nil && *cfg.BaseUrl != "" {
		baseUrl = *cfg.BaseUrl
	} else {
		baseUrl = defBaseUrl
	}
	if baseUrl == "" {
		return nil, fmt.Errorf("Provider %s: \"baseUrl\" is required when defining custom models.", providerId)
	}

	switch d := def.(type) {
	case *ProviderImageModelConfig:
		return &ai.ImageModel{
			BaseModel: ai.BaseModel{
				Id:          d.Id,
				Name:        d.Name,
				Api:         api,
				Provider:    ai.ProviderId(providerId),
				BaseUrl:     baseUrl,
				Input:       slices.Clone(d.Input),
				InputLimits: d.InputLimits,
				Cost:        d.Cost,
			},
			Type:   ai.ModelTypeImage,
			Output: slices.Clone(d.Output),
		}, nil
	case *ProviderClassifierModelConfig:
		return &ai.ClassifierModel{
			BaseModel: ai.BaseModel{
				Id:          d.Id,
				Name:        d.Name,
				Api:         api,
				Provider:    ai.ProviderId(providerId),
				BaseUrl:     baseUrl,
				Input:       slices.Clone(d.Input),
				InputLimits: d.InputLimits,
				Cost:        d.Cost,
			},
			Type:          ai.ModelTypeClassifier,
			ContextWindow: d.ContextWindow,
			Compat:        d.Compat,
		}, nil
	case *ProviderChatModelConfig:
		return &ai.Model{
			BaseModel: ai.BaseModel{
				Id:          d.Id,
				Name:        d.Name,
				Api:         api,
				Provider:    ai.ProviderId(providerId),
				BaseUrl:     baseUrl,
				Input:       slices.Clone(d.Input),
				InputLimits: d.InputLimits,
				Cost:        d.Cost,
			},
			Reasoning:                     d.Reasoning,
			ThinkingLevelMap:              d.ThinkingLevelMap,
			PromptCache:                   d.PromptCache,
			ContextWindow:                 d.ContextWindow,
			MaxTokens:                     d.MaxTokens,
			SamplingParams:                d.SamplingParams,
			SamplingParamsByThinkingLevel: d.SamplingParamsByThinkingLevel,
			Compat:                        d.Compat,
		}, nil
	default:
		return nil, fmt.Errorf("unknown ProviderModelConfig variant")
	}
}

func pcApplyModelsJson(providerId string, baseModels []ai.AnyModel, cfg *ModelsJsonProvider) ([]ai.AnyModel, error) {
	if cfg == nil {
		return slices.Clone(baseModels), nil
	}
	if cfg.OAuth != nil && (cfg.BaseUrl == nil || *cfg.BaseUrl == "") {
		return nil, fmt.Errorf("Provider %s: \"baseUrl\" is required when \"oauth\" is set.", providerId)
	}
	hasOverrides := cfg.ModelOverrides != nil && cfg.ModelOverrides.Len() > 0
	hasHeaders := cfg.Headers != nil && cfg.Headers.Len() > 0
	if len(cfg.Models) == 0 &&
		cfg.BaseUrl == nil &&
		!hasHeaders &&
		cfg.Compat == nil &&
		!hasOverrides &&
		cfg.ApiKey == nil &&
		cfg.OAuth == nil &&
		cfg.AuthHeader == nil {
		return nil, fmt.Errorf("Provider %s: must specify \"baseUrl\", \"headers\", \"compat\", \"modelOverrides\", or \"models\".", providerId)
	}

	models := make([]ai.AnyModel, 0, len(baseModels)+len(cfg.Models))
	for _, model := range baseModels {
		_, _, origBaseUrl := pcAnyModelMeta(model)
		baseUrl := origBaseUrl
		if (cfg.OAuth == nil || *cfg.OAuth != "radius") && cfg.BaseUrl != nil {
			baseUrl = *cfg.BaseUrl
		}
		switch m := model.(type) {
		case *ai.Model:
			copyM := *m
			copyM.BaseUrl = baseUrl
			copyM.Compat = pcMergeCompatValue(copyM.Api, m.Compat, cfg.Compat)
			models = append(models, &copyM)
		case *ai.ClassifierModel:
			copyM := *m
			copyM.BaseUrl = baseUrl
			if cfg.Compat != nil {
				copyM.Compat = pcMergeClassifierCompatValue(copyM.Api, m.Compat, cfg.Compat)
			}
			models = append(models, &copyM)
		default:
			models = append(models, pcWithModelBaseUrl(model, baseUrl))
		}
	}

	for _, def := range cfg.Models {
		existingIdx := -1
		for i, m := range models {
			if ai.IsModelType(m, ai.ModelTypeChat) {
				if cm, ok := m.(*ai.Model); ok && cm.Id == def.Id {
					existingIdx = i
					break
				}
			}
		}
		var providerApi string
		if def.Api != nil {
			providerApi = *def.Api
		} else if cfg.Api != nil {
			providerApi = *cfg.Api
		}
		defaults := pcFindModelDefaults(models, def.Id, providerApi)
		model, err := pcModelFromJson(providerId, def, cfg, defaults)
		if err != nil {
			return nil, err
		}
		if existingIdx >= 0 {
			models[existingIdx] = model
		} else {
			models = append(models, model)
		}
	}
	return models, nil
}

func pcApplyExtension(providerId string, models []ai.AnyModel, cfg *ProviderConfig) ([]ai.AnyModel, error) {
	if cfg == nil {
		return slices.Clone(models), nil
	}
	if cfg.Models == nil {
		if cfg.BaseUrl != nil {
			out := make([]ai.AnyModel, len(models))
			for i, m := range models {
				out[i] = pcWithModelBaseUrl(m, *cfg.BaseUrl)
			}
			return out, nil
		}
		return slices.Clone(models), nil
	}
	out := make([]ai.AnyModel, 0, len(cfg.Models))
	for _, def := range cfg.Models {
		m, err := pcExtensionModelFromDefinition(providerId, models, cfg, def)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

type pcOAuthCallbacksAdapter struct {
	ctx         context.Context
	interaction ai.AuthInteraction
}

func (a *pcOAuthCallbacksAdapter) OnAuth(info *compat.OAuthAuthInfo) {
	if a.interaction == nil || info == nil {
		return
	}
	a.interaction.Notify(&ai.AuthEventAuthUrl{
		Type:         ai.AuthEventKindAuthUrl,
		Url:          info.Url,
		Instructions: info.Instructions,
	})
}

func (a *pcOAuthCallbacksAdapter) OnDeviceCode(info *compat.OAuthDeviceCodeInfo) {
	if a.interaction == nil || info == nil {
		return
	}
	a.interaction.Notify(&ai.AuthEventDeviceCode{
		Type:                 ai.AuthEventKindDeviceCode,
		UserCode:             info.UserCode,
		VerificationUri:      info.VerificationUri,
		OpenBrowserOnConfirm: info.OpenBrowserOnConfirm,
		CopyCodeOnConfirm:    info.CopyCodeOnConfirm,
		IntervalSeconds:      info.IntervalSeconds,
		ExpiresInSeconds:     info.ExpiresInSeconds,
	})
}

func (a *pcOAuthCallbacksAdapter) OnPrompt(ctx context.Context, prompt *compat.OAuthPrompt) (string, error) {
	if a.interaction == nil {
		return "", errors.New("no auth interaction available")
	}
	var msg string
	var placeholder *string
	if prompt != nil {
		msg = prompt.Message
		placeholder = prompt.Placeholder
	}
	return a.interaction.Prompt(ctx, &ai.AuthPromptText{
		Type:          ai.AuthPromptKindText,
		Message:       msg,
		Placeholder:   placeholder,
		PromptContext: ctx,
	})
}

func (a *pcOAuthCallbacksAdapter) OnProgress(message string) {
	if a.interaction == nil {
		return
	}
	a.interaction.Notify(&ai.AuthEventProgress{
		Type:    ai.AuthEventKindProgress,
		Message: message,
	})
}

func (a *pcOAuthCallbacksAdapter) OnManualCodeInput(ctx context.Context) (string, error) {
	if a.interaction == nil {
		return "", errors.New("no auth interaction available")
	}
	return a.interaction.Prompt(ctx, &ai.AuthPromptManualCode{
		Type:          ai.AuthPromptKindManualCode,
		Message:       "Paste the authorization code",
		PromptContext: ctx,
	})
}

func (a *pcOAuthCallbacksAdapter) OnSelect(ctx context.Context, prompt *compat.OAuthSelectPrompt) (*string, error) {
	if a.interaction == nil {
		return nil, errors.New("no auth interaction available")
	}
	var msg string
	var opts []ai.AuthSelectOption
	if prompt != nil {
		msg = prompt.Message
		opts = make([]ai.AuthSelectOption, len(prompt.Options))
		for i, o := range prompt.Options {
			opts[i] = ai.AuthSelectOption{Id: o.Id, Label: o.Label}
		}
	}
	choice, err := a.interaction.Prompt(ctx, &ai.AuthPromptSelect{
		Type:          ai.AuthPromptKindSelect,
		Message:       msg,
		Options:       opts,
		PromptContext: ctx,
	})
	if err != nil {
		return nil, err
	}
	if choice == "" {
		return nil, nil
	}
	return &choice, nil
}

func pcAdaptOAuth(config *ExtensionOAuthConfig) *ai.OAuthAuth {
	if config == nil {
		return nil
	}
	return &ai.OAuthAuth{
		Name:           config.Name,
		IsSubscription: config.IsSubscription,
		Login: func(ctx context.Context, interaction ai.AuthInteraction, _ *ai.LoginOptions) (*ai.OAuthCredential, error) {
			creds, err := config.Login(ctx, &pcOAuthCallbacksAdapter{ctx: ctx, interaction: interaction})
			if err != nil {
				return nil, err
			}
			return &ai.OAuthCredential{
				Type:             ai.AuthTypeOAuth,
				OAuthCredentials: creds,
			}, nil
		},
		Refresh: func(ctx context.Context, credential *ai.OAuthCredential) (*ai.OAuthCredential, error) {
			if credential == nil {
				return nil, errors.New("missing OAuth credential")
			}
			creds, err := config.RefreshToken(ctx, credential.OAuthCredentials)
			if err != nil {
				return nil, err
			}
			return &ai.OAuthCredential{
				Type:             ai.AuthTypeOAuth,
				OAuthCredentials: creds,
			}, nil
		},
		ToAuth: func(_ context.Context, credential *ai.OAuthCredential) (ai.ModelAuth, error) {
			if credential == nil {
				return ai.ModelAuth{}, errors.New("missing OAuth credential")
			}
			key := config.GetApiKey(credential.OAuthCredentials)
			return ai.ModelAuth{ApiKey: &key}, nil
		},
	}
}

func pcWithConfiguredAuth(auth ai.ModelAuth, headers *omap.Map[string, string], authHeader bool) (ai.ModelAuth, error) {
	var mergedHeaders ai.ProviderHeaders
	if (auth.Headers != nil && auth.Headers.Len() > 0) || (headers != nil && headers.Len() > 0) {
		mergedHeaders = omap.NewMap[string, *string]()
		if auth.Headers != nil {
			for k, v := range auth.Headers.All() {
				mergedHeaders.Set(k, v)
			}
		}
		if headers != nil {
			for k, v := range headers.All() {
				val := v
				mergedHeaders.Set(k, &val)
			}
		}
	}
	if authHeader {
		if auth.ApiKey == nil || *auth.ApiKey == "" {
			return ai.ModelAuth{}, errors.New("authHeader requires a resolved API key")
		}
		if mergedHeaders == nil {
			mergedHeaders = omap.NewMap[string, *string]()
		}
		bearer := "Bearer " + *auth.ApiKey
		mergedHeaders.Set("Authorization", &bearer)
	}
	out := auth
	out.Headers = mergedHeaders
	return out, nil
}

func pcConfiguredApiKey(cfg *ModelsJsonProvider, ext *ProviderConfig) *string {
	if ext != nil && ext.ApiKey != nil {
		return ext.ApiKey
	}
	if cfg != nil {
		return cfg.ApiKey
	}
	return nil
}

func pcConfiguredHeaders(cfg *ModelsJsonProvider, ext *ProviderConfig) *omap.Map[string, string] {
	hasCfg := cfg != nil && cfg.Headers != nil && cfg.Headers.Len() > 0
	hasExt := ext != nil && ext.Headers != nil && ext.Headers.Len() > 0
	if !hasCfg && !hasExt {
		return nil
	}
	out := omap.NewMap[string, string]()
	if hasCfg {
		for k, v := range cfg.Headers.All() {
			out.Set(k, v)
		}
	}
	if hasExt {
		for k, v := range ext.Headers.All() {
			out.Set(k, v)
		}
	}
	return out
}

func pcConfigContextEnv(ctx context.Context, values []string, authCtx ai.AuthContext, explicit map[string]string) (map[string]string, error) {
	env := make(map[string]string, len(explicit))
	for k, v := range explicit {
		env[k] = v
	}
	seen := make(map[string]struct{})
	for _, value := range values {
		for _, name := range GetConfigValueEnvVarNames(value) {
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			if _, ok := env[name]; ok {
				continue
			}
			if authCtx != nil {
				resolved, err := authCtx.Env(ctx, name)
				if err != nil {
					return nil, err
				}
				if resolved != nil {
					env[name] = *resolved
				}
			}
		}
	}
	if len(env) == 0 {
		return nil, nil
	}
	return env, nil
}

func pcResolveHeadersOrNil(headers *omap.Map[string, string], desc string, env map[string]string) (*omap.Map[string, string], error) {
	if headers == nil || headers.Len() == 0 {
		return nil, nil
	}
	if env == nil {
		return ResolveHeadersOrThrow(headers, desc)
	}
	return ResolveHeadersOrThrow(headers, desc, env)
}

func pcComposeApiKeyAuth(providerId string, base *ai.Provider, cfg *ModelsJsonProvider, ext *ProviderConfig) *ai.ApiKeyAuth {
	var inherited *ai.ApiKeyAuth
	var baseOAuth *ai.OAuthAuth
	if base != nil {
		inherited = base.Auth.ApiKey
		baseOAuth = base.Auth.OAuth
	}
	rawKey := pcConfiguredApiKey(cfg, ext)
	var hasOAuth bool
	if ext != nil && ext.OAuth != nil {
		hasOAuth = true
	} else if baseOAuth != nil {
		hasOAuth = true
	}
	if inherited == nil && rawKey == nil && hasOAuth {
		return nil
	}
	rawHeaders := pcConfiguredHeaders(cfg, ext)
	authHeader := false
	if ext != nil && ext.AuthHeader != nil {
		authHeader = *ext.AuthHeader
	} else if cfg != nil && cfg.AuthHeader != nil {
		authHeader = *cfg.AuthHeader
	}

	name := "API key"
	if inherited != nil && inherited.Name != "" {
		name = inherited.Name
	}
	var loginFn func(ctx context.Context, interaction ai.AuthInteraction) (*ai.ApiKeyCredential, error)
	if inherited != nil && inherited.Login != nil {
		loginFn = inherited.Login
	} else {
		loginFn = func(ctx context.Context, interaction ai.AuthInteraction) (*ai.ApiKeyCredential, error) {
			key, err := interaction.Prompt(ctx, &ai.AuthPromptSecret{
				Type:          ai.AuthPromptKindSecret,
				Message:       "Enter API key",
				PromptContext: ctx,
			})
			if err != nil {
				return nil, err
			}
			return &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Key: &key}, nil
		}
	}

	return &ai.ApiKeyAuth{
		Name:  name,
		Login: loginFn,
		Check: func(ctx context.Context, input ai.ApiKeyCheckInput) (*ai.AuthCheck, error) {
			if input.Credential != nil {
				if inherited != nil && inherited.Check != nil {
					return inherited.Check(ctx, input)
				}
				if input.Credential.Key != nil && *input.Credential.Key != "" {
					src := "stored credential"
					return &ai.AuthCheck{Type: ai.AuthTypeApiKey, Source: &src}, nil
				}
				if inherited != nil && inherited.Resolve != nil {
					resolved, err := inherited.Resolve(ctx, ai.ApiKeyResolveInput{Ctx: input.Ctx, Credential: input.Credential})
					if err != nil || resolved == nil {
						return nil, err
					}
					return &ai.AuthCheck{Type: ai.AuthTypeApiKey, Source: resolved.Source}, nil
				}
				return nil, nil
			}
			if rawKey != nil {
				src := "configured API key"
				if IsCommandConfigValue(*rawKey) {
					return &ai.AuthCheck{Type: ai.AuthTypeApiKey, Source: &src}, nil
				}
				envNames := GetConfigValueEnvVarNames(*rawKey)
				for _, envName := range envNames {
					if input.Ctx == nil {
						return nil, nil
					}
					val, err := input.Ctx.Env(ctx, envName)
					if err != nil {
						return nil, err
					}
					if val == nil {
						return nil, nil
					}
				}
				return &ai.AuthCheck{Type: ai.AuthTypeApiKey, Source: &src}, nil
			}
			if inherited != nil && inherited.Check != nil {
				return inherited.Check(ctx, input)
			}
			if inherited != nil && inherited.Resolve != nil {
				resolved, err := inherited.Resolve(ctx, ai.ApiKeyResolveInput{Ctx: input.Ctx, Credential: input.Credential})
				if err != nil || resolved == nil {
					return nil, err
				}
				return &ai.AuthCheck{Type: ai.AuthTypeApiKey, Source: resolved.Source}, nil
			}
			return nil, nil
		},
		Resolve: func(ctx context.Context, input ai.ApiKeyResolveInput) (*ai.AuthResult, error) {
			var result *ai.AuthResult
			var err error
			if input.Credential != nil {
				if inherited != nil && inherited.Resolve != nil {
					result, err = inherited.Resolve(ctx, input)
					if err != nil {
						return nil, err
					}
				} else if input.Credential.Key != nil && *input.Credential.Key != "" {
					src := "stored credential"
					result = &ai.AuthResult{
						Auth:   ai.ModelAuth{ApiKey: input.Credential.Key},
						Env:    input.Credential.Env,
						Source: &src,
					}
				}
			} else if rawKey != nil {
				envMap, envErr := pcConfigContextEnv(ctx, []string{*rawKey}, input.Ctx, nil)
				if envErr != nil {
					return nil, envErr
				}
				key, keyErr := ResolveConfigValueOrThrow(*rawKey, fmt.Sprintf("API key for provider %q", providerId), envMap)
				if keyErr != nil {
					return nil, keyErr
				}
				if inherited != nil && inherited.Resolve != nil {
					result, err = inherited.Resolve(ctx, ai.ApiKeyResolveInput{
						Ctx:        input.Ctx,
						Credential: &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Key: &key},
					})
					if err != nil {
						return nil, err
					}
				} else {
					src := "configured API key"
					result = &ai.AuthResult{
						Auth:   ai.ModelAuth{ApiKey: &key},
						Source: &src,
					}
				}
			} else if inherited != nil && inherited.Resolve != nil {
				result, err = inherited.Resolve(ctx, input)
				if err != nil {
					return nil, err
				}
			}
			if result == nil {
				return nil, nil
			}

			explicitEnv := make(map[string]string)
			if input.Credential != nil {
				for k, v := range input.Credential.Env {
					explicitEnv[k] = v
				}
			}
			for k, v := range result.Env {
				explicitEnv[k] = v
			}
			var rawHeaderVals []string
			if rawHeaders != nil {
				rawHeaderVals = rawHeaders.Values()
			}
			headerEnv, err := pcConfigContextEnv(ctx, rawHeaderVals, input.Ctx, explicitEnv)
			if err != nil {
				return nil, err
			}
			resolvedHeaders, err := pcResolveHeadersOrNil(rawHeaders, fmt.Sprintf("provider %q", providerId), headerEnv)
			if err != nil {
				return nil, err
			}
			nextAuth, err := pcWithConfiguredAuth(result.Auth, resolvedHeaders, authHeader)
			if err != nil {
				return nil, err
			}
			out := *result
			out.Auth = nextAuth
			return &out, nil
		},
	}
}

func pcComposeOAuthAuth(providerId string, base *ai.Provider, cfg *ModelsJsonProvider, ext *ProviderConfig) *ai.OAuthAuth {
	var oauth *ai.OAuthAuth
	if ext != nil && ext.OAuth != nil {
		oauth = pcAdaptOAuth(ext.OAuth)
	} else if base != nil {
		oauth = base.Auth.OAuth
	}
	if oauth == nil {
		return nil
	}
	rawHeaders := pcConfiguredHeaders(cfg, ext)
	authHeader := false
	if ext != nil && ext.AuthHeader != nil {
		authHeader = *ext.AuthHeader
	} else if cfg != nil && cfg.AuthHeader != nil {
		authHeader = *cfg.AuthHeader
	}

	out := *oauth
	origToAuth := oauth.ToAuth
	out.ToAuth = func(ctx context.Context, credential *ai.OAuthCredential) (ai.ModelAuth, error) {
		auth, err := origToAuth(ctx, credential)
		if err != nil {
			return ai.ModelAuth{}, err
		}
		var envMap map[string]string
		if credential != nil && credential.Extra != nil {
			if rawEnv, ok := credential.Extra.Get("env"); ok && rawEnv != nil {
				switch eo := rawEnv.(type) {
				case *jsonx.Object:
					if eo != nil && eo.Len() > 0 {
						envMap = make(map[string]string, eo.Len())
						for k, v := range eo.All() {
							if s, ok := v.(string); ok {
								envMap[k] = s
							}
						}
					}
				case jsonx.Object:
					if eo.Len() > 0 {
						envMap = make(map[string]string, eo.Len())
						for k, v := range eo.All() {
							if s, ok := v.(string); ok {
								envMap[k] = s
							}
						}
					}
				case map[string]any:
					if len(eo) > 0 {
						envMap = make(map[string]string, len(eo))
						for k, v := range eo {
							if s, ok := v.(string); ok {
								envMap[k] = s
							}
						}
					}
				}
			}
		}
		headers, err := pcResolveHeadersOrNil(rawHeaders, fmt.Sprintf("provider %q", providerId), envMap)
		if err != nil {
			return ai.ModelAuth{}, err
		}
		return pcWithConfiguredAuth(auth, headers, authHeader)
	}
	return &out
}

func pcRawModelHeaders(model ai.AnyModel, cfg *ModelsJsonProvider, ext *ProviderConfig) *omap.Map[string, string] {
	id, _, _ := pcAnyModelMeta(model)
	typ := ai.GetModelType(model)

	var overrideHeaders *omap.Map[string, string]
	var chatDefHeaders *omap.Map[string, string]
	if typ == ai.ModelTypeChat && cfg != nil {
		if cfg.ModelOverrides != nil {
			if ov, ok := cfg.ModelOverrides.Get(id); ok {
				overrideHeaders = ov.Headers
			}
		}
		for _, entry := range cfg.Models {
			if entry.Id == id {
				chatDefHeaders = entry.Headers
				break
			}
		}
	}
	var extHeaders *omap.Map[string, string]
	if ext != nil {
		for _, entry := range ext.Models {
			if entry.modelConfigType() == typ && entry.modelConfigId() == id {
				extHeaders = entry.modelConfigHeaders()
				break
			}
		}
	}

	out := omap.NewMap[string, string]()
	for _, m := range []*omap.Map[string, string]{overrideHeaders, chatDefHeaders, extHeaders} {
		if m != nil {
			for k, v := range m.All() {
				out.Set(k, v)
			}
		}
	}
	if out.Len() == 0 {
		return nil
	}
	return out
}

// ValidateExtensionProvider validates a provider registration before composing it into the runtime.
func ValidateExtensionProvider(providerId string, base *ai.Provider, modelsConfig *ModelsJsonProvider, extension *ProviderConfig) error {
	if extension != nil && extension.StreamSimple != nil && (extension.Api == nil || *extension.Api == "") {
		return fmt.Errorf("Provider %s: \"api\" is required when registering streamSimple.", providerId)
	}
	appliedJson, err := pcApplyModelsJson(providerId, pcGetAllProviderModels(base), modelsConfig)
	if err != nil {
		return err
	}
	_, err = pcApplyExtension(providerId, appliedJson, extension)
	return err
}

// ComposeModelProvider composes built-in, models.json, and extension layers without reading credentials.
func ComposeModelProvider(providerId string, base *ai.Provider, modelConfig *ModelConfig, extension *ProviderConfig) (*ai.Provider, error) {
	var cfg *ModelsJsonProvider
	if modelConfig != nil {
		cfg = modelConfig.GetProvider(providerId)
	}

	var extOAuthCred *ai.OAuthCredentials
	var refreshedExtModels []ProviderModelConfig

	currentExtension := func() *ProviderConfig {
		if extension != nil && refreshedExtModels != nil {
			copyExt := *extension
			copyExt.Models = refreshedExtModels
			return &copyExt
		}
		return extension
	}

	getAllModels := func() ([]ai.AnyModel, error) {
		appliedJson, err := pcApplyModelsJson(providerId, pcGetAllProviderModels(base), cfg)
		if err != nil {
			return nil, err
		}
		models, err := pcApplyExtension(providerId, appliedJson, currentExtension())
		if err != nil {
			return nil, err
		}
		if extOAuthCred != nil && extension != nil && extension.OAuth != nil && extension.OAuth.ModifyModels != nil {
			var chatModels []*ai.Model
			var nonChat []ai.AnyModel
			for _, m := range models {
				if ai.IsModelType(m, ai.ModelTypeChat) {
					if cm, ok := m.(*ai.Model); ok && cm != nil {
						chatModels = append(chatModels, cm)
					}
				} else {
					nonChat = append(nonChat, m)
				}
			}
			modified := extension.OAuth.ModifyModels(chatModels, *extOAuthCred)
			nextModels := make([]ai.AnyModel, 0, len(modified)+len(nonChat))
			for _, cm := range modified {
				nextModels = append(nextModels, cm)
			}
			nextModels = append(nextModels, nonChat...)
			models = nextModels
		}
		if cfg != nil && cfg.ModelOverrides != nil && cfg.ModelOverrides.Len() > 0 {
			for i, m := range models {
				if ai.IsModelType(m, ai.ModelTypeChat) {
					if cm, ok := m.(*ai.Model); ok && cm != nil {
						if override, ok := cfg.ModelOverrides.Get(cm.Id); ok {
							models[i] = pcApplyModelOverride(cm, override)
						}
					}
				}
			}
		}
		return models, nil
	}

	if _, err := getAllModels(); err != nil {
		return nil, err
	}

	apiKey := pcComposeApiKeyAuth(providerId, base, cfg, extension)
	oauth := pcComposeOAuthAuth(providerId, base, cfg, extension)
	if apiKey == nil && oauth == nil {
		return nil, fmt.Errorf("Provider %s: no authentication method configured.", providerId)
	}

	supportsBaseApi := func(model *ai.Model) bool {
		if base == nil || base.GetModels == nil || model == nil {
			return false
		}
		for _, entry := range base.GetModels() {
			if entry.Api == model.Api {
				return true
			}
		}
		return false
	}

	streamWith := func(ctx context.Context, model *ai.Model, transcript *ai.TranscriptContext, options *ai.SimpleStreamOptions, apiOpts ai.ProviderStreamOptions, simple bool) *ai.AssistantMessageEventStream {
		return ai.LazyStream(model, func() (*ai.AssistantMessageEventStream, error) {
			if extension != nil && extension.StreamSimple != nil && extension.Api != nil && model.Api == *extension.Api {
				return extension.StreamSimple(ctx, model, transcript, options), nil
			}
			if base != nil && supportsBaseApi(model) {
				if simple && base.StreamSimple != nil {
					return base.StreamSimple(ctx, model, transcript, options), nil
				}
				if !simple && base.Stream != nil {
					return base.Stream(ctx, model, transcript, apiOpts), nil
				}
			}
			apiProvider := compat.GetApiProvider(model.Api)
			if apiProvider == nil {
				return nil, fmt.Errorf("No API provider registered for api: %s", model.Api)
			}
			if simple {
				return apiProvider.StreamSimple(ctx, model, transcript, options), nil
			}
			return apiProvider.Stream(ctx, model, transcript, apiOpts), nil
		}, nil, ctx)
	}

	name := providerId
	if extension != nil && extension.Name != nil && *extension.Name != "" {
		name = *extension.Name
	} else if cfg != nil && cfg.Name != nil && *cfg.Name != "" {
		name = *cfg.Name
	} else if base != nil && base.Name != "" {
		name = base.Name
	} else if extension != nil && extension.OAuth != nil && extension.OAuth.Name != "" {
		name = extension.OAuth.Name
	}

	var baseUrl *string
	if extension != nil && extension.BaseUrl != nil {
		baseUrl = extension.BaseUrl
	} else if cfg != nil && cfg.BaseUrl != nil {
		baseUrl = cfg.BaseUrl
	} else if base != nil {
		baseUrl = base.BaseUrl
	}

	var baseHeaders ai.ProviderHeaders
	if base != nil {
		baseHeaders = base.Headers
	}

	provider := &ai.Provider{
		Id:      providerId,
		Name:    name,
		BaseUrl: baseUrl,
		Headers: baseHeaders,
		Auth: ai.ProviderAuth{
			ApiKey: apiKey,
			OAuth:  oauth,
		},
		GetModels: func() []*ai.Model {
			all, err := getAllModels()
			if err != nil {
				return nil
			}
			var out []*ai.Model
			for _, m := range all {
				if ai.IsModelType(m, ai.ModelTypeChat) {
					if cm, ok := m.(*ai.Model); ok && cm != nil {
						out = append(out, cm)
					}
				}
			}
			return out
		},
		GetAllModels: func() []ai.AnyModel {
			all, err := getAllModels()
			if err != nil {
				return nil
			}
			return all
		},
		Stream: func(ctx context.Context, model *ai.Model, transcript *ai.TranscriptContext, opts ai.ProviderStreamOptions) *ai.AssistantMessageEventStream {
			var simpleOpts *ai.SimpleStreamOptions
			if opts != nil {
				simpleOpts = &ai.SimpleStreamOptions{StreamOptions: *opts.BaseStreamOptions()}
			}
			return streamWith(ctx, model, transcript, simpleOpts, opts, false)
		},
		StreamSimple: func(ctx context.Context, model *ai.Model, transcript *ai.TranscriptContext, opts *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
			var apiOpts ai.ProviderStreamOptions
			if opts != nil {
				apiOpts = &opts.StreamOptions
			}
			return streamWith(ctx, model, transcript, opts, apiOpts, true)
		},
	}

	if (extension != nil && (extension.RefreshModels != nil || (extension.OAuth != nil && extension.OAuth.ModifyModels != nil))) ||
		base == nil {
		provider.RefreshModelsAuth = ""
	} else {
		provider.RefreshModelsAuth = base.RefreshModelsAuth
	}

	if (base != nil && base.RefreshModels != nil) || (extension != nil && (extension.RefreshModels != nil || (extension.OAuth != nil && extension.OAuth.ModifyModels != nil))) {
		provider.RefreshModels = func(ctx context.Context, refresh *ai.RefreshModelsContext) error {
			if base != nil && base.RefreshModels != nil {
				if err := base.RefreshModels(ctx, refresh); err != nil {
					return err
				}
			}
			var refreshed []ProviderModelConfig
			if extension != nil && extension.RefreshModels != nil {
				var err error
				refreshed, err = extension.RefreshModels(ctx, refresh)
				if err != nil {
					return err
				}
			}
			if ctx != nil && ctx.Err() != nil {
				return nil
			}
			var oauthCredCopy *ai.OAuthCredentials
			if refresh != nil && refresh.Credential != nil {
				if oc, ok := refresh.Credential.(*ai.OAuthCredential); ok && oc != nil {
					c := oc.OAuthCredentials
					oauthCredCopy = &c
				}
			}
			var updateErr error
			_, err := refresh.Publish(&ai.ModelsPublication{
				Update: func() {
					if refreshed != nil {
						appliedJson, err := pcApplyModelsJson(providerId, pcGetAllProviderModels(base), cfg)
						if err != nil {
							updateErr = err
							return
						}
						extCopy := &ProviderConfig{Models: refreshed}
						if extension != nil {
							c := *extension
							c.Models = refreshed
							extCopy = &c
						}
						if _, err := pcApplyExtension(providerId, appliedJson, extCopy); err != nil {
							updateErr = err
							return
						}
						refreshedExtModels = refreshed
					}
					extOAuthCred = oauthCredCopy
				},
			})
			if updateErr != nil {
				return updateErr
			}
			return err
		}
	}

	if base != nil && base.FilterModels != nil {
		provider.FilterModels = func(models []*ai.Model, credential ai.Credential) []*ai.Model {
			return base.FilterModels(models, credential)
		}
	}
	if base != nil && base.FilterAllModels != nil {
		provider.FilterAllModels = func(models []ai.AnyModel, credential ai.Credential) []ai.AnyModel {
			return base.FilterAllModels(models, credential)
		}
	}
	if base != nil && base.FetchDeferred != nil {
		provider.FetchDeferred = func(ctx context.Context, model *ai.Model, handle *ai.DeferredHandle, opts *ai.DeferredFetchOptions) *ai.AssistantMessageEventStream {
			return base.FetchDeferred(ctx, model, handle, opts)
		}
	}
	if base != nil && base.CancelDeferred != nil {
		provider.CancelDeferred = func(ctx context.Context, model *ai.Model, handle *ai.DeferredHandle, opts *ai.DeferredCancelOptions) error {
			return base.CancelDeferred(ctx, model, handle, opts)
		}
	}
	var hasExtImages bool
	if extension != nil && len(extension.Images) > 0 {
		hasExtImages = true
	}
	if (base != nil && base.GenerateImages != nil) || hasExtImages {
		provider.GenerateImages = func(ctx context.Context, model *ai.ImageModel, c *ai.ImagesContext, opts *ai.ImagesOptions) (*ai.AssistantImages, error) {
			if extension != nil {
				if impl, ok := extension.Images[model.Api]; ok && impl.GenerateImages != nil {
					return impl.GenerateImages(ctx, model, c, opts)
				}
			}
			if base != nil && base.GenerateImages != nil {
				return base.GenerateImages(ctx, model, c, opts)
			}
			return ai.ImageErrorResult(model, fmt.Errorf("Provider %s has no image implementation for %q", providerId, model.Api)), nil
		}
	}

	var hasExtClassifiers bool
	if extension != nil && len(extension.Classifiers) > 0 {
		hasExtClassifiers = true
	}
	if (base != nil && base.Classify != nil) || hasExtClassifiers {
		provider.Classify = func(ctx context.Context, model *ai.ClassifierModel, c *ai.ClassifierContext, opts *ai.ClassifierOptions) (*ai.ClassifierResult, error) {
			if extension != nil {
				if impl, ok := extension.Classifiers[model.Api]; ok && impl.Classify != nil {
					return impl.Classify(ctx, model, c, opts)
				}
			}
			if base != nil && base.Classify != nil {
				return base.Classify(ctx, model, c, opts)
			}
			return ai.ClassifierErrorResult(model, fmt.Errorf("Provider %s has no classifier implementation for %q", providerId, model.Api)), nil
		}
	}

	return provider, nil
}

// ResolveConfiguredModelHeaders resolves model-level configured headers from models.json and extensions.
func ResolveConfiguredModelHeaders(model ai.AnyModel, cfg *ModelsJsonProvider, ext *ProviderConfig, env ...map[string]string) (*omap.Map[string, string], error) {
	raw := pcRawModelHeaders(model, cfg, ext)
	if raw == nil || raw.Len() == 0 {
		return nil, nil
	}
	_, _, _ = pcAnyModelMeta(model)
	id, _, _ := pcAnyModelMeta(model)
	desc := fmt.Sprintf("model %q", pcModelProviderAndId(model, id))
	return ResolveHeadersOrThrow(raw, desc, env...)
}

func pcModelProviderAndId(model ai.AnyModel, id string) string {
	switch m := model.(type) {
	case *ai.Model:
		if m != nil {
			return fmt.Sprintf("%s/%s", m.Provider, m.Id)
		}
	case *ai.ImageModel:
		if m != nil {
			return fmt.Sprintf("%s/%s", m.Provider, m.Id)
		}
	case *ai.ClassifierModel:
		if m != nil {
			return fmt.Sprintf("%s/%s", m.Provider, m.Id)
		}
	}
	return "/" + id
}

// CompatibilityRequestConfig holds effective headers and authHeader behavior for compatibility callers.
type CompatibilityRequestConfig struct {
	Headers    ai.ProviderHeaders `json:"headers,omitzero"`
	AuthHeader bool               `json:"authHeader"`
}

// ResolveCompatibilityRequestConfig computes compatibility headers and authHeader flag for a model.
func ResolveCompatibilityRequestConfig(model ai.AnyModel, cfg *ModelsJsonProvider, ext *ProviderConfig) (CompatibilityRequestConfig, error) {
	merged := omap.NewMap[string, string]()
	if provHeaders := pcConfiguredHeaders(cfg, ext); provHeaders != nil {
		for k, v := range provHeaders.All() {
			merged.Set(k, v)
		}
	}
	if modHeaders := pcRawModelHeaders(model, cfg, ext); modHeaders != nil {
		for k, v := range modHeaders.All() {
			merged.Set(k, v)
		}
	}
	id, _, _ := pcAnyModelMeta(model)
	var configured *omap.Map[string, string]
	if merged.Len() > 0 {
		var err error
		configured, err = ResolveHeadersOrThrow(merged, fmt.Sprintf("model %q", pcModelProviderAndId(model, id)))
		if err != nil {
			return CompatibilityRequestConfig{}, err
		}
	}

	var modelHeaders *omap.Map[string, string]
	switch m := model.(type) {
	case *ai.Model:
		if m != nil {
			modelHeaders = m.Headers
		}
	case *ai.ImageModel:
		if m != nil {
			modelHeaders = m.Headers
		}
	case *ai.ClassifierModel:
		if m != nil {
			modelHeaders = m.Headers
		}
	}

	var outHeaders ai.ProviderHeaders
	if (modelHeaders != nil && modelHeaders.Len() > 0) || (configured != nil && configured.Len() > 0) {
		outHeaders = omap.NewMap[string, *string]()
		if modelHeaders != nil {
			for k, v := range modelHeaders.All() {
				val := v
				outHeaders.Set(k, &val)
			}
		}
		if configured != nil {
			for k, v := range configured.All() {
				val := v
				outHeaders.Set(k, &val)
			}
		}
	}

	authHeader := false
	if ext != nil && ext.AuthHeader != nil {
		authHeader = *ext.AuthHeader
	} else if cfg != nil && cfg.AuthHeader != nil {
		authHeader = *cfg.AuthHeader
	}
	return CompatibilityRequestConfig{
		Headers:    outHeaders,
		AuthHeader: authHeader,
	}, nil
}

// ConfiguredRequestAuthStatus returns static auth status derived from configured apiKey values.
func ConfiguredRequestAuthStatus(cfg *ModelsJsonProvider, ext *ProviderConfig) *AuthStatus {
	value := pcConfiguredApiKey(cfg, ext)
	if value == nil {
		return nil
	}
	if IsCommandConfigValue(*value) {
		src := "models_json_command"
		return &AuthStatus{Configured: true, Source: &src}
	}
	names := GetConfigValueEnvVarNames(*value)
	if len(names) > 0 {
		if IsConfigValueConfigured(*value) {
			src := "environment"
			lbl := pcJoinComma(names)
			return &AuthStatus{Configured: true, Source: &src, Label: &lbl}
		}
		return &AuthStatus{Configured: false}
	}
	src := "models_json_key"
	if ext != nil && ext.ApiKey != nil {
		src = "fallback"
	}
	return &AuthStatus{Configured: true, Source: &src}
}

func pcJoinComma(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	res := parts[0]
	for i := 1; i < len(parts); i++ {
		res += ", " + parts[i]
	}
	return res
}

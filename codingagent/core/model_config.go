// Ported from packages/coding-agent/src/core/model-config.ts (pi v1.5.2).

package core

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

// ModelsJsonPartialModelCost is a partial model cost override used in models.json modelOverrides.
type ModelsJsonPartialModelCost struct {
	Input      *float64           `json:"input,omitzero"`
	Output     *float64           `json:"output,omitzero"`
	CacheRead  *float64           `json:"cacheRead,omitzero"`
	CacheWrite *float64           `json:"cacheWrite,omitzero"`
	Tiers      []ai.ModelCostTier `json:"tiers,omitzero"`
}

// ModelsJsonModel represents one model definition entry under a provider in models.json.
type ModelsJsonModel struct {
	Id                            string                            `json:"id"`
	Name                          *string                           `json:"name,omitzero"`
	Api                           *string                           `json:"api,omitzero"`
	BaseUrl                       *string                           `json:"baseUrl,omitzero"`
	Reasoning                     *bool                             `json:"reasoning,omitzero"`
	ThinkingLevelMap              *ai.ThinkingLevelMap              `json:"thinkingLevelMap,omitzero"`
	Input                         []ai.ModelModality                `json:"input,omitzero"`
	InputLimits                   *ai.ModelInputLimits              `json:"inputLimits,omitzero"`
	Cost                          *ai.ModelCost                     `json:"cost,omitzero"`
	PromptCache                   *ai.ModelPromptCache              `json:"promptCache,omitzero"`
	ContextWindow                 *int                              `json:"contextWindow,omitzero"`
	MaxTokens                     *int                              `json:"maxTokens,omitzero"`
	SamplingParams                *jsonx.Object                     `json:"samplingParams,omitzero"`
	SamplingParamsByThinkingLevel *ai.SamplingParamsByThinkingLevel `json:"samplingParamsByThinkingLevel,omitzero"`
	Headers                       *omap.Map[string, string]         `json:"headers,omitzero"`
	Compat                        *ai.ProviderCompat                `json:"compat,omitzero"`
}

// ModelsJsonModelOverride represents one per-model override under modelOverrides in models.json.
type ModelsJsonModelOverride struct {
	Name                          *string                           `json:"name,omitzero"`
	Reasoning                     *bool                             `json:"reasoning,omitzero"`
	ThinkingLevelMap              *ai.ThinkingLevelMap              `json:"thinkingLevelMap,omitzero"`
	Input                         []ai.ModelModality                `json:"input,omitzero"`
	InputLimits                   *ai.ModelInputLimits              `json:"inputLimits,omitzero"`
	Cost                          *ModelsJsonPartialModelCost       `json:"cost,omitzero"`
	PromptCache                   *ai.ModelPromptCache              `json:"promptCache,omitzero"`
	ContextWindow                 *int                              `json:"contextWindow,omitzero"`
	MaxTokens                     *int                              `json:"maxTokens,omitzero"`
	SamplingParams                *jsonx.Object                     `json:"samplingParams,omitzero"`
	SamplingParamsByThinkingLevel *ai.SamplingParamsByThinkingLevel `json:"samplingParamsByThinkingLevel,omitzero"`
	Headers                       *omap.Map[string, string]         `json:"headers,omitzero"`
	Compat                        *ai.ProviderCompat                `json:"compat,omitzero"`
}

// ModelsJsonProvider represents one provider entry under providers in models.json.
type ModelsJsonProvider struct {
	Name           *string                                    `json:"name,omitzero"`
	BaseUrl        *string                                    `json:"baseUrl,omitzero"`
	ApiKey         *string                                    `json:"apiKey,omitzero"`
	Api            *string                                    `json:"api,omitzero"`
	OAuth          *string                                    `json:"oauth,omitzero"`
	Headers        *omap.Map[string, string]                  `json:"headers,omitzero"`
	Compat         *ai.ProviderCompat                         `json:"compat,omitzero"`
	AuthHeader     *bool                                      `json:"authHeader,omitzero"`
	Models         []ModelsJsonModel                          `json:"models,omitzero"`
	ModelOverrides *omap.Map[string, ModelsJsonModelOverride] `json:"modelOverrides,omitzero"`
}

// ModelsJsonFile is the decoded top-level structure of models.json.
type ModelsJsonFile struct {
	Schema    *string                                `json:"$schema,omitzero"`
	Providers *omap.Map[string, *ModelsJsonProvider] `json:"providers"`
}

// ModelsJsonSchemasGroup groups entry schemas rendered into docs/models-configuration.md.
type ModelsJsonSchemasGroup struct {
	Provider      *typebox.Schema
	Model         *typebox.Schema
	ModelOverride *typebox.Schema
}

var (
	mcSamplingParamsSchema = typebox.Record(typebox.String(), typebox.Unknown())

	mcSamplingParamsByThinkingLevelSchema = func() *typebox.Schema {
		props := omap.NewMap[string, *typebox.Schema]()
		for _, k := range []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"} {
			props.Set(k, typebox.Optional(mcSamplingParamsSchema))
		}
		return typebox.Object(props, jsonx.ObjectOf(
			"description", "samplingParams per thinking level, merged over samplingParams.",
		))
	}()

	mcSamplingParamsFieldSchema = typebox.Record(typebox.String(), typebox.Unknown(), jsonx.ObjectOf(
		"description", "Fields merged into every request body. Applies only to OpenAI-compatible APIs.",
	))

	mcHeadersSchema = typebox.Record(typebox.String(), typebox.String(), jsonx.ObjectOf(
		"description", "Request headers. Values resolve like apiKey.",
	))

	mcModelFieldDescriptions = struct {
		Name          string
		Reasoning     string
		ContextWindow string
		MaxTokens     string
	}{
		Name:          "Display name, also matched by --model patterns.",
		Reasoning:     "Whether the model supports thinking.",
		ContextWindow: "Context window in tokens.",
		MaxTokens:     "Maximum output tokens.",
	}

	mcModelDefinitionSchema = func() *typebox.Schema {
		props := omap.NewMap[string, *typebox.Schema]()
		props.Set("id", typebox.String(jsonx.ObjectOf("minLength", 1, "description", "Model ID sent to the API.")))
		props.Set("name", typebox.Optional(typebox.String(jsonx.ObjectOf("minLength", 1, "description", mcModelFieldDescriptions.Name))))
		props.Set("api", typebox.Optional(typebox.String(jsonx.ObjectOf("minLength", 1, "description", "Overrides the provider api."))))
		props.Set("baseUrl", typebox.Optional(typebox.String(jsonx.ObjectOf("minLength", 1, "description", "Overrides the provider baseUrl."))))
		props.Set("reasoning", typebox.Optional(typebox.Boolean(jsonx.ObjectOf("default", false, "description", mcModelFieldDescriptions.Reasoning))))
		props.Set("thinkingLevelMap", typebox.Optional(ThinkingLevelMapSchema))
		props.Set("input", typebox.Optional(typebox.Array(ModelInputModalitySchema, jsonx.ObjectOf("default", []any{"text"}))))
		props.Set("inputLimits", typebox.Optional(ModelInputLimitsSchema))
		props.Set("cost", typebox.Optional(ModelCostSchema))
		props.Set("promptCache", typebox.Optional(ModelPromptCacheSchema))
		props.Set("contextWindow", typebox.Optional(typebox.Number(jsonx.ObjectOf("exclusiveMinimum", 0, "default", 128000, "description", mcModelFieldDescriptions.ContextWindow))))
		props.Set("maxTokens", typebox.Optional(typebox.Number(jsonx.ObjectOf("exclusiveMinimum", 0, "default", 16384, "description", mcModelFieldDescriptions.MaxTokens))))
		props.Set("samplingParams", typebox.Optional(mcSamplingParamsFieldSchema))
		props.Set("samplingParamsByThinkingLevel", typebox.Optional(mcSamplingParamsByThinkingLevelSchema))
		props.Set("headers", typebox.Optional(mcHeadersSchema))
		props.Set("compat", typebox.Optional(ProviderCompatSchema))
		return typebox.Object(props)
	}()

	mcModelOverrideSchema = func() *typebox.Schema {
		props := omap.NewMap[string, *typebox.Schema]()
		props.Set("name", typebox.Optional(typebox.String(jsonx.ObjectOf("minLength", 1, "description", mcModelFieldDescriptions.Name))))
		props.Set("reasoning", typebox.Optional(typebox.Boolean(jsonx.ObjectOf("description", mcModelFieldDescriptions.Reasoning))))
		props.Set("thinkingLevelMap", typebox.Optional(ThinkingLevelMapSchema))
		props.Set("input", typebox.Optional(typebox.Array(ModelInputModalitySchema)))
		props.Set("inputLimits", typebox.Optional(ModelInputLimitsSchema))
		props.Set("cost", typebox.Optional(PartialModelCostSchema))
		props.Set("promptCache", typebox.Optional(ModelPromptCacheSchema))
		props.Set("contextWindow", typebox.Optional(typebox.Number(jsonx.ObjectOf("exclusiveMinimum", 0, "description", mcModelFieldDescriptions.ContextWindow))))
		props.Set("maxTokens", typebox.Optional(typebox.Number(jsonx.ObjectOf("exclusiveMinimum", 0, "description", mcModelFieldDescriptions.MaxTokens))))
		props.Set("samplingParams", typebox.Optional(mcSamplingParamsFieldSchema))
		props.Set("samplingParamsByThinkingLevel", typebox.Optional(mcSamplingParamsByThinkingLevelSchema))
		props.Set("headers", typebox.Optional(mcHeadersSchema))
		props.Set("compat", typebox.Optional(ProviderCompatSchema))
		return typebox.Object(props)
	}()

	mcProviderConfigSchema = func() *typebox.Schema {
		props := omap.NewMap[string, *typebox.Schema]()
		props.Set("name", typebox.Optional(typebox.String(jsonx.ObjectOf("minLength", 1, "description", "Display name."))))
		props.Set("baseUrl", typebox.Optional(typebox.String(jsonx.ObjectOf("minLength", 1, "description", "API base URL. Also applies to the provider's built-in models."))))
		props.Set("apiKey", typebox.Optional(typebox.String(jsonx.ObjectOf("minLength", 1, "description", `Literal key, "$NAME" environment interpolation, or "!command" output.`))))
		props.Set("api", typebox.Optional(typebox.String(jsonx.ObjectOf("minLength", 1, "description", `API for the provider's models, such as "openai-completions", "openai-responses", "anthropic-messages", or "google-generative-ai".`))))
		props.Set("oauth", typebox.Optional(typebox.Literal("radius", jsonx.ObjectOf("description", "OAuth login flow. Requires baseUrl."))))
		props.Set("headers", typebox.Optional(mcHeadersSchema))
		props.Set("compat", typebox.Optional(ProviderCompatSchema))
		props.Set("authHeader", typebox.Optional(typebox.Boolean(jsonx.ObjectOf("default", false, "description", "Whether to send the resolved API key as Authorization: Bearer."))))
		props.Set("models", typebox.Optional(typebox.Array(mcModelDefinitionSchema)))
		props.Set("modelOverrides", typebox.Optional(typebox.Record(typebox.String(), mcModelOverrideSchema)))
		return typebox.Object(props)
	}()

	// ModelsConfigSchema is the TypeBox schema for models.json.
	ModelsConfigSchema = func() *typebox.Schema {
		props := omap.NewMap[string, *typebox.Schema]()
		props.Set("$schema", typebox.Optional(typebox.String()))
		props.Set("providers", typebox.Record(typebox.String(), mcProviderConfigSchema))
		return typebox.Object(props)
	}()

	// ModelsJsonSchemas exposes entry schemas for documentation generation.
	ModelsJsonSchemas = ModelsJsonSchemasGroup{
		Provider:      mcProviderConfigSchema,
		Model:         mcModelDefinitionSchema,
		ModelOverride: mcModelOverrideSchema,
	}

	mcValidateModelsConfig = func() *typebox.Validator {
		v, err := typebox.Compile(ModelsConfigSchema)
		if err != nil {
			panic(err)
		}
		return v
	}()
)

func mcFormatValidationPath(err typebox.ValidationError) string {
	basePath := strings.ReplaceAll(strings.TrimPrefix(err.InstancePath, "/"), "/", ".")
	if err.Keyword == "required" && err.Params != nil {
		if reqRaw, ok := err.Params.Get("requiredProperties"); ok {
			if reqArr, ok := reqRaw.([]any); ok && len(reqArr) > 0 {
				if reqProp, ok := reqArr[0].(string); ok && reqProp != "" {
					if basePath != "" {
						return basePath + "." + reqProp
					}
					return reqProp
				}
			}
		}
	}
	if basePath == "" {
		return "root"
	}
	return basePath
}

// ModelConfig is an immutable, credential-blind models.json snapshot.
type ModelConfig struct {
	providers *omap.Map[string, *ModelsJsonProvider]
	err       *string
}

// LoadModelConfig loads and validates models.json from modelsJsonPath.
// Missing files (ENOENT) or empty paths yield an empty valid config without error.
func LoadModelConfig(modelsJsonPath string) *ModelConfig {
	if modelsJsonPath == "" {
		return &ModelConfig{providers: omap.NewMap[string, *ModelsJsonProvider]()}
	}
	p, err := utils.NormalizePath(modelsJsonPath, nil)
	if err != nil {
		msg := fmt.Sprintf("Failed to load models.json: %v\n\nFile: %s", err, modelsJsonPath)
		return &ModelConfig{providers: omap.NewMap[string, *ModelsJsonProvider](), err: &msg}
	}

	contentBytes, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &ModelConfig{providers: omap.NewMap[string, *ModelsJsonProvider]()}
		}
		msg := fmt.Sprintf("Failed to load models.json: %v\n\nFile: %s", err, p)
		return &ModelConfig{providers: omap.NewMap[string, *ModelsJsonProvider](), err: &msg}
	}

	stripped := utils.StripJsonComments(utils.StripBom(string(contentBytes)))
	parsed, err := jsonx.ParseString(stripped)
	if err != nil {
		msg := fmt.Sprintf("Failed to parse models.json: %v\n\nFile: %s", err, p)
		return &ModelConfig{providers: omap.NewMap[string, *ModelsJsonProvider](), err: &msg}
	}

	if !mcValidateModelsConfig.Check(parsed) {
		errs := mcValidateModelsConfig.Errors(parsed)
		lines := make([]string, 0, len(errs))
		for _, verr := range errs {
			lines = append(lines, fmt.Sprintf("  - %s: %s", mcFormatValidationPath(verr), verr.Message))
		}
		errBody := strings.Join(lines, "\n")
		if errBody == "" {
			errBody = "Unknown schema error"
		}
		msg := fmt.Sprintf("Invalid models.json schema:\n%s\n\nFile: %s", errBody, p)
		return &ModelConfig{providers: omap.NewMap[string, *ModelsJsonProvider](), err: &msg}
	}

	var decoded ModelsJsonFile
	if err := jsonx.Decode(parsed, &decoded); err != nil {
		msg := fmt.Sprintf("Failed to parse models.json: %v\n\nFile: %s", err, p)
		return &ModelConfig{providers: omap.NewMap[string, *ModelsJsonProvider](), err: &msg}
	}

	providers := omap.NewMap[string, *ModelsJsonProvider]()
	if decoded.Providers != nil {
		for k, v := range decoded.Providers.All() {
			providers.Set(k, mcCloneProvider(v))
		}
	}
	return &ModelConfig{providers: providers}
}

// GetProvider returns a cloned ModelsJsonProvider for providerId, or nil if absent.
func (c *ModelConfig) GetProvider(providerId string) *ModelsJsonProvider {
	if c == nil || c.providers == nil {
		return nil
	}
	p, ok := c.providers.Get(providerId)
	if !ok {
		return nil
	}
	return mcCloneProvider(p)
}

// GetProviderIds returns the configured provider IDs in insertion order.
func (c *ModelConfig) GetProviderIds() []string {
	if c == nil || c.providers == nil {
		return nil
	}
	return c.providers.Keys()
}

// GetError returns the models.json load/validation error, if any.
func (c *ModelConfig) GetError() *string {
	if c == nil {
		return nil
	}
	return c.err
}

func mcCloneProvider(src *ModelsJsonProvider) *ModelsJsonProvider {
	if src == nil {
		return nil
	}
	raw, err := jsonx.Marshal(src)
	if err != nil {
		return nil
	}
	var out ModelsJsonProvider
	if err := jsonx.Unmarshal(raw, &out); err != nil {
		return nil
	}
	if out.Models != nil {
		out.Models = slices.Clone(out.Models)
	}
	return &out
}

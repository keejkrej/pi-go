// Ported from packages/ai/src/providers/model-schema.ts (pi v1.5.2).

package providers

import (
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

var (
	ThinkingLevels             = []string{"minimal", "low", "medium", "high", "xhigh", "max"}
	ModelThinkingLevels        = []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}
	ModelInputModalities       = []string{"text", "image"}
	CacheRetentions            = []string{"none", "short", "long"}
	ModelPromptCacheRetentions = []string{"short", "long"}
)

func msEnumSchema(values []string) *typebox.Schema {
	anyVals := make([]any, len(values))
	for i, v := range values {
		anyVals[i] = v
	}
	return typebox.Enum(anyVals)
}

func msAddModelCostRatesProps(props *omap.Map[string, *typebox.Schema]) {
	props.Set("input", typebox.Number(jsonx.ObjectOf("description", "Input cost in USD per million tokens.")))
	props.Set("output", typebox.Number(jsonx.ObjectOf("description", "Output cost in USD per million tokens.")))
	props.Set("cacheRead", typebox.Number(jsonx.ObjectOf("description", "Cache-read cost in USD per million tokens.")))
	props.Set("cacheWrite", typebox.Number(jsonx.ObjectOf("description", "Cache-write cost in USD per million tokens.")))
}

func msModelImageResizeOptions(options ...*jsonx.Object) *typebox.Schema {
	props := omap.NewMap[string, *typebox.Schema]()
	props.Set("maxWidth", typebox.Optional(typebox.Integer(jsonx.ObjectOf("minimum", 1))))
	props.Set("maxHeight", typebox.Optional(typebox.Integer(jsonx.ObjectOf("minimum", 1))))
	props.Set("maxBytes", typebox.Optional(typebox.Integer(jsonx.ObjectOf(
		"minimum", 1,
		"description", "Maximum base64-encoded payload size in bytes.",
	))))
	props.Set("jpegQuality", typebox.Optional(typebox.Integer(jsonx.ObjectOf(
		"minimum", 1,
		"maximum", 100,
	))))
	return OpenObject(props, options...)
}

var (
	ThinkingLevelSchema      = msEnumSchema(ThinkingLevels)
	ModelThinkingLevelSchema = msEnumSchema(ModelThinkingLevels)
	ModelInputModalitySchema = msEnumSchema(ModelInputModalities)
	CacheRetentionSchema     = msEnumSchema(CacheRetentions)

	msThinkingLevelMapValueSchema = typebox.Union([]*typebox.Schema{typebox.String(), typebox.Null()})

	ThinkingLevelMapSchema = func() *typebox.Schema {
		props := omap.NewMap[string, *typebox.Schema]()
		for _, k := range ModelThinkingLevels {
			props.Set(k, typebox.Optional(msThinkingLevelMapValueSchema))
		}
		return OpenObject(props, jsonx.ObjectOf(
			"description", "Provider value sent for each thinking level. null marks a level unsupported. Levels the model does not map use the provider's default value through high; xhigh and max are unsupported.",
		))
	}()

	msPromptCacheLifetimeSchema = typebox.Number(jsonx.ObjectOf("exclusiveMinimum", 0))

	ModelPromptCacheSchema = func() *typebox.Schema {
		props := omap.NewMap[string, *typebox.Schema]()
		props.Set("short", typebox.Optional(msPromptCacheLifetimeSchema))
		props.Set("long", typebox.Optional(msPromptCacheLifetimeSchema))
		return OpenObject(props, jsonx.ObjectOf(
			"description", "Best-effort prompt cache lifetime in seconds for the short and long retention tiers. Pi warms only tiers with a known lifetime.",
		))
	}()

	ModelCostRatesSchema = func() *typebox.Schema {
		props := omap.NewMap[string, *typebox.Schema]()
		msAddModelCostRatesProps(props)
		return OpenObject(props)
	}()

	ModelCostTierSchema = func() *typebox.Schema {
		props := omap.NewMap[string, *typebox.Schema]()
		props.Set("inputTokensAbove", typebox.Number(jsonx.ObjectOf(
			"description", "Use this tier when total request input exceeds this token count.",
		)))
		msAddModelCostRatesProps(props)
		return OpenObject(props)
	}()

	ModelCostSchema = func() *typebox.Schema {
		props := omap.NewMap[string, *typebox.Schema]()
		msAddModelCostRatesProps(props)
		props.Set("tiers", typebox.Optional(typebox.Array(ModelCostTierSchema, jsonx.ObjectOf(
			"description", "Request-wide pricing tiers. The highest matching input threshold applies to the full request.",
		))))
		return OpenObject(props)
	}()

	ModelImageResizeOptionsSchema = msModelImageResizeOptions()

	ModelImageInputLimitsSchema = func() *typebox.Schema {
		props := omap.NewMap[string, *typebox.Schema]()
		props.Set("resize", typebox.Optional(msModelImageResizeOptions(jsonx.ObjectOf(
			"description", "Cache-safe resize profile applied before a new image enters conversation history.",
		))))
		props.Set("maxPerMessage", typebox.Optional(typebox.Integer(jsonx.ObjectOf(
			"minimum", 1,
			"description", "Maximum images accepted in one provider message.",
		))))
		props.Set("maxPerRequest", typebox.Optional(typebox.Integer(jsonx.ObjectOf(
			"minimum", 1,
			"description", "Maximum images accepted across one provider request.",
		))))
		return OpenObject(props)
	}()

	ModelInputLimitsSchema = func() *typebox.Schema {
		props := omap.NewMap[string, *typebox.Schema]()
		props.Set("maxRequestBytes", typebox.Optional(typebox.Integer(jsonx.ObjectOf(
			"minimum", 1,
			"description", "Maximum serialized provider request size in bytes.",
		))))
		props.Set("images", typebox.Optional(ModelImageInputLimitsSchema))
		return OpenObject(props)
	}()
)

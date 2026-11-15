// Ported from packages/coding-agent/src/core/pi-ai-schemas.ts (pi v1.5.2).

package core

import (
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/typebox"
)

// Pi-ai configuration schemas wrapped as TypeBox schemas for models.json and settings.json.
var (
	ProviderCompatSchema     = typebox.Unsafe(providers.ProviderCompatSchema.Object.Clone())
	ModelCostSchema          = typebox.Unsafe(providers.ModelCostSchema.Object.Clone())
	ModelInputLimitsSchema   = typebox.Unsafe(providers.ModelInputLimitsSchema.Object.Clone())
	ModelInputModalitySchema = typebox.Unsafe(providers.ModelInputModalitySchema.Object.Clone())
	ModelPromptCacheSchema   = typebox.Unsafe(providers.ModelPromptCacheSchema.Object.Clone())
	ModelThinkingLevelSchema = typebox.Unsafe(providers.ModelThinkingLevelSchema.Object.Clone())
	ThinkingLevelMapSchema   = typebox.Unsafe(providers.ThinkingLevelMapSchema.Object.Clone())
	PartialModelCostSchema   = typebox.Unsafe(piasWithoutRequired(providers.ModelCostSchema.Object))
)

func piasWithoutRequired(schema *jsonx.Object) *jsonx.Object {
	if schema == nil {
		return jsonx.NewObject()
	}
	cloned := schema.Clone()
	cloned.Delete("required")
	return cloned
}

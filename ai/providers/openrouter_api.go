// Ported from packages/ai/src/providers/openrouter-api.ts (pi v1.5.2).

package providers

import (
	"math"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// OpenRouterPricingOverride is a conditional price entry returned by OpenRouter's models API.
// min_prompt_tokens selects prompt-length pricing; utc_* fields select time-of-day or weekday pricing.
type OpenRouterPricingOverride struct {
	MinPromptTokens *int     `json:"min_prompt_tokens,omitzero"`
	UtcStart        *float64 `json:"utc_start,omitzero"`
	UtcEnd          *float64 `json:"utc_end,omitzero"`
	UtcDays         []string `json:"utc_days,omitzero"`
	Prompt          *string  `json:"prompt,omitzero"`
	Completion      *string  `json:"completion,omitzero"`
	InputCacheRead  *string  `json:"input_cache_read,omitzero"`
	InputCacheWrite *string  `json:"input_cache_write,omitzero"`
}

// OpenRouterPricing is OpenRouter's per-token prices as decimal strings.
type OpenRouterPricing struct {
	Prompt          *string                     `json:"prompt,omitzero"`
	Completion      *string                     `json:"completion,omitzero"`
	InputCacheRead  *string                     `json:"input_cache_read,omitzero"`
	InputCacheWrite *string                     `json:"input_cache_write,omitzero"`
	Overrides       []OpenRouterPricingOverride `json:"overrides,omitzero"`
}

// OpenRouterTopProvider holds OpenRouter top_provider limits metadata.
type OpenRouterTopProvider struct {
	ContextLength       jsonx.Opt[int] `json:"context_length,omitzero"`
	MaxCompletionTokens jsonx.Opt[int] `json:"max_completion_tokens,omitzero"`
}

// OpenRouterModel represents the fields pi reads from OpenRouter's GET /models and GET /models/user.
type OpenRouterModel struct {
	Id            string                 `json:"id"`
	ContextLength jsonx.Opt[int]         `json:"context_length,omitzero"`
	TopProvider   *OpenRouterTopProvider `json:"top_provider,omitzero"`
	Pricing       *OpenRouterPricing     `json:"pricing,omitzero"`
}

func oaPerMillion(value *string, fallback float64) float64 {
	raw := ""
	if value != nil {
		raw = *value
	}
	perToken := js.ParseFloat(raw)
	if !math.IsNaN(perToken) && perToken >= 0 {
		return js.ParseFloat(js.ToFixed(perToken*1_000_000, 6))
	}
	return fallback
}

// OpenRouterCost converts OpenRouter's $/token prices to pi's $/1M tokens.
// Missing, invalid and negative base prices are 0. Prompt-length overrides become
// request-wide tiers; time-of-day overrides are skipped.
func OpenRouterCost(pricing *OpenRouterPricing) ai.ModelCost {
	var base ai.ModelCostRates
	if pricing != nil {
		base = ai.ModelCostRates{
			Input:      oaPerMillion(pricing.Prompt, 0),
			Output:     oaPerMillion(pricing.Completion, 0),
			CacheRead:  oaPerMillion(pricing.InputCacheRead, 0),
			CacheWrite: oaPerMillion(pricing.InputCacheWrite, 0),
		}
	}
	var tiers []ai.ModelCostTier
	if pricing != nil {
		for _, override := range pricing.Overrides {
			if override.MinPromptTokens == nil ||
				override.UtcStart != nil ||
				override.UtcEnd != nil ||
				override.UtcDays != nil {
				continue
			}
			tiers = append(tiers, ai.ModelCostTier{
				InputTokensAbove: *override.MinPromptTokens,
				ModelCostRates: ai.ModelCostRates{
					Input:      oaPerMillion(override.Prompt, base.Input),
					Output:     oaPerMillion(override.Completion, base.Output),
					CacheRead:  oaPerMillion(override.InputCacheRead, base.CacheRead),
					CacheWrite: oaPerMillion(override.InputCacheWrite, base.CacheWrite),
				},
			})
		}
	}
	if len(tiers) > 0 {
		return ai.ModelCost{
			ModelCostRates: base,
			Tiers:          tiers,
		}
	}
	return ai.ModelCost{ModelCostRates: base}
}

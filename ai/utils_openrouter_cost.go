// Ported from packages/ai/src/utils/openrouter-cost.ts (pi v1.5.2).

package ai

import (
	"math"
	"strings"

	"github.com/keejkrej/pi-go/internal/jsonx"
)

// OpenRouterUsageCostDetails carries upstream inference cost details for BYOK requests.
type OpenRouterUsageCostDetails struct {
	UpstreamInferenceCost            jsonx.Opt[float64] `json:"upstream_inference_cost"`
	UpstreamInferencePromptCost      float64            `json:"upstream_inference_prompt_cost"`
	UpstreamInferenceCompletionsCost float64            `json:"upstream_inference_completions_cost"`
	ServerToolCost                   jsonx.Opt[float64] `json:"server_tool_cost,omitzero"`
}

// OpenRouterUsageAccounting matches the OpenRouter response usage accounting object.
type OpenRouterUsageAccounting struct {
	Cost        jsonx.Opt[float64]          `json:"cost"`
	IsByok      bool                        `json:"is_byok"`
	CostDetails *OpenRouterUsageCostDetails `json:"cost_details"`
}

func uocIsValidCost(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}

// IsOpenRouterModel reports whether model targets OpenRouter (provider or baseUrl).
func IsOpenRouterModel(model AnyModel) bool {
	var provider string
	var baseUrl string
	switch m := model.(type) {
	case *Model:
		if m != nil {
			provider = string(m.Provider)
			baseUrl = m.BaseUrl
		}
	case *ImageModel:
		if m != nil {
			provider = string(m.Provider)
			baseUrl = m.BaseUrl
		}
	case *ClassifierModel:
		if m != nil {
			provider = string(m.Provider)
			baseUrl = m.BaseUrl
		}
	case *UnknownAnyModel:
		if m != nil && m.Raw != nil {
			if p, ok := m.Raw.Get("provider"); ok {
				provider, _ = p.(string)
			}
			if u, ok := m.Raw.Get("baseUrl"); ok {
				baseUrl, _ = u.(string)
			}
		}
	}
	return provider == string(KnownProviderOpenrouter) || strings.Contains(baseUrl, "openrouter.ai")
}

// ApplyOpenRouterReportedCost replaces the catalog total with OpenRouter's billed cost
// when the response contains complete, valid accounting.
func ApplyOpenRouterReportedCost(usage *Usage, reported *OpenRouterUsageAccounting) {
	if usage == nil || reported == nil {
		return
	}
	reportedCost, hasCost := reported.Cost.Get()
	if !hasCost || !uocIsValidCost(reportedCost) {
		return
	}

	if !reported.IsByok {
		usage.Cost.Total = reportedCost
		t := true
		usage.Cost.TotalFromProvider = &t
		return
	}

	details := reported.CostDetails
	if details == nil {
		return
	}
	upstreamCost, hasUpstream := details.UpstreamInferenceCost.Get()
	if !hasUpstream {
		if !details.UpstreamInferenceCost.IsNull() {
			return
		}
		if !uocIsValidCost(details.UpstreamInferencePromptCost) || !uocIsValidCost(details.UpstreamInferenceCompletionsCost) {
			return
		}
		upstreamCost = details.UpstreamInferencePromptCost + details.UpstreamInferenceCompletionsCost
	}
	if !uocIsValidCost(upstreamCost) {
		return
	}
	usage.Cost.Total = reportedCost + upstreamCost
	t := true
	usage.Cost.TotalFromProvider = &t
}

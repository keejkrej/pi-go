package ai_test

import (
	"math"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func testOpenRouterUsage(total float64) *ai.Usage {
	return &ai.Usage{
		Input:       1,
		Output:      1,
		CacheRead:   0,
		CacheWrite:  0,
		TotalTokens: 2,
		Cost:        ai.UsageCost{Input: 0.25, Output: 0.75, CacheRead: 0, CacheWrite: 0, Total: total},
	}
}

func TestIsOpenRouterModel(t *testing.T) {
	if !ai.IsOpenRouterModel(&ai.Model{BaseModel: ai.BaseModel{Provider: "openrouter", BaseUrl: "https://example.com"}}) {
		t.Fatal("expected openrouter provider to match")
	}
	if !ai.IsOpenRouterModel(&ai.Model{BaseModel: ai.BaseModel{Provider: "openrouter-work", BaseUrl: "https://openrouter.ai/api/v1"}}) {
		t.Fatal("expected openrouter.ai baseUrl to match")
	}
	if ai.IsOpenRouterModel(&ai.Model{BaseModel: ai.BaseModel{Provider: "custom", BaseUrl: "https://example.com/v1"}}) {
		t.Fatal("expected custom provider/url not to match")
	}
}

func TestApplyOpenRouterReportedCost(t *testing.T) {
	t.Run("uses OpenRouter's reported cost for non-BYOK requests", func(t *testing.T) {
		value := testOpenRouterUsage(1)
		ai.ApplyOpenRouterReportedCost(value, &ai.OpenRouterUsageAccounting{
			Cost:   jsonx.Some(0.0),
			IsByok: false,
			CostDetails: &ai.OpenRouterUsageCostDetails{
				UpstreamInferenceCost:            jsonx.Some(0.0),
				UpstreamInferencePromptCost:      0,
				UpstreamInferenceCompletionsCost: 0,
			},
		})
		if value.Cost.Total != 0 {
			t.Fatalf("Total = %v, want 0", value.Cost.Total)
		}
		if value.Cost.TotalFromProvider == nil || !*value.Cost.TotalFromProvider {
			t.Fatalf("TotalFromProvider = %v, want true", value.Cost.TotalFromProvider)
		}
	})

	t.Run("adds the upstream inference cost for BYOK requests", func(t *testing.T) {
		value := testOpenRouterUsage(1)
		ai.ApplyOpenRouterReportedCost(value, &ai.OpenRouterUsageAccounting{
			Cost:   jsonx.Some(0.1),
			IsByok: true,
			CostDetails: &ai.OpenRouterUsageCostDetails{
				UpstreamInferenceCost:            jsonx.Some(0.9),
				UpstreamInferencePromptCost:      0.6,
				UpstreamInferenceCompletionsCost: 0.3,
			},
		})
		if value.Cost.Total != 1 {
			t.Fatalf("Total = %v, want 1", value.Cost.Total)
		}
		if value.Cost.TotalFromProvider == nil || !*value.Cost.TotalFromProvider {
			t.Fatalf("TotalFromProvider = %v, want true", value.Cost.TotalFromProvider)
		}
	})

	t.Run("uses upstream cost components when OpenRouter omits their total", func(t *testing.T) {
		value := testOpenRouterUsage(1)
		ai.ApplyOpenRouterReportedCost(value, &ai.OpenRouterUsageAccounting{
			Cost:   jsonx.Some(0.1),
			IsByok: true,
			CostDetails: &ai.OpenRouterUsageCostDetails{
				UpstreamInferenceCost:            jsonx.Null[float64](),
				UpstreamInferencePromptCost:      0.6,
				UpstreamInferenceCompletionsCost: 0.3,
			},
		})
		if math.Abs(value.Cost.Total-1) > 1e-9 {
			t.Fatalf("Total = %v, want ~1", value.Cost.Total)
		}
		if value.Cost.TotalFromProvider == nil || !*value.Cost.TotalFromProvider {
			t.Fatalf("TotalFromProvider = %v, want true", value.Cost.TotalFromProvider)
		}
	})

	t.Run("keeps the catalog total when OpenRouter's accounting is incomplete", func(t *testing.T) {
		value := testOpenRouterUsage(1)
		f := false
		value.Cost.TotalFromProvider = &f
		ai.ApplyOpenRouterReportedCost(value, &ai.OpenRouterUsageAccounting{
			Cost:        jsonx.Null[float64](),
			IsByok:      false,
			CostDetails: nil,
		})
		ai.ApplyOpenRouterReportedCost(value, &ai.OpenRouterUsageAccounting{
			Cost:        jsonx.Some(0.1),
			IsByok:      true,
			CostDetails: nil,
		})
		if value.Cost.Total != 1 {
			t.Fatalf("Total = %v, want 1", value.Cost.Total)
		}
		if value.Cost.TotalFromProvider == nil || *value.Cost.TotalFromProvider {
			t.Fatalf("TotalFromProvider = %v, want false", value.Cost.TotalFromProvider)
		}
	})
}

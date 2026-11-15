// Ported from packages/ai/test/zai-coding-plan-models.test.ts (pi v1.5.2).

package providers_test

import (
	"slices"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
)

func TestZaiCodingPlanModels_ExposesGlm46v(t *testing.T) {
	t.Parallel()
	model := providers.GetBuiltinModel("zai-coding-cn", "glm-4.6v")
	if model == nil {
		t.Fatal("expected zai-coding-cn/glm-4.6v")
	}
	if model.Id != "glm-4.6v" ||
		model.Provider != "zai-coding-cn" ||
		model.Api != string(ai.KnownApiOpenaiCompletions) ||
		model.BaseUrl != "https://open.bigmodel.cn/api/coding/paas/v4" ||
		!model.Reasoning ||
		!slices.Equal(model.Input, []ai.ModelModality{ai.ModelModalityText, ai.ModelModalityImage}) ||
		model.Cost.ModelCostRates != (ai.ModelCostRates{Input: 0.3, Output: 0.9, CacheRead: 0.05, CacheWrite: 0}) ||
		model.ContextWindow != 128000 ||
		model.MaxTokens != 32768 {
		t.Fatalf("unexpected glm-4.6v model: %+v", model)
	}
	compat, ok := model.Compat.V.(*ai.OpenAICompletionsCompat)
	if !ok || compat == nil {
		t.Fatalf("expected *ai.OpenAICompletionsCompat, got %T", model.Compat.V)
	}
	if compat.MaxTokensField == nil || *compat.MaxTokensField != ai.MaxTokensFieldMaxTokens {
		t.Fatalf("expected maxTokensField=max_tokens, got %v", compat.MaxTokensField)
	}
	if compat.ThinkingFormat == nil || *compat.ThinkingFormat != ai.ThinkingFormatZai {
		t.Fatalf("expected thinkingFormat=zai, got %v", compat.ThinkingFormat)
	}
	if compat.ZaiToolStream == nil || !*compat.ZaiToolStream {
		t.Fatalf("expected zaiToolStream=true, got %v", compat.ZaiToolStream)
	}
}

func TestZaiCodingPlanModels_ApiEquivalentCosts(t *testing.T) {
	t.Parallel()
	want := ai.ModelCostRates{Input: 1.4, Output: 4.4, CacheRead: 0.26, CacheWrite: 0}
	m52 := providers.GetBuiltinModel("zai", "glm-5.2")
	if m52 == nil || m52.Cost.ModelCostRates != want {
		t.Fatalf("expected zai/glm-5.2 cost %+v, got %+v", want, m52)
	}
	for _, provider := range []providers.BuiltinProvider{"zai", "zai-coding-cn"} {
		m53 := providers.GetBuiltinModel(provider, "glm-5.3")
		if m53 == nil || m53.Cost.ModelCostRates != want {
			t.Fatalf("expected %s/glm-5.3 cost %+v, got %+v", provider, want, m53)
		}
	}
}

func TestZaiCodingPlanModels_ZeroCostsWithoutMatchingApiPrice(t *testing.T) {
	t.Parallel()
	zeroCost := ai.ModelCostRates{Input: 0, Output: 0, CacheRead: 0, CacheWrite: 0}
	m52 := providers.GetBuiltinModel("zai", "glm-5.2-highspeed")
	if m52 == nil || m52.Cost.ModelCostRates != zeroCost {
		t.Fatalf("expected zai/glm-5.2-highspeed zero cost, got %+v", m52)
	}
	for _, provider := range []providers.BuiltinProvider{"zai", "zai-coding-cn"} {
		m53 := providers.GetBuiltinModel(provider, "glm-5.3-highspeed")
		if m53 == nil || m53.Cost.ModelCostRates != zeroCost {
			t.Fatalf("expected %s/glm-5.3-highspeed zero cost, got %+v", provider, m53)
		}
	}
}

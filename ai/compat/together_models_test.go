// Ported from packages/ai/test/together-models.test.ts (pi v1.5.2).

package compat_test

import (
	"slices"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/compat"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func TestTogetherModels_KimiK3Defaults(t *testing.T) {
	t.Parallel()
	model := compat.GetModel("together", "moonshotai/Kimi-K3")
	if model == nil {
		t.Fatal("expected together/moonshotai/Kimi-K3")
	}
	if model.Api != string(ai.KnownApiOpenaiCompletions) {
		t.Fatalf("api = %q, want openai-completions", model.Api)
	}
	if model.Provider != "together" {
		t.Fatalf("provider = %q, want together", model.Provider)
	}
	if model.BaseUrl != "https://api.together.ai/v1" {
		t.Fatalf("baseUrl = %q", model.BaseUrl)
	}
	if !model.Reasoning {
		t.Fatal("expected reasoning=true")
	}
	wantMap := ai.ThinkingLevelMap{
		Minimal: jsonx.Null[string](),
		Low:     jsonx.Null[string](),
		Medium:  jsonx.Null[string](),
	}
	if model.ThinkingLevelMap == nil || *model.ThinkingLevelMap != wantMap {
		t.Fatalf("thinkingLevelMap = %+v, want %+v", model.ThinkingLevelMap, wantMap)
	}
	if !slices.Equal(model.Input, []ai.ModelModality{ai.ModelModalityText, ai.ModelModalityImage}) {
		t.Fatalf("input = %v", model.Input)
	}
	if model.ContextWindow != 1048576 || model.MaxTokens != 131072 {
		t.Fatalf("limits = (%d, %d)", model.ContextWindow, model.MaxTokens)
	}
	wantCost := ai.ModelCostRates{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 0}
	if model.Cost.ModelCostRates != wantCost {
		t.Fatalf("cost = %+v, want %+v", model.Cost.ModelCostRates, wantCost)
	}
	comp, ok := model.Compat.V.(*ai.OpenAICompletionsCompat)
	if !ok || comp == nil {
		t.Fatalf("expected *ai.OpenAICompletionsCompat, got %T", model.Compat.V)
	}
	if comp.SupportsStore == nil || *comp.SupportsStore != false ||
		comp.SupportsDeveloperRole == nil || *comp.SupportsDeveloperRole != false ||
		comp.SupportsReasoningEffort == nil || *comp.SupportsReasoningEffort != false ||
		comp.MaxTokensField == nil || *comp.MaxTokensField != ai.MaxTokensFieldMaxTokens ||
		comp.ThinkingFormat == nil || *comp.ThinkingFormat != ai.ThinkingFormatTogether ||
		comp.SupportsStrictMode == nil || *comp.SupportsStrictMode != false ||
		comp.SupportsLongCacheRetention == nil || *comp.SupportsLongCacheRetention != false {
		t.Fatalf("unexpected compat: %+v", comp)
	}
}

func TestTogetherModels_ReasoningControls(t *testing.T) {
	t.Parallel()
	gptOss := compat.GetModel("together", "openai/gpt-oss-120b")
	if gptOss == nil {
		t.Fatal("expected together/openai/gpt-oss-120b")
	}
	wantGptMap := ai.ThinkingLevelMap{
		Off:     jsonx.Null[string](),
		Minimal: jsonx.Null[string](),
		Low:     jsonx.Some("low"),
		Medium:  jsonx.Some("medium"),
		High:    jsonx.Some("high"),
		Max:     jsonx.Null[string](),
		Xhigh:   jsonx.Null[string](),
	}
	if gptOss.ThinkingLevelMap == nil || *gptOss.ThinkingLevelMap != wantGptMap {
		t.Fatalf("gpt-oss thinkingLevelMap = %+v, want %+v", gptOss.ThinkingLevelMap, wantGptMap)
	}
	gptCompat, ok := gptOss.Compat.V.(*ai.OpenAICompletionsCompat)
	if !ok || gptCompat == nil ||
		gptCompat.SupportsReasoningEffort == nil || !*gptCompat.SupportsReasoningEffort ||
		gptCompat.ThinkingFormat == nil || *gptCompat.ThinkingFormat != ai.ThinkingFormatOpenai {
		t.Fatalf("unexpected gpt-oss compat: %+v", gptOss.Compat.V)
	}

	deepSeekV4 := compat.GetModel("together", "deepseek-ai/DeepSeek-V4-Pro-0813")
	if deepSeekV4 == nil {
		t.Fatal("expected together/deepseek-ai/DeepSeek-V4-Pro-0813")
	}
	wantDSMap := ai.ThinkingLevelMap{
		Minimal: jsonx.Null[string](),
		Low:     jsonx.Null[string](),
		Medium:  jsonx.Null[string](),
		High:    jsonx.Some("high"),
		Xhigh:   jsonx.Null[string](),
	}
	if deepSeekV4.ThinkingLevelMap == nil || *deepSeekV4.ThinkingLevelMap != wantDSMap {
		t.Fatalf("DeepSeek-V4 thinkingLevelMap = %+v, want %+v", deepSeekV4.ThinkingLevelMap, wantDSMap)
	}
	dsCompat, ok := deepSeekV4.Compat.V.(*ai.OpenAICompletionsCompat)
	if !ok || dsCompat == nil ||
		dsCompat.SupportsReasoningEffort == nil || !*dsCompat.SupportsReasoningEffort ||
		dsCompat.ThinkingFormat == nil || *dsCompat.ThinkingFormat != ai.ThinkingFormatTogether {
		t.Fatalf("unexpected DeepSeek-V4 compat: %+v", deepSeekV4.Compat.V)
	}
}

func TestTogetherModels_ResolvesEnvApiKey(t *testing.T) {
	t.Setenv("TOGETHER_API_KEY", "test-together-key")

	keys := ai.FindEnvKeys("together", nil)
	if !slices.Equal(keys, []string{"TOGETHER_API_KEY"}) {
		t.Fatalf("FindEnvKeys = %v, want [TOGETHER_API_KEY]", keys)
	}
	apiKey := ai.GetEnvApiKey("together", nil)
	if apiKey == nil || *apiKey != "test-together-key" {
		t.Fatalf("GetEnvApiKey = %v, want test-together-key", apiKey)
	}
}

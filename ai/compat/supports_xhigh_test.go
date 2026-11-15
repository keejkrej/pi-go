// Ported from packages/ai/test/supports-xhigh.test.ts (pi v1.5.2).

package compat_test

import (
	"slices"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/compat"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func tsxRequireModel(t *testing.T, provider compat.BuiltinProvider, modelId string) *ai.Model {
	t.Helper()
	m := compat.GetModel(provider, modelId)
	if m == nil {
		t.Fatalf("missing model %s/%s", provider, modelId)
	}
	return m
}

func tsxHasLevel(levels []ai.ModelThinkingLevel, want ai.ModelThinkingLevel) bool {
	return slices.Contains(levels, want)
}

func TestSupportedThinkingLevels_AnthropicModels(t *testing.T) {
	t.Parallel()
	opus46 := tsxRequireModel(t, "anthropic", "claude-opus-4-6")
	levels46 := ai.GetSupportedThinkingLevels(opus46)
	if !tsxHasLevel(levels46, ai.ModelThinkingLevelMax) || tsxHasLevel(levels46, ai.ModelThinkingLevelXhigh) {
		t.Fatalf("opus-4-6 levels = %v", levels46)
	}

	opus48 := tsxRequireModel(t, "anthropic", "claude-opus-4-8")
	levels48 := ai.GetSupportedThinkingLevels(opus48)
	if !tsxHasLevel(levels48, ai.ModelThinkingLevelXhigh) || !tsxHasLevel(levels48, ai.ModelThinkingLevelMax) {
		t.Fatalf("opus-4-8 levels = %v", levels48)
	}

	opus5 := tsxRequireModel(t, "anthropic", "claude-opus-5")
	levels5 := ai.GetSupportedThinkingLevels(opus5)
	if !tsxHasLevel(levels5, ai.ModelThinkingLevelXhigh) || !tsxHasLevel(levels5, ai.ModelThinkingLevelMax) {
		t.Fatalf("opus-5 levels = %v", levels5)
	}

	opus55 := tsxRequireModel(t, "anthropic", "claude-opus-5-5")
	if opus55.Cost.ModelCostRates != (ai.ModelCostRates{Input: 4, Output: 20, CacheRead: 0.2, CacheWrite: 5}) ||
		opus55.ContextWindow != 1_000_000 ||
		opus55.MaxTokens != 128_000 {
		t.Fatalf("unexpected opus-5-5 metadata: %+v", opus55)
	}
	c55, ok := opus55.Compat.V.(*ai.AnthropicMessagesCompat)
	if !ok || c55 == nil ||
		c55.ForceAdaptiveThinking == nil || !*c55.ForceAdaptiveThinking ||
		c55.SupportsMidConvoEffort == nil || !*c55.SupportsMidConvoEffort ||
		c55.SupportsMidConvoSystemMessages == nil || !*c55.SupportsMidConvoSystemMessages ||
		c55.SupportsMidConvoToolChanges == nil || !*c55.SupportsMidConvoToolChanges {
		t.Fatalf("unexpected opus-5-5 compat: %+v", opus55.Compat.V)
	}
	wantAlwaysOn := []ai.ModelThinkingLevel{
		ai.ModelThinkingLevelLow,
		ai.ModelThinkingLevelMedium,
		ai.ModelThinkingLevelHigh,
		ai.ModelThinkingLevelXhigh,
		ai.ModelThinkingLevelMax,
	}
	if got := ai.GetSupportedThinkingLevels(opus55); !slices.Equal(got, wantAlwaysOn) {
		t.Fatalf("opus-5-5 levels = %v, want %v", got, wantAlwaysOn)
	}

	sonnet55 := tsxRequireModel(t, "anthropic", "claude-sonnet-5-5")
	if sonnet55.Cost.ModelCostRates != (ai.ModelCostRates{Input: 2, Output: 10, CacheRead: 0.1, CacheWrite: 2.5}) ||
		sonnet55.ContextWindow != 1_000_000 ||
		sonnet55.MaxTokens != 128_000 {
		t.Fatalf("unexpected sonnet-5-5 metadata: %+v", sonnet55)
	}
	sc55, ok := sonnet55.Compat.V.(*ai.AnthropicMessagesCompat)
	if !ok || sc55 == nil ||
		sc55.ForceAdaptiveThinking == nil || !*sc55.ForceAdaptiveThinking ||
		sc55.SupportsMidConvoEffort == nil || !*sc55.SupportsMidConvoEffort ||
		sc55.SupportsMidConvoSystemMessages == nil || !*sc55.SupportsMidConvoSystemMessages ||
		sc55.SupportsMidConvoToolChanges == nil || !*sc55.SupportsMidConvoToolChanges ||
		sc55.SupportsTemperature == nil || *sc55.SupportsTemperature != false {
		t.Fatalf("unexpected sonnet-5-5 compat: %+v", sonnet55.Compat.V)
	}
	if got := ai.GetSupportedThinkingLevels(sonnet55); !slices.Equal(got, wantAlwaysOn) {
		t.Fatalf("sonnet-5-5 levels = %v, want %v", got, wantAlwaysOn)
	}

	sonnet46 := tsxRequireModel(t, "anthropic", "claude-sonnet-4-6")
	ls46 := ai.GetSupportedThinkingLevels(sonnet46)
	if !tsxHasLevel(ls46, ai.ModelThinkingLevelMax) || tsxHasLevel(ls46, ai.ModelThinkingLevelXhigh) {
		t.Fatalf("sonnet-4-6 levels = %v", ls46)
	}

	sonnet5 := tsxRequireModel(t, "anthropic", "claude-sonnet-5")
	ls5 := ai.GetSupportedThinkingLevels(sonnet5)
	if !tsxHasLevel(ls5, ai.ModelThinkingLevelXhigh) || !tsxHasLevel(ls5, ai.ModelThinkingLevelMax) {
		t.Fatalf("sonnet-5 levels = %v", ls5)
	}

	fable5 := tsxRequireModel(t, "anthropic", "claude-fable-5")
	lf5 := ai.GetSupportedThinkingLevels(fable5)
	if !tsxHasLevel(lf5, ai.ModelThinkingLevelXhigh) ||
		!tsxHasLevel(lf5, ai.ModelThinkingLevelMax) ||
		tsxHasLevel(lf5, ai.ModelThinkingLevelOff) {
		t.Fatalf("claude-fable-5 levels = %v", lf5)
	}

	sonnet45 := tsxRequireModel(t, "anthropic", "claude-sonnet-4-5")
	ls45 := ai.GetSupportedThinkingLevels(sonnet45)
	if tsxHasLevel(ls45, ai.ModelThinkingLevelXhigh) || tsxHasLevel(ls45, ai.ModelThinkingLevelMax) {
		t.Fatalf("sonnet-4-5 levels = %v", ls45)
	}
}

func TestSupportedThinkingLevels_OpenAIAndCodexAndAzure(t *testing.T) {
	t.Parallel()
	codexModels := []string{
		"gpt-5.6-sol",
		"gpt-5.6-terra",
		"gpt-5.6-luna",
		"gpt-6-astra",
		"gpt-6-sol",
		"gpt-6-luna",
		"gpt-6.1-sol",
		"gpt-6.1-terra",
	}
	for _, id := range codexModels {
		m := tsxRequireModel(t, "openai-codex", id)
		if !tsxHasLevel(ai.GetSupportedThinkingLevels(m), ai.ModelThinkingLevelXhigh) {
			t.Fatalf("openai-codex/%s missing xhigh", id)
		}
	}

	openAIWant := []ai.ModelThinkingLevel{
		ai.ModelThinkingLevelOff,
		ai.ModelThinkingLevelLow,
		ai.ModelThinkingLevelMedium,
		ai.ModelThinkingLevelHigh,
		ai.ModelThinkingLevelXhigh,
		ai.ModelThinkingLevelMax,
	}
	for _, id := range []string{"gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "gpt-6-sol", "gpt-6-luna", "gpt-6.1-terra"} {
		m := tsxRequireModel(t, "openai", id)
		if got := ai.GetSupportedThinkingLevels(m); !slices.Equal(got, openAIWant) {
			t.Fatalf("openai/%s levels = %v, want %v", id, got, openAIWant)
		}
	}

	solExpected := map[compat.BuiltinProvider][]ai.ModelThinkingLevel{
		"openai": {ai.ModelThinkingLevelLow, ai.ModelThinkingLevelMedium, ai.ModelThinkingLevelHigh, ai.ModelThinkingLevelXhigh, ai.ModelThinkingLevelMax},
		"azure":  {ai.ModelThinkingLevelLow, ai.ModelThinkingLevelMedium, ai.ModelThinkingLevelHigh, ai.ModelThinkingLevelXhigh, ai.ModelThinkingLevelMax},
		"openai-codex": {
			ai.ModelThinkingLevelMinimal, ai.ModelThinkingLevelLow, ai.ModelThinkingLevelMedium,
			ai.ModelThinkingLevelHigh, ai.ModelThinkingLevelXhigh, ai.ModelThinkingLevelMax,
		},
	}
	for provider, want := range solExpected {
		m := tsxRequireModel(t, provider, "gpt-6.1-sol")
		if got := ai.GetSupportedThinkingLevels(m); !slices.Equal(got, want) {
			t.Fatalf("%s/gpt-6.1-sol levels = %v, want %v", provider, got, want)
		}
		if m.ThinkingLevelMap == nil || !m.ThinkingLevelMap.Off.IsNull() {
			t.Fatalf("%s/gpt-6.1-sol thinkingLevelMap.off = %+v, want null", provider, m.ThinkingLevelMap)
		}
	}

	for _, provider := range []compat.BuiltinProvider{"openai", "azure", "openai-codex"} {
		m := tsxRequireModel(t, provider, "gpt-6.1-terra")
		if m.ThinkingLevelMap == nil || !m.ThinkingLevelMap.Off.Equal(jsonx.Some("none")) {
			t.Fatalf("%s/gpt-6.1-terra off = %+v, want none", provider, m.ThinkingLevelMap)
		}
		wantMin := jsonx.Null[string]()
		if provider == "openai-codex" {
			wantMin = jsonx.Some("low")
		}
		if !m.ThinkingLevelMap.Minimal.Equal(wantMin) {
			t.Fatalf("%s/gpt-6.1-terra minimal = %+v, want %+v", provider, m.ThinkingLevelMap.Minimal, wantMin)
		}
	}

	metadataCases := []struct {
		modelId            string
		cost               ai.ModelCostRates
		codexContextWindow int
	}{
		{"gpt-6-sol", ai.ModelCostRates{Input: 2, Output: 10, CacheRead: 0.2, CacheWrite: 2.5}, 272000},
		{"gpt-6-luna", ai.ModelCostRates{Input: 0.1, Output: 0.5, CacheRead: 0.01, CacheWrite: 0.125}, 272000},
		{"gpt-6.1-sol", ai.ModelCostRates{Input: 2, Output: 10, CacheRead: 0.1, CacheWrite: 2.5}, 500000},
		{"gpt-6.1-terra", ai.ModelCostRates{Input: 1, Output: 5, CacheRead: 0.05, CacheWrite: 1.25}, 500000},
	}
	for _, tc := range metadataCases {
		for _, provider := range []compat.BuiltinProvider{"openai", "openai-codex"} {
			m := tsxRequireModel(t, provider, tc.modelId)
			if !slices.Equal(m.Input, []ai.ModelModality{ai.ModelModalityText, ai.ModelModalityImage}) {
				t.Fatalf("%s/%s input = %v", provider, tc.modelId, m.Input)
			}
			wantCtx := 272000
			if provider == "openai-codex" {
				wantCtx = tc.codexContextWindow
			}
			if m.ContextWindow != wantCtx || m.MaxTokens != 128000 {
				t.Fatalf("%s/%s limits = (%d, %d)", provider, tc.modelId, m.ContextWindow, m.MaxTokens)
			}
			wantTier := ai.ModelCostTier{
				InputTokensAbove: 272000,
				ModelCostRates: ai.ModelCostRates{
					Input:      tc.cost.Input * 2,
					Output:     tc.cost.Output * 1.5,
					CacheRead:  tc.cost.CacheRead * 2,
					CacheWrite: tc.cost.CacheWrite * 2,
				},
			}
			if m.Cost.ModelCostRates != tc.cost || len(m.Cost.Tiers) != 1 || m.Cost.Tiers[0] != wantTier {
				t.Fatalf("%s/%s cost = %+v", provider, tc.modelId, m.Cost)
			}
			rc, ok := m.Compat.V.(*ai.OpenAIResponsesCompat)
			if !ok || rc == nil ||
				rc.SupportsAdditionalTools == nil || !*rc.SupportsAdditionalTools ||
				rc.SupportsMidConvoSystemMessages == nil || !*rc.SupportsMidConvoSystemMessages ||
				rc.SupportsOpenAIGrammarTools == nil || !*rc.SupportsOpenAIGrammarTools ||
				rc.SupportsToolSearch == nil || !*rc.SupportsToolSearch {
				t.Fatalf("%s/%s compat = %+v", provider, tc.modelId, m.Compat.V)
			}
		}
	}

	for _, id := range []string{"gpt-6-astra", "gpt-6-luna", "gpt-6-sol", "gpt-6.1-sol", "gpt-6.1-terra"} {
		m := tsxRequireModel(t, "azure", id)
		if m.ContextWindow != 1050000 {
			t.Fatalf("azure/%s contextWindow = %d, want 1050000", id, m.ContextWindow)
		}
	}
}

func TestSupportedThinkingLevels_OtherProviders(t *testing.T) {
	t.Parallel()
	pro := tsxRequireModel(t, "openai", "gpt-5.5-pro")
	wantPro := []ai.ModelThinkingLevel{ai.ModelThinkingLevelMedium, ai.ModelThinkingLevelHigh, ai.ModelThinkingLevelXhigh}
	if got := ai.GetSupportedThinkingLevels(pro); !slices.Equal(got, wantPro) {
		t.Fatalf("openai/gpt-5.5-pro = %v", got)
	}
	orPro := tsxRequireModel(t, "openrouter", "openai/gpt-5.5-pro")
	if got := ai.GetSupportedThinkingLevels(orPro); !slices.Equal(got, wantPro) {
		t.Fatalf("openrouter/openai/gpt-5.5-pro = %v", got)
	}

	dsFlash := tsxRequireModel(t, "deepseek", "deepseek-flash")
	wantDs := []ai.ModelThinkingLevel{ai.ModelThinkingLevelOff, ai.ModelThinkingLevelLow, ai.ModelThinkingLevelHigh, ai.ModelThinkingLevelMax}
	if got := ai.GetSupportedThinkingLevels(dsFlash); !slices.Equal(got, wantDs) {
		t.Fatalf("deepseek/deepseek-flash = %v", got)
	}
	orDs := tsxRequireModel(t, "openrouter", "deepseek/deepseek-v4.1-flash")
	if got := ai.GetSupportedThinkingLevels(orDs); !slices.Equal(got, wantDs) {
		t.Fatalf("openrouter/deepseek/deepseek-v4.1-flash = %v", got)
	}
	ogDs := tsxRequireModel(t, "opencode-go", "deepseek-v4.1-flash")
	if got := ai.GetSupportedThinkingLevels(ogDs); !slices.Equal(got, []ai.ModelThinkingLevel{ai.ModelThinkingLevelLow, ai.ModelThinkingLevelHigh, ai.ModelThinkingLevelMax}) {
		t.Fatalf("opencode-go/deepseek-v4.1-flash = %v", got)
	}

	for _, p := range []compat.BuiltinProvider{"moonshotai", "moonshotai-cn"} {
		k27 := tsxRequireModel(t, p, "kimi-k2.7-code")
		if got := ai.GetSupportedThinkingLevels(k27); !slices.Equal(got, []ai.ModelThinkingLevel{
			ai.ModelThinkingLevelMinimal, ai.ModelThinkingLevelLow, ai.ModelThinkingLevelMedium, ai.ModelThinkingLevelHigh,
		}) {
			t.Fatalf("%s/kimi-k2.7-code = %v", p, got)
		}
		k3 := tsxRequireModel(t, p, "kimi-k3")
		if got := ai.GetSupportedThinkingLevels(k3); !slices.Equal(got, []ai.ModelThinkingLevel{
			ai.ModelThinkingLevelLow, ai.ModelThinkingLevelHigh, ai.ModelThinkingLevelMax,
		}) {
			t.Fatalf("%s/kimi-k3 = %v", p, got)
		}
	}

	kc3 := tsxRequireModel(t, "kimi-coding", "k3")
	if got := ai.GetSupportedThinkingLevels(kc3); !slices.Equal(got, []ai.ModelThinkingLevel{
		ai.ModelThinkingLevelLow, ai.ModelThinkingLevelHigh, ai.ModelThinkingLevelMax,
	}) {
		t.Fatalf("kimi-coding/k3 = %v", got)
	}
	kcHighspeed := tsxRequireModel(t, "kimi-coding", "kimi-for-coding-highspeed")
	if tsxHasLevel(ai.GetSupportedThinkingLevels(kcHighspeed), ai.ModelThinkingLevelOff) {
		t.Fatal("kimi-coding/kimi-for-coding-highspeed must exclude off")
	}

	grokBuild := tsxRequireModel(t, "opencode", "grok-build-0.1")
	if got := ai.GetSupportedThinkingLevels(grokBuild); !slices.Equal(got, []ai.ModelThinkingLevel{ai.ModelThinkingLevelHigh}) {
		t.Fatalf("opencode/grok-build-0.1 = %v", got)
	}

	orDsFlash := tsxRequireModel(t, "openrouter", "deepseek/deepseek-v4-flash")
	if got := ai.GetSupportedThinkingLevels(orDsFlash); !slices.Equal(got, []ai.ModelThinkingLevel{
		ai.ModelThinkingLevelOff, ai.ModelThinkingLevelHigh, ai.ModelThinkingLevelXhigh,
	}) {
		t.Fatalf("openrouter/deepseek/deepseek-v4-flash = %v", got)
	}

	orOpus46 := tsxRequireModel(t, "openrouter", "anthropic/claude-opus-4.6")
	lor46 := ai.GetSupportedThinkingLevels(orOpus46)
	if !tsxHasLevel(lor46, ai.ModelThinkingLevelMax) || tsxHasLevel(lor46, ai.ModelThinkingLevelXhigh) {
		t.Fatalf("openrouter/anthropic/claude-opus-4.6 = %v", lor46)
	}

	brOpus5 := tsxRequireModel(t, "amazon-bedrock", "global.anthropic.claude-opus-5")
	lbr5 := ai.GetSupportedThinkingLevels(brOpus5)
	if !tsxHasLevel(lbr5, ai.ModelThinkingLevelXhigh) || !tsxHasLevel(lbr5, ai.ModelThinkingLevelMax) {
		t.Fatalf("amazon-bedrock/global.anthropic.claude-opus-5 = %v", lbr5)
	}

	grok46 := tsxRequireModel(t, "xai", "grok-4.6")
	if got := ai.GetSupportedThinkingLevels(grok46); !slices.Equal(got, []ai.ModelThinkingLevel{
		ai.ModelThinkingLevelLow, ai.ModelThinkingLevelMedium, ai.ModelThinkingLevelHigh, ai.ModelThinkingLevelXhigh,
	}) {
		t.Fatalf("xai/grok-4.6 = %v", got)
	}

	brFable5 := tsxRequireModel(t, "amazon-bedrock", "global.anthropic.claude-fable-5")
	lbrFable5 := ai.GetSupportedThinkingLevels(brFable5)
	if !tsxHasLevel(lbrFable5, ai.ModelThinkingLevelXhigh) ||
		!tsxHasLevel(lbrFable5, ai.ModelThinkingLevelMax) ||
		tsxHasLevel(lbrFable5, ai.ModelThinkingLevelOff) {
		t.Fatalf("amazon-bedrock/global.anthropic.claude-fable-5 = %v", lbrFable5)
	}
}

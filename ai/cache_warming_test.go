package ai_test

import (
	"context"
	"math"
	"testing"

	"github.com/keejkrej/pi-go/ai"
)

func cwSampleModel(cacheReadCost, cacheWriteCost, inputCost float64, promptCache *ai.ModelPromptCache) *ai.Model {
	return &ai.Model{
		BaseModel: ai.BaseModel{
			Id:       "cw-test",
			Name:     "CW Test",
			Api:      string(ai.KnownApiAnthropicMessages),
			Provider: "anthropic",
			BaseUrl:  "https://api.anthropic.com",
			Input:    []ai.ModelModality{ai.ModelModalityText},
			Cost: ai.ModelCost{
				ModelCostRates: ai.ModelCostRates{
					Input:      inputCost,
					Output:     15,
					CacheRead:  cacheReadCost,
					CacheWrite: cacheWriteCost,
				},
			},
		},
		PromptCache: promptCache,
	}
}

func cwAlmostEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

func TestCacheWarmingDelayMs(t *testing.T) {
	if got := ai.GetCacheWarmingDelayMs(math.NaN()); got != nil {
		t.Fatalf("expected nil for NaN, got %v", *got)
	}
	if got := ai.GetCacheWarmingDelayMs(math.Inf(1)); got != nil {
		t.Fatalf("expected nil for +Inf, got %v", *got)
	}
	if got := ai.GetCacheWarmingDelayMs(10_000); got != nil {
		t.Fatalf("expected nil for <= 10s, got %v", *got)
	}
	// min(300_000 * 0.9 = 270_000, 300_000 - 10_000 = 290_000) = 270_000
	if got := ai.GetCacheWarmingDelayMs(300_000); got == nil || *got != 270_000 {
		t.Fatalf("expected 270000 for 5m TTL, got %v", got)
	}
	// min(15_000 * 0.9 = 13_500, 15_000 - 10_000 = 5_000) = 5_000 (keeps >= 10s margin)
	if got := ai.GetCacheWarmingDelayMs(15_000); got == nil || *got != 5_000 {
		t.Fatalf("expected 5000 for 15s TTL, got %v", got)
	}
}

func TestPromptCacheTtlMs(t *testing.T) {
	if got := ai.GetPromptCacheTtlMs(nil, nil); got != nil {
		t.Fatalf("expected nil for nil model, got %v", *got)
	}

	shortSec := 300
	longSec := 3600
	model := cwSampleModel(0.3, 3.75, 3.0, &ai.ModelPromptCache{
		Short: &shortSec,
		Long:  &longSec,
	})

	if got := ai.GetPromptCacheTtlMs(model, nil); got == nil || *got != 300_000 {
		t.Fatalf("expected default short TTL 300000, got %v", got)
	}

	env := ai.ProviderEnv{"PI_CACHE_RETENTION": "long"}
	if got := ai.GetPromptCacheTtlMs(model, &ai.SimpleStreamOptions{
		StreamOptions: ai.StreamOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{Env: env},
		},
	}); got == nil || *got != 3_600_000 {
		t.Fatalf("expected PI_CACHE_RETENTION=long TTL 3600000, got %v", got)
	}

	noneRet := ai.CacheRetentionNone
	if got := ai.GetPromptCacheTtlMs(model, &ai.SimpleStreamOptions{
		StreamOptions: ai.StreamOptions{
			CacheRetention:         &noneRet,
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{Env: env},
		},
	}); got != nil {
		t.Fatalf("expected explicit CacheRetentionNone to override env and return nil, got %v", *got)
	}

	longRet := ai.CacheRetentionLong
	shortOnly := cwSampleModel(0.3, 3.75, 3.0, &ai.ModelPromptCache{Short: &shortSec})
	if got := ai.GetPromptCacheTtlMs(shortOnly, &ai.SimpleStreamOptions{
		StreamOptions: ai.StreamOptions{CacheRetention: &longRet},
	}); got != nil {
		t.Fatalf("expected nil when long tier missing, got %v", *got)
	}
}

func TestCacheWarmingReplayable(t *testing.T) {
	high := ai.ThinkingLevelHigh
	anthropicNoCompat := cwSampleModel(0.3, 3.75, 3.0, nil)
	if !ai.IsCacheWarmingReplayable(anthropicNoCompat, nil) {
		t.Fatal("expected replayable without reasoning")
	}
	if ai.IsCacheWarmingReplayable(anthropicNoCompat, &ai.SimpleStreamOptions{Reasoning: &high}) {
		t.Fatal("expected Anthropic budget thinking without ForceAdaptiveThinking not replayable")
	}

	adaptiveTrue := true
	anthropicAdaptive := cwSampleModel(0.3, 3.75, 3.0, nil)
	anthropicAdaptive.Compat = ai.ModelCompatValue{V: &ai.AnthropicMessagesCompat{ForceAdaptiveThinking: &adaptiveTrue}}
	if !ai.IsCacheWarmingReplayable(anthropicAdaptive, &ai.SimpleStreamOptions{Reasoning: &high}) {
		t.Fatal("expected ForceAdaptiveThinking=true to be replayable")
	}

	otherApi := cwSampleModel(0.3, 3.75, 3.0, nil)
	otherApi.Api = string(ai.KnownApiOpenaiResponses)
	if !ai.IsCacheWarmingReplayable(otherApi, &ai.SimpleStreamOptions{Reasoning: &high}) {
		t.Fatal("expected non-Anthropic APIs to be replayable with reasoning")
	}
}

func TestCacheWarmingRequestOptions(t *testing.T) {
	temp := 0.7
	origRetries := 3
	origMaxTokens := 4096
	orig := &ai.SimpleStreamOptions{
		StreamOptions: ai.StreamOptions{
			Temperature: &temp,
			MaxTokens:   &origMaxTokens,
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
				MaxRetries: &origRetries,
			},
		},
	}
	out := ai.GetCacheWarmingRequestOptions(context.Background(), orig)
	if out.MaxTokens == nil || *out.MaxTokens != 1 {
		t.Fatalf("expected MaxTokens=1, got %v", out.MaxTokens)
	}
	if out.MaxRetries == nil || *out.MaxRetries != 0 {
		t.Fatalf("expected MaxRetries=0, got %v", out.MaxRetries)
	}
	if out.DisableContinuation == nil || !*out.DisableContinuation {
		t.Fatalf("expected DisableContinuation=true, got %v", out.DisableContinuation)
	}
	if out.Temperature == nil || *out.Temperature != 0.7 {
		t.Fatalf("expected Temperature preserved, got %v", out.Temperature)
	}
	// Original struct fields must remain untouched.
	if *orig.MaxTokens != 4096 || *orig.MaxRetries != 3 || orig.DisableContinuation != nil {
		t.Fatal("expected GetCacheWarmingRequestOptions not to mutate original options")
	}
}

func TestEvaluateCacheWarming(t *testing.T) {
	// Opus-like pricing: cacheRead=0.5/M, cacheWrite=6.25/M, input=5/M, output=25/M
	model := cwSampleModel(0.5, 6.25, 5.0, nil)
	model.Cost.Output = 25.0

	// 100k tokens: cacheHit=0.05, cacheMiss=0.625, missSavings=0.575, warmCost=0.05 + 0.000025 = 0.050025
	// streaming: expected = 1.0*0.575 - 0.050025 = 0.524975 >= 0.05 -> warm
	streaming := ai.EvaluateCacheWarming(model, 100_000, ai.CacheWarmingPhaseStreaming)
	if !streaming.EconomicsAvailable || streaming.Action != ai.CacheWarmingActionWarm {
		t.Fatalf("expected warm in streaming phase, got %+v", streaming)
	}
	if streaming.ContinuationProbability != 1.0 || !cwAlmostEqual(streaming.MissCost, 0.575) {
		t.Fatalf("unexpected streaming metrics: %+v", streaming)
	}

	// idle: expected = 0.15*0.575 - 0.050025 = 0.036225 < 0.05 -> stop
	idle := ai.EvaluateCacheWarming(model, 100_000, ai.CacheWarmingPhaseIdle)
	if idle.Action != ai.CacheWarmingActionStop {
		t.Fatalf("expected stop in idle phase below $0.05 floor, got %+v", idle)
	}
	if !cwAlmostEqual(idle.ExpectedSavings, 0.15*0.575-streaming.WarmCost) {
		t.Fatalf("unexpected idle expectedSavings: %+v", idle)
	}

	// When CacheWrite == 0, miss uses Input pricing (OpenAI-style).
	noWriteModel := cwSampleModel(0.5, 0, 5.0, nil)
	noWrite := ai.EvaluateCacheWarming(noWriteModel, 100_000, ai.CacheWarmingPhaseStreaming)
	if !cwAlmostEqual(noWrite.MissCost, 0.45) || noWrite.Action != ai.CacheWarmingActionWarm {
		t.Fatalf("expected input-priced miss fallback (0.45) -> warm, got %+v", noWrite)
	}

	// Zero-cost / zero-token model disables economics.
	freeModel := cwSampleModel(0, 0, 0, nil)
	freeModel.Cost.Output = 0
	free := ai.EvaluateCacheWarming(freeModel, 100_000, ai.CacheWarmingPhaseStreaming)
	if free.EconomicsAvailable || free.Action != ai.CacheWarmingActionStop {
		t.Fatalf("expected free model not to warm, got %+v", free)
	}
	if zero := ai.EvaluateCacheWarming(model, 0, ai.CacheWarmingPhaseStreaming); zero.EconomicsAvailable || zero.Action != ai.CacheWarmingActionStop {
		t.Fatalf("expected zero promptTokens not to warm, got %+v", zero)
	}
}

// Ported from packages/ai/src/cache-warming.ts (pi v1.5.2).

package ai

import (
	"context"
	"math"
)

// CacheWarmingAction indicates whether a cache-warming refresh is sent or stopped.
type CacheWarmingAction string

const (
	CacheWarmingActionWarm CacheWarmingAction = "warm"
	CacheWarmingActionStop CacheWarmingAction = "stop"
)

// CacheWarmingPhase identifies whether the agent run is still streaming or idle.
type CacheWarmingPhase string

const (
	CacheWarmingPhaseStreaming CacheWarmingPhase = "streaming"
	CacheWarmingPhaseIdle      CacheWarmingPhase = "idle"
)

// CacheWarmingDecision holds inputs and outcome of a prompt cache warming evaluation.
type CacheWarmingDecision struct {
	Phase                   CacheWarmingPhase  `json:"phase"`
	WarmCost                float64            `json:"warmCost"`
	MissCost                float64            `json:"missCost"`
	ContinuationProbability float64            `json:"continuationProbability"`
	ExpectedSavings         float64            `json:"expectedSavings"`
	EconomicsAvailable      bool               `json:"economicsAvailable"`
	Action                  CacheWarmingAction `json:"action"`
}

const (
	// CacheWarmingMaxAgeMs caps streaming warming past one hour after the real request.
	CacheWarmingMaxAgeMs int64 = 60 * 60_000
	// CacheWarmingMaxIdleAgeMs caps idle warming past 30 minutes.
	CacheWarmingMaxIdleAgeMs int64 = 30 * 60_000
	// CacheWarmingMinimumExpectedSavings is the USD savings floor for warming.
	CacheWarmingMinimumExpectedSavings float64 = 0.05
	// CacheWarmingIdleContinuationProbability is the idle continuation probability estimate.
	CacheWarmingIdleContinuationProbability float64 = 0.15
)

// GetCacheWarmingDelayMs returns the refresh delay (90% of TTL while keeping >= 10s margin),
// or nil when ttlMs <= 10_000.
func GetCacheWarmingDelayMs(ttlMs float64) *int64 {
	if math.IsNaN(ttlMs) || math.IsInf(ttlMs, 0) || ttlMs <= 10_000 {
		return nil
	}
	delay := int64(math.Floor(math.Min(ttlMs*0.9, ttlMs-10_000)))
	if delay < 1 {
		delay = 1
	}
	return &delay
}

// GetPromptCacheTtlMs returns the prompt cache lifetime in milliseconds for model and options,
// or nil when caching is disabled or the model specifies no lifetime for the selected retention tier.
func GetPromptCacheTtlMs(model *Model, options *SimpleStreamOptions) *int64 {
	if model == nil {
		return nil
	}
	var retention CacheRetention
	if options != nil && options.CacheRetention != nil {
		retention = *options.CacheRetention
	} else {
		var env ProviderEnv
		if options != nil {
			env = options.Env
		}
		if GetProviderEnvValue("PI_CACHE_RETENTION", env) == "long" {
			retention = CacheRetentionLong
		} else {
			retention = CacheRetentionShort
		}
	}
	if retention == CacheRetentionNone || model.PromptCache == nil {
		return nil
	}
	var seconds *int
	switch retention {
	case CacheRetentionShort:
		seconds = model.PromptCache.Short
	case CacheRetentionLong:
		seconds = model.PromptCache.Long
	}
	if seconds == nil {
		return nil
	}
	ms := int64(*seconds) * 1000
	return &ms
}

// IsCacheWarmingReplayable reports whether replaying a request with maxTokens=1 leaves
// its cache entry intact. Anthropic budget-based thinking derives budget_tokens from max_tokens,
// so non-adaptive Anthropic thinking requests cannot be safely replayed.
func IsCacheWarmingReplayable(model *Model, options *SimpleStreamOptions) bool {
	if model == nil || options == nil || options.Reasoning == nil || *options.Reasoning == "" {
		return true
	}
	if model.Api != string(KnownApiAnthropicMessages) {
		return true
	}
	switch c := model.Compat.V.(type) {
	case *AnthropicMessagesCompat:
		if c != nil && c.ForceAdaptiveThinking != nil {
			return *c.ForceAdaptiveThinking
		}
	case *ProviderCompat:
		if c != nil && c.ForceAdaptiveThinking != nil {
			return *c.ForceAdaptiveThinking
		}
	}
	return false
}

// GetCacheWarmingRequestOptions copies options with a 1-token cap, zero retries,
// and disableContinuation=true. ctx is passed alongside the request.
func GetCacheWarmingRequestOptions(ctx context.Context, options *SimpleStreamOptions) *SimpleStreamOptions {
	var cp SimpleStreamOptions
	if options != nil {
		cp = *options
	}
	one := 1
	disable := true
	zero := 0
	cp.MaxTokens = &one
	cp.DisableContinuation = &disable
	cp.MaxRetries = &zero
	_ = ctx
	return &cp
}

// EvaluateCacheWarming computes whether refreshing a promptTokens cache entry pays off.
func EvaluateCacheWarming(model *Model, promptTokens int, phase CacheWarmingPhase) CacheWarmingDecision {
	cacheHitCost := cwPrice(model, Usage{CacheRead: promptTokens})
	var cacheMissTokens Usage
	if model != nil && model.Cost.CacheWrite > 0 {
		cacheMissTokens = Usage{CacheWrite: promptTokens}
	} else {
		cacheMissTokens = Usage{Input: promptTokens}
	}
	cacheMissCost := cwPrice(model, cacheMissTokens)
	warmCost := cwPrice(model, Usage{CacheRead: promptTokens, Output: 1})
	missCost := math.Max(0, cacheMissCost-cacheHitCost)
	continuationProb := 1.0
	if phase == CacheWarmingPhaseIdle {
		continuationProb = CacheWarmingIdleContinuationProbability
	}
	economicsAvailable := promptTokens > 0 && (cacheHitCost > 0 || cacheMissCost > 0)
	expectedSavings := continuationProb*missCost - warmCost
	action := CacheWarmingActionStop
	if expectedSavings >= CacheWarmingMinimumExpectedSavings {
		action = CacheWarmingActionWarm
	}
	return CacheWarmingDecision{
		Phase:                   phase,
		WarmCost:                warmCost,
		MissCost:                missCost,
		ContinuationProbability: continuationProb,
		ExpectedSavings:         expectedSavings,
		EconomicsAvailable:      economicsAvailable,
		Action:                  action,
	}
}

func cwPrice(model *Model, usage Usage) float64 {
	if model == nil {
		return 0
	}
	return CalculateCost(model, &usage).Total
}

// Ported from packages/ai/src/api/simple-options.ts (pi v1.5.2).

package api

import (
	"math"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

const (
	soContextSafetyTokens      = 4096
	soContextSafetyRatio       = 0.15
	soMinOutputWithRatioMargin = 32768
	soMinMaxTokens             = 1
	// MinAnswerTokens is the token floor always left for the answer when a thinking budget shares the response ceiling.
	MinAnswerTokens = 1024
)

func soIntPtr(v int) *int { return &v }

// DefaultThinkingBudgets defines default thinking budgets per reasoning level.
var DefaultThinkingBudgets = ai.ThinkingBudgets{
	Minimal: soIntPtr(1024),
	Low:     soIntPtr(2048),
	Medium:  soIntPtr(8192),
	High:    soIntPtr(16384),
}

// ClampMaxTokensToContext fits maxTokens into the context window, leaving room for the input
// being counted higher than estimated.
func ClampMaxTokensToContext(model *ai.Model, c *ai.TranscriptContext, maxTokens int) int {
	if model.ContextWindow <= 0 {
		if maxTokens < soMinMaxTokens {
			return soMinMaxTokens
		}
		return maxTokens
	}
	input := ai.EstimateContextTokens(c).Tokens
	margin := int(math.Ceil(float64(input) * soContextSafetyRatio))
	if margin < soContextSafetyTokens {
		margin = soContextSafetyTokens
	}
	room := model.ContextWindow - input - margin
	if room < soMinOutputWithRatioMargin {
		alt := model.ContextWindow - input - soContextSafetyTokens
		if alt < soMinOutputWithRatioMargin {
			room = alt
		} else {
			room = soMinOutputWithRatioMargin
		}
	}
	if room < soMinMaxTokens {
		room = soMinMaxTokens
	}
	if maxTokens < room {
		return maxTokens
	}
	return room
}

// ResolveSamplingParams merges model-level, thinking-level, and request-level sampling params in order.
func ResolveSamplingParams(
	model *ai.Model,
	thinkingLevel ai.ModelThinkingLevel,
	requestParams *jsonx.Object,
) *jsonx.Object {
	effectiveThinkingLevel := ai.ClampThinkingLevel(model, thinkingLevel)
	var thinkingLevelParams *jsonx.Object
	if model != nil && model.SamplingParamsByThinkingLevel != nil {
		thinkingLevelParams = model.SamplingParamsByThinkingLevel.Get(effectiveThinkingLevel)
	}
	var modelParams *jsonx.Object
	if model != nil {
		modelParams = model.SamplingParams
	}
	if modelParams == nil && thinkingLevelParams == nil && requestParams == nil {
		return nil
	}
	out := jsonx.NewObject()
	if modelParams != nil {
		out.Assign(modelParams)
	}
	if thinkingLevelParams != nil {
		out.Assign(thinkingLevelParams)
	}
	if requestParams != nil {
		out.Assign(requestParams)
	}
	return out
}

// BuildBaseOptions constructs shared StreamOptions from SimpleStreamOptions.
func BuildBaseOptions(
	model *ai.Model,
	c *ai.TranscriptContext,
	options *ai.SimpleStreamOptions,
	apiKey *string,
) ai.StreamOptions {
	thinkingLevel := ai.ModelThinkingLevelOff
	var reqSamplingParams *jsonx.Object
	maxTokensReq := model.MaxTokens
	if options != nil {
		if options.Reasoning != nil {
			thinkingLevel = ai.ModelThinkingLevel(*options.Reasoning)
		}
		reqSamplingParams = options.SamplingParams
		if options.MaxTokens != nil {
			maxTokensReq = *options.MaxTokens
		}
	}
	samplingParams := ResolveSamplingParams(model, thinkingLevel, reqSamplingParams)
	clampedMaxTokens := ClampMaxTokensToContext(model, c, maxTokensReq)

	out := ai.StreamOptions{
		SamplingParams: samplingParams,
		MaxTokens:      &clampedMaxTokens,
	}
	var resolvedKey *string
	if apiKey != nil && *apiKey != "" {
		resolvedKey = apiKey
	} else if options != nil {
		resolvedKey = options.ApiKey
	}
	out.ApiKey = resolvedKey

	if options != nil {
		out.Temperature = options.Temperature
		out.TelemetryContext = options.TelemetryContext
		out.HTTPClient = options.HTTPClient
		out.Transport = options.Transport
		out.CacheRetention = options.CacheRetention
		out.SessionId = options.SessionId
		out.DisableContinuation = options.DisableContinuation
		out.Headers = options.Headers
		out.Identity = options.Identity
		out.OnPayload = options.OnPayload
		out.OnResponse = options.OnResponse
		out.OnProviderStreamEvent = options.OnProviderStreamEvent
		out.TimeoutMs = options.TimeoutMs
		out.WebsocketConnectTimeoutMs = options.WebsocketConnectTimeoutMs
		out.MaxRetries = options.MaxRetries
		out.MaxRetryDelayMs = options.MaxRetryDelayMs
		out.Metadata = options.Metadata
		out.Env = options.Env
	}
	return out
}

// ClampReasoning maps xhigh and max efforts down to high for providers that cap reasoning at high.
func ClampReasoning(effort *ai.ThinkingLevel) *ai.ThinkingLevel {
	if effort == nil {
		return nil
	}
	if *effort == ai.ThinkingLevelXhigh || *effort == ai.ThinkingLevelMax {
		high := ai.ThinkingLevelHigh
		return &high
	}
	v := *effort
	return &v
}

// ThinkingBudgetForLevel returns the token budget for reasoningLevel, using customBudgets overrides.
func ThinkingBudgetForLevel(reasoningLevel ai.ThinkingLevel, customBudgets *ai.ThinkingBudgets) int {
	minimal := *DefaultThinkingBudgets.Minimal
	low := *DefaultThinkingBudgets.Low
	medium := *DefaultThinkingBudgets.Medium
	high := *DefaultThinkingBudgets.High
	if customBudgets != nil {
		if customBudgets.Minimal != nil {
			minimal = *customBudgets.Minimal
		}
		if customBudgets.Low != nil {
			low = *customBudgets.Low
		}
		if customBudgets.Medium != nil {
			medium = *customBudgets.Medium
		}
		if customBudgets.High != nil {
			high = *customBudgets.High
		}
	}
	level := ClampReasoning(&reasoningLevel)
	switch *level {
	case ai.ThinkingLevelMinimal:
		return minimal
	case ai.ThinkingLevelLow:
		return low
	case ai.ThinkingLevelMedium:
		return medium
	case ai.ThinkingLevelHigh:
		return high
	default:
		return high
	}
}

// ClampThinkingBudgetToAnswerRoom caps a thinking budget so at least MinAnswerTokens remain under a shared ceiling.
func ClampThinkingBudgetToAnswerRoom(thinkingBudget int, ceiling int) int {
	room := ceiling - MinAnswerTokens
	if room < 0 {
		room = 0
	}
	if thinkingBudget < room {
		return thinkingBudget
	}
	return room
}

// AdjustMaxTokensForThinking expands maxTokens to accommodate thinkingBudget (capped at modelMaxTokens)
// and ensures the thinking budget leaves MinAnswerTokens for the response.
func AdjustMaxTokensForThinking(
	baseMaxTokens *int,
	modelMaxTokens int,
	reasoningLevel ai.ThinkingLevel,
	customBudgets *ai.ThinkingBudgets,
) (maxTokens int, thinkingBudget int) {
	thinkingBudget = ThinkingBudgetForLevel(reasoningLevel, customBudgets)
	if baseMaxTokens == nil {
		maxTokens = modelMaxTokens
	} else {
		sum := *baseMaxTokens + thinkingBudget
		if sum < modelMaxTokens {
			maxTokens = sum
		} else {
			maxTokens = modelMaxTokens
		}
	}
	if maxTokens <= thinkingBudget {
		thinkingBudget = ClampThinkingBudgetToAnswerRoom(thinkingBudget, maxTokens)
	}
	return maxTokens, thinkingBudget
}

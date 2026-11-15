// Ported from packages/ai/src/utils/retry.ts (pi v1.5.2).

package ai

import (
	"context"
	"math"
	"strings"

	"github.com/keejkrej/pi-go/internal/jsre"
)

func urBuildProviderErrorPattern(patterns []string) *jsre.Regexp {
	return jsre.MustCompile(strings.Join(patterns, "|"), "i")
}

var urNonRetryableProviderLimitErrorPattern = urBuildProviderErrorPattern([]string{
	"GoUsageLimitError",
	"FreeUsageLimitError",
	"Monthly usage limit reached",
	"available balance",
	"insufficient_quota",
	"out of budget",
	"quota exceeded",
	"billing",
	"subscription_sharing_usage_limit_exceeded",
	"subscription_sharing_workplace_individual_usage_limit_exceeded",
	`"code"\s*:\s*"?1308(?![0-9])`,
})

var urRetryableProviderErrorPattern = urBuildProviderErrorPattern([]string{
	"overloaded",
	"server_busy",
	"servers are currently busy",
	"currently experiencing high demand",
	"model is at capacity",
	"rate.?limit",
	"too many requests",
	`(?<![a-z0-9])(?:429|500|502|503|504|520|524)(?![a-z0-9])`,
	`service.?(?:is.?)?(?:temporarily.?)?unavailable`,
	"server.?error",
	"internal.?error",
	`provider.?returned.?(?:an.?)?error`,
	"exceeded request buffer limit while retrying upstream",
	"unable to forward upstream",
	"upstream.?request.?failed",
	"upstream_stream_read_error",
	"stream was interrupted",
	"http/?2 stream failed",
	"network.?error",
	"connection.?error",
	"connection.?refused",
	"connection.?lost",
	"connection.?closed",
	"other side closed",
	"fetch failed",
	`request to \S+ failed, reason:`,
	"getaddrinfo",
	`(?<![a-z0-9_])(?:ECONNRESET|ECONNABORTED|ECONNREFUSED|ETIMEDOUT|EPIPE)(?![a-z0-9_])`,
	"connection reset by peer",
	"ENOTFOUND",
	"EAI_AGAIN",
	"upstream.?connect",
	"reset before headers",
	"socket hang up",
	"socket connection was closed",
	"timed? out",
	"timeout",
	"terminated",
	"websocket.?closed",
	"websocket.?error",
	"ended without",
	"stream ended before message_stop",
	"stream ended before a terminal response event",
	"http2 request did not get a response",
	"stream disconnected before completion",
	"cut short after the reply had begun",
	"pending stream has been canceled",
	"retry delay",
	"you can retry your request",
	"try your request again",
	"please retry your request",
	"ResourceExhausted",
	"subscription_sharing_usage_unavailable",
	"subscription_sharing_user_unavailable",
})

// DefaultMaxAgentRetryDelayMs is the default agent-level retry delay cap (60 seconds).
const DefaultMaxAgentRetryDelayMs int64 = 60_000

// RetryPolicy configures bounded exponential backoff retry attempts.
type RetryPolicy struct {
	Enabled         bool   `json:"enabled"`
	MaxRetries      int    `json:"maxRetries"`
	BaseDelayMs     int64  `json:"baseDelayMs"`
	MaxAgentDelayMs *int64 `json:"maxAgentDelayMs,omitzero"`
}

// RetryCallbacks emits lifecycle notifications during RetryAssistantCall.
type RetryCallbacks struct {
	OnRetryScheduled    func(attempt int, maxAttempts int, delayMs int64, errorMessage string)
	OnRetryAttemptStart func()
	OnRetryFinished     func(success bool, attempt int, finalError *string)
}

// RetryDelayMs calculates the exponential backoff delay for 1-indexed attempt.
func RetryDelayMs(policy *RetryPolicy, attempt int) int64 {
	if policy == nil {
		return 0
	}
	maxCap := DefaultMaxAgentRetryDelayMs
	if policy.MaxAgentDelayMs != nil {
		maxCap = *policy.MaxAgentDelayMs
	}
	exp := math.Max(0, float64(attempt-1))
	delay := float64(policy.BaseDelayMs) * math.Pow(2, exp)
	if math.IsNaN(delay) || math.IsInf(delay, 0) || delay > float64(math.MaxInt64) {
		return maxCap
	}
	delayInt := int64(delay)
	if delayInt > maxCap {
		return maxCap
	}
	return delayInt
}

// RetryAssistantCall runs a single assistant-producing call with bounded retry on transient errors.
func RetryAssistantCall(
	ctx context.Context,
	produce func() (*AssistantMessage, error),
	policy *RetryPolicy,
	callbacks *RetryCallbacks,
) (*AssistantMessage, error) {
	maxAttempts := 0
	if policy != nil && policy.Enabled {
		maxAttempts = policy.MaxRetries
	}

	attempt := 0
	hasLastRetry := false
	var lastAttempt int
	var lastErrMsg string

	for {
		response, err := produce()
		if err != nil {
			return nil, err
		}

		if response.StopReason == StopReasonAborted {
			if hasLastRetry && callbacks != nil && callbacks.OnRetryFinished != nil {
				callbacks.OnRetryFinished(false, lastAttempt, nil)
			}
			return response, nil
		}

		if response.StopReason != StopReasonError {
			if hasLastRetry && callbacks != nil && callbacks.OnRetryFinished != nil {
				callbacks.OnRetryFinished(true, lastAttempt, nil)
			}
			return response, nil
		}

		if attempt >= maxAttempts || !IsRetryableAssistantError(response) {
			if hasLastRetry && callbacks != nil && callbacks.OnRetryFinished != nil {
				callbacks.OnRetryFinished(false, lastAttempt, response.ErrorMessage)
			}
			return response, nil
		}

		attempt++
		errText := "Unknown error"
		if response.ErrorMessage != nil && *response.ErrorMessage != "" {
			errText = *response.ErrorMessage
		}
		hasLastRetry = true
		lastAttempt = attempt
		lastErrMsg = errText

		delayMs := RetryDelayMs(policy, attempt)
		if callbacks != nil && callbacks.OnRetryScheduled != nil {
			callbacks.OnRetryScheduled(attempt, maxAttempts, delayMs, errText)
		}

		if err := Sleep(ctx, delayMs); err != nil {
			if callbacks != nil && callbacks.OnRetryFinished != nil {
				msg := lastErrMsg
				callbacks.OnRetryFinished(false, attempt, &msg)
			}
			return urCloneAssistantAborted(response), nil
		}

		if callbacks != nil && callbacks.OnRetryAttemptStart != nil {
			callbacks.OnRetryAttemptStart()
		}
	}
}

func urCloneAssistantAborted(response *AssistantMessage) *AssistantMessage {
	if response == nil {
		return &AssistantMessage{
			Role:       "assistant",
			Content:    ContentList{},
			StopReason: StopReasonAborted,
		}
	}
	return &AssistantMessage{
		Role:                  response.Role,
		Content:               response.Content,
		Api:                   response.Api,
		Provider:              response.Provider,
		Model:                 response.Model,
		ResponseModel:         response.ResponseModel,
		ResponseId:            response.ResponseId,
		ProviderThinkingLevel: response.ProviderThinkingLevel,
		ThinkingLevel:         response.ThinkingLevel,
		Diagnostics:           response.Diagnostics,
		Usage:                 response.Usage,
		StopReason:            StopReasonAborted,
		Deferred:              response.Deferred,
		ErrorMessage:          nil,
		RawStopReason:         response.RawStopReason,
		EndTurn:               response.EndTurn,
		Timestamp:             response.Timestamp,
		DurationMs:            response.DurationMs,
	}
}

// IsRetryableAssistantError classifies whether a failed assistant message looks like a transient provider or transport error.
func IsRetryableAssistantError(message *AssistantMessage) bool {
	if message == nil || message.StopReason != StopReasonError || message.ErrorMessage == nil || *message.ErrorMessage == "" {
		return false
	}
	errMsg := *message.ErrorMessage
	if urNonRetryableProviderLimitErrorPattern.Test(errMsg) {
		return false
	}
	return urRetryableProviderErrorPattern.Test(errMsg)
}

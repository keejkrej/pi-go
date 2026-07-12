package agentloop

import (
	"regexp"
	"strings"
	"time"
)

// nonRetryablePatterns match subscription/account limits, not transient
// throttles (port of NON_RETRYABLE_PROVIDER_LIMIT_ERROR_PATTERN in
// pi's utils/retry.ts).
var nonRetryablePattern = buildProviderErrorPattern([]string{
	"GoUsageLimitError",
	"FreeUsageLimitError",
	"Monthly usage limit reached",
	"available balance",
	"insufficient_quota",
	"out of budget",
	"quota exceeded",
	"billing",
})

// retryablePattern matches provider load, HTTP status, transport, and
// premature-stream-end failures (port of RETRYABLE_PROVIDER_ERROR_PATTERN).
var retryablePattern = buildProviderErrorPattern([]string{
	"overloaded",
	"rate.?limit",
	"too many requests",
	"429",
	"500",
	"502",
	"503",
	"504",
	"service.?unavailable",
	"server.?error",
	"internal.?error",
	"provider.?returned.?error",
	"network.?error",
	"connection.?error",
	"connection.?refused",
	"connection.?lost",
	"other side closed",
	"fetch failed",
	"upstream.?connect",
	"reset before headers",
	"socket hang up",
	"timed? out",
	"timeout",
	"terminated",
	"websocket.?closed",
	"websocket.?error",
	"ended without",
	"stream ended before message_stop",
	"http2 request did not get a response",
	"retry delay",
	"you can retry your request",
	"try your request again",
	"please retry your request",
})

func buildProviderErrorPattern(patterns []string) *regexp.Regexp {
	return regexp.MustCompile("(?i)" + strings.Join(patterns, "|"))
}

// IsRetryableAssistantError classifies whether a failed assistant message
// looks like a transient provider or transport error, so callers can decide
// if the last assistant turn should be restarted.
//
// It does not implement retry policy. Callers should apply their own retry
// budget, backoff, and reporting before restarting the assistant turn (or set
// AgentLoopConfig.AutoRetry to let the loop do it).
func IsRetryableAssistantError(m *AssistantMessage) bool {
	if m == nil || m.StopReason != StopReasonError || m.ErrorMessage == "" {
		return false
	}
	if nonRetryablePattern.MatchString(m.ErrorMessage) {
		return false
	}
	return retryablePattern.MatchString(m.ErrorMessage)
}

// AutoRetryConfig makes the agent loop restart an assistant turn when the
// model response fails with a retryable error (per
// IsRetryableAssistantError). The errored assistant message is removed from
// the context before retrying, mirroring pi's coding-agent auto-retry. The
// loop emits an AutoRetryEvent before each backoff sleep.
type AutoRetryConfig struct {
	// MaxAttempts is the number of retries after the initial attempt.
	MaxAttempts int
	// BaseDelay is the first backoff delay; each subsequent retry doubles it
	// (exponential backoff). Zero means DefaultRetryBaseDelay.
	BaseDelay time.Duration
	// MaxDelay caps the backoff delay. Zero means no cap.
	MaxDelay time.Duration
}

// DefaultRetryBaseDelay is the default AutoRetryConfig.BaseDelay.
const DefaultRetryBaseDelay = 2 * time.Second

// delay returns the backoff delay before retry attempt n (1-based).
func (c *AutoRetryConfig) delay(attempt int) time.Duration {
	base := c.BaseDelay
	if base <= 0 {
		base = DefaultRetryBaseDelay
	}
	d := base << (attempt - 1)
	if d < base { // overflow guard for absurd attempt counts
		d = base
	}
	if c.MaxDelay > 0 && d > c.MaxDelay {
		d = c.MaxDelay
	}
	return d
}

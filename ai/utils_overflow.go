// Ported from packages/ai/src/utils/overflow.ts (pi v1.5.2).

package ai

import "regexp"

var uoOverflowPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)prompt (?:is )?too long`),
	regexp.MustCompile(`(?i)prompt exceeds max length`),
	regexp.MustCompile(`(?i)request_too_large`),
	regexp.MustCompile(`(?i)input is too long for requested model`),
	regexp.MustCompile(`(?i)exceeds the context window`),
	regexp.MustCompile(`(?i)exceeds (?:the )?(?:model'?s )?maximum context length(?: of [\d,]+ tokens?|\s*\([\d,]+\))`),
	regexp.MustCompile(`(?i)context window exceeded`),
	regexp.MustCompile(`(?i)input token count.*exceeds the maximum`),
	regexp.MustCompile(`(?i)maximum prompt length is \d+`),
	regexp.MustCompile(`(?i)reduce the length of the messages`),
	regexp.MustCompile(`(?i)maximum context length is \d+ tokens`),
	regexp.MustCompile(`(?i)exceeds (?:the )?maximum allowed input length of [\d,]+ tokens?`),
	regexp.MustCompile(`(?i)input \(\d+ tokens\) is longer than the model'?s context length \(\d+ tokens\)`),
	regexp.MustCompile(`(?i)exceeds the limit of \d+`),
	regexp.MustCompile(`(?i)exceeds the available context size`),
	regexp.MustCompile(`(?i)greater than the context length`),
	regexp.MustCompile(`(?i)context window exceeds limit`),
	regexp.MustCompile(`(?i)exceeded model token limit`),
	regexp.MustCompile(`(?i)too large for model with \d+ maximum context length`),
	regexp.MustCompile(`(?i)prompt has [\d,]+ tokens?, but the configured context size is [\d,]+ tokens?`),
	regexp.MustCompile(`(?i)model_context_window_exceeded`),
	regexp.MustCompile(`(?i)prompt too long; exceeded (?:max )?context length`),
	regexp.MustCompile(`(?i)range of input length should be`),
	regexp.MustCompile(`(?i)context[_ ]length[_ ]exceeded`),
	regexp.MustCompile(`(?i)too many tokens`),
	regexp.MustCompile(`(?i)token limit exceeded`),
	regexp.MustCompile(`(?i)^(?:[\w -]+ API error \(413\):|413(?:\s|:|$))`),
}

var uoCerebrasBodylessOverflowPattern = regexp.MustCompile(`(?i)^4(?:00|13)\s*(?:status code)?\s*\(no body\)`)

var uoNonOverflowPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^(Throttling error|Service unavailable):`),
	regexp.MustCompile(`(?i)rate limit`),
	regexp.MustCompile(`(?i)too many requests`),
}

// IsContextOverflow checks whether an assistant message represents a context overflow error.
func IsContextOverflow(message *AssistantMessage, contextWindow ...int) bool {
	if message == nil {
		return false
	}
	cw := 0
	if len(contextWindow) > 0 {
		cw = contextWindow[0]
	}

	// Case 1: Check error message patterns
	if message.StopReason == StopReasonError && message.ErrorMessage != nil && *message.ErrorMessage != "" {
		errMsg := *message.ErrorMessage
		isNonOverflow := false
		for _, p := range uoNonOverflowPatterns {
			if p.MatchString(errMsg) {
				isNonOverflow = true
				break
			}
		}
		if !isNonOverflow {
			for _, p := range uoOverflowPatterns {
				if p.MatchString(errMsg) {
					return true
				}
			}
			if message.Provider == KnownProviderCerebras && uoCerebrasBodylessOverflowPattern.MatchString(errMsg) {
				return true
			}
		}
	}

	// Case 2: Silent overflow (z.ai style) - successful but usage exceeds context
	if cw > 0 && message.StopReason == StopReasonStop {
		inputTokens := message.Usage.Input + message.Usage.CacheRead
		if inputTokens > cw {
			return true
		}
	}

	// Case 3: Length-stop overflow (Xiaomi MiMo style)
	if cw > 0 && message.StopReason == StopReasonLength && message.Usage.Output == 0 {
		inputTokens := message.Usage.Input + message.Usage.CacheRead
		if float64(inputTokens) >= float64(cw)*0.99 {
			return true
		}
	}

	return false
}

// IsRecoverableLength checks whether a length stop ended below the intended output limit.
func IsRecoverableLength(message *AssistantMessage, desiredMaxOutput int) bool {
	if message == nil {
		return false
	}
	return message.StopReason == StopReasonLength && desiredMaxOutput > 0 && message.Usage.Output < desiredMaxOutput
}

// GetOverflowPatterns returns a copy of the compiled overflow patterns for testing purposes.
func GetOverflowPatterns() []*regexp.Regexp {
	out := make([]*regexp.Regexp, len(uoOverflowPatterns))
	copy(out, uoOverflowPatterns)
	return out
}

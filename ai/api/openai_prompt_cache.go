// Ported from packages/ai/src/api/openai-prompt-cache.ts (pi v1.5.2).

package api

// OpenaiPromptCacheKeyMaxLength is the maximum character length accepted by OpenAI prompt_cache_key.
const OpenaiPromptCacheKeyMaxLength = 64

// ClampOpenAIPromptCacheKey truncates key to OpenaiPromptCacheKeyMaxLength Unicode scalar values.
func ClampOpenAIPromptCacheKey(key *string) *string {
	if key == nil {
		return nil
	}
	runes := []rune(*key)
	if len(runes) <= OpenaiPromptCacheKeyMaxLength {
		out := *key
		return &out
	}
	out := string(runes[:OpenaiPromptCacheKeyMaxLength])
	return &out
}

// Ported from packages/ai/src/api/openai-completions.lazy.ts (pi v1.5.2).

package api

import "github.com/keejkrej/pi-go/ai"

// OpenAICompletionsApi returns a lazily bound ProviderStreams wrapper for OpenAI Completions.
func OpenAICompletionsApi() ai.ProviderStreams {
	return ai.LazyApi(func() (ai.ProviderStreams, error) {
		return ai.ProviderStreams{
			Stream:       StreamOpenaiCompletions,
			StreamSimple: StreamSimpleOpenaiCompletions,
		}, nil
	}, nil)
}

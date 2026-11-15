// Ported from packages/ai/src/api/openai-responses.lazy.ts (pi v1.5.2).

package api

import "github.com/keejkrej/pi-go/ai"

// OpenAIResponsesApi returns a lazily bound ProviderStreams wrapper for OpenAI Responses.
func OpenAIResponsesApi() ai.ProviderStreams {
	return ai.LazyApi(func() (ai.ProviderStreams, error) {
		return ai.ProviderStreams{
			Stream:       StreamOpenaiResponses,
			StreamSimple: StreamSimpleOpenaiResponses,
		}, nil
	}, nil)
}

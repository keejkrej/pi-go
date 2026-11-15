// Ported from packages/ai/src/api/openai-codex-responses.lazy.ts (pi v1.5.2).

package api

import "github.com/keejkrej/pi-go/ai"

// OpenAICodexResponsesApi returns a lazily bound ProviderStreams wrapper for OpenAI Codex Responses.
func OpenAICodexResponsesApi() ai.ProviderStreams {
	return ai.LazyApi(func() (ai.ProviderStreams, error) {
		return ai.ProviderStreams{
			Stream:       StreamOpenaiCodexResponses,
			StreamSimple: StreamSimpleOpenaiCodexResponses,
		}, nil
	}, nil)
}

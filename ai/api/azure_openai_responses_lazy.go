// Ported from packages/ai/src/api/azure-openai-responses.lazy.ts (pi v1.5.2).

package api

import "github.com/keejkrej/pi-go/ai"

// AzureOpenAIResponsesApi returns a lazily bound ProviderStreams wrapper for Azure OpenAI Responses.
func AzureOpenAIResponsesApi() ai.ProviderStreams {
	return ai.LazyApi(func() (ai.ProviderStreams, error) {
		return ai.ProviderStreams{
			Stream:       StreamAzureOpenaiResponses,
			StreamSimple: StreamSimpleAzureOpenaiResponses,
		}, nil
	}, nil)
}

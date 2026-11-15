// Ported from packages/ai/src/api/mistral-conversations.lazy.ts (pi v1.5.2).

package api

import "github.com/keejkrej/pi-go/ai"

// MistralConversationsApi returns the lazy ProviderStreams entrypoints for Mistral.
func MistralConversationsApi() ai.ProviderStreams {
	return ai.LazyApi(func() (ai.ProviderStreams, error) {
		return ai.ProviderStreams{
			Stream:       StreamMistralConversations,
			StreamSimple: StreamSimpleMistralConversations,
		}, nil
	}, nil)
}

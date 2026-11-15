// Ported from packages/ai/src/api/anthropic-messages.lazy.ts (pi v1.5.2).

package api

import "github.com/keejkrej/pi-go/ai"

// AnthropicMessagesApi returns the lazy ProviderStreams entrypoints for anthropic-messages.
func AnthropicMessagesApi() ai.ProviderStreams {
	return ai.LazyApi(func() (ai.ProviderStreams, error) {
		return ai.ProviderStreams{
			Stream:       StreamAnthropicMessages,
			StreamSimple: StreamSimpleAnthropicMessages,
		}, nil
	}, nil)
}

// Ported from packages/ai/src/api/pi-messages.lazy.ts (pi v1.5.2).

package api

import "github.com/keejkrej/pi-go/ai"

// PiMessagesApi returns the lazy ProviderStreams entrypoints for pi-messages.
func PiMessagesApi() ai.ProviderStreams {
	return ai.LazyApi(func() (ai.ProviderStreams, error) {
		return ai.ProviderStreams{
			Stream:       StreamPiMessages,
			StreamSimple: StreamSimplePiMessages,
		}, nil
	}, nil)
}

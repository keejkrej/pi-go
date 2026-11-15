// Ported from packages/ai/src/api/google-generative-ai.lazy.ts (pi v1.5.2).

package api

import "github.com/keejkrej/pi-go/ai"

// GoogleGenerativeAiApi returns the lazy ProviderStreams entrypoints for google-generative-ai.
func GoogleGenerativeAiApi() ai.ProviderStreams {
	return ai.LazyApi(func() (ai.ProviderStreams, error) {
		return ai.ProviderStreams{
			Stream:       StreamGoogleGenerativeAi,
			StreamSimple: StreamSimpleGoogleGenerativeAi,
		}, nil
	}, nil)
}

// Ported from packages/ai/src/api/google-vertex.lazy.ts (pi v1.5.2).

package api

import "github.com/keejkrej/pi-go/ai"

// GoogleVertexApi returns the lazy ProviderStreams entrypoints for google-vertex.
func GoogleVertexApi() ai.ProviderStreams {
	return ai.LazyApi(func() (ai.ProviderStreams, error) {
		return ai.ProviderStreams{
			Stream:       StreamGoogleVertex,
			StreamSimple: StreamSimpleGoogleVertex,
		}, nil
	}, nil)
}

// Ported from packages/ai/src/api/bedrock-converse-stream.lazy.ts (pi v1.5.2).

package api

import (
	"context"
	"sync"

	"github.com/keejkrej/pi-go/ai"
)

var (
	bcslMu           sync.Mutex
	bcslModuleLoader func() (ai.ProviderStreams, error)
)

// SetBedrockProviderModule overrides the statically or lazily loaded Bedrock stream module.
func SetBedrockProviderModule(module ai.ProviderStreams) {
	bcslMu.Lock()
	defer bcslMu.Unlock()
	bcslModuleLoader = func() (ai.ProviderStreams, error) {
		return module, nil
	}
}

// SetBedrockProviderLoader overrides how the Bedrock stream module is loaded on first use.
func SetBedrockProviderLoader(load func() (ai.ProviderStreams, error)) {
	bcslMu.Lock()
	defer bcslMu.Unlock()
	bcslModuleLoader = load
}

// BedrockConverseStreamApi returns the lazily initialized Amazon Bedrock ConverseStream ProviderStreams.
func BedrockConverseStreamApi() ai.ProviderStreams {
	return ai.LazyApi(func() (ai.ProviderStreams, error) {
		bcslMu.Lock()
		loader := bcslModuleLoader
		bcslMu.Unlock()
		if loader != nil {
			return loader()
		}
		return ai.ProviderStreams{
			Stream: func(ctx context.Context, model *ai.Model, c *ai.TranscriptContext, options ai.ProviderStreamOptions) *ai.AssistantMessageEventStream {
				return StreamBedrockConverseStream(ctx, model, c, options)
			},
			StreamSimple: func(ctx context.Context, model *ai.Model, c *ai.TranscriptContext, options *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
				return StreamSimpleBedrockConverseStream(ctx, model, c, options)
			},
		}, nil
	}, nil)
}

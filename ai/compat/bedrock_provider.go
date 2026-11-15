// Ported from packages/ai/src/bedrock-provider.ts (pi v1.5.2).

package compat

import (
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
)

// BedrockProviderModule exposes the eager Bedrock Converse Stream entrypoints.
var BedrockProviderModule = ai.ProviderStreams{
	Stream:       api.StreamBedrockConverseStream,
	StreamSimple: api.StreamSimpleBedrockConverseStream,
}

func init() {
	api.SetBedrockProviderModule(BedrockProviderModule)
}

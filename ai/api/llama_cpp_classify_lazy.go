// Ported from packages/ai/src/api/llama-cpp-classify.lazy.ts (pi v1.5.2).

package api

import "github.com/keejkrej/pi-go/ai"

// LlamaCppClassifyApi returns the ProviderClassifier implementation for llama.cpp logprob classification.
func LlamaCppClassifyApi() ai.ProviderClassifier {
	return ai.ProviderClassifier{
		Classify: ClassifyLlamaCppClassify,
	}
}

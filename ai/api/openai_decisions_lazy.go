// Ported from packages/ai/src/api/openai-decisions.lazy.ts (pi v1.5.2).

package api

import "github.com/keejkrej/pi-go/ai"

// OpenAIDecisionsApi returns a lazily bound ProviderClassifier wrapper for OpenAI Decisions.
func OpenAIDecisionsApi() ai.ProviderClassifier {
	return ai.ProviderClassifier{
		Classify: ClassifyOpenaiDecisions,
	}
}

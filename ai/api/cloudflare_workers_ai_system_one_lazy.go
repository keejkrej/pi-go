// Ported from packages/ai/src/api/cloudflare-workers-ai-system-one.lazy.ts (pi v1.5.2).

package api

import "github.com/keejkrej/pi-go/ai"

// CloudflareWorkersAISystemOneApi returns the ProviderClassifier implementation for Workers AI System One.
func CloudflareWorkersAISystemOneApi() ai.ProviderClassifier {
	return ai.ProviderClassifier{
		Classify: ClassifyCloudflareWorkersAiSystemOne,
	}
}

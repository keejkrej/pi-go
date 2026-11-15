// Ported from packages/ai/src/api/typesafe-system-one.lazy.ts (pi v1.5.2).

package api

import "github.com/keejkrej/pi-go/ai"

// TypesafeSystemOneApi returns the ProviderClassifier implementation for TypeSafe System One.
func TypesafeSystemOneApi() ai.ProviderClassifier {
	return ai.ProviderClassifier{
		Classify: ClassifyTypesafeSystemOne,
	}
}

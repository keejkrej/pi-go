// Ported from packages/ai/src/providers/typesafe.ts (pi v1.5.2).

package providers

import (
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/ai/catalog"
)

// TypesafeProvider constructs the built-in TypeSafe provider.
func TypesafeProvider() *ai.Provider {
	return allMustCreateProvider(&ai.CreateProviderOptions{
		Id:     "typesafe",
		Name:   allPtr("TypeSafe"),
		Auth:   ai.ProviderAuth{ApiKey: ai.EnvApiKeyAuth("TypeSafe API key", []string{"TYPESAFE_API_KEY"})},
		Models: allCatalogModels(nil, nil, catalog.TypesafeClassifierModels),
		Classifiers: map[string]ai.ProviderClassifier{
			"typesafe-system-one": api.TypesafeSystemOneApi(),
		},
	})
}

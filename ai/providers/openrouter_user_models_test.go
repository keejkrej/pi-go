// Ported from packages/ai/test/openrouter-user-models.test.ts (pi v1.5.2).

package providers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
)

var toumZeroCost = ai.ModelCost{}

func toumChatModel(id string) *ai.Model {
	return &ai.Model{
		BaseModel: ai.BaseModel{
			Id:       id,
			Name:     id,
			Api:      string(ai.KnownApiOpenaiCompletions),
			Provider: "openrouter",
			BaseUrl:  "https://openrouter.ai/api/v1",
			Input:    []ai.ModelModality{ai.ModelModalityText},
			Cost:     ai.ModelCost{ModelCostRates: ai.ModelCostRates{Input: 1, Output: 2, CacheRead: 0.1, CacheWrite: 0}},
		},
		ContextWindow: 200_000,
		MaxTokens:     50_000,
	}
}

var (
	toumImageModel = &ai.ImageModel{
		Type: ai.ModelTypeImage,
		BaseModel: ai.BaseModel{
			Id:       "image",
			Name:     "image",
			Api:      "openrouter-images",
			Provider: "openrouter",
			BaseUrl:  "https://openrouter.ai/api/v1",
			Input:    []ai.ModelModality{ai.ModelModalityText},
			Cost:     toumZeroCost,
		},
		Output: []ai.ModelModality{ai.ModelModalityImage},
	}
	toumClassifierModel = &ai.ClassifierModel{
		Type: ai.ModelTypeClassifier,
		BaseModel: ai.BaseModel{
			Id:       "classifier",
			Name:     "classifier",
			Api:      string(ai.KnownClassifierApiTypesafeSystemOne),
			Provider: "openrouter",
			BaseUrl:  "https://openrouter.ai/api/v1",
			Input:    []ai.ModelModality{ai.ModelModalityText},
			Cost:     toumZeroCost,
		},
		ContextWindow: 64_000,
	}
)

func toumRefreshContext(baseUrl string) *ai.RefreshModelsContext {
	key := "sk-or-test"
	return &ai.RefreshModelsContext{
		Credential:   &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Key: &key},
		Publish:      func(_ *ai.ModelsPublication) (bool, error) { return true, nil },
		AllowNetwork: true,
		BaseUrl:      &baseUrl,
	}
}

func toumAnyModelIds(models []ai.AnyModel) []string {
	out := make([]string, len(models))
	for i, m := range models {
		switch x := m.(type) {
		case *ai.Model:
			out[i] = x.Id
		case *ai.ImageModel:
			out[i] = x.Id
		case *ai.ClassifierModel:
			out[i] = x.Id
		}
	}
	return out
}

func TestPersonalizeOpenRouterCatalog_FiltersChatModelsWithKeyLimitsAndPrices(t *testing.T) {
	t.Parallel()
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[
			{"id":"listed","context_length":1000000,"top_provider":{"context_length":262144,"max_completion_tokens":32768},"pricing":{"prompt":"0.000003","completion":"0.000015"}},
			{"id":"no-limits","context_length":0,"top_provider":{"context_length":null,"max_completion_tokens":null}},
			{"id":"router","pricing":{"prompt":"-1","completion":"-1"}}
		]}`))
	}))
	defer srv.Close()

	catalog := []ai.AnyModel{
		toumChatModel("listed"),
		toumChatModel("no-limits"),
		toumChatModel("router"),
		toumChatModel("blocked"),
		toumImageModel,
		toumClassifierModel,
	}

	result, err := providers.PersonalizeOpenRouterCatalog(context.Background(), catalog, toumRefreshContext(srv.URL))
	if err != nil {
		t.Fatalf("PersonalizeOpenRouterCatalog failed: %v", err)
	}
	if gotPath != "/models/user" || gotAuth != "Bearer sk-or-test" {
		t.Fatalf("unexpected request path=%q auth=%q", gotPath, gotAuth)
	}
	if !slices.Equal(toumAnyModelIds(result), []string{"listed", "no-limits", "router", "image", "classifier"}) {
		t.Fatalf("got ids %v", toumAnyModelIds(result))
	}

	listed := result[0].(*ai.Model)
	if listed.ContextWindow != 262_144 || listed.MaxTokens != 32_768 ||
		listed.Cost.ModelCostRates != (ai.ModelCostRates{Input: 3, Output: 15, CacheRead: 0, CacheWrite: 0}) {
		t.Fatalf("unexpected listed model: %+v", listed)
	}
	noLimits := result[1].(*ai.Model)
	refCost := toumChatModel("x").Cost.ModelCostRates
	if noLimits.ContextWindow != 200_000 || noLimits.MaxTokens != 50_000 || noLimits.Cost.ModelCostRates != refCost {
		t.Fatalf("unexpected no-limits model: %+v", noLimits)
	}
	router := result[2].(*ai.Model)
	if router.Cost.ModelCostRates != toumZeroCost.ModelCostRates {
		t.Fatalf("expected zero cost for router, got %+v", router.Cost)
	}
	// /models/user lists text models only, so image and classifier models are not filtered.
	if result[3] != toumImageModel {
		t.Fatalf("expected identical imageModel pointer, got %v", result[3])
	}
	if result[4] != toumClassifierModel {
		t.Fatalf("expected identical classifierModel pointer, got %v", result[4])
	}
}

func TestPersonalizeOpenRouterCatalog_FallbackStatusesReturnCatalogUnchanged(t *testing.T) {
	t.Parallel()
	for _, status := range []int{401, 403, 404} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
		}))
		catalog := []ai.AnyModel{toumChatModel("a")}
		result, err := providers.PersonalizeOpenRouterCatalog(context.Background(), catalog, toumRefreshContext(srv.URL))
		srv.Close()
		if err != nil {
			t.Fatalf("status %d failed: %v", status, err)
		}
		if len(result) != len(catalog) || result[0] != catalog[0] {
			t.Fatalf("status %d did not return catalog unchanged: %+v", status, result)
		}
	}
}

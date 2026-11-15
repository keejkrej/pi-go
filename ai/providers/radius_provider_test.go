// Ported from packages/ai/test/radius-provider.test.ts (pi v1.5.2).

package providers_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"slices"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	aioauth "github.com/keejkrej/pi-go/ai/oauth"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func trpPtr[T any](v T) *T { return &v }

func trpRadiusConfig() *aioauth.RadiusGatewayConfig {
	return &aioauth.RadiusGatewayConfig{
		BaseUrl: "https://radius.example/v1",
		Models: []aioauth.RadiusGatewayModel{
			{
				Id:            "balanced",
				Name:          "Fresh Balanced",
				Reasoning:     true,
				Input:         []ai.ModelModality{ai.ModelModalityText},
				Cost:          ai.ModelCost{ModelCostRates: ai.ModelCostRates{Input: 1, Output: 2, CacheRead: 0.1, CacheWrite: 0}},
				ContextWindow: 424242,
				MaxTokens:     32000,
			},
			{
				Id:            "organization-only",
				Name:          "Organization Only",
				Reasoning:     false,
				Input:         []ai.ModelModality{ai.ModelModalityText},
				Cost:          ai.ModelCost{ModelCostRates: ai.ModelCostRates{Input: 0, Output: 0, CacheRead: 0, CacheWrite: 0}},
				ContextWindow: 128000,
				MaxTokens:     16000,
			},
		},
	}
}

type trpRoundTripFunc func(req *http.Request) (*http.Response, error)

func (f trpRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func trpJsonClient(t *testing.T, v any) *http.Client {
	t.Helper()
	raw, err := jsonx.Marshal(v)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return &http.Client{
		Transport: trpRoundTripFunc(func(_ *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(bytes.NewReader(raw)),
			}, nil
		}),
	}
}

func trpModelIds(models []*ai.Model) []string {
	out := make([]string, len(models))
	for i, m := range models {
		out[i] = m.Id
	}
	return out
}

func trpToAnyModels(models []*ai.Model) ai.AnyModelList {
	out := make(ai.AnyModelList, len(models))
	for i, m := range models {
		out[i] = m
	}
	return out
}

func TestRadiusProvider_StaticDefaultCatalog(t *testing.T) {
	t.Parallel()
	provider := providers.RadiusProvider(nil)
	models := provider.GetModels()
	if len(models) == 0 {
		t.Fatal("expected static public Radius catalog")
	}
	var found *ai.Model
	for _, m := range models {
		if m != nil && m.Id == "balanced" {
			found = m
			break
		}
	}
	if found == nil || found.Provider != "radius" || found.Api != string(ai.KnownApiPiMessages) {
		t.Fatalf("expected balanced model with provider=radius, api=pi-messages, got %+v", found)
	}
}

func TestRadiusProvider_CustomGatewayStartsEmpty(t *testing.T) {
	t.Parallel()
	provider := providers.RadiusProvider(&providers.RadiusProviderOptions{
		Id:      trpPtr("radius-dev"),
		Gateway: trpPtr("http://localhost:8788"),
	})
	if got := provider.GetModels(); len(got) != 0 {
		t.Fatalf("expected empty model list for custom gateway, got %+v", got)
	}
}

func TestRadiusProvider_RefreshReplacesStaticCatalog(t *testing.T) {
	t.Parallel()
	ctx := aioauth.LoadWithHttpClient(context.Background(), trpJsonClient(t, trpRadiusConfig()))
	creds := ai.NewInMemoryCredentialStore()
	_, err := creds.Modify(ctx, "radius", func(_ ai.Credential) (ai.Credential, error) {
		return &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Key: trpPtr("radius-key")}, nil
	}, nil)
	if err != nil {
		t.Fatalf("Modify credential: %v", err)
	}
	models := ai.CreateModels(&ai.CreateModelsOptions{Credentials: creds})
	models.SetProvider(providers.RadiusProvider(nil))

	res, err := models.Refresh(ctx, &ai.ModelsRefreshOptions{Providers: []string{"radius"}})
	if err != nil {
		t.Fatalf("Refresh failed: %v", err)
	}
	if res.Errors != nil && res.Errors.Len() != 0 {
		t.Fatalf("unexpected refresh errors: %v", res.Errors)
	}
	balanced := models.GetModel("radius", "balanced")
	if balanced == nil || balanced.Name != "Fresh Balanced" || balanced.BaseUrl != "https://radius.example/v1" || balanced.ContextWindow != 424242 {
		t.Fatalf("unexpected balanced model: %+v", balanced)
	}
	if models.GetModel("radius", "organization-only") == nil {
		t.Fatal("expected organization-only model")
	}
	// Models disabled by the organization must not reappear from the shipped catalog.
	gotIds := trpModelIds(models.GetModels(trpPtr("radius")))
	if !slices.Equal(gotIds, []string{"balanced", "organization-only"}) {
		t.Fatalf("expected [balanced organization-only], got %v", gotIds)
	}
}

func TestRadiusProvider_RestoresCachedCatalogWithoutNetwork(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := ai.NewInMemoryModelsStore()
	now := js.DateNow()
	err := store.Write(ctx, "radius", &ai.ModelsStoreEntry{
		Models:    trpToAnyModels(aioauth.GetRadiusModelsFromConfig("radius", trpRadiusConfig())),
		CheckedAt: &now,
	}, nil)
	if err != nil {
		t.Fatalf("store.Write failed: %v", err)
	}
	models := ai.CreateModels(&ai.CreateModelsOptions{ModelsStore: store})
	models.SetProvider(providers.RadiusProvider(nil))

	if _, err := models.Refresh(ctx, &ai.ModelsRefreshOptions{
		Providers:    []string{"radius"},
		AllowNetwork: trpPtr(false),
	}); err != nil {
		t.Fatalf("Refresh failed: %v", err)
	}

	balanced := models.GetModel("radius", "balanced")
	if balanced == nil || balanced.Name != "Fresh Balanced" {
		t.Fatalf("expected Fresh Balanced, got %+v", balanced)
	}
	if models.GetModel("radius", "organization-only") == nil {
		t.Fatal("expected organization-only model")
	}
	gotIds := trpModelIds(models.GetModels(trpPtr("radius")))
	if !slices.Equal(gotIds, []string{"balanced", "organization-only"}) {
		t.Fatalf("expected [balanced organization-only], got %v", gotIds)
	}
}

func TestRadiusProvider_EmptyOrganizationCatalog(t *testing.T) {
	t.Parallel()
	ctx := aioauth.LoadWithHttpClient(context.Background(), trpJsonClient(t, &aioauth.RadiusGatewayConfig{
		BaseUrl: "https://radius.example/v1",
		Models:  []aioauth.RadiusGatewayModel{},
	}))
	creds := ai.NewInMemoryCredentialStore()
	if _, err := creds.Modify(ctx, "radius", func(_ ai.Credential) (ai.Credential, error) {
		return &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Key: trpPtr("radius-key")}, nil
	}, nil); err != nil {
		t.Fatalf("Modify credential: %v", err)
	}
	models := ai.CreateModels(&ai.CreateModelsOptions{Credentials: creds})
	models.SetProvider(providers.RadiusProvider(nil))

	res, err := models.Refresh(ctx, &ai.ModelsRefreshOptions{Providers: []string{"radius"}})
	if err != nil {
		t.Fatalf("Refresh failed: %v", err)
	}
	if res.Errors != nil && res.Errors.Len() != 0 {
		t.Fatalf("unexpected refresh errors: %v", res.Errors)
	}
	if got := models.GetModels(trpPtr("radius")); len(got) != 0 {
		t.Fatalf("expected empty model list, got %+v", got)
	}
}

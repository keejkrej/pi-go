// Ported from packages/coding-agent/test/remote-catalog-provider.test.ts (pi v1.5.2).

package core

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func tmrcpWriteJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	raw, err := jsonx.Stringify(v)
	if err != nil {
		panic(err)
	}
	_, _ = w.Write([]byte(raw))
}

func tmrcpBoolPtr(b bool) *bool { return &b }

func tmrcpChatModel(id string) *ai.Model {
	return &ai.Model{
		BaseModel: ai.BaseModel{
			Id:       id,
			Name:     id,
			Api:      "openai-completions",
			Provider: "test-provider",
			BaseUrl:  "https://example.test/v1",
			Input:    []ai.ModelModality{ai.ModelModalityText},
		},
		ContextWindow: 1000,
		MaxTokens:     100,
	}
}

func tmrcpImageModel(id string) *ai.ImageModel {
	return &ai.ImageModel{
		BaseModel: ai.BaseModel{
			Id:       id,
			Name:     id,
			Api:      "openrouter-images",
			Provider: "test-provider",
			BaseUrl:  "https://example.test/v1",
			Input:    []ai.ModelModality{ai.ModelModalityText},
		},
		Type:   ai.ModelTypeImage,
		Output: []ai.ModelModality{ai.ModelModalityImage},
	}
}

func tmrcpBuiltinProvider() *ai.Provider {
	p, err := ai.CreateProvider(&ai.CreateProviderOptions{
		Id: "test-provider",
		Auth: ai.ProviderAuth{
			ApiKey: &ai.ApiKeyAuth{
				Name: "Test",
				Resolve: func(_ context.Context, _ ai.ApiKeyResolveInput) (*ai.AuthResult, error) {
					return &ai.AuthResult{}, nil
				},
			},
		},
		Models: []ai.AnyModel{tmrcpChatModel("static")},
		Api: ai.ProviderStreams{
			Stream: func(_ context.Context, _ *ai.Model, _ *ai.TranscriptContext, _ ai.ProviderStreamOptions) *ai.AssistantMessageEventStream {
				panic("not used")
			},
			StreamSimple: func(_ context.Context, _ *ai.Model, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
				panic("not used")
			},
		},
	})
	if err != nil {
		panic(err)
	}
	return p
}

func tmrcpRefresh(t *testing.T, provider *ai.Provider, store ai.ModelsStore, allowNetwork bool, force *bool) error {
	t.Helper()
	stored, err := store.Read(t.Context(), provider.Id, nil)
	if err != nil {
		t.Fatalf("store.Read: %v", err)
	}
	return provider.RefreshModels(t.Context(), &ai.RefreshModelsContext{
		Credential:   &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey},
		Stored:       stored,
		AllowNetwork: allowNetwork,
		Force:        force,
		Publish: func(pub *ai.ModelsPublication) (bool, error) {
			if pub.Persist.IsNull() {
				if err := store.Delete(t.Context(), provider.Id, nil); err != nil {
					return false, err
				}
			} else if v, ok := pub.Persist.Get(); ok {
				if err := store.Write(t.Context(), provider.Id, &v, nil); err != nil {
					return false, err
				}
			}
			if pub.Update != nil {
				pub.Update()
			}
			return true, nil
		},
	})
}

func tmrcpModelIds(models []*ai.Model) []string {
	out := make([]string, len(models))
	for i, m := range models {
		out[i] = m.Id
	}
	return out
}

func tmrcpAnyIds(models []ai.AnyModel) []string {
	out := make([]string, len(models))
	for i, m := range models {
		out[i] = rcpAnyModelId(m)
	}
	return out
}

func TestRemoteCatalogTTLHeadersAndKeyedPayload(t *testing.T) {
	var mu sync.Mutex
	var calls int
	var firstUA, firstPath, firstTypes string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		if calls == 1 {
			firstUA = r.Header.Get("User-Agent")
			firstPath = r.URL.Path
			firstTypes = r.URL.Query().Get("types")
		}
		mu.Unlock()
		tmrcpWriteJSON(w, jsonx.ObjectOf("dynamic", tmrcpChatModel("dynamic")))
	}))
	defer srv.Close()

	provider := WithRemoteCatalog(tmrcpBuiltinProvider(), srv.URL, nil)
	store := ai.NewInMemoryModelsStore()

	if err := tmrcpRefresh(t, provider, store, true, nil); err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	// Second refresh hits TTL and makes no network request.
	if err := tmrcpRefresh(t, provider, store, true, nil); err != nil {
		t.Fatalf("second refresh: %v", err)
	}
	// Forced refresh ignores TTL.
	if err := tmrcpRefresh(t, provider, store, true, tmrcpBoolPtr(true)); err != nil {
		t.Fatalf("forced refresh: %v", err)
	}

	if got := tmrcpModelIds(provider.GetModels()); !slices.Equal(got, []string{"static", "dynamic"}) {
		t.Fatalf("GetModels = %v, want [static dynamic]", got)
	}
	stored, err := store.Read(t.Context(), provider.Id, nil)
	if err != nil || stored == nil {
		t.Fatalf("store.Read: err=%v stored=%+v", err, stored)
	}
	if got := tmrcpAnyIds(stored.Models); !slices.Equal(got, []string{"dynamic"}) {
		t.Fatalf("stored models = %v, want [dynamic]", got)
	}
	if calls != 2 {
		t.Fatalf("HTTP call count = %d, want 2", calls)
	}
	if !strings.Contains(firstUA, "pi/"+utils.VERSION) {
		t.Fatalf("User-Agent %q missing version prefix", firstUA)
	}
	if firstPath != "/api/models/providers/test-provider" || firstTypes != "chat,image,classifier" {
		t.Fatalf("unexpected request path=%q types=%q", firstPath, firstTypes)
	}
}

func TestRemoteCatalogOverlaysTypesAndDropsUnknown(t *testing.T) {
	raw := `{"chat":{"type":"chat","id":"chat","name":"chat","api":"openai-completions","provider":"test-provider","baseUrl":"https://example.test/v1","input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":1000,"maxTokens":100},"flux":{"type":"image","id":"flux","name":"FLUX","api":"openrouter-images","provider":"test-provider","baseUrl":"https://example.test/v1","input":["text"],"output":["image"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0}},"jev":{"type":"classifier","id":"jev","name":"Jev","api":"typesafe-system-one","provider":"test-provider","baseUrl":"https://example.test/v1","input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":64000},"clip":{"type":"video","id":"clip","name":"clip","api":"openai-completions","provider":"test-provider","baseUrl":"https://example.test/v1","input":["text"]}}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(raw))
	}))
	defer srv.Close()

	provider := WithRemoteCatalog(tmrcpBuiltinProvider(), srv.URL, nil)
	store := ai.NewInMemoryModelsStore()
	if err := tmrcpRefresh(t, provider, store, true, nil); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	models := ai.CreateModels(&ai.CreateModelsOptions{ModelsStore: store})
	models.SetProvider(provider)
	pid := provider.Id
	if got := tmrcpAnyIds(models.GetAllModels(&pid)); !slices.Equal(got, []string{"static", "chat", "flux", "jev"}) {
		t.Fatalf("GetAllModels = %v, want [static chat flux jev]", got)
	}
	if models.GetModelOfType(ai.ModelTypeImage, pid, "flux") == nil {
		t.Fatalf("expected flux image model")
	}
	if models.GetModelOfType(ai.ModelTypeClassifier, pid, "jev") == nil {
		t.Fatalf("expected jev classifier model")
	}
	if models.GetModel(pid, "flux") != nil {
		t.Fatalf("expected non-chat image model to be excluded from chat GetModel")
	}
}

func TestRemoteCatalogLastModifiedETagAndErrors(t *testing.T) {
	localGeneratedAt := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC).UnixMilli()
	olderTime := time.UnixMilli(localGeneratedAt - 60_000).UTC().Format(http.TimeFormat)
	newerTime := time.UnixMilli(localGeneratedAt + 60_000).UTC().Format(http.TimeFormat)

	step := 0
	var seenIfNoneMatch []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		step++
		seenIfNoneMatch = append(seenIfNoneMatch, r.Header.Get("If-None-Match"))
		switch step {
		case 1:
			w.Header().Set("Last-Modified", olderTime)
			tmrcpWriteJSON(w, jsonx.ObjectOf("old", tmrcpChatModel("old")))
		case 2:
			w.Header().Set("Last-Modified", newerTime)
			w.Header().Set("ETag", `"catalog-1"`)
			tmrcpWriteJSON(w, jsonx.ObjectOf("newer", tmrcpChatModel("newer")))
		case 3:
			w.Header().Set("ETag", `"catalog-1"`)
			w.WriteHeader(http.StatusNotModified)
		case 4:
			http.Error(w, "bad request", http.StatusBadRequest)
		case 5:
			w.Header().Set("ETag", `"catalog-1"`)
			w.WriteHeader(http.StatusNotModified)
		case 6:
			http.Error(w, "not implemented", http.StatusNotImplemented)
		}
	}))
	defer srv.Close()

	provider := WithRemoteCatalog(tmrcpBuiltinProvider(), srv.URL, &localGeneratedAt)
	store := ai.NewInMemoryModelsStore()

	// Step 1: remote older than localGeneratedAt -> keep static only.
	if err := tmrcpRefresh(t, provider, store, true, nil); err != nil {
		t.Fatalf("step 1: %v", err)
	}
	if got := tmrcpModelIds(provider.GetModels()); !slices.Equal(got, []string{"static"}) {
		t.Fatalf("step 1 models = %v, want [static]", got)
	}

	// Step 2: newer Last-Modified + ETag -> overlay applied.
	if err := tmrcpRefresh(t, provider, store, true, tmrcpBoolPtr(true)); err != nil {
		t.Fatalf("step 2: %v", err)
	}
	if got := tmrcpModelIds(provider.GetModels()); !slices.Equal(got, []string{"static", "newer"}) {
		t.Fatalf("step 2 models = %v, want [static newer]", got)
	}

	// Step 3: 304 sends If-None-Match and keeps overlay.
	if err := tmrcpRefresh(t, provider, store, true, tmrcpBoolPtr(true)); err != nil {
		t.Fatalf("step 3: %v", err)
	}
	if seenIfNoneMatch[2] != `"catalog-1"` {
		t.Fatalf("step 3 If-None-Match = %q, want %q", seenIfNoneMatch[2], `"catalog-1"`)
	}

	// Step 4: transient non-2xx keeps ETag and overlay.
	err := tmrcpRefresh(t, provider, store, true, tmrcpBoolPtr(true))
	if err == nil || !strings.Contains(err.Error(), "400") {
		t.Fatalf("step 4 expected 400 error, got %v", err)
	}
	stored, _ := store.Read(t.Context(), provider.Id, nil)
	if stored == nil || stored.Etag == nil || *stored.Etag != `"catalog-1"` {
		t.Fatalf("step 4 should preserve Etag, got %+v", stored)
	}

	// Step 5: 304 still validates after non-404/501 failure.
	if err := tmrcpRefresh(t, provider, store, true, tmrcpBoolPtr(true)); err != nil {
		t.Fatalf("step 5: %v", err)
	}

	// Step 6: 501 drops ETag and sets LastModified=0.
	if err := tmrcpRefresh(t, provider, store, true, tmrcpBoolPtr(true)); err != nil {
		t.Fatalf("step 6: %v", err)
	}
	stored, _ = store.Read(t.Context(), provider.Id, nil)
	if stored == nil || stored.Etag != nil || stored.LastModified == nil || *stored.LastModified != 0 {
		t.Fatalf("step 6 expected cleared Etag & LastModified=0, got %+v", stored)
	}
}

func TestRemoteCatalogSkipsOAuthRefreshOnExpiredCred(t *testing.T) {
	oauthRefreshed := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		tmrcpWriteJSON(w, jsonx.ObjectOf("dynamic", tmrcpChatModel("dynamic")))
	}))
	defer srv.Close()

	base, err := ai.CreateProvider(&ai.CreateProviderOptions{
		Id: "test-provider",
		Auth: ai.ProviderAuth{
			OAuth: &ai.OAuthAuth{
				Name: "Test OAuth",
				Refresh: func(_ context.Context, _ *ai.OAuthCredential) (*ai.OAuthCredential, error) {
					oauthRefreshed++
					return nil, errors.New("invalid_grant")
				},
				ToAuth: func(_ context.Context, cred *ai.OAuthCredential) (ai.ModelAuth, error) {
					return ai.ModelAuth{ApiKey: &cred.Access}, nil
				},
			},
		},
		Models: []ai.AnyModel{tmrcpChatModel("static")},
		Api: ai.ProviderStreams{
			Stream: func(_ context.Context, _ *ai.Model, _ *ai.TranscriptContext, _ ai.ProviderStreamOptions) *ai.AssistantMessageEventStream {
				panic("not used")
			},
			StreamSimple: func(_ context.Context, _ *ai.Model, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
				panic("not used")
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	provider := WithRemoteCatalog(base, srv.URL, nil)
	creds := ai.NewInMemoryCredentialStore()
	_, _ = creds.Modify(t.Context(), provider.Id, func(_ ai.Credential) (ai.Credential, error) {
		return &ai.OAuthCredential{
			Type:             ai.AuthTypeOAuth,
			OAuthCredentials: ai.OAuthCredentials{Access: "expired", Refresh: "revoked", Expires: 0},
		}, nil
	}, nil)

	models := ai.CreateModels(&ai.CreateModelsOptions{
		Credentials: creds,
		ModelsStore: ai.NewInMemoryModelsStore(),
	})
	models.SetProvider(provider)
	res, err := models.Refresh(t.Context(), &ai.ModelsRefreshOptions{Force: tmrcpBoolPtr(true)})
	if err != nil || (res.Errors != nil && res.Errors.Len() != 0) {
		t.Fatalf("Refresh failed: err=%v errors=%v", err, res.Errors)
	}
	if oauthRefreshed != 0 {
		t.Fatalf("expected OAuth refresh not to be called during catalog refresh, called %d times", oauthRefreshed)
	}
	if got := tmrcpModelIds(provider.GetModels()); !slices.Equal(got, []string{"static", "dynamic"}) {
		t.Fatalf("models = %v, want [static dynamic]", got)
	}
}

func TestPersonalizedCatalogFailureAndOfflineFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.NotFound(w, nil)
	}))
	defer srv.Close()

	// Personalization error preserves saved catalog and does not overwrite store.
	store := ai.NewInMemoryModelsStore()
	checkedAt := int64(1)
	saved := &ai.ModelsStoreEntry{
		Models:    ai.AnyModelList{tmrcpChatModel("allowed")},
		CheckedAt: &checkedAt,
	}
	if err := store.Write(t.Context(), "test-provider", saved, nil); err != nil {
		t.Fatal(err)
	}
	pErr := WithPersonalizedCatalog(tmrcpBuiltinProvider(), func(_ context.Context, _ []ai.AnyModel, _ *ai.RefreshModelsContext) ([]ai.AnyModel, error) {
		return nil, errors.New("user models request failed")
	}, srv.URL, nil)

	err := tmrcpRefresh(t, pErr, store, true, nil)
	if err == nil || !strings.Contains(err.Error(), "user models request failed") {
		t.Fatalf("expected personalization error, got %v", err)
	}
	if got := tmrcpModelIds(pErr.GetModels()); !slices.Equal(got, []string{"allowed"}) {
		t.Fatalf("expected saved allowed model to remain active, got %v", got)
	}
	afterErr, _ := store.Read(t.Context(), "test-provider", nil)
	if afterErr == nil || len(afterErr.Models) != 1 || rcpAnyModelId(afterErr.Models[0]) != "allowed" {
		t.Fatalf("expected saved store entry unchanged, got %+v", afterErr)
	}

	// Offline fallback: empty stored entry falls back to built-in; non-empty [image] keeps 0 chat models.
	offlineStore := ai.NewInMemoryModelsStore()
	pOffline := WithPersonalizedCatalog(tmrcpBuiltinProvider(), func(_ context.Context, _ []ai.AnyModel, _ *ai.RefreshModelsContext) ([]ai.AnyModel, error) {
		return nil, errors.New("not used offline")
	}, srv.URL, nil)

	_ = offlineStore.Write(t.Context(), pOffline.Id, &ai.ModelsStoreEntry{Models: ai.AnyModelList{}, CheckedAt: &checkedAt}, nil)
	if err := tmrcpRefresh(t, pOffline, offlineStore, false, nil); err != nil {
		t.Fatalf("offline empty stored: %v", err)
	}
	if got := tmrcpModelIds(pOffline.GetModels()); !slices.Equal(got, []string{"static"}) {
		t.Fatalf("empty stored offline models = %v, want [static]", got)
	}

	_ = offlineStore.Write(t.Context(), pOffline.Id, &ai.ModelsStoreEntry{Models: ai.AnyModelList{tmrcpImageModel("img-only")}, CheckedAt: &checkedAt}, nil)
	if err := tmrcpRefresh(t, pOffline, offlineStore, false, nil); err != nil {
		t.Fatalf("offline image stored: %v", err)
	}
	if len(pOffline.GetModels()) != 0 {
		t.Fatalf("expected 0 chat models when stored has only image model, got %v", tmrcpModelIds(pOffline.GetModels()))
	}
	if got := tmrcpAnyIds(pOffline.GetAllModels()); !slices.Equal(got, []string{"img-only"}) {
		t.Fatalf("GetAllModels = %v, want [img-only]", got)
	}
}

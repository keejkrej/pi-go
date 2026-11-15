// Ported from packages/coding-agent/test/radius.test.ts (pi v1.5.2).

package core

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/oauth"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func tradStrPtr(s string) *string { return &s }
func tradBoolPtr(b bool) *bool    { return &b }

func tradUnsetOffline(t *testing.T) {
	t.Helper()
	prev, had := os.LookupEnv("PI_OFFLINE")
	t.Cleanup(func() {
		if had {
			_ = os.Setenv("PI_OFFLINE", prev)
		} else {
			_ = os.Unsetenv("PI_OFFLINE")
		}
	})
	_ = os.Unsetenv("PI_OFFLINE")
}

func tradGatewayConfig(baseUrl string) *jsonx.Object {
	return jsonx.ObjectOf(
		"baseUrl", baseUrl,
		"models", []any{
			jsonx.ObjectOf(
				"id", "auto",
				"name", "Radius Auto",
				"reasoning", false,
				"input", []any{"text"},
				"cost", jsonx.ObjectOf("input", 1.0, "output", 2.0, "cacheRead", 0.1, "cacheWrite", 0.2),
				"contextWindow", 128000,
				"maxTokens", 16384,
			),
		},
	)
}

func tradGatewayConfigJSON(baseUrl string) string {
	return `{"baseUrl":"` + baseUrl + `","models":[{"id":"auto","name":"Radius Auto","reasoning":false,"input":["text"],"cost":{"input":1,"output":2,"cacheRead":0.1,"cacheWrite":0.2},"contextWindow":128000,"maxTokens":16384}]}`
}

func tradOAuthCredential(gatewayBaseUrl *string) *ai.OAuthCredential {
	var extra *jsonx.Object
	if gatewayBaseUrl != nil {
		extra = jsonx.ObjectOf("gatewayConfig", tradGatewayConfig(*gatewayBaseUrl))
	}
	return &ai.OAuthCredential{
		Type: ai.AuthTypeOAuth,
		OAuthCredentials: ai.OAuthCredentials{
			Access:  "access-token",
			Refresh: "refresh-token",
			Expires: float64(time.Now().Add(time.Hour).UnixMilli()),
			Extra:   extra,
		},
	}
}

func tradSeedCred(t *testing.T, providerId string, cred ai.Credential) ai.CredentialStore {
	t.Helper()
	store := ai.NewInMemoryCredentialStore()
	if cred != nil {
		if _, err := store.Modify(t.Context(), providerId, func(_ ai.Credential) (ai.Credential, error) {
			return cred, nil
		}, nil); err != nil {
			t.Fatalf("seed credential: %v", err)
		}
	}
	return store
}

func tradHermeticCatalogServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.NotFound(w, nil)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRadiusEnvAndConstants(t *testing.T) {
	if RadiusProviderId != "radius" {
		t.Fatalf("RadiusProviderId = %q, want radius", RadiusProviderId)
	}
	if EnvRadiusGateway != "PI_RADIUS_GATEWAY" {
		t.Fatalf("EnvRadiusGateway = %q, want PI_RADIUS_GATEWAY", EnvRadiusGateway)
	}
	expectedMcp := oauth.NormalizeRadiusGatewayUrl(oauth.DefaultRadiusGateway) + "/mcp"
	if RadiusMcpUrl != expectedMcp {
		t.Fatalf("RadiusMcpUrl = %q, want %q", RadiusMcpUrl, expectedMcp)
	}

	prev, hadPrev := os.LookupEnv(EnvRadiusGateway)
	t.Cleanup(func() {
		if hadPrev {
			_ = os.Setenv(EnvRadiusGateway, prev)
		} else {
			_ = os.Unsetenv(EnvRadiusGateway)
		}
	})
	_ = os.Unsetenv(EnvRadiusGateway)
	if got := GetRadiusGatewayUrl(); got != oauth.NormalizeRadiusGatewayUrl(oauth.DefaultRadiusGateway) {
		t.Fatalf("default GetRadiusGatewayUrl = %q", got)
	}

	t.Setenv(EnvRadiusGateway, "http://127.0.0.1:8788/")
	if got := GetRadiusGatewayUrl(); got != "http://127.0.0.1:8788" {
		t.Fatalf("overridden GetRadiusGatewayUrl = %q, want http://127.0.0.1:8788", got)
	}
}

func TestRadiusProviderLifecycle(t *testing.T) {
	t.Run("restores legacy credential catalog without network access", func(t *testing.T) {
		tradUnsetOffline(t)
		baseUrl := "https://radius.example.com/v1"
		rt, err := CreateModelRuntime(t.Context(), &CreateModelRuntimeOptions{
			Credentials:       tradSeedCred(t, RadiusProviderId, tradOAuthCredential(&baseUrl)),
			ModelsStore:       NewInMemoryCodingAgentModelsStore(),
			ModelsPath:        tradStrPtr(""),
			AllowModelNetwork: false,
		})
		if err != nil {
			t.Fatalf("CreateModelRuntime: %v", err)
		}
		t.Cleanup(rt.Close)

		model := rt.GetModel(RadiusProviderId, "auto")
		if model == nil || model.Api != "pi-messages" || model.BaseUrl != baseUrl {
			t.Fatalf("unexpected restored radius model: %+v", model)
		}
		if prov := rt.GetProvider(RadiusProviderId); prov == nil || prov.Name != "Radius" {
			t.Fatalf("unexpected radius provider: %+v", prov)
		}
		if !rt.HasConfiguredAuth(RadiusProviderId) {
			t.Fatalf("expected HasConfiguredAuth(radius) to be true")
		}
	})

	t.Run("fetches and stores catalog for configured Radius auth", func(t *testing.T) {
		tradUnsetOffline(t)
		var seenAuth string
		var seenPath string
		var srv *httptest.Server
		srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v1/config" {
				seenPath = r.URL.Path
				seenAuth = r.Header.Get("Authorization")
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tradGatewayConfigJSON(srv.URL + "/v1")))
				return
			}
			http.NotFound(w, r)
		}))
		defer srv.Close()

		catSrv := tradHermeticCatalogServer(t)
		modelsStore := NewInMemoryCodingAgentModelsStore()
		rt, err := CreateModelRuntime(t.Context(), &CreateModelRuntimeOptions{
			Credentials:       tradSeedCred(t, RadiusProviderId, tradOAuthCredential(nil)),
			ModelsStore:       modelsStore,
			ModelsPath:        tradStrPtr(""),
			CatalogBaseUrl:    &catSrv.URL,
			RefreshOnCreate:   tradBoolPtr(false),
			AllowModelNetwork: false,
		})
		if err != nil {
			t.Fatalf("CreateModelRuntime: %v", err)
		}
		t.Cleanup(rt.Close)
		rt.RegisterNativeProvider(providers.RadiusProvider(&providers.RadiusProviderOptions{
			Gateway: &srv.URL,
		}))
		res, err := rt.Refresh(t.Context(), &ai.ModelsRefreshOptions{
			AllowNetwork: tradBoolPtr(true),
			Providers:    []string{RadiusProviderId},
		})
		if err != nil {
			t.Fatalf("Refresh radius: %v", err)
		}
		if res.Errors != nil && res.Errors.Len() > 0 {
			for k, v := range res.Errors.All() {
				t.Fatalf("Refresh radius error[%s]: %v (seenPath=%q)", k, v, seenPath)
			}
		}

		if rt.GetModel(RadiusProviderId, "auto") == nil {
			t.Fatalf("expected auto model after network refresh (seenPath=%q models=%+v)", seenPath, rt.GetModels(tradStrPtr(RadiusProviderId)))
		}
		stored, err := modelsStore.Read(t.Context(), RadiusProviderId, nil)
		if err != nil || stored == nil || len(stored.Models) != 1 {
			t.Fatalf("expected stored radius model, got err=%v stored=%+v", err, stored)
		}
		if seenPath != "/v1/config" || seenAuth != "Bearer access-token" {
			t.Fatalf("unexpected gateway request path=%q auth=%q", seenPath, seenAuth)
		}
	})

	t.Run("does not refresh over network by default", func(t *testing.T) {
		tradUnsetOffline(t)
		called := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			called++
			http.Error(w, "unexpected network call", http.StatusInternalServerError)
		}))
		defer srv.Close()

		legacyUrl := "https://radius.example.com/v1"
		rt, err := CreateModelRuntime(t.Context(), &CreateModelRuntimeOptions{
			Credentials:     tradSeedCred(t, RadiusProviderId, tradOAuthCredential(&legacyUrl)),
			ModelsStore:     NewInMemoryCodingAgentModelsStore(),
			ModelsPath:      tradStrPtr(""),
			RefreshOnCreate: tradBoolPtr(false),
		})
		if err != nil {
			t.Fatalf("CreateModelRuntime: %v", err)
		}
		t.Cleanup(rt.Close)
		rt.RegisterNativeProvider(providers.RadiusProvider(&providers.RadiusProviderOptions{
			Gateway: &srv.URL,
		}))
		if _, err := rt.Refresh(t.Context(), &ai.ModelsRefreshOptions{
			AllowNetwork: tradBoolPtr(false),
			Providers:    []string{RadiusProviderId},
		}); err != nil {
			t.Fatalf("Refresh: %v", err)
		}
		if rt.GetModel(RadiusProviderId, "auto") == nil {
			t.Fatalf("expected restored auto model")
		}
		if called != 0 {
			t.Fatalf("expected 0 HTTP calls with AllowModelNetwork=false, got %d", called)
		}
	})

	t.Run("does not fetch or expose Radius models without configured auth", func(t *testing.T) {
		tradUnsetOffline(t)
		configCalls := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v1/config" {
				configCalls++
			}
			http.NotFound(w, r)
		}))
		defer srv.Close()

		catSrv := tradHermeticCatalogServer(t)
		rt, err := CreateModelRuntime(t.Context(), &CreateModelRuntimeOptions{
			Credentials:     ai.NewInMemoryCredentialStore(),
			ModelsStore:     NewInMemoryCodingAgentModelsStore(),
			ModelsPath:      tradStrPtr(""),
			CatalogBaseUrl:  &catSrv.URL,
			RefreshOnCreate: tradBoolPtr(false),
		})
		if err != nil {
			t.Fatalf("CreateModelRuntime: %v", err)
		}
		t.Cleanup(rt.Close)
		rt.RegisterNativeProvider(providers.RadiusProvider(&providers.RadiusProviderOptions{
			Gateway: &srv.URL,
		}))
		if _, err := rt.Refresh(t.Context(), &ai.ModelsRefreshOptions{
			AllowNetwork: tradBoolPtr(true),
			Providers:    []string{RadiusProviderId},
		}); err != nil {
			t.Fatalf("Refresh: %v", err)
		}
		for _, m := range rt.GetAvailableSnapshot() {
			if m.Provider == RadiusProviderId {
				t.Fatalf("unexpected available radius model without auth: %+v", m)
			}
		}
		if configCalls != 0 {
			t.Fatalf("expected 0 /v1/config calls without auth, got %d", configCalls)
		}
	})

	t.Run("supports custom Radius gateways from models.json and validates baseUrl", func(t *testing.T) {
		tradUnsetOffline(t)
		var srv *httptest.Server
		srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(tradGatewayConfigJSON(srv.URL + "/v1")))
		}))
		defer srv.Close()

		dir := t.TempDir()
		modelsPath := filepath.Join(dir, "models.json")
		cfgJson := `{"providers":{"radius-dev":{"name":"Radius (dev)","baseUrl":"` + srv.URL + `","oauth":"radius"}}}`
		if err := os.WriteFile(modelsPath, []byte(cfgJson), 0o600); err != nil {
			t.Fatal(err)
		}

		catSrv := tradHermeticCatalogServer(t)
		rt, err := CreateModelRuntime(t.Context(), &CreateModelRuntimeOptions{
			Credentials:       tradSeedCred(t, "radius-dev", tradOAuthCredential(nil)),
			ModelsStore:       NewInMemoryCodingAgentModelsStore(),
			ModelsPath:        &modelsPath,
			CatalogBaseUrl:    &catSrv.URL,
			RefreshOnCreate:   tradBoolPtr(false),
			AllowModelNetwork: false,
		})
		if err != nil {
			t.Fatalf("CreateModelRuntime: %v", err)
		}
		t.Cleanup(rt.Close)
		if _, err := rt.Refresh(t.Context(), &ai.ModelsRefreshOptions{
			AllowNetwork: tradBoolPtr(true),
			Providers:    []string{"radius-dev"},
		}); err != nil {
			t.Fatalf("Refresh radius-dev: %v", err)
		}
		m := rt.GetModel("radius-dev", "auto")
		if m == nil || m.Api != "pi-messages" || m.BaseUrl != srv.URL+"/v1" {
			t.Fatalf("unexpected custom radius model: %+v", m)
		}
		if p := rt.GetProvider("radius-dev"); p == nil || p.Name != "Radius (dev)" {
			t.Fatalf("unexpected custom radius provider: %+v", p)
		}

		// Missing baseUrl fails validation.
		invalidPath := filepath.Join(dir, "invalid-models.json")
		if err := os.WriteFile(invalidPath, []byte(`{"providers":{"radius-dev":{"oauth":"radius"}}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		rtErr, err := CreateModelRuntime(t.Context(), &CreateModelRuntimeOptions{
			Credentials:       ai.NewInMemoryCredentialStore(),
			ModelsStore:       NewInMemoryCodingAgentModelsStore(),
			ModelsPath:        &invalidPath,
			AllowModelNetwork: false,
		})
		if err != nil {
			t.Fatalf("CreateModelRuntime invalid: %v", err)
		}
		t.Cleanup(rtErr.Close)
		if rtErr.GetError() == nil || !strings.Contains(*rtErr.GetError(), `"baseUrl" is required when "oauth" is set`) {
			t.Fatalf("expected missing baseUrl error, got %v", rtErr.GetError())
		}
	})
}

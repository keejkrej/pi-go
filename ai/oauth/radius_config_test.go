package oauth

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func TestRadiusConfig_NormalizeCredentialAndLoad(t *testing.T) {
	if got := NormalizeRadiusGatewayUrl("radius.example.com/"); got != "https://radius.example.com" {
		t.Fatalf("got %q", got)
	}
	if got := NormalizeRadiusGatewayUrl("HTTP://localhost:8080///"); got != "HTTP://localhost:8080" {
		t.Fatalf("got %q", got)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/config" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("authorization") != "Bearer tok-1" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, "unauthorized")
			return
		}
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, `{
			"baseUrl": "https://gw.example/v1",
			"models": [
				{
					"id": "m1",
					"name": "Model One",
					"reasoning": true,
					"input": ["text", "image"],
					"cost": {"input": 1, "output": 2, "cacheRead": 0, "cacheWrite": 0},
					"contextWindow": 128000,
					"maxTokens": 8192
				},
				{ "id": 123, "name": "invalid" }
			]
		}`)
	}))
	defer srv.Close()

	ctx := tuWithHttpClient(t.Context(), srv.Client())
	apiKey := "tok-1"
	cfg, err := LoadRadiusGatewayConfig(ctx, srv.URL+"/ignored/path", &apiKey)
	if err != nil {
		t.Fatalf("LoadRadiusGatewayConfig failed: %v", err)
	}
	if cfg.BaseUrl != "https://gw.example/v1" || len(cfg.Models) != 1 || cfg.Models[0].Id != "m1" {
		t.Fatalf("unexpected config: %+v", cfg)
	}

	extra := jsonx.NewObject()
	enc, err := jsonx.Encode(cfg)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}
	extra.Set("gatewayConfig", enc)
	cred := &ai.OAuthCredential{
		Type:             ai.AuthTypeOAuth,
		OAuthCredentials: ai.OAuthCredentials{Refresh: "r", Access: "tok-1", Expires: 9999999999999, Extra: extra},
	}

	models := GetRadiusModels("radius", cred)
	if len(models) != 1 || models[0].Api != "pi-messages" || models[0].Provider != "radius" || models[0].BaseUrl != "https://gw.example/v1" {
		t.Fatalf("unexpected models: %+v", models)
	}

	badKey := "wrong"
	if _, err := LoadRadiusGatewayConfig(ctx, srv.URL, &badKey); err == nil || !strings.Contains(err.Error(), "401: unauthorized") {
		t.Fatalf("expected 401 error, got %v", err)
	}
}

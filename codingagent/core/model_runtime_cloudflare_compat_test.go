// Ported from packages/coding-agent/test/model-runtime-cloudflare-compat.test.ts (pi v1.5.2).

package core

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/internal/omap"
)

func tmrccStrPtr(s string) *string { return &s }
func tmrccBoolPtr(b bool) *bool    { return &b }

type tmrccRewriteTransport struct {
	targetBase string
	inner      http.RoundTripper
	seenURL    string
	seenHdr    http.Header
}

func (tr *tmrccRewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	tr.seenURL = req.URL.String()
	tr.seenHdr = req.Header.Clone()
	parsed, _ := http.NewRequestWithContext(req.Context(), req.Method, tr.targetBase+req.URL.RequestURI(), nil)
	clone.URL = parsed.URL
	clone.Host = parsed.URL.Host
	return tr.inner.RoundTrip(clone)
}

func TestModelRuntimeCloudflareCompatHeadersAndStream(t *testing.T) {
	t.Setenv("PI_OFFLINE", "1")
	t.Setenv("CLOUDFLARE_API_KEY", "")
	t.Setenv("CLOUDFLARE_ACCOUNT_ID", "")
	t.Setenv("CLOUDFLARE_GATEWAY_ID", "")

	creds := ai.NewInMemoryCredentialStore()
	_, _ = creds.Modify(t.Context(), "cloudflare-ai-gateway", func(ai.Credential) (ai.Credential, error) {
		return &ai.ApiKeyCredential{
			Type: ai.AuthTypeApiKey,
			Key:  tmrccStrPtr("cf-gw-secret"),
			Env: map[string]string{
				"CLOUDFLARE_ACCOUNT_ID": "acct-123",
				"CLOUDFLARE_GATEWAY_ID": "gw-456",
			},
		}, nil
	}, nil)
	_, _ = creds.Modify(t.Context(), "cloudflare-workers-ai", func(ai.Credential) (ai.Credential, error) {
		return &ai.ApiKeyCredential{
			Type: ai.AuthTypeApiKey,
			Key:  tmrccStrPtr("cf-workers-secret"),
			Env: map[string]string{
				"CLOUDFLARE_ACCOUNT_ID": "acct-workers",
			},
		}, nil
	}, nil)

	rt, err := CreateModelRuntime(context.Background(), &CreateModelRuntimeOptions{
		Credentials:     creds,
		ModelsPath:      tmrccStrPtr(filepath.Join(t.TempDir(), "models.json")),
		ModelsStore:     NewInMemoryCodingAgentModelsStore(),
		RefreshOnCreate: tmrccBoolPtr(true),
	})
	if err != nil {
		t.Fatalf("CreateModelRuntime: %v", err)
	}
	t.Cleanup(rt.Close)
	reg := NewModelRegistry(rt)

	// Verify cloudflare-ai-gateway auth suppression via ModelRegistry.
	gwModels := rt.GetModels(tmrccStrPtr("cloudflare-ai-gateway"))
	if len(gwModels) == 0 {
		t.Fatal("expected cloudflare-ai-gateway models")
	}
	gwModel := gwModels[0]
	auth := reg.GetApiKeyAndHeaders(t.Context(), gwModel)
	if !auth.Ok || auth.Headers == nil {
		t.Fatalf("gateway GetApiKeyAndHeaders failed: %+v", auth)
	}
	if v, ok := auth.Headers.Get("cf-aig-authorization"); !ok || v == nil || *v != "Bearer cf-gw-secret" {
		t.Fatalf("cf-aig-authorization = (%v, %v)", v, ok)
	}
	if v, ok := auth.Headers.Get("Authorization"); !ok || v != nil {
		t.Fatalf("expected nil Authorization suppression, got (%v, %v)", v, ok)
	}

	// Verify ResolveCloudflareModel interpolates stored credential Env cleanly.
	resolvedGW := providers.ResolveCloudflareModel(gwModel, auth.Env)
	if !strings.Contains(resolvedGW.BaseUrl, "acct-123") || !strings.Contains(resolvedGW.BaseUrl, "gw-456") {
		t.Fatalf("resolved cloudflare gateway BaseUrl = %q", resolvedGW.BaseUrl)
	}

	// Also exercise HTTP request path + headers via StreamSimple and an intercepting httptest server.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"mock stop"}}`))
	}))
	defer srv.Close()

	tr := &tmrccRewriteTransport{targetBase: srv.URL, inner: srv.Client().Transport}
	client := &http.Client{Transport: tr}

	// Use custom cloudflare-ai-gateway provider wrapper preserving CloudflareStreams behavior with mock API model.
	workersModels := rt.GetModels(tmrccStrPtr("cloudflare-workers-ai"))
	if len(workersModels) == 0 {
		t.Fatal("expected cloudflare-workers-ai models")
	}
	wModel := workersModels[0]
	wAuth := reg.GetApiKeyAndHeaders(t.Context(), wModel)
	if !wAuth.Ok {
		t.Fatalf("workers-ai GetApiKeyAndHeaders failed: %+v", wAuth)
	}
	resolvedWorkers := providers.ResolveCloudflareModel(wModel, wAuth.Env)
	if !strings.Contains(resolvedWorkers.BaseUrl, "acct-workers") {
		t.Fatalf("resolved workers BaseUrl = %q", resolvedWorkers.BaseUrl)
	}

	// Also verify Stream through runtime passes interpolated URL & headers to HTTP transport.
	reqHeaders := omap.NewMap[string, *string]()
	reqVal := "from-opt"
	reqHeaders.Set("x-test-hdr", &reqVal)
	_, _ = rt.CompleteSimple(t.Context(), gwModel, &ai.Context{
		Messages: ai.MessageList{&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tmrccStrPtr("ping")}}},
	}, &ai.ModelsSimpleStreamOptions{
		SimpleStreamOptions: ai.SimpleStreamOptions{
			StreamOptions: ai.StreamOptions{
				ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
					HTTPClient: client,
					Headers:    reqHeaders,
					MaxRetries: new(int),
				},
			},
		},
	})
	if !strings.Contains(tr.seenURL, "acct-123") || !strings.Contains(tr.seenURL, "gw-456") {
		t.Fatalf("expected request URL to contain acct-123 and gw-456, got %q", tr.seenURL)
	}
	if tr.seenHdr.Get("cf-aig-authorization") != "Bearer cf-gw-secret" {
		t.Fatalf("missing cf-aig-authorization header: %v", tr.seenHdr)
	}
	if tr.seenHdr.Get("Authorization") != "" {
		t.Fatalf("expected Authorization header suppressed, got %q", tr.seenHdr.Get("Authorization"))
	}
	if tr.seenHdr.Get("x-test-hdr") != "from-opt" {
		t.Fatalf("missing x-test-hdr header: %v", tr.seenHdr)
	}
}

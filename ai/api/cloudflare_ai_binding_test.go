// Ported from packages/ai/test/cloudflare-ai-binding.test.ts (pi v1.5.2).

package api_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai/api"
)

func TestCloudflareAiBindingClientAndFetch(t *testing.T) {
	if _, err := api.CreateAiBindingFetch(nil); err == nil {
		t.Fatal("expected error for nil binding")
	}
	if _, err := api.CreateAiBindingHTTPClient(&api.AiBinding{}); err == nil {
		t.Fatal("expected error for binding missing Fetch")
	}

	var seenMethod, seenUrl, seenAuth string
	client, err := api.CreateAiBindingHTTPClient(&api.AiBinding{
		Fetch: func(ctx context.Context, req *http.Request) (*http.Response, error) {
			seenMethod = req.Method
			seenUrl = req.URL.String()
			seenAuth = req.Header.Get("Authorization")
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("CreateAiBindingHTTPClient failed: %v", err)
	}

	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		"https://gateway.ai.cloudflare.com/v1/acct/gw/compat/chat/completions",
		bytes.NewBufferString(`{"hello":"world"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+api.CloudflareGatewayBindingAuthSentinel)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do failed: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"ok":true}` {
		t.Fatalf("unexpected response body: %q", string(body))
	}
	if seenMethod != http.MethodPost || !strings.HasSuffix(seenUrl, "/compat/chat/completions") {
		t.Fatalf("unexpected forwarded request: %s %s", seenMethod, seenUrl)
	}
	if seenAuth != "Bearer "+api.CloudflareGatewayBindingAuthSentinel {
		t.Fatalf("unexpected auth header: %q", seenAuth)
	}
}

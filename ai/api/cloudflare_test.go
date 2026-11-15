// Ported from packages/ai/test/cloudflare-stream.test.ts URL template checks (pi v1.5.2).

package api_test

import (
	"testing"

	"github.com/keejkrej/pi-go/ai/api"
)

func TestCloudflareEndpointTemplates(t *testing.T) {
	if api.CloudflareWorkersAiBaseUrl != "https://api.cloudflare.com/client/v4/accounts/{CLOUDFLARE_ACCOUNT_ID}/ai/v1" {
		t.Fatalf("CloudflareWorkersAiBaseUrl = %q", api.CloudflareWorkersAiBaseUrl)
	}
	if api.CloudflareWorkersAiRestBaseUrl != "https://api.cloudflare.com/client/v4/accounts/{CLOUDFLARE_ACCOUNT_ID}/ai" {
		t.Fatalf("CloudflareWorkersAiRestBaseUrl = %q", api.CloudflareWorkersAiRestBaseUrl)
	}
	if api.CloudflareAiGatewayCompatBaseUrl != "https://gateway.ai.cloudflare.com/v1/{CLOUDFLARE_ACCOUNT_ID}/{CLOUDFLARE_GATEWAY_ID}/compat" {
		t.Fatalf("CloudflareAiGatewayCompatBaseUrl = %q", api.CloudflareAiGatewayCompatBaseUrl)
	}
	if api.CloudflareAiGatewayOpenaiBaseUrl != "https://gateway.ai.cloudflare.com/v1/{CLOUDFLARE_ACCOUNT_ID}/{CLOUDFLARE_GATEWAY_ID}/openai" {
		t.Fatalf("CloudflareAiGatewayOpenaiBaseUrl = %q", api.CloudflareAiGatewayOpenaiBaseUrl)
	}
	if api.CloudflareAiGatewayAnthropicBaseUrl != "https://gateway.ai.cloudflare.com/v1/{CLOUDFLARE_ACCOUNT_ID}/{CLOUDFLARE_GATEWAY_ID}/anthropic" {
		t.Fatalf("CloudflareAiGatewayAnthropicBaseUrl = %q", api.CloudflareAiGatewayAnthropicBaseUrl)
	}
}

// Ported from packages/ai/src/api/cloudflare.ts (pi v1.5.2).

package api

// Cloudflare Workers AI and AI Gateway endpoint templates.
const (
	// CloudflareWorkersAiBaseUrl is the Workers AI direct OpenAI-compatible endpoint.
	CloudflareWorkersAiBaseUrl = "https://api.cloudflare.com/client/v4/accounts/{CLOUDFLARE_ACCOUNT_ID}/ai/v1"

	// CloudflareWorkersAiRestBaseUrl is the Workers AI REST endpoint root; model runs are posted to `run`.
	CloudflareWorkersAiRestBaseUrl = "https://api.cloudflare.com/client/v4/accounts/{CLOUDFLARE_ACCOUNT_ID}/ai"

	// CloudflareAiGatewayCompatBaseUrl is the AI Gateway Unified API endpoint.
	// https://developers.cloudflare.com/ai-gateway/usage/unified-api/
	CloudflareAiGatewayCompatBaseUrl = "https://gateway.ai.cloudflare.com/v1/{CLOUDFLARE_ACCOUNT_ID}/{CLOUDFLARE_GATEWAY_ID}/compat"

	// CloudflareAiGatewayOpenaiBaseUrl is the AI Gateway -> OpenAI passthrough endpoint.
	CloudflareAiGatewayOpenaiBaseUrl = "https://gateway.ai.cloudflare.com/v1/{CLOUDFLARE_ACCOUNT_ID}/{CLOUDFLARE_GATEWAY_ID}/openai"

	// CloudflareAiGatewayAnthropicBaseUrl is the AI Gateway -> Anthropic passthrough endpoint.
	CloudflareAiGatewayAnthropicBaseUrl = "https://gateway.ai.cloudflare.com/v1/{CLOUDFLARE_ACCOUNT_ID}/{CLOUDFLARE_GATEWAY_ID}/anthropic"
)

// Ported from packages/ai/src/api/cloudflare-ai-binding.ts (pi v1.5.2).

package api

import (
	"context"
	"fmt"
	"net/http"

	"github.com/keejkrej/pi-go/ai"
)

// AiBinding describes a Workers AI binding (env.AI) without depending on external runtime types.
//
// Fetch is nilable so bindings missing a round-trip implementation can be checked at construction
// time; AiGatewayLogId pins the shape to the Workers AI binding.
type AiBinding struct {
	AiGatewayLogId *string          `json:"aiGatewayLogId,omitzero"`
	Fetch          ai.FetchFunction `json:"-"`
}

// CloudflareGatewayBindingAuthSentinel satisfies request-auth validation on binding-routed requests.
// Pair it with nil Authorization / x-api-key headers so SDK placeholder credentials never reach
// the gateway.
const CloudflareGatewayBindingAuthSentinel = "cloudflare-gateway-binding"

type cabRoundTripper struct {
	fetch ai.FetchFunction
}

func (r cabRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return r.fetch(req.Context(), req)
}

// CreateAiBindingFetch creates a FetchFunction backed by binding, passing requests through untouched.
func CreateAiBindingFetch(binding *AiBinding) (ai.FetchFunction, error) {
	if binding == nil || binding.Fetch == nil {
		return nil, fmt.Errorf("createAiBindingFetch: the AI binding does not expose fetch()")
	}
	bindingFetch := binding.Fetch
	return func(ctx context.Context, req *http.Request) (*http.Response, error) {
		return bindingFetch(ctx, req)
	}, nil
}

// CreateAiBindingHTTPClient adapts CreateAiBindingFetch into an *http.Client for Go request options.
func CreateAiBindingHTTPClient(binding *AiBinding) (*http.Client, error) {
	fetchFn, err := CreateAiBindingFetch(binding)
	if err != nil {
		return nil, err
	}
	return &http.Client{Transport: cabRoundTripper{fetch: fetchFn}}, nil
}

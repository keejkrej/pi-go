// Ported from packages/mcp/src/auth-provider.ts (pi v1.0.0).

package mcp

import (
	"context"
	"net/http"
	"net/url"
)

// McpRequestInit is the RequestInit passed to McpFetch.
// The abort signal is the ctx argument of McpFetch, not a field.
// An empty Method means GET. A nil Body means no body.
// Headers use canonical MIME header keys. Body is the raw bytes
// (a string body, or URLSearchParams already encoded as application/x-www-form-urlencoded).
type McpRequestInit struct {
	Method  string
	Headers http.Header
	Body    []byte
}

// McpFetch is fetch(input, init). input is the URL string (TS string | URL).
// A nil init means no method, headers, or body. The caller closes the response body.
type McpFetch func(ctx context.Context, input string, init *McpRequestInit) (*http.Response, error)

// UnauthorizedContext is the argument of AuthProvider.onUnauthorized.
// Response is the rejected 401, or a 403 whose challenge reports insufficient_scope.
type UnauthorizedContext struct {
	Response  *http.Response
	ServerUrl *url.URL
	Fetch     McpFetch
	Token     *string
}

// AuthProvider supplies bearer tokens to an MCP HTTP transport.
type AuthProvider interface {
	// Token returns the access token, or nil when the request should be unauthenticated.
	Token() (*string, error)
}

// AuthProviderUnauthorized is the optional AuthProvider.onUnauthorized method.
// Streamable HTTP type-asserts to it. A missing implementation means a 401 is not retried.
// A nil error means the request is retried with whatever credentials Token now returns.
type AuthProviderUnauthorized interface {
	OnUnauthorized(unauth *UnauthorizedContext) error
}

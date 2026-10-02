// Ported from packages/mcp/src/oauth/errors.ts (pi v1.0.0).

package oauth

// OAuthError is an OAuth error object from a token response.
type OAuthError struct {
	Code     string
	Message  string
	ErrorUri *string
}

// NewOAuthError returns an OAuth error. message is the description; an empty message uses code.
func NewOAuthError(code, message string, errorUri *string) *OAuthError {
	panic("unported: NewOAuthError")
}

func (e *OAuthError) Error() string { return e.Message }

// Name returns the TS error name.
func (e *OAuthError) Name() string { return "OAuthError" }

// OAuthIssuerMismatchError means the issuer did not match the expected authorization server.
type OAuthIssuerMismatchError struct {
	Expected string
	// Received is nil when an authorization response lacks the iss parameter its server promised (RFC 9207).
	Received *string
	Message  string
}

// NewOAuthIssuerMismatchError returns an issuer-mismatch error.
func NewOAuthIssuerMismatchError(expected string, received *string) *OAuthIssuerMismatchError {
	panic("unported: NewOAuthIssuerMismatchError")
}

func (e *OAuthIssuerMismatchError) Error() string { return e.Message }

// Name returns the TS error name.
func (e *OAuthIssuerMismatchError) Name() string { return "OAuthIssuerMismatchError" }

// OAuthInsecureEndpointError means a token request refused a non-HTTPS, non-loopback endpoint.
type OAuthInsecureEndpointError struct {
	Endpoint string
	Message  string
}

// NewOAuthInsecureEndpointError returns an insecure-endpoint error. endpoint is the URL text.
func NewOAuthInsecureEndpointError(endpoint string) *OAuthInsecureEndpointError {
	panic("unported: NewOAuthInsecureEndpointError")
}

func (e *OAuthInsecureEndpointError) Error() string { return e.Message }

// Name returns the TS error name.
func (e *OAuthInsecureEndpointError) Name() string { return "OAuthInsecureEndpointError" }

// OAuthRegistrationError means dynamic client registration returned a non-OK status.
type OAuthRegistrationError struct {
	Status  int
	Body    string
	Message string
}

// NewOAuthRegistrationError returns a registration error.
func NewOAuthRegistrationError(status int, body string) *OAuthRegistrationError {
	panic("unported: NewOAuthRegistrationError")
}

func (e *OAuthRegistrationError) Error() string { return e.Message }

// Name returns the TS error name.
func (e *OAuthRegistrationError) Name() string { return "OAuthRegistrationError" }

// McpOAuthAuthorizationRequiredError means the user has to authorize in a browser.
type McpOAuthAuthorizationRequiredError struct {
	Message string
}

// NewMcpOAuthAuthorizationRequiredError returns the authorization-required error.
func NewMcpOAuthAuthorizationRequiredError() *McpOAuthAuthorizationRequiredError {
	return &McpOAuthAuthorizationRequiredError{Message: "MCP OAuth authorization requires user interaction"}
}

func (e *McpOAuthAuthorizationRequiredError) Error() string { return e.Message }

// Name returns the TS error name.
func (e *McpOAuthAuthorizationRequiredError) Name() string {
	return "McpOAuthAuthorizationRequiredError"
}

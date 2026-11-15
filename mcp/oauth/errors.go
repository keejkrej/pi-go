// Ported from packages/mcp/src/oauth/errors.ts (pi v1.5.2).

package oauth

import (
	"fmt"

	"github.com/keejkrej/pi-go/internal/jsonx"
)

// OAuthError is an OAuth error object from a token response.
type OAuthError struct {
	Code     string
	Message  string
	ErrorUri *string
}

// NewOAuthError returns an OAuth error. message is the description; an empty message uses code.
func NewOAuthError(code, message string, errorUri *string) *OAuthError {
	msg := message
	if msg == "" {
		msg = code
	}
	return &OAuthError{
		Code:     code,
		Message:  msg,
		ErrorUri: errorUri,
	}
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
	expFmt := "none"
	if expected != "" {
		expFmt = errorsQuote(expected)
	}
	recFmt := "none"
	if received != nil {
		recFmt = errorsQuote(*received)
	}
	return &OAuthIssuerMismatchError{
		Expected: expected,
		Received: received,
		Message:  fmt.Sprintf("OAuth issuer mismatch: expected %s, received %s", expFmt, recFmt),
	}
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
	return &OAuthInsecureEndpointError{
		Endpoint: endpoint,
		Message:  fmt.Sprintf("Refusing to send OAuth credentials to non-HTTPS endpoint %s", endpoint),
	}
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
	return &OAuthRegistrationError{
		Status:  status,
		Body:    body,
		Message: fmt.Sprintf("OAuth dynamic client registration failed with status %d: %s", status, body),
	}
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

// McpRefreshLockBusyError means another process holds the refresh lock for the whole wait.
type McpRefreshLockBusyError struct {
	Message string
}

// NewMcpRefreshLockBusyError returns the lock-busy error.
func NewMcpRefreshLockBusyError() *McpRefreshLockBusyError {
	return &McpRefreshLockBusyError{Message: "Another process is refreshing the MCP server's tokens"}
}

func (e *McpRefreshLockBusyError) Error() string { return e.Message }

// Name returns the TS error name.
func (e *McpRefreshLockBusyError) Name() string { return "McpRefreshLockBusyError" }

func errorsQuote(s string) string {
	b, err := jsonx.Marshal(s)
	if err != nil {
		return fmt.Sprintf("%q", s)
	}
	return string(b)
}

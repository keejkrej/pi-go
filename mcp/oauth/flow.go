// Ported from packages/mcp/src/oauth/flow.ts (pi v1.0.0).

package oauth

import (
	"net/http"
	"net/url"

	"github.com/keejkrej/pi-go/mcp"
)

// AddClientAuthentication sets client authentication on a token request.
// headers and params are mutated. rawUrl is the token endpoint (TS string | URL).
type AddClientAuthentication func(headers http.Header, params url.Values, rawUrl string, metadata *AuthorizationServerMetadata) error

// OAuthFlowResult is the outcome of AuthorizeMcp.
type OAuthFlowResult string

const (
	OAuthFlowResultAuthorized OAuthFlowResult = "AUTHORIZED"
	OAuthFlowResultRedirect   OAuthFlowResult = "REDIRECT"
)

// OAuthInvalidateCredentialsKind selects which stored credentials InvalidateCredentials drops.
type OAuthInvalidateCredentialsKind string

const (
	OAuthInvalidateCredentialsKindAll       OAuthInvalidateCredentialsKind = "all"
	OAuthInvalidateCredentialsKindClient    OAuthInvalidateCredentialsKind = "client"
	OAuthInvalidateCredentialsKindTokens    OAuthInvalidateCredentialsKind = "tokens"
	OAuthInvalidateCredentialsKindVerifier  OAuthInvalidateCredentialsKind = "verifier"
	OAuthInvalidateCredentialsKindDiscovery OAuthInvalidateCredentialsKind = "discovery"
)

// OAuthClientProvider is the required half of the TS OAuthClientProvider interface.
// Optional members are separate interfaces so a missing method is an absent TS property.
type OAuthClientProvider interface {
	RedirectUrl() string
	ClientMetadata() OAuthClientMetadata
	ClientInformation() (OAuthClientInformationMixed, error)
	Tokens() (*OAuthTokens, error)
	SaveTokens(tokens *OAuthTokens) error
	RedirectToAuthorization(u *url.URL) error
	SaveCodeVerifier(verifier string) error
	CodeVerifier() (string, error)
}

// OAuthClientProviderClientMetadataUrl is the optional clientMetadataUrl property.
type OAuthClientProviderClientMetadataUrl interface {
	ClientMetadataUrl() string
}

// OAuthClientProviderState is the optional state method.
type OAuthClientProviderState interface {
	State() (string, error)
}

// OAuthClientProviderSaveClientInformation is the optional saveClientInformation method.
// Absence is not a no-op: the flow refuses to register a client it cannot persist.
type OAuthClientProviderSaveClientInformation interface {
	SaveClientInformation(information OAuthClientInformationMixed) error
}

// OAuthClientProviderAddClientAuthentication is the optional addClientAuthentication property.
type OAuthClientProviderAddClientAuthentication interface {
	AddClientAuthentication() AddClientAuthentication
}

// OAuthClientProviderInvalidateCredentials is the optional invalidateCredentials method.
type OAuthClientProviderInvalidateCredentials interface {
	InvalidateCredentials(kind OAuthInvalidateCredentialsKind) error
}

// OAuthClientProviderSaveDiscoveryState is the optional saveDiscoveryState method.
type OAuthClientProviderSaveDiscoveryState interface {
	SaveDiscoveryState(state *OAuthDiscoveryState) error
}

// OAuthClientProviderDiscoveryState is the optional discoveryState method.
type OAuthClientProviderDiscoveryState interface {
	DiscoveryState() (*OAuthDiscoveryState, error)
}

// OAuthFlowOptions are the options of AuthorizeMcp.
type OAuthFlowOptions struct {
	ServerUrl         string
	AuthorizationCode *string
	// Iss is the iss parameter of the authorization response that delivered AuthorizationCode (RFC 9207).
	Iss                 *string
	Scope               *string
	ResourceMetadataUrl *url.URL
	// AuthorizationServerMetadataUrl is an authorization server metadata document to use instead of
	// discovery, for servers that advertise a wrong authorization server or none. It is trusted as
	// configured. It must use https, except on loopback.
	AuthorizationServerMetadataUrl *url.URL
	Fetch                          mcp.McpFetch
	SkipIssuerValidation           *bool
	// SkipRefresh goes straight to the authorization redirect instead of refreshing stored tokens,
	// for example when the server asks for scopes the current grant lacks (a refresh keeps the old scope).
	SkipRefresh *bool
	// HTTPClient is the Go-only HTTP client. Nil means the default client. Ignored when Fetch is set.
	HTTPClient *http.Client
}

// TokenRequestOptions are the shared options of the token endpoint calls.
type TokenRequestOptions struct {
	Metadata                *AuthorizationServerMetadata
	ClientInformation       OAuthClientInformationMixed
	Resource                *string
	AddClientAuthentication AddClientAuthentication
	Fetch                   mcp.McpFetch
	// HTTPClient is the Go-only HTTP client. Nil means the default client. Ignored when Fetch is set.
	HTTPClient *http.Client
}

// StartAuthorizationOptions are the options of StartAuthorization.
type StartAuthorizationOptions struct {
	Metadata          *AuthorizationServerMetadata
	ClientInformation OAuthClientInformationMixed
	RedirectUrl       string
	Scope             *string
	State             *string
	Resource          *string
}

// RegisterClientOptions are the options of RegisterClient.
type RegisterClientOptions struct {
	Metadata       *AuthorizationServerMetadata
	ClientMetadata OAuthClientMetadata
	Scope          *string
	Fetch          mcp.McpFetch
	// HTTPClient is the Go-only HTTP client. Nil means the default client. Ignored when Fetch is set.
	HTTPClient *http.Client
}

// ExchangeAuthorizationCodeOptions are TokenRequestOptions plus the authorization code.
type ExchangeAuthorizationCodeOptions struct {
	TokenRequestOptions
	Code         string
	CodeVerifier string
	RedirectUrl  string
}

// RefreshAuthorizationOptions are TokenRequestOptions plus the refresh token.
type RefreshAuthorizationOptions struct {
	TokenRequestOptions
	RefreshToken string
}

// StartAuthorization builds the authorization URL and a PKCE verifier.
func StartAuthorization(authorizationServerUrl string, options *StartAuthorizationOptions) (authorizationUrl *url.URL, codeVerifier string, err error) {
	panic("unported: StartAuthorization")
}

// RegisterClient registers a public or confidential client at the authorization server.
func RegisterClient(authorizationServerUrl string, options *RegisterClientOptions) (*OAuthClientInformationFull, error) {
	panic("unported: RegisterClient")
}

// ExchangeAuthorizationCode trades an authorization code for tokens.
func ExchangeAuthorizationCode(authorizationServerUrl string, options *ExchangeAuthorizationCodeOptions) (*OAuthTokens, error) {
	panic("unported: ExchangeAuthorizationCode")
}

// RefreshAuthorization trades a refresh token for tokens. The returned tokens keep the old refresh token when the response omits one.
func RefreshAuthorization(authorizationServerUrl string, options *RefreshAuthorizationOptions) (*OAuthTokens, error) {
	panic("unported: RefreshAuthorization")
}

// StepUpScope merges scopes for a step-up authorization: the challenged scopes plus the ones granted
// so far, since a challenge may list only the missing scopes and a token with just those would lose
// access the old one had (SEP-2350). Without challenged scopes, nil lets the flow pick its default.
func StepUpScope(granted, challenged *string) *string {
	panic("unported: StepUpScope")
}

// AuthorizeMcp runs the OAuth flow until the provider has tokens or the user must be redirected.
func AuthorizeMcp(provider OAuthClientProvider, options *OAuthFlowOptions) (OAuthFlowResult, error) {
	panic("unported: AuthorizeMcp")
}

// AdaptOAuthProvider is an auth provider for StreamableHttpTransport. After a 401 it refreshes the
// tokens, or returns McpOAuthAuthorizationRequiredError when the user has to authorize (again).
// Concurrent 401s share one refresh, and a request whose token was already replaced is just retried:
// with rotating refresh tokens, a second refresh with the old refresh token would fail and discard
// the new grant.
func AdaptOAuthProvider(provider OAuthClientProvider) mcp.AuthProvider {
	panic("unported: AdaptOAuthProvider")
}

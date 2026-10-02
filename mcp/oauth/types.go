// Ported from packages/mcp/src/oauth/types.ts (pi v1.0.0).

package oauth

import (
	"net/url"

	"github.com/keejkrej/pi-go/internal/jsonx"
)

// OAuthProtectedResourceMetadata is a protected-resource metadata document.
// Field order is the TS interface order. Extra is not a JSON key of its own.
type OAuthProtectedResourceMetadata struct {
	Resource             string   `json:"resource"`
	AuthorizationServers []string `json:"authorization_servers,omitzero"`
	ScopesSupported      []string `json:"scopes_supported,omitzero"`
	// Extra is the TS index signature, excluding the known fields.
	Extra *jsonx.Object `json:"-"`
}

// AuthorizationServerMetadata is an authorization-server metadata document.
type AuthorizationServerMetadata struct {
	Issuer                            string   `json:"issuer"`
	AuthorizationEndpoint             string   `json:"authorization_endpoint"`
	TokenEndpoint                     string   `json:"token_endpoint"`
	RegistrationEndpoint              *string  `json:"registration_endpoint,omitzero"`
	ScopesSupported                   []string `json:"scopes_supported,omitzero"`
	ResponseTypesSupported            []string `json:"response_types_supported"`
	GrantTypesSupported               []string `json:"grant_types_supported,omitzero"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported,omitzero"`
	CodeChallengeMethodsSupported     []string `json:"code_challenge_methods_supported,omitzero"`
	ClientIdMetadataDocumentSupported *bool    `json:"client_id_metadata_document_supported,omitzero"`
	// AuthorizationResponseIssParameterSupported reports whether authorization responses carry an iss parameter (RFC 9207).
	AuthorizationResponseIssParameterSupported *bool `json:"authorization_response_iss_parameter_supported,omitzero"`
	// Extra is the TS index signature, excluding the known fields.
	Extra *jsonx.Object `json:"-"`
}

// OAuthTokens is a token-endpoint response.
type OAuthTokens struct {
	AccessToken  string   `json:"access_token"`
	TokenType    string   `json:"token_type"`
	ExpiresIn    *float64 `json:"expires_in,omitzero"`
	Scope        *string  `json:"scope,omitzero"`
	RefreshToken *string  `json:"refresh_token,omitzero"`
	IdToken      *string  `json:"id_token,omitzero"`
}

// OAuthClientMetadata is client metadata sent to dynamic registration.
type OAuthClientMetadata struct {
	RedirectUris            []string `json:"redirect_uris"`
	TokenEndpointAuthMethod *string  `json:"token_endpoint_auth_method,omitzero"`
	GrantTypes              []string `json:"grant_types,omitzero"`
	ResponseTypes           []string `json:"response_types,omitzero"`
	ClientName              *string  `json:"client_name,omitzero"`
	ClientUri               *string  `json:"client_uri,omitzero"`
	LogoUri                 *string  `json:"logo_uri,omitzero"`
	Scope                   *string  `json:"scope,omitzero"`
	Contacts                []string `json:"contacts,omitzero"`
	TosUri                  *string  `json:"tos_uri,omitzero"`
	PolicyUri               *string  `json:"policy_uri,omitzero"`
	JwksUri                 *string  `json:"jwks_uri,omitzero"`
	Jwks                    any      `json:"jwks,omitzero"`
	SoftwareId              *string  `json:"software_id,omitzero"`
	SoftwareVersion         *string  `json:"software_version,omitzero"`
	SoftwareStatement       *string  `json:"software_statement,omitzero"`
}

// OAuthClientInformation is a client id, with an optional secret.
type OAuthClientInformation struct {
	ClientId              string   `json:"client_id"`
	ClientSecret          *string  `json:"client_secret,omitzero"`
	ClientIdIssuedAt      *float64 `json:"client_id_issued_at,omitzero"`
	ClientSecretExpiresAt *float64 `json:"client_secret_expires_at,omitzero"`
}

// OAuthClientInformationFull is OAuthClientInformation plus OAuthClientMetadata.
type OAuthClientInformationFull struct {
	OAuthClientInformation
	OAuthClientMetadata
}

// OAuthClientInformationMixed is OAuthClientInformation or OAuthClientInformationFull.
// There is no discriminator; narrow with a type switch. "token_endpoint_auth_method" in
// the object is *OAuthClientInformationFull with TokenEndpointAuthMethod set.
type OAuthClientInformationMixed interface {
	isOAuthClientInformationMixed()
}

func (*OAuthClientInformation) isOAuthClientInformationMixed()     {}
func (*OAuthClientInformationFull) isOAuthClientInformationMixed() {}

var (
	_ OAuthClientInformationMixed = (*OAuthClientInformation)(nil)
	_ OAuthClientInformationMixed = (*OAuthClientInformationFull)(nil)
)

// OAuthDiscoveryState is the cached discovery result for one authorization server.
type OAuthDiscoveryState struct {
	AuthorizationServerUrl      string                          `json:"authorizationServerUrl"`
	AuthorizationServerMetadata *AuthorizationServerMetadata    `json:"authorizationServerMetadata,omitzero"`
	ResourceMetadata            *OAuthProtectedResourceMetadata `json:"resourceMetadata,omitzero"`
	ResourceMetadataUrl         *string                         `json:"resourceMetadataUrl,omitzero"`
}

// OAuthServerInfo is the discovered authorization server and optional resource metadata.
type OAuthServerInfo struct {
	AuthorizationServerUrl      string                          `json:"authorizationServerUrl"`
	AuthorizationServerMetadata *AuthorizationServerMetadata    `json:"authorizationServerMetadata,omitzero"`
	ResourceMetadata            *OAuthProtectedResourceMetadata `json:"resourceMetadata,omitzero"`
}

// OAuthChallenge is a parsed WWW-Authenticate bearer or DPoP challenge.
// ResourceMetadataUrl is nil when the parameter is absent or not a URL.
type OAuthChallenge struct {
	ResourceMetadataUrl *url.URL
	Scope               *string
	Error               *string
	ErrorDescription    *string
}

// ParseProtectedResourceMetadata validates a protected-resource metadata document.
func ParseProtectedResourceMetadata(value any) (*OAuthProtectedResourceMetadata, error) {
	panic("unported: ParseProtectedResourceMetadata")
}

// ParseAuthorizationServerMetadata validates an authorization-server metadata document.
func ParseAuthorizationServerMetadata(value any) (*AuthorizationServerMetadata, error) {
	panic("unported: ParseAuthorizationServerMetadata")
}

// ParseOAuthTokens validates a token-endpoint JSON body.
func ParseOAuthTokens(value any) (*OAuthTokens, error) {
	panic("unported: ParseOAuthTokens")
}

// ParseClientInformation validates a dynamic-client-registration response.
func ParseClientInformation(value any) (*OAuthClientInformationFull, error) {
	panic("unported: ParseClientInformation")
}

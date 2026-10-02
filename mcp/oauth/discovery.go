// Ported from packages/mcp/src/oauth/discovery.ts (pi v1.0.0).

package oauth

import (
	"net/http"
	"net/url"

	"github.com/keejkrej/pi-go/mcp"
)

// AuthorizationServerDiscoveryUrlType is the well-known metadata document kind.
type AuthorizationServerDiscoveryUrlType string

const (
	AuthorizationServerDiscoveryUrlTypeOauth AuthorizationServerDiscoveryUrlType = "oauth"
	AuthorizationServerDiscoveryUrlTypeOidc  AuthorizationServerDiscoveryUrlType = "oidc"
)

// AuthorizationServerDiscoveryUrl is one candidate metadata URL.
type AuthorizationServerDiscoveryUrl struct {
	Url  *url.URL
	Type AuthorizationServerDiscoveryUrlType
}

// DiscoverProtectedResourceMetadataOptions are the options of DiscoverProtectedResourceMetadata.
// Nil means the TS default {}.
type DiscoverProtectedResourceMetadataOptions struct {
	// ResourceMetadataUrl is a metadata URL. A string | URL in TS; pass the URL text.
	ResourceMetadataUrl *string
	ProtocolVersion     *string
	Fetch               mcp.McpFetch
	// HTTPClient is the Go-only HTTP client. Nil means the default client. Ignored when Fetch is set.
	HTTPClient *http.Client
}

// DiscoverAuthorizationServerMetadataOptions are the options of DiscoverAuthorizationServerMetadata.
type DiscoverAuthorizationServerMetadataOptions struct {
	Fetch                mcp.McpFetch
	ProtocolVersion      *string
	SkipIssuerValidation *bool
	// HTTPClient is the Go-only HTTP client. Nil means the default client. Ignored when Fetch is set.
	HTTPClient *http.Client
}

// DiscoverOAuthServerInfoOptions are the options of DiscoverOAuthServerInfo.
type DiscoverOAuthServerInfoOptions struct {
	ResourceMetadataUrl *url.URL
	// AuthorizationServerMetadataUrl is a metadata document to use instead of discovery.
	// It is trusted as configured, so its issuer is not checked.
	AuthorizationServerMetadataUrl *url.URL
	Fetch                          mcp.McpFetch
	SkipIssuerValidation           *bool
	// HTTPClient is the Go-only HTTP client. Nil means the default client. Ignored when Fetch is set.
	HTTPClient *http.Client
}

// ParseWwwAuthenticate parses one WWW-Authenticate header value. Nil means no header.
func ParseWwwAuthenticate(header *string) OAuthChallenge {
	panic("unported: ParseWwwAuthenticate")
}

// DiscoverProtectedResourceMetadata loads OAuth protected-resource metadata for an MCP server.
func DiscoverProtectedResourceMetadata(serverUrl string, options *DiscoverProtectedResourceMetadataOptions) (*OAuthProtectedResourceMetadata, error) {
	panic("unported: DiscoverProtectedResourceMetadata")
}

// BuildAuthorizationServerDiscoveryUrls lists the metadata documents tried for an issuer.
func BuildAuthorizationServerDiscoveryUrls(authorizationServerUrl string) ([]AuthorizationServerDiscoveryUrl, error) {
	panic("unported: BuildAuthorizationServerDiscoveryUrls")
}

// DiscoverAuthorizationServerMetadata loads authorization-server metadata.
// A nil metadata pointer and a nil error means every candidate was a discovery miss.
func DiscoverAuthorizationServerMetadata(authorizationServerUrl string, options *DiscoverAuthorizationServerMetadataOptions) (*AuthorizationServerMetadata, error) {
	panic("unported: DiscoverAuthorizationServerMetadata")
}

// DiscoverOAuthServerInfo discovers the authorization server for an MCP server URL.
func DiscoverOAuthServerInfo(serverUrl string, options *DiscoverOAuthServerInfoOptions) (*OAuthServerInfo, error) {
	panic("unported: DiscoverOAuthServerInfo")
}

// ResourceUrlFromServerUrl returns the canonical resource URL with the fragment removed.
func ResourceUrlFromServerUrl(value string) (*url.URL, error) {
	panic("unported: ResourceUrlFromServerUrl")
}

// SelectResource returns the protected-resource identifier to send, or nil when metadata is nil.
func SelectResource(serverUrl string, metadata *OAuthProtectedResourceMetadata) (*string, error) {
	panic("unported: SelectResource")
}

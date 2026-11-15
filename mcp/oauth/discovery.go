// Ported from packages/mcp/src/oauth/discovery.ts (pi v1.5.2).

package oauth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/mcp"
	"github.com/keejkrej/pi-go/mcp/protocol"
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

type discNetworkError struct {
	err error
}

func (e *discNetworkError) Error() string { return e.err.Error() }
func (e *discNetworkError) Unwrap() error { return e.err }

// ParseWwwAuthenticate parses one WWW-Authenticate header value. Nil means no header.
func ParseWwwAuthenticate(header *string) OAuthChallenge {
	if header == nil || *header == "" {
		return OAuthChallenge{}
	}
	trimmed := strings.TrimLeft(*header, " \t\r\n")
	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return OAuthChallenge{}
	}
	scheme := strings.ToLower(fields[0])
	if scheme != "bearer" && scheme != "dpop" {
		return OAuthChallenge{}
	}
	var rmUrl *url.URL
	if raw := discField(*header, "resource_metadata"); raw != nil {
		if u, err := discParseUrl(*raw); err == nil {
			rmUrl = u
		}
	}
	return OAuthChallenge{
		ResourceMetadataUrl: rmUrl,
		Scope:               discField(*header, "scope"),
		Error:               discField(*header, "error"),
		ErrorDescription:    discField(*header, "error_description"),
	}
}

// DiscoverProtectedResourceMetadata loads OAuth protected-resource metadata for an MCP server.
func DiscoverProtectedResourceMetadata(serverUrl string, options *DiscoverProtectedResourceMetadataOptions) (*OAuthProtectedResourceMetadata, error) {
	return discDiscoverProtectedResourceMetadata(context.Background(), serverUrl, options)
}

func discDiscoverProtectedResourceMetadata(ctx context.Context, serverUrl string, options *DiscoverProtectedResourceMetadataOptions) (*OAuthProtectedResourceMetadata, error) {
	var opts DiscoverProtectedResourceMetadataOptions
	if options != nil {
		opts = *options
	}
	server, err := discParseUrl(serverUrl)
	if err != nil {
		return nil, &discNetworkError{err: err}
	}
	fetch := typesHttpClientFetch(opts.Fetch, opts.HTTPClient)
	version := protocol.LatestProtocolVersion
	if opts.ProtocolVersion != nil && *opts.ProtocolVersion != "" {
		version = *opts.ProtocolVersion
	}

	var target string
	if opts.ResourceMetadataUrl != nil {
		targetUrl, err := discParseUrl(*opts.ResourceMetadataUrl)
		if err != nil {
			return nil, &discNetworkError{err: err}
		}
		target = targetUrl.String()
	} else {
		target = discWellKnownUrl(server, "/.well-known/oauth-protected-resource"+discPathSuffix(server.Path)).String()
	}

	resp, err := discFetchMetadata(ctx, target, fetch, version)
	if err != nil {
		return nil, &discNetworkError{err: err}
	}
	if opts.ResourceMetadataUrl == nil && server.Path != "/" && server.Path != "" && discIsDiscoveryMiss(resp.StatusCode) {
		discDiscard(resp)
		fallbackTarget := discWellKnownUrl(server, "/.well-known/oauth-protected-resource").String()
		resp, err = discFetchMetadata(ctx, fallbackTarget, fetch, version)
		if err != nil {
			return nil, &discNetworkError{err: err}
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		discDiscard(resp)
		return nil, fmt.Errorf("HTTP %d loading OAuth protected resource metadata", resp.StatusCode)
	}
	parsed, err := discReadJson(resp)
	if err != nil {
		return nil, err
	}
	return ParseProtectedResourceMetadata(parsed)
}

// BuildAuthorizationServerDiscoveryUrls lists the metadata documents tried for an issuer.
func BuildAuthorizationServerDiscoveryUrls(authorizationServerUrl string) ([]AuthorizationServerDiscoveryUrl, error) {
	issuer, err := discParseUrl(authorizationServerUrl)
	if err != nil {
		return nil, err
	}
	path := discPathSuffix(issuer.Path)
	urls := []AuthorizationServerDiscoveryUrl{
		{Url: discWellKnownUrl(issuer, "/.well-known/oauth-authorization-server"+path), Type: AuthorizationServerDiscoveryUrlTypeOauth},
		{Url: discWellKnownUrl(issuer, "/.well-known/openid-configuration"+path), Type: AuthorizationServerDiscoveryUrlTypeOidc},
	}
	if path != "" {
		urls = append(urls, AuthorizationServerDiscoveryUrl{
			Url:  discWellKnownUrl(issuer, path+"/.well-known/openid-configuration"),
			Type: AuthorizationServerDiscoveryUrlTypeOidc,
		})
	}
	return urls, nil
}

// DiscoverAuthorizationServerMetadata loads authorization-server metadata.
// A nil metadata pointer and a nil error means every candidate was a discovery miss.
func DiscoverAuthorizationServerMetadata(authorizationServerUrl string, options *DiscoverAuthorizationServerMetadataOptions) (*AuthorizationServerMetadata, error) {
	return discDiscoverAuthorizationServerMetadata(context.Background(), authorizationServerUrl, options)
}

func discDiscoverAuthorizationServerMetadata(ctx context.Context, authorizationServerUrl string, options *DiscoverAuthorizationServerMetadataOptions) (*AuthorizationServerMetadata, error) {
	var opts DiscoverAuthorizationServerMetadataOptions
	if options != nil {
		opts = *options
	}
	fetch := typesHttpClientFetch(opts.Fetch, opts.HTTPClient)
	version := protocol.LatestProtocolVersion
	if opts.ProtocolVersion != nil && *opts.ProtocolVersion != "" {
		version = *opts.ProtocolVersion
	}
	candidates, err := BuildAuthorizationServerDiscoveryUrls(authorizationServerUrl)
	if err != nil {
		return nil, err
	}
	for _, cand := range candidates {
		resp, err := discFetchMetadata(ctx, cand.Url.String(), fetch, version)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			discDiscard(resp)
			if discIsDiscoveryMiss(resp.StatusCode) {
				continue
			}
			return nil, fmt.Errorf("HTTP %d loading authorization server metadata from %s", resp.StatusCode, cand.Url.String())
		}
		parsed, err := discReadJson(resp)
		if err != nil {
			return nil, err
		}
		metadata, err := ParseAuthorizationServerMetadata(parsed)
		if err != nil {
			return nil, err
		}
		if opts.SkipIssuerValidation == nil || !*opts.SkipIssuerValidation {
			if discTrimTrailingSlash(metadata.Issuer) != discTrimTrailingSlash(authorizationServerUrl) {
				return nil, NewOAuthIssuerMismatchError(authorizationServerUrl, &metadata.Issuer)
			}
		}
		return metadata, nil
	}
	return nil, nil
}

// DiscoverOAuthServerInfo discovers the authorization server for an MCP server URL.
func DiscoverOAuthServerInfo(serverUrl string, options *DiscoverOAuthServerInfoOptions) (*OAuthServerInfo, error) {
	return discDiscoverOAuthServerInfo(context.Background(), serverUrl, options)
}

func discDiscoverOAuthServerInfo(ctx context.Context, serverUrl string, options *DiscoverOAuthServerInfoOptions) (*OAuthServerInfo, error) {
	var opts DiscoverOAuthServerInfoOptions
	if options != nil {
		opts = *options
	}
	var rmUrlPtr *string
	if opts.ResourceMetadataUrl != nil {
		s := opts.ResourceMetadataUrl.String()
		rmUrlPtr = &s
	}
	resourceMetadata, err := discDiscoverProtectedResourceMetadata(ctx, serverUrl, &DiscoverProtectedResourceMetadataOptions{
		ResourceMetadataUrl: rmUrlPtr,
		Fetch:               opts.Fetch,
		HTTPClient:          opts.HTTPClient,
	})
	if err != nil {
		var netErr *discNetworkError
		if errors.As(err, &netErr) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		resourceMetadata = nil
	}
	if opts.AuthorizationServerMetadataUrl != nil {
		target := opts.AuthorizationServerMetadataUrl.String()
		resp, err := discFetchMetadata(ctx, target, typesHttpClientFetch(opts.Fetch, opts.HTTPClient), protocol.LatestProtocolVersion)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			discDiscard(resp)
			return nil, fmt.Errorf("HTTP %d loading authorization server metadata from %s", resp.StatusCode, target)
		}
		parsed, err := discReadJson(resp)
		if err != nil {
			return nil, err
		}
		metadata, err := ParseAuthorizationServerMetadata(parsed)
		if err != nil {
			return nil, err
		}
		return &OAuthServerInfo{
			AuthorizationServerUrl:      metadata.Issuer,
			AuthorizationServerMetadata: metadata,
			ResourceMetadata:            resourceMetadata,
		}, nil
	}

	var authorizationServerUrl string
	if resourceMetadata != nil && len(resourceMetadata.AuthorizationServers) > 0 {
		authorizationServerUrl = resourceMetadata.AuthorizationServers[0]
	} else {
		server, err := discParseUrl(serverUrl)
		if err != nil {
			return nil, err
		}
		authorizationServerUrl = discOriginRootUrl(server).String()
	}
	asMetadata, err := discDiscoverAuthorizationServerMetadata(ctx, authorizationServerUrl, &DiscoverAuthorizationServerMetadataOptions{
		Fetch:                opts.Fetch,
		SkipIssuerValidation: opts.SkipIssuerValidation,
		HTTPClient:           opts.HTTPClient,
	})
	if err != nil {
		return nil, err
	}
	return &OAuthServerInfo{
		AuthorizationServerUrl:      authorizationServerUrl,
		AuthorizationServerMetadata: asMetadata,
		ResourceMetadata:            resourceMetadata,
	}, nil
}

// ResourceUrlFromServerUrl returns the canonical resource URL with the fragment removed.
func ResourceUrlFromServerUrl(value string) (*url.URL, error) {
	u, err := discParseUrl(value)
	if err != nil {
		return nil, err
	}
	u.Fragment = ""
	u.RawFragment = ""
	if u.Path == "" {
		u.Path = "/"
	}
	return u, nil
}

// SelectResource returns the protected-resource identifier to send, or nil when metadata is nil.
func SelectResource(serverUrl string, metadata *OAuthProtectedResourceMetadata) (*string, error) {
	if metadata == nil {
		return nil, nil
	}
	requested, err := ResourceUrlFromServerUrl(serverUrl)
	if err != nil {
		return nil, err
	}
	configured, err := ResourceUrlFromServerUrl(metadata.Resource)
	if err != nil {
		return nil, err
	}
	if discOrigin(requested) != discOrigin(configured) {
		return nil, fmt.Errorf("Protected resource %s does not match MCP server %s", metadata.Resource, requested.String())
	}
	requestedPath := requested.Path
	if !strings.HasSuffix(requestedPath, "/") {
		requestedPath += "/"
	}
	configuredPath := configured.Path
	if !strings.HasSuffix(configuredPath, "/") {
		configuredPath += "/"
	}
	if !strings.HasPrefix(requestedPath, configuredPath) {
		return nil, fmt.Errorf("Protected resource %s does not match MCP server %s", metadata.Resource, requested.String())
	}
	res := metadata.Resource
	return &res, nil
}

func discDiscard(resp *http.Response) {
	if resp != nil && resp.Body != nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
}

func discIsDiscoveryMiss(status int) bool {
	return (status >= 400 && status < 500) || status == 502
}

func discPathSuffix(pathname string) string {
	return strings.TrimSuffix(pathname, "/")
}

func discTrimTrailingSlash(value string) string {
	return strings.TrimSuffix(value, "/")
}

func discField(header, name string) *string {
	re := regexp.MustCompile(`(?i)(?:^|[,\s])` + regexp.QuoteMeta(name) + `=(?:"([^"]*)"|([^\s,]+))`)
	m := re.FindStringSubmatch(header)
	if len(m) == 0 {
		return nil
	}
	val := m[1]
	if val == "" {
		val = m[2]
	}
	if val == "" {
		return nil
	}
	return &val
}

func discFetchMetadata(ctx context.Context, target string, fetch mcp.McpFetch, protocolVersion string) (*http.Response, error) {
	hdrs := make(http.Header)
	hdrs.Set("Accept", "application/json")
	hdrs.Set("MCP-Protocol-Version", protocolVersion)
	return fetch(ctx, target, &mcp.McpRequestInit{Headers: hdrs})
}

func discReadJson(resp *http.Response) (any, error) {
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return jsonx.Parse(body)
}

func discParseUrl(raw string) (*url.URL, error) {
	if raw == "" || strings.ContainsAny(raw, "\r\n\t ") {
		return nil, fmt.Errorf("Invalid URL %q", raw)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("Invalid URL %q", raw)
	}
	u.Scheme = strings.ToLower(u.Scheme)
	return u, nil
}

func discOrigin(u *url.URL) string {
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port != "" {
		return scheme + "://" + host + ":" + port
	}
	return scheme + "://" + host
}

func discOriginRootUrl(u *url.URL) *url.URL {
	out, _ := url.Parse(discOrigin(u) + "/")
	return out
}

func discWellKnownUrl(base *url.URL, path string) *url.URL {
	out, _ := url.Parse(discOrigin(base) + path)
	return out
}

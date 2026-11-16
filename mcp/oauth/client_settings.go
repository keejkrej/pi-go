// Ported from packages/mcp/src/oauth/client-settings.ts (pi v1.5.2).

package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/keejkrej/pi-go/mcp"
)

const csOAuthRequestTimeout = 15 * time.Second

const (
	csCallbackHost = "127.0.0.1"
	csCallbackPath = "/callback"
)

// DefaultRedirectUrl is the redirect URI when settings name none and no client is stored.
const DefaultRedirectUrl = "http://" + csCallbackHost + csCallbackPath

// McpOAuthSettings are the client OAuth settings for one MCP server.
type McpOAuthSettings struct {
	ClientId              *string  `json:"clientId,omitzero"`
	ClientSecret          *string  `json:"clientSecret,omitzero"`
	CallbackPort          *int     `json:"callbackPort,omitzero"`
	CallbackUrl           *string  `json:"callbackUrl,omitzero"`
	Scope                 *string  `json:"scope,omitzero"`
	ClientName            *string  `json:"clientName,omitzero"`
	ClientRegistration    string   `json:"clientRegistration,omitzero"`
	AuthServerMetadataUrl *url.URL `json:"-"`
}

// McpClientMetadataDocuments describes the Client ID Metadata Documents an application publishes.
type McpClientMetadataDocuments struct {
	BaseUrl          string
	ListsRedirectUrl func(redirectUrl string) bool
}

// McpOAuthServerStore is the state store of one MCP server, with a lock keeping other
// processes from refreshing at the same time.
type McpOAuthServerStore interface {
	McpOAuthStateStore
	WithRefreshLock(fn func() error) error
}

// McpOAuthClientOptions configures the OAuth client of one MCP server.
type McpOAuthClientOptions struct {
	ServerUrl               string
	Store                   McpOAuthServerStore
	Settings                McpOAuthSettings
	ClientName              string
	ClientMetadataDocuments *McpClientMetadataDocuments
	HTTPClient              *http.Client
}

// LoopbackSettings describes where the loopback callback server listens and the redirect URI it serves.
type LoopbackSettings struct {
	Url              string
	Host             string
	RedirectHost     string
	Port             *int
	Path             string
	FixedRedirectUrl *string
}

// CreateOAuthClientProvider builds an McpOAuthProvider configured from client options.
func CreateOAuthClientProvider(
	options McpOAuthClientOptions,
	redirectUrl string,
	onRedirect func(u *url.URL) error,
) (*McpOAuthProvider, error) {
	settings := options.Settings
	documents := options.ClientMetadataDocuments
	clientName := options.ClientName
	if settings.ClientName != nil {
		clientName = *settings.ClientName
	}

	var docFn func(metadata *AuthorizationServerMetadata) (*OAuthClientMetadataDocument, error)
	if OffersClientMetadataDocument(settings, documents, redirectUrl) {
		docFn = func(metadata *AuthorizationServerMetadata) (*OAuthClientMetadataDocument, error) {
			if documents == nil || documents.ListsRedirectUrl == nil || !documents.ListsRedirectUrl(redirectUrl) {
				return nil, fmt.Errorf(
					`oauth.clientRegistration "cimd" needs Client ID Metadata Documents that list the redirect URI %s`,
					redirectUrl,
				)
			}
			doc, err := csClientMetadataDocument(options.ServerUrl, redirectUrl, metadata, documents.BaseUrl)
			if err != nil {
				return nil, err
			}
			if doc == nil && settings.ClientRegistration == "cimd" {
				return nil, fmt.Errorf(
					`The authorization server does not support Client ID Metadata Documents for public clients; remove oauth.clientRegistration "cimd"`,
				)
			}
			return doc, nil
		}
	}

	return NewMcpOAuthProvider(&McpOAuthProviderOptions{
		ServerUrl:              options.ServerUrl,
		RedirectUrl:            redirectUrl,
		ClientMetadata:         OAuthClientMetadata{ClientName: &clientName},
		ClientMetadataDocument: docFn,
		ClientId:               settings.ClientId,
		ClientSecret:           settings.ClientSecret,
		Store:                  options.Store,
		OnRedirect:             onRedirect,
	})
}

// OffersClientMetadataDocument reports whether the client may identify with a Client ID
// Metadata Document instead of registering dynamically.
func OffersClientMetadataDocument(
	settings McpOAuthSettings,
	documents *McpClientMetadataDocuments,
	redirectUrl string,
) bool {
	reg := settings.ClientRegistration
	if reg == "" {
		reg = "auto"
	}
	if reg != "auto" {
		return reg == "cimd"
	}
	hasClientId := settings.ClientId != nil && *settings.ClientId != ""
	hasClientName := settings.ClientName != nil && *settings.ClientName != ""
	lists := documents != nil && documents.ListsRedirectUrl != nil && documents.ListsRedirectUrl(redirectUrl)
	return !hasClientId && !hasClientName && lists
}

func csClientMetadataDocument(
	serverUrl string,
	redirectUrl string,
	metadata *AuthorizationServerMetadata,
	baseUrl string,
) (*OAuthClientMetadataDocument, error) {
	if metadata == nil ||
		metadata.ClientIdMetadataDocumentSupported == nil ||
		!*metadata.ClientIdMetadataDocumentSupported {
		return nil, nil
	}
	supportsNone := false
	for _, m := range metadata.TokenEndpointAuthMethodsSupported {
		if m == "none" {
			supportsNone = true
			break
		}
	}
	if !supportsNone {
		return nil, nil
	}
	if metadata.AuthorizationResponseIssParameterSupported != nil && *metadata.AuthorizationResponseIssParameterSupported {
		return &OAuthClientMetadataDocument{
			Url:         baseUrl + "/client.json",
			RedirectUrl: redirectUrl,
		}, nil
	}
	redirect, err := discParseUrl(redirectUrl)
	if err != nil {
		return nil, err
	}
	cbId, err := csCallbackId(serverUrl)
	if err != nil {
		return nil, err
	}
	redirect.Path = csServerSpecificPathWithId(redirect.Path, cbId)
	return &OAuthClientMetadataDocument{
		Url:         baseUrl + "/" + cbId + "/client.json",
		RedirectUrl: redirect.String(),
	}, nil
}

// ServerSpecificPath returns the path of the redirect URI for a server-specific
// Client ID Metadata Document: "<path>/<callback ID>".
func ServerSpecificPath(path, serverUrl string) (string, error) {
	id, err := csCallbackId(serverUrl)
	if err != nil {
		return "", err
	}
	return csServerSpecificPathWithId(path, id), nil
}

func csServerSpecificPathWithId(path, id string) string {
	return strings.TrimSuffix(path, "/") + "/" + id
}

func csCallbackId(serverUrl string) (string, error) {
	u, err := ResourceUrlFromServerUrl(serverUrl)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(u.String()))
	return base64.RawURLEncoding.EncodeToString(sum[:9]), nil
}

// RegisteredRedirectUrls returns the redirect URIs a stored client was registered with.
func RegisteredRedirectUrls(client OAuthClientInformationMixed) []string {
	if full, ok := client.(*OAuthClientInformationFull); ok && full != nil {
		return full.RedirectUris
	}
	return nil
}

// TimedFetch wraps fetch with a 15-second per-request timeout on top of the caller's context.
func TimedFetch(fetch mcp.McpFetch, client *http.Client) mcp.McpFetch {
	base := typesHttpClientFetch(fetch, client)
	return func(ctx context.Context, input string, init *mcp.McpRequestInit) (*http.Response, error) {
		if ctx == nil {
			ctx = context.Background()
		}
		tctx, cancel := context.WithTimeout(ctx, csOAuthRequestTimeout)
		resp, err := base(tctx, input, init)
		if err != nil {
			cancel()
			return nil, err
		}
		if resp.Body != nil {
			resp.Body = &csTimeoutReadCloser{ReadCloser: resp.Body, cancel: cancel}
		} else {
			cancel()
		}
		return resp, nil
	}
}

type csTimeoutReadCloser struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (r *csTimeoutReadCloser) Close() error {
	err := r.ReadCloser.Close()
	r.cancel()
	return err
}

// ResolveLoopbackSettings computes the loopback callback server address and redirect URI from settings.
func ResolveLoopbackSettings(settings McpOAuthSettings) (*LoopbackSettings, error) {
	configured := DefaultRedirectUrl
	if settings.CallbackUrl != nil && *settings.CallbackUrl != "" {
		configured = *settings.CallbackUrl
	}
	u, err := discParseUrl(configured)
	if err != nil {
		return nil, err
	}
	address := u.Hostname()
	portStr := u.Port()
	if u.Scheme == "http" && portStr == "80" {
		portStr = ""
	}
	var portPtr *int
	if portStr != "" {
		p, err := strconv.Atoi(portStr)
		if err != nil {
			return nil, err
		}
		portPtr = &p
	} else if settings.CallbackPort != nil {
		p := *settings.CallbackPort
		portPtr = &p
	}

	var fixedRedirectUrl *string
	if portStr != "" && settings.CallbackUrl != nil {
		s := *settings.CallbackUrl
		fixedRedirectUrl = &s
	} else if portPtr != nil {
		copyUrl := *u
		copyUrl.Host = net.JoinHostPort(address, strconv.Itoa(*portPtr))
		if copyUrl.Path == "" {
			copyUrl.Path = "/"
		}
		s := copyUrl.String()
		fixedRedirectUrl = &s
	}

	host := address
	if strings.EqualFold(host, "localhost") {
		host = csCallbackHost
	}
	path := u.Path
	if path == "" {
		path = "/"
	}

	return &LoopbackSettings{
		Url:              configured,
		Host:             host,
		RedirectHost:     address,
		Port:             portPtr,
		Path:             path,
		FixedRedirectUrl: fixedRedirectUrl,
	}, nil
}

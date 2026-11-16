// Ported from packages/mcp/src/oauth/auth-provider.ts (pi v1.5.2).

package oauth

import (
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/mcp"
)

const oauthapRefreshSkewMs = 30_000

// McpAuthProvider is an mcp.AuthProvider that can report when active token refreshes finish.
type McpAuthProvider interface {
	mcp.AuthProvider
	mcp.AuthProviderUnauthorized
	Settled()
}

// McpAuthProviderOptions configures CreateMcpAuthProvider.
type McpAuthProviderOptions struct {
	ServerUrl               string
	Store                   McpOAuthServerStore
	Settings                func() McpOAuthSettings
	ClientName              string
	ClientMetadataDocuments *McpClientMetadataDocuments
	HTTPClient              *http.Client
	OnChallenge             func(challenge OAuthChallenge)
	RedirectUrl             *string
}

// RefreshStoredTokensOptions configures RefreshStoredTokens.
type RefreshStoredTokensOptions struct {
	Fetch       mcp.McpFetch
	Challenge   *OAuthChallenge
	RedirectUrl *string
}

type oauthapRefreshCall struct {
	done chan struct{}
	err  error
}

type oauthapProvider struct {
	opts       McpAuthProviderOptions
	mu         sync.Mutex
	refreshing *oauthapRefreshCall
}

// CreateMcpAuthProvider builds the auth provider for StreamableHttpTransport connections
// to OAuth-protected MCP servers: it sends the stored access token and refreshes it before expiry
// or on 401, failing with McpOAuthAuthorizationRequiredError when user sign-in is needed.
func CreateMcpAuthProvider(options McpAuthProviderOptions) McpAuthProvider {
	return &oauthapProvider{opts: options}
}

func (p *oauthapProvider) Settled() {
	p.mu.Lock()
	call := p.refreshing
	p.mu.Unlock()
	if call != nil {
		<-call.done
	}
}

func (p *oauthapProvider) oauthapRefresh(staleToken *string, fetch mcp.McpFetch, challenge *OAuthChallenge) error {
	p.mu.Lock()
	if p.refreshing != nil {
		call := p.refreshing
		p.mu.Unlock()
		<-call.done
		return call.err
	}
	call := &oauthapRefreshCall{done: make(chan struct{})}
	p.refreshing = call
	p.mu.Unlock()

	var err error
	defer func() {
		call.err = err
		close(call.done)
		p.mu.Lock()
		if p.refreshing == call {
			p.refreshing = nil
		}
		p.mu.Unlock()
	}()

	err = p.opts.Store.WithRefreshLock(func() error {
		state, loadErr := p.opts.Store.Load()
		if loadErr != nil {
			return loadErr
		}
		var currentToken *string
		if state != nil && state.Tokens != nil {
			t := state.Tokens.AccessToken
			currentToken = &t
		}
		if !flowPtrStringEqual(currentToken, staleToken) {
			return nil
		}
		if state == nil || state.Tokens == nil || state.Tokens.RefreshToken == nil || *state.Tokens.RefreshToken == "" {
			return NewMcpOAuthAuthorizationRequiredError()
		}
		client := McpOAuthClientOptions{
			ServerUrl:               p.opts.ServerUrl,
			Store:                   p.opts.Store,
			Settings:                p.opts.Settings(),
			ClientName:              p.opts.ClientName,
			ClientMetadataDocuments: p.opts.ClientMetadataDocuments,
			HTTPClient:              p.opts.HTTPClient,
		}
		res, refErr := RefreshStoredTokens(client, state, &RefreshStoredTokensOptions{
			Fetch:       fetch,
			Challenge:   challenge,
			RedirectUrl: p.opts.RedirectUrl,
		})
		if refErr != nil {
			return refErr
		}
		if res == OAuthFlowResultRedirect {
			return NewMcpOAuthAuthorizationRequiredError()
		}
		return nil
	})
	return err
}

func (p *oauthapProvider) Token() (*string, error) {
	p.Settled()
	state, err := p.opts.Store.Load()
	if err != nil {
		return nil, err
	}
	var token *string
	if state != nil && state.Tokens != nil {
		t := state.Tokens.AccessToken
		token = &t
	}
	expired := state != nil && state.TokensExpireAt != nil &&
		*state.TokensExpireAt-oauthapRefreshSkewMs <= time.Now().UnixMilli()
	if !expired || state == nil || state.Tokens == nil || state.Tokens.RefreshToken == nil || *state.Tokens.RefreshToken == "" {
		return token, nil
	}
	_ = p.oauthapRefresh(token, nil, nil)
	latest, err := p.opts.Store.Load()
	if err != nil {
		return nil, err
	}
	if latest != nil && latest.Tokens != nil {
		t := latest.Tokens.AccessToken
		return &t, nil
	}
	return nil, nil
}

func (p *oauthapProvider) OnUnauthorized(unauth *mcp.UnauthorizedContext) error {
	var header *string
	if unauth != nil && unauth.Response != nil {
		if v := unauth.Response.Header.Get("WWW-Authenticate"); v != "" {
			header = &v
		}
	}
	challenge := ParseWwwAuthenticate(header)
	if p.opts.OnChallenge != nil {
		p.opts.OnChallenge(challenge)
	}
	if challenge.Error != nil && *challenge.Error == "insufficient_scope" {
		return NewMcpOAuthAuthorizationRequiredError()
	}
	var fetch mcp.McpFetch
	var stale *string
	if unauth != nil {
		fetch = unauth.Fetch
		stale = unauth.Token
	}
	return p.oauthapRefresh(stale, fetch, &challenge)
}

// RefreshStoredTokens refreshes state's tokens with the stored client or the Client ID Metadata
// Document it signed in with, or returns OAuthFlowResultRedirect when that is not possible.
func RefreshStoredTokens(client McpOAuthClientOptions, state *McpOAuthState, options *RefreshStoredTokensOptions) (OAuthFlowResult, error) {
	var opts RefreshStoredTokensOptions
	if options != nil {
		opts = *options
	}
	var candidates []string
	if opts.RedirectUrl != nil && *opts.RedirectUrl != "" {
		candidates = append(candidates, *opts.RedirectUrl)
	}
	if lb, err := ResolveLoopbackSettings(client.Settings); err == nil && lb.FixedRedirectUrl != nil && *lb.FixedRedirectUrl != "" {
		candidates = append(candidates, *lb.FixedRedirectUrl)
	}
	if state != nil {
		if reg := RegisteredRedirectUrls(state.ClientInformation); len(reg) > 0 && reg[0] != "" {
			candidates = append(candidates, reg[0])
		}
	}

	redirectUrl := DefaultRedirectUrl
	if client.ClientMetadataDocuments != nil && client.ClientMetadataDocuments.ListsRedirectUrl != nil {
		for _, cand := range candidates {
			if client.ClientMetadataDocuments.ListsRedirectUrl(cand) {
				redirectUrl = cand
				break
			}
		}
	}

	provider, err := CreateOAuthClientProvider(client, redirectUrl, nil)
	if err != nil {
		return "", err
	}
	var rmUrl *url.URL
	if opts.Challenge != nil {
		rmUrl = opts.Challenge.ResourceMetadataUrl
	}
	refreshOnly := true
	return AuthorizeMcp(provider, &OAuthFlowOptions{
		ServerUrl:                      client.ServerUrl,
		ResourceMetadataUrl:            rmUrl,
		AuthorizationServerMetadataUrl: client.Settings.AuthServerMetadataUrl,
		Fetch:                          TimedFetch(opts.Fetch, client.HTTPClient),
		RefreshOnly:                    &refreshOnly,
		HTTPClient:                     client.HTTPClient,
	})
}

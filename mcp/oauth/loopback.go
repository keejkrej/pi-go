// Ported from packages/mcp/src/oauth/loopback.ts (pi v1.5.2).

package oauth

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// McpLoopbackCallback is a running loopback callback server and the redirect URI it serves.
type McpLoopbackCallback struct {
	Callback    *OAuthCallbackServer
	RedirectUrl string
}

// ListenForLoopbackSignInOptions configures ListenForLoopbackSignIn.
type ListenForLoopbackSignInOptions struct {
	ServerUrl               string
	Store                   McpOAuthStateStore
	Settings                McpOAuthSettings
	ClientMetadataDocuments *McpClientMetadataDocuments
	RenderPage              func(page OAuthCallbackPage) string
}

// WaitForLoopbackSignInOptions configures WaitForLoopbackSignIn.
type WaitForLoopbackSignInOptions struct {
	Callback             *OAuthCallbackServer
	Authorization        McpSignInRedirect
	PromptForRedirectUrl func(ctx context.Context) (*string, error)
	Context              context.Context
}

// ListenForLoopbackSignIn listens for the authorization response of a sign-in on loopback.
// Close Callback when the sign-in ends.
func ListenForLoopbackSignIn(options ListenForLoopbackSignInOptions) (*McpLoopbackCallback, error) {
	loopback, err := ResolveLoopbackSettings(options.Settings)
	if err != nil {
		return nil, err
	}
	state, err := options.Store.Load()
	if err != nil {
		return nil, err
	}

	port := loopback.Port
	if port == nil && state != nil {
		if reg := RegisteredRedirectUrls(state.ClientInformation); len(reg) > 0 && reg[0] != "" {
			if u, parseErr := discParseUrl(reg[0]); parseErr == nil {
				if pStr := u.Port(); pStr != "" {
					if p, convErr := strconv.Atoi(pStr); convErr == nil && p > 0 {
						port = &p
					}
				}
			}
		}
	}

	var extraPaths []string
	if OffersClientMetadataDocument(options.Settings, options.ClientMetadataDocuments, loopback.Url) {
		sp, err := ServerSpecificPath(loopback.Path, options.ServerUrl)
		if err != nil {
			return nil, err
		}
		extraPaths = []string{sp}
	}

	listenOpts := OAuthCallbackServerOptions{
		Host:         &loopback.Host,
		RedirectHost: &loopback.RedirectHost,
		Port:         port,
		Path:         &loopback.Path,
		ExtraPaths:   extraPaths,
		RenderPage:   options.RenderPage,
	}
	callback, err := OAuthCallbackServerListen(&listenOpts)
	if err != nil {
		if loopback.Port != nil || port == nil {
			return nil, err
		}
		listenOpts.Port = nil
		callback, err = OAuthCallbackServerListen(&listenOpts)
		if err != nil {
			return nil, err
		}
	}

	redirectUrl := callback.RedirectUrl
	if loopback.FixedRedirectUrl != nil {
		redirectUrl = *loopback.FixedRedirectUrl
	}
	return &McpLoopbackCallback{
		Callback:    callback,
		RedirectUrl: redirectUrl,
	}, nil
}

// WaitForLoopbackSignIn waits for the authorization response on the loopback callback server
// or for a redirect URL pasted by the user, whichever arrives first.
func WaitForLoopbackSignIn(options WaitForLoopbackSignInOptions) (*McpAuthorizationResponse, error) {
	parentCtx := options.Context
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	if parentCtx.Err() != nil {
		return nil, NewMcpSignInCancelledError()
	}

	authUrl, err := discParseUrl(options.Authorization.RedirectUrl)
	if err != nil {
		return nil, err
	}
	authPath := authUrl.Path
	if authPath == "" {
		authPath = "/"
	}

	childCtx, cancel := context.WithCancel(parentCtx)
	defer cancel()

	type raceResult struct {
		resp *McpAuthorizationResponse
		err  error
	}
	resCh := make(chan raceResult, 2)

	go func() {
		cb, waitErr := options.Callback.WaitForCallbackOnPath(options.Authorization.State, authPath)
		resCh <- raceResult{resp: cb, err: waitErr}
	}()

	if options.PromptForRedirectUrl != nil {
		go func() {
			input, promptErr := options.PromptForRedirectUrl(childCtx)
			if promptErr != nil {
				resCh <- raceResult{err: promptErr}
				return
			}
			if input == nil || strings.TrimSpace(*input) == "" {
				resCh <- raceResult{err: NewMcpSignInCancelledError()}
				return
			}
			resp, respErr := ResponseFromRedirectUrl(*input, options.Authorization)
			resCh <- raceResult{resp: resp, err: respErr}
		}()
	}

	select {
	case <-parentCtx.Done():
		return nil, NewMcpSignInCancelledError()
	case r := <-resCh:
		if parentCtx.Err() != nil {
			return nil, NewMcpSignInCancelledError()
		}
		return r.resp, r.err
	}
}

// ResponseFromRedirectUrl extracts the authorization response from a pasted browser redirect URL.
func ResponseFromRedirectUrl(input string, authorization McpSignInRedirect) (*McpAuthorizationResponse, error) {
	parsed, err := discParseUrl(strings.TrimSpace(input))
	if err != nil {
		return nil, fmt.Errorf("Expected the full redirect URL from the browser address bar")
	}
	redirectUrl, err := discParseUrl(authorization.RedirectUrl)
	if err != nil {
		return nil, err
	}
	parsedPath := parsed.Path
	if parsedPath == "" {
		parsedPath = "/"
	}
	redirectPath := redirectUrl.Path
	if redirectPath == "" {
		redirectPath = "/"
	}
	if discOrigin(parsed) != discOrigin(redirectUrl) || parsedPath != redirectPath {
		return nil, fmt.Errorf("The redirect URL does not match this sign-in's redirect URI")
	}
	q := parsed.Query()
	if errParam := q.Get("error"); errParam != "" {
		desc := q.Get("error_description")
		if desc == "" {
			desc = errParam
		}
		return nil, fmt.Errorf("%s", desc)
	}
	state := q.Get("state")
	if state != authorization.State {
		return nil, fmt.Errorf("The redirect URL belongs to a different sign-in")
	}
	code := q.Get("code")
	if code == "" {
		return nil, fmt.Errorf("The redirect URL does not contain an authorization code")
	}
	var issPtr *string
	if iss := q.Get("iss"); iss != "" {
		issPtr = &iss
	}
	return &McpAuthorizationResponse{
		Code:  code,
		State: state,
		Iss:   issPtr,
	}, nil
}

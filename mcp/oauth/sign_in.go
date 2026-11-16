// Ported from packages/mcp/src/oauth/sign-in.ts (pi v1.5.2).

package oauth

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
)

// McpSignInOptions configures both halves of an MCP OAuth sign-in.
type McpSignInOptions struct {
	McpOAuthClientOptions
	RedirectUrl string
	Challenge   *OAuthChallenge
	Context     context.Context
}

// McpSignInRedirect describes the browser step returned by StartMcpSignIn.
type McpSignInRedirect struct {
	Kind             string  `json:"kind"`
	AuthorizationUrl string  `json:"authorizationUrl"`
	RedirectUrl      string  `json:"redirectUrl"`
	State            string  `json:"state"`
	Scope            *string `json:"scope,omitzero"`
}

// McpSignInStart is the outcome of StartMcpSignIn: Kind is "authorized" or "redirect".
type McpSignInStart struct {
	Kind             string  `json:"kind"`
	AuthorizationUrl string  `json:"authorizationUrl,omitzero"`
	RedirectUrl      string  `json:"redirectUrl,omitzero"`
	State            string  `json:"state,omitzero"`
	Scope            *string `json:"scope,omitzero"`
}

// Redirect returns the McpSignInRedirect payload when Kind == "redirect".
func (s *McpSignInStart) Redirect() McpSignInRedirect {
	return McpSignInRedirect{
		Kind:             s.Kind,
		AuthorizationUrl: s.AuthorizationUrl,
		RedirectUrl:      s.RedirectUrl,
		State:            s.State,
		Scope:            s.Scope,
	}
}

// McpAuthorizationResponse is an authorization response from a callback server,
// callback route, or ResponseFromRedirectUrl.
type McpAuthorizationResponse = OAuthCallback

// CompleteMcpSignInOptions configures CompleteMcpSignIn.
type CompleteMcpSignInOptions struct {
	McpSignInOptions
	Authorization McpSignInRedirect
	Response      McpAuthorizationResponse
}

// McpSignInCancelledError means the user or application cancelled a sign-in.
type McpSignInCancelledError struct {
	Message string
}

// NewMcpSignInCancelledError returns the sign-in cancelled error.
func NewMcpSignInCancelledError() *McpSignInCancelledError {
	return &McpSignInCancelledError{Message: "Sign-in cancelled"}
}

func (e *McpSignInCancelledError) Error() string { return e.Message }

// Name returns the TS error name.
func (e *McpSignInCancelledError) Name() string { return "McpSignInCancelledError" }

// StartMcpSignIn runs the first half of a sign-in: tries to refresh stored tokens
// unless stepping up scope, or prepares the browser authorization URL.
func StartMcpSignIn(options McpSignInOptions) (*McpSignInStart, error) {
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return nil, NewMcpSignInCancelledError()
	}
	stepUp := options.Challenge != nil && options.Challenge.Error != nil && *options.Challenge.Error == "insufficient_scope"
	if !stepUp {
		res, err := signinRefreshBeforeSignIn(ctx, options)
		if err != nil {
			return nil, err
		}
		if res == OAuthFlowResultAuthorized {
			return &McpSignInStart{Kind: "authorized"}, nil
		}
	}
	return signinCancellable(ctx, func() (*McpSignInStart, error) {
		red, err := signinStartBrowserStep(ctx, options, stepUp)
		if err != nil {
			return nil, err
		}
		return &McpSignInStart{
			Kind:             red.Kind,
			AuthorizationUrl: red.AuthorizationUrl,
			RedirectUrl:      red.RedirectUrl,
			State:            red.State,
			Scope:            red.Scope,
		}, nil
	})
}

func signinRefreshBeforeSignIn(ctx context.Context, options McpSignInOptions) (OAuthFlowResult, error) {
	type lockResult struct {
		flowRes OAuthFlowResult
		err     error
	}
	ch := make(chan lockResult, 1)
	go func() {
		var flowRes OAuthFlowResult
		err := options.Store.WithRefreshLock(func() error {
			if ctx.Err() != nil {
				flowRes = OAuthFlowResultRedirect
				return nil
			}
			state, loadErr := options.Store.Load()
			if loadErr != nil {
				return loadErr
			}
			if state == nil || state.Tokens == nil || state.Tokens.RefreshToken == nil || *state.Tokens.RefreshToken == "" {
				flowRes = OAuthFlowResultRedirect
				return nil
			}
			hasClientId := options.Settings.ClientId != nil && *options.Settings.ClientId != ""
			if options.Settings.ClientRegistration == "cimd" && state.ClientInformation != nil && !hasClientId {
				flowRes = OAuthFlowResultRedirect
				return nil
			}
			var rerr error
			flowRes, rerr = RefreshStoredTokens(options.McpOAuthClientOptions, state, &RefreshStoredTokensOptions{
				Challenge:   options.Challenge,
				RedirectUrl: &options.RedirectUrl,
			})
			return rerr
		})
		ch <- lockResult{flowRes: flowRes, err: err}
	}()

	select {
	case <-ctx.Done():
		return "", NewMcpSignInCancelledError()
	case r := <-ch:
		if r.err != nil {
			var busy *McpRefreshLockBusyError
			if errors.As(r.err, &busy) {
				return OAuthFlowResultRedirect, nil
			}
			return "", r.err
		}
		return r.flowRes, nil
	}
}

func signinStartBrowserStep(ctx context.Context, options McpSignInOptions, stepUp bool) (*McpSignInRedirect, error) {
	stored, err := options.Store.Load()
	if err != nil {
		return nil, err
	}
	if stored != nil {
		if err := options.Store.Save(signinStateForSignIn(stored, options)); err != nil {
			return nil, err
		}
	}

	var authorizationUrl *url.URL
	provider, err := CreateOAuthClientProvider(options.McpOAuthClientOptions, options.RedirectUrl, func(u *url.URL) error {
		authorizationUrl = u
		return nil
	})
	if err != nil {
		return nil, err
	}

	var grantedScope *string
	if stored != nil && stored.Tokens != nil {
		grantedScope = stored.Tokens.Scope
	}
	var challengeScope *string
	if options.Challenge != nil {
		challengeScope = options.Challenge.Scope
	}
	var scopeArg *string
	if stepUp {
		scopeArg = StepUpScope(grantedScope, challengeScope)
	} else {
		scopeArg = challengeScope
	}
	scope := signinMergeScopes(options.Settings.Scope, scopeArg)

	flowOpts := signinFlowOptions(ctx, options)
	flowOpts.Scope = scope
	skipRefresh := true
	flowOpts.SkipRefresh = &skipRefresh

	if _, err := AuthorizeMcp(provider, &flowOpts); err != nil {
		return nil, err
	}
	if authorizationUrl == nil {
		return nil, fmt.Errorf("OAuth flow did not produce an authorization URL")
	}

	var requestedScope *string
	if rs := authorizationUrl.Query().Get("scope"); rs != "" {
		requestedScope = &rs
	}
	redirectUri := authorizationUrl.Query().Get("redirect_uri")
	if redirectUri == "" {
		redirectUri = options.RedirectUrl
	}
	state, err := provider.State()
	if err != nil {
		return nil, err
	}

	return &McpSignInRedirect{
		Kind:             "redirect",
		AuthorizationUrl: authorizationUrl.String(),
		RedirectUrl:      redirectUri,
		State:            state,
		Scope:            requestedScope,
	}, nil
}

func signinStateForSignIn(stored *McpOAuthState, options McpSignInOptions) *McpOAuthState {
	next := provCloneState(stored)
	next.OauthState = nil

	settings := options.Settings
	hasClientId := settings.ClientId != nil && *settings.ClientId != ""
	var keepClient bool
	if hasClientId {
		keepClient = true
	} else if stored.ClientInformation != nil {
		keepClient = settings.ClientRegistration != "cimd" &&
			slices.Contains(RegisteredRedirectUrls(stored.ClientInformation), options.RedirectUrl)
	} else {
		keepClient = OffersClientMetadataDocument(settings, options.ClientMetadataDocuments, options.RedirectUrl)
	}
	if !keepClient {
		next.ClientInformation = nil
		next.Tokens = nil
		next.TokensExpireAt = nil
	}
	return next
}

func signinMergeScopes(scopes ...*string) *string {
	seen := make(map[string]struct{})
	var merged []string
	for _, s := range scopes {
		if s == nil || *s == "" {
			continue
		}
		for _, tok := range strings.Fields(*s) {
			if _, ok := seen[tok]; ok {
				continue
			}
			seen[tok] = struct{}{}
			merged = append(merged, tok)
		}
	}
	if len(merged) == 0 {
		return nil
	}
	joined := strings.Join(merged, " ")
	return &joined
}

// CompleteMcpSignIn exchanges the authorization code from response and saves the tokens.
func CompleteMcpSignIn(options CompleteMcpSignInOptions) error {
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	_, err := signinCancellable(ctx, func() (struct{}, error) {
		if options.Response.State != options.Authorization.State {
			return struct{}{}, fmt.Errorf("The authorization response belongs to a different sign-in")
		}
		loaded, err := options.Store.Load()
		if err != nil {
			return struct{}{}, err
		}
		if loaded == nil || loaded.OauthState == nil || *loaded.OauthState != options.Authorization.State {
			return struct{}{}, fmt.Errorf("Another sign-in to this MCP server started since; sign in again")
		}
		provider, err := CreateOAuthClientProvider(options.McpOAuthClientOptions, options.RedirectUrl, nil)
		if err != nil {
			return struct{}{}, err
		}
		flowOpts := signinFlowOptions(ctx, options.McpSignInOptions)
		flowOpts.Scope = options.Authorization.Scope
		flowOpts.AuthorizationRedirectUrl = &options.Authorization.RedirectUrl
		flowOpts.AuthorizationCode = &options.Response.Code
		flowOpts.Iss = options.Response.Iss
		_, err = AuthorizeMcp(provider, &flowOpts)
		return struct{}{}, err
	})
	return err
}

func signinFlowOptions(ctx context.Context, options McpSignInOptions) OAuthFlowOptions {
	var rmUrl *url.URL
	if options.Challenge != nil {
		rmUrl = options.Challenge.ResourceMetadataUrl
	}
	return OAuthFlowOptions{
		ServerUrl:                      options.ServerUrl,
		Fetch:                          TimedFetch(nil, options.HTTPClient),
		Context:                        ctx,
		ResourceMetadataUrl:            rmUrl,
		AuthorizationServerMetadataUrl: options.Settings.AuthServerMetadataUrl,
		HTTPClient:                     options.HTTPClient,
	}
}

func signinCancellable[T any](ctx context.Context, fn func() (T, error)) (T, error) {
	var zero T
	if ctx != nil && ctx.Err() != nil {
		return zero, NewMcpSignInCancelledError()
	}
	v, err := fn()
	if err != nil {
		if ctx != nil && ctx.Err() != nil {
			return zero, NewMcpSignInCancelledError()
		}
		return zero, err
	}
	return v, nil
}

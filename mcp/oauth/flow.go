// Ported from packages/mcp/src/oauth/flow.ts (pi v1.5.2).

package oauth

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"

	aioauth "github.com/keejkrej/pi-go/ai/oauth"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/mcp"
)

// AddClientAuthentication sets client authentication on a token request.
// headers and params are mutated. rawUrl is the token endpoint (TS string | URL).
type AddClientAuthentication func(headers http.Header, params url.Values, rawUrl string, metadata *AuthorizationServerMetadata) error

// OAuthClientMetadataDocument is a Client ID Metadata Document: an https URL used as
// client_id and a redirect URI that the document lists.
type OAuthClientMetadataDocument struct {
	Url         string `json:"url"`
	RedirectUrl string `json:"redirectUrl"`
}

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

// OAuthClientProviderClientMetadataDocument is the optional clientMetadataDocument method.
type OAuthClientProviderClientMetadataDocument interface {
	ClientMetadataDocument(metadata *AuthorizationServerMetadata) (*OAuthClientMetadataDocument, error)
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
	// Context cancels or bounds every HTTP request of the flow. Nil means context.Background().
	Context              context.Context
	SkipIssuerValidation *bool
	// SkipRefresh goes straight to the authorization redirect instead of refreshing stored tokens,
	// for example when the server asks for scopes the current grant lacks (a refresh keeps the old scope).
	SkipRefresh *bool
	// RefreshOnly only refreshes stored tokens: returns REDIRECT instead of registering a client or
	// preparing an authorization request, so a pending authorization (its state and verifier) stays usable.
	RefreshOnly *bool
	// AuthorizationRedirectUrl is the redirect URI the authorization request used when exchanging
	// AuthorizationCode. The exchange fails before sending the code if another URI would be used.
	AuthorizationRedirectUrl *string
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
	// Context cancels or bounds the request. Nil means context.Background().
	Context context.Context
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
	// Context cancels or bounds the request. Nil means context.Background().
	Context context.Context
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

type flowClientAuthMethod string

const (
	flowAuthSecretBasic flowClientAuthMethod = "client_secret_basic"
	flowAuthSecretPost  flowClientAuthMethod = "client_secret_post"
	flowAuthNone        flowClientAuthMethod = "none"
)

type flowUse struct {
	storedClientId *string
}

// StartAuthorization builds the authorization URL and a PKCE verifier.
func StartAuthorization(authorizationServerUrl string, options *StartAuthorizationOptions) (authorizationUrl *url.URL, codeVerifier string, err error) {
	if options == nil {
		return nil, "", fmt.Errorf("StartAuthorization options are required")
	}
	metadata := options.Metadata
	if metadata != nil {
		if !slices.Contains(metadata.ResponseTypesSupported, "code") {
			return nil, "", fmt.Errorf("Authorization server does not support authorization codes")
		}
		if metadata.CodeChallengeMethodsSupported != nil && !slices.Contains(metadata.CodeChallengeMethodsSupported, "S256") {
			return nil, "", fmt.Errorf("Authorization server does not support PKCE S256")
		}
	}

	var authUrl *url.URL
	if metadata != nil && metadata.AuthorizationEndpoint != "" {
		authUrl, err = url.Parse(metadata.AuthorizationEndpoint)
	} else {
		authUrl, err = flowResolvePath(authorizationServerUrl, "/authorize")
	}
	if err != nil {
		return nil, "", err
	}
	scheme := strings.ToLower(authUrl.Scheme)
	if (scheme != "http" && scheme != "https") || authUrl.Host == "" {
		return nil, "", fmt.Errorf("Invalid authorization endpoint %s", authUrl.String())
	}

	pkcePair, err := aioauth.GeneratePKCE()
	if err != nil {
		return nil, "", err
	}
	info := flowBaseClientInfo(options.ClientInformation)
	if info == nil {
		return nil, "", fmt.Errorf("ClientInformation is required")
	}

	q := authUrl.Query()
	q.Set("response_type", "code")
	q.Set("client_id", info.ClientId)
	q.Set("code_challenge", pkcePair.Challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("redirect_uri", options.RedirectUrl)
	if options.State != nil && *options.State != "" {
		q.Set("state", *options.State)
	}
	if options.Scope != nil && *options.Scope != "" {
		q.Set("scope", *options.Scope)
		if slices.Contains(strings.Fields(*options.Scope), "offline_access") {
			q.Set("prompt", "consent")
		}
	}
	if options.Resource != nil && *options.Resource != "" {
		q.Set("resource", *options.Resource)
	}
	authUrl.RawQuery = q.Encode()
	return authUrl, pkcePair.Verifier, nil
}

// RegisterClient registers a public or confidential client at the authorization server.
func RegisterClient(authorizationServerUrl string, options *RegisterClientOptions) (*OAuthClientInformationFull, error) {
	if options == nil {
		return nil, fmt.Errorf("RegisterClient options are required")
	}
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	var endpoint string
	if options.Metadata != nil {
		if options.Metadata.RegistrationEndpoint == nil || *options.Metadata.RegistrationEndpoint == "" {
			return nil, fmt.Errorf("Authorization server does not support dynamic client registration")
		}
		endpoint = *options.Metadata.RegistrationEndpoint
	} else {
		u, err := flowResolvePath(authorizationServerUrl, "/register")
		if err != nil {
			return nil, err
		}
		endpoint = u.String()
	}

	encodedMeta, err := jsonx.Encode(options.ClientMetadata)
	if err != nil {
		return nil, err
	}
	bodyObj, ok := encodedMeta.(*jsonx.Object)
	if !ok {
		return nil, fmt.Errorf("Invalid client metadata")
	}
	if appTypeVal, hasAppType := bodyObj.Get("application_type"); !hasAppType || appTypeVal == nil || appTypeVal == "" {
		bodyObj.Set("application_type", flowApplicationType(options.ClientMetadata.RedirectUris))
	}
	if options.Scope != nil && *options.Scope != "" {
		bodyObj.Set("scope", *options.Scope)
	}
	bodyBytes, err := jsonx.Marshal(bodyObj)
	if err != nil {
		return nil, err
	}

	hdrs := make(http.Header)
	hdrs.Set("Accept", "application/json")
	hdrs.Set("Content-Type", "application/json")
	fetch := typesHttpClientFetch(options.Fetch, options.HTTPClient)
	resp, err := fetch(ctx, endpoint, &mcp.McpRequestInit{
		Method:  http.MethodPost,
		Headers: hdrs,
		Body:    bodyBytes,
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, NewOAuthRegistrationError(resp.StatusCode, string(respBytes))
	}
	parsed, err := jsonx.Parse(respBytes)
	if err != nil {
		return nil, err
	}
	return ParseClientInformation(parsed)
}

// ExchangeAuthorizationCode trades an authorization code for tokens.
func ExchangeAuthorizationCode(authorizationServerUrl string, options *ExchangeAuthorizationCodeOptions) (*OAuthTokens, error) {
	if options == nil {
		return nil, fmt.Errorf("ExchangeAuthorizationCode options are required")
	}
	params := make(url.Values)
	params.Set("grant_type", "authorization_code")
	params.Set("code", options.Code)
	params.Set("code_verifier", options.CodeVerifier)
	params.Set("redirect_uri", options.RedirectUrl)
	return flowTokenRequest(authorizationServerUrl, &options.TokenRequestOptions, params)
}

// RefreshAuthorization trades a refresh token for tokens. The returned tokens keep the old refresh token when the response omits one.
func RefreshAuthorization(authorizationServerUrl string, options *RefreshAuthorizationOptions) (*OAuthTokens, error) {
	if options == nil {
		return nil, fmt.Errorf("RefreshAuthorization options are required")
	}
	params := make(url.Values)
	params.Set("grant_type", "refresh_token")
	params.Set("refresh_token", options.RefreshToken)
	tokens, err := flowTokenRequest(authorizationServerUrl, &options.TokenRequestOptions, params)
	if err != nil {
		return nil, err
	}
	if tokens.RefreshToken == nil {
		rt := options.RefreshToken
		tokens.RefreshToken = &rt
	}
	return tokens, nil
}

// StepUpScope merges scopes for a step-up authorization: the challenged scopes plus the ones granted
// so far, since a challenge may list only the missing scopes and a token with just those would lose
// access the old one had (SEP-2350). Without challenged scopes, nil lets the flow pick its default.
func StepUpScope(granted, challenged *string) *string {
	if challenged == nil || *challenged == "" {
		return nil
	}
	seen := make(map[string]struct{})
	var merged []string
	for _, src := range []*string{granted, challenged} {
		if src == nil || *src == "" {
			continue
		}
		for _, tok := range strings.Fields(*src) {
			if _, ok := seen[tok]; ok {
				continue
			}
			seen[tok] = struct{}{}
			merged = append(merged, tok)
		}
	}
	joined := strings.Join(merged, " ")
	return &joined
}

// AuthorizeMcp runs the OAuth flow until the provider has tokens or the user must be redirected.
func AuthorizeMcp(provider OAuthClientProvider, options *OAuthFlowOptions) (OAuthFlowResult, error) {
	if options == nil {
		return "", fmt.Errorf("AuthorizeMcp options are required")
	}
	var use flowUse
	res, err := flowRun(provider, options, &use)
	if err == nil {
		return res, nil
	}
	var oauthErr *OAuthError
	if errors.As(err, &oauthErr) {
		if oauthErr.Code == "invalid_client" || oauthErr.Code == "unauthorized_client" {
			if options.RefreshOnly != nil && *options.RefreshOnly {
				cur, curErr := provider.ClientInformation()
				var curId *string
				if curErr == nil && cur != nil {
					if info := flowBaseClientInfo(cur); info != nil {
						id := info.ClientId
						curId = &id
					}
				}
				if flowPtrStringEqual(curId, use.storedClientId) {
					if inv, ok := provider.(OAuthClientProviderInvalidateCredentials); ok {
						for _, kind := range []OAuthInvalidateCredentialsKind{
							OAuthInvalidateCredentialsKindClient,
							OAuthInvalidateCredentialsKindTokens,
							OAuthInvalidateCredentialsKindDiscovery,
						} {
							if ierr := inv.InvalidateCredentials(kind); ierr != nil {
								return "", ierr
							}
						}
					}
				}
			} else if inv, ok := provider.(OAuthClientProviderInvalidateCredentials); ok {
				if ierr := inv.InvalidateCredentials(OAuthInvalidateCredentialsKindAll); ierr != nil {
					return "", ierr
				}
			}
			return flowRun(provider, options, &flowUse{})
		}
		if oauthErr.Code == "invalid_grant" {
			if inv, ok := provider.(OAuthClientProviderInvalidateCredentials); ok {
				if ierr := inv.InvalidateCredentials(OAuthInvalidateCredentialsKindTokens); ierr != nil {
					return "", ierr
				}
			}
			return flowRun(provider, options, &flowUse{})
		}
	}
	return "", err
}

type flowInFlightCall struct {
	done chan struct{}
	err  error
}

type flowAdaptedAuthProvider struct {
	provider OAuthClientProvider
	mu       sync.Mutex
	inFlight *flowInFlightCall
}

func (a *flowAdaptedAuthProvider) Token() (*string, error) {
	tokens, err := a.provider.Tokens()
	if err != nil {
		return nil, err
	}
	if tokens == nil {
		return nil, nil
	}
	tok := tokens.AccessToken
	return &tok, nil
}

func (a *flowAdaptedAuthProvider) OnUnauthorized(unauth *mcp.UnauthorizedContext) error {
	if unauth == nil {
		return fmt.Errorf("unauthorized context is required")
	}
	var wwwAuth *string
	if unauth.Response != nil {
		if v := unauth.Response.Header.Get("WWW-Authenticate"); v != "" {
			wwwAuth = &v
		}
	}
	challenge := ParseWwwAuthenticate(wwwAuth)
	insufficientScope := challenge.Error != nil && *challenge.Error == "insufficient_scope"

	a.mu.Lock()
	active := a.inFlight
	if !insufficientScope && active == nil && unauth.Token != nil {
		a.mu.Unlock()
		tokens, err := a.provider.Tokens()
		if err != nil {
			return err
		}
		if tokens != nil && tokens.AccessToken != *unauth.Token {
			return nil
		}
		a.mu.Lock()
		active = a.inFlight
	}
	if active != nil {
		a.mu.Unlock()
		<-active.done
		return active.err
	}
	call := &flowInFlightCall{done: make(chan struct{})}
	a.inFlight = call
	a.mu.Unlock()

	var err error
	defer func() {
		call.err = err
		close(call.done)
		a.mu.Lock()
		if a.inFlight == call {
			a.inFlight = nil
		}
		a.mu.Unlock()
	}()

	var scope *string
	if insufficientScope {
		granted, tokErr := a.provider.Tokens()
		if tokErr != nil {
			err = tokErr
			return err
		}
		var grantedScope *string
		if granted != nil {
			grantedScope = granted.Scope
		}
		scope = StepUpScope(grantedScope, challenge.Scope)
	} else {
		scope = challenge.Scope
	}

	skipRefresh := insufficientScope
	var serverUrlStr string
	if unauth.ServerUrl != nil {
		serverUrlStr = unauth.ServerUrl.String()
	}
	result, flowErr := AuthorizeMcp(a.provider, &OAuthFlowOptions{
		ServerUrl:           serverUrlStr,
		ResourceMetadataUrl: challenge.ResourceMetadataUrl,
		Scope:               scope,
		Fetch:               unauth.Fetch,
		SkipRefresh:         &skipRefresh,
	})
	if flowErr != nil {
		err = flowErr
		return err
	}
	if result == OAuthFlowResultRedirect {
		err = NewMcpOAuthAuthorizationRequiredError()
		return err
	}
	return nil
}

// AdaptOAuthProvider is an auth provider for StreamableHttpTransport. After a 401 it refreshes the
// tokens, or returns McpOAuthAuthorizationRequiredError when the user has to authorize (again).
// Concurrent 401s share one refresh, and a request whose token was already replaced is just retried:
// with rotating refresh tokens, a second refresh with the old refresh token would fail and discard
// the new grant.
func AdaptOAuthProvider(provider OAuthClientProvider) mcp.AuthProvider {
	return &flowAdaptedAuthProvider{provider: provider}
}

func flowRun(provider OAuthClientProvider, options *OAuthFlowOptions, use *flowUse) (OAuthFlowResult, error) {
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	var metadataUrl *url.URL
	if options.AuthorizationServerMetadataUrl != nil {
		u, err := flowSecureEndpoint(options.AuthorizationServerMetadataUrl.String())
		if err != nil {
			return "", err
		}
		metadataUrl = u
	}

	var cached *OAuthDiscoveryState
	if metadataUrl == nil {
		if dsProv, ok := provider.(OAuthClientProviderDiscoveryState); ok {
			var err error
			cached, err = dsProv.DiscoveryState()
			if err != nil {
				return "", err
			}
		}
	}

	var discovered *OAuthServerInfo
	if cached != nil && cached.AuthorizationServerUrl != "" {
		asMeta := cached.AuthorizationServerMetadata
		if asMeta == nil {
			var err error
			asMeta, err = discDiscoverAuthorizationServerMetadata(ctx, cached.AuthorizationServerUrl, &DiscoverAuthorizationServerMetadataOptions{
				Fetch:                options.Fetch,
				SkipIssuerValidation: options.SkipIssuerValidation,
				HTTPClient:           options.HTTPClient,
			})
			if err != nil {
				return "", err
			}
		}
		discovered = &OAuthServerInfo{
			AuthorizationServerUrl:      cached.AuthorizationServerUrl,
			AuthorizationServerMetadata: asMeta,
			ResourceMetadata:            cached.ResourceMetadata,
		}
	} else {
		var err error
		discovered, err = discDiscoverOAuthServerInfo(ctx, options.ServerUrl, &DiscoverOAuthServerInfoOptions{
			ResourceMetadataUrl:            options.ResourceMetadataUrl,
			AuthorizationServerMetadataUrl: metadataUrl,
			Fetch:                          options.Fetch,
			SkipIssuerValidation:           options.SkipIssuerValidation,
			HTTPClient:                     options.HTTPClient,
		})
		if err != nil {
			return "", err
		}
	}

	if metadataUrl == nil {
		if saveDs, ok := provider.(OAuthClientProviderSaveDiscoveryState); ok {
			var rmUrlPtr *string
			if options.ResourceMetadataUrl != nil {
				s := options.ResourceMetadataUrl.String()
				rmUrlPtr = &s
			}
			if err := saveDs.SaveDiscoveryState(&OAuthDiscoveryState{
				AuthorizationServerUrl:      discovered.AuthorizationServerUrl,
				AuthorizationServerMetadata: discovered.AuthorizationServerMetadata,
				ResourceMetadata:            discovered.ResourceMetadata,
				ResourceMetadataUrl:         rmUrlPtr,
			}); err != nil {
				return "", err
			}
		}
	}

	metadata := discovered.AuthorizationServerMetadata
	resource, err := SelectResource(options.ServerUrl, discovered.ResourceMetadata)
	if err != nil {
		return "", err
	}

	scope := flowPickScope(options.Scope, discovered.ResourceMetadata, provider.ClientMetadata())

	stored, err := provider.ClientInformation()
	if err != nil {
		return "", err
	}
	if stored != nil {
		if info := flowBaseClientInfo(stored); info != nil {
			id := info.ClientId
			use.storedClientId = &id
		}
	}

	var clientDoc *OAuthClientMetadataDocument
	if stored == nil {
		if cmdProv, ok := provider.(OAuthClientProviderClientMetadataDocument); ok {
			clientDoc, err = cmdProv.ClientMetadataDocument(metadata)
			if err != nil {
				return "", err
			}
			if clientDoc != nil {
				docUrl, parseErr := url.Parse(clientDoc.Url)
				if parseErr != nil || strings.ToLower(docUrl.Scheme) != "https" || docUrl.Host == "" || docUrl.Path == "" || docUrl.Path == "/" {
					return "", fmt.Errorf("Invalid OAuth client metadata URL")
				}
			}
		}
	}

	redirectUrl := provider.RedirectUrl()
	if clientDoc != nil {
		redirectUrl = clientDoc.RedirectUrl
	}

	hasAuthCode := options.AuthorizationCode != nil && *options.AuthorizationCode != ""
	if hasAuthCode && options.AuthorizationRedirectUrl != nil && redirectUrl != *options.AuthorizationRedirectUrl {
		return "", fmt.Errorf(
			"The code exchange would use the redirect URI %s, but the authorization request used %s",
			redirectUrl,
			*options.AuthorizationRedirectUrl,
		)
	}

	client := stored
	if client == nil && clientDoc != nil {
		client = &OAuthClientInformation{ClientId: clientDoc.Url}
	}
	if client == nil {
		if options.RefreshOnly != nil && *options.RefreshOnly {
			return OAuthFlowResultRedirect, nil
		}
		if hasAuthCode {
			return "", fmt.Errorf("OAuth client information is missing during code exchange: the authorization request's client was removed, or the client settings changed")
		}
		saveClient, ok := provider.(OAuthClientProviderSaveClientInformation)
		if !ok {
			return "", fmt.Errorf("OAuth client information cannot be persisted")
		}
		regClient, err := RegisterClient(discovered.AuthorizationServerUrl, &RegisterClientOptions{
			Metadata:       metadata,
			ClientMetadata: provider.ClientMetadata(),
			Scope:          scope,
			Fetch:          options.Fetch,
			Context:        ctx,
			HTTPClient:     options.HTTPClient,
		})
		if err != nil {
			return "", err
		}
		if err := saveClient.SaveClientInformation(regClient); err != nil {
			return "", err
		}
		client = regClient
	}

	var addClientAuth AddClientAuthentication
	if authProv, ok := provider.(OAuthClientProviderAddClientAuthentication); ok {
		addClientAuth = authProv.AddClientAuthentication()
	}

	tokenOpts := TokenRequestOptions{
		Metadata:                metadata,
		ClientInformation:       client,
		Resource:                resource,
		AddClientAuthentication: addClientAuth,
		Fetch:                   options.Fetch,
		Context:                 ctx,
		HTTPClient:              options.HTTPClient,
	}

	if hasAuthCode {
		issSupported := metadata != nil && metadata.AuthorizationResponseIssParameterSupported != nil && *metadata.AuthorizationResponseIssParameterSupported
		if options.Iss != nil || issSupported {
			expected := ""
			if metadata != nil {
				expected = metadata.Issuer
			}
			if options.Iss == nil || *options.Iss != expected {
				return "", NewOAuthIssuerMismatchError(expected, options.Iss)
			}
		}
		verifier, err := provider.CodeVerifier()
		if err != nil {
			return "", err
		}
		tokens, err := ExchangeAuthorizationCode(discovered.AuthorizationServerUrl, &ExchangeAuthorizationCodeOptions{
			TokenRequestOptions: tokenOpts,
			Code:                *options.AuthorizationCode,
			CodeVerifier:        verifier,
			RedirectUrl:         redirectUrl,
		})
		if err != nil {
			return "", err
		}
		if err := provider.SaveTokens(flowWithScope(tokens, scope)); err != nil {
			return "", err
		}
		return OAuthFlowResultAuthorized, nil
	}

	var existing *OAuthTokens
	if options.SkipRefresh == nil || !*options.SkipRefresh {
		existing, err = provider.Tokens()
		if err != nil {
			return "", err
		}
	}
	if existing != nil && existing.RefreshToken != nil && *existing.RefreshToken != "" {
		tokens, refErr := RefreshAuthorization(discovered.AuthorizationServerUrl, &RefreshAuthorizationOptions{
			TokenRequestOptions: tokenOpts,
			RefreshToken:        *existing.RefreshToken,
		})
		if refErr == nil {
			if err := provider.SaveTokens(flowWithScope(tokens, existing.Scope)); err != nil {
				return "", err
			}
			return OAuthFlowResultAuthorized, nil
		}
		if ctx.Err() != nil {
			return "", refErr
		}
		var insecureErr *OAuthInsecureEndpointError
		if errors.As(refErr, &insecureErr) {
			return "", refErr
		}
		var oauthErr *OAuthError
		if errors.As(refErr, &oauthErr) && oauthErr.Code != "server_error" {
			return "", refErr
		}
	}

	if options.RefreshOnly != nil && *options.RefreshOnly {
		return OAuthFlowResultRedirect, nil
	}

	var statePtr *string
	if stateProv, ok := provider.(OAuthClientProviderState); ok {
		st, err := stateProv.State()
		if err != nil {
			return "", err
		}
		if st != "" {
			statePtr = &st
		}
	}

	authUrl, codeVerifier, err := StartAuthorization(discovered.AuthorizationServerUrl, &StartAuthorizationOptions{
		Metadata:          metadata,
		ClientInformation: client,
		RedirectUrl:       redirectUrl,
		Scope:             scope,
		State:             statePtr,
		Resource:          resource,
	})
	if err != nil {
		return "", err
	}
	if err := provider.SaveCodeVerifier(codeVerifier); err != nil {
		return "", err
	}
	if err := provider.RedirectToAuthorization(authUrl); err != nil {
		return "", err
	}
	return OAuthFlowResultRedirect, nil
}

func flowLoopbackHost(hostname string) bool {
	switch strings.ToLower(hostname) {
	case "localhost", "127.0.0.1", "[::1]", "::1":
		return true
	default:
		return false
	}
}

func flowApplicationType(redirectUris []string) string {
	for _, raw := range redirectUris {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme == "" {
			continue
		}
		if (u.Scheme != "http" && u.Scheme != "https") || flowLoopbackHost(u.Hostname()) {
			return "native"
		}
	}
	return "web"
}

func flowSecureEndpoint(raw string) (*url.URL, error) {
	u, err := discParseUrl(raw)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "https" && !flowLoopbackHost(u.Hostname()) {
		return nil, NewOAuthInsecureEndpointError(u.String())
	}
	return u, nil
}

func flowSelectClientAuthMethod(info OAuthClientInformationMixed, supported []string) flowClientAuthMethod {
	var hinted string
	if full, ok := info.(*OAuthClientInformationFull); ok && full != nil && full.TokenEndpointAuthMethod != nil {
		hinted = *full.TokenEndpointAuthMethod
	}
	if (hinted == "client_secret_basic" || hinted == "client_secret_post" || hinted == "none") &&
		(len(supported) == 0 || slices.Contains(supported, hinted)) {
		return flowClientAuthMethod(hinted)
	}
	base := flowBaseClientInfo(info)
	hasSecret := base != nil && base.ClientSecret != nil && *base.ClientSecret != ""
	if len(supported) == 0 {
		if hasSecret {
			return flowAuthSecretBasic
		}
		return flowAuthNone
	}
	if hasSecret && slices.Contains(supported, "client_secret_basic") {
		return flowAuthSecretBasic
	}
	if hasSecret && slices.Contains(supported, "client_secret_post") {
		return flowAuthSecretPost
	}
	if slices.Contains(supported, "none") {
		return flowAuthNone
	}
	if hasSecret {
		return flowAuthSecretPost
	}
	return flowAuthNone
}

func flowApplyClientAuthentication(method flowClientAuthMethod, info *OAuthClientInformation, headers http.Header, params url.Values) error {
	if method == flowAuthSecretBasic {
		if info.ClientSecret == nil || *info.ClientSecret == "" {
			return fmt.Errorf("client_secret_basic requires a client secret")
		}
		creds := info.ClientId + ":" + *info.ClientSecret
		headers.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(creds)))
		return nil
	}
	params.Set("client_id", info.ClientId)
	if method == flowAuthSecretPost && info.ClientSecret != nil && *info.ClientSecret != "" {
		params.Set("client_secret", *info.ClientSecret)
	}
	return nil
}

func flowTokenRequest(authorizationServerUrl string, options *TokenRequestOptions, params url.Values) (*OAuthTokens, error) {
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	var rawEndpoint string
	if options.Metadata != nil && options.Metadata.TokenEndpoint != "" {
		rawEndpoint = options.Metadata.TokenEndpoint
	} else {
		u, err := flowResolvePath(authorizationServerUrl, "/token")
		if err != nil {
			return nil, err
		}
		rawEndpoint = u.String()
	}
	endpointUrl, err := flowSecureEndpoint(rawEndpoint)
	if err != nil {
		return nil, err
	}

	hdrs := make(http.Header)
	hdrs.Set("Accept", "application/json")
	hdrs.Set("Content-Type", "application/x-www-form-urlencoded")
	if options.Resource != nil && *options.Resource != "" {
		params.Set("resource", *options.Resource)
	}

	if options.AddClientAuthentication != nil {
		if err := options.AddClientAuthentication(hdrs, params, endpointUrl.String(), options.Metadata); err != nil {
			return nil, err
		}
	} else {
		var supported []string
		if options.Metadata != nil {
			supported = options.Metadata.TokenEndpointAuthMethodsSupported
		}
		base := flowBaseClientInfo(options.ClientInformation)
		if base == nil {
			return nil, fmt.Errorf("ClientInformation is required")
		}
		method := flowSelectClientAuthMethod(options.ClientInformation, supported)
		if err := flowApplyClientAuthentication(method, base, hdrs, params); err != nil {
			return nil, err
		}
	}

	fetch := typesHttpClientFetch(options.Fetch, options.HTTPClient)
	resp, err := fetch(ctx, endpointUrl.String(), &mcp.McpRequestInit{
		Method:  http.MethodPost,
		Headers: hdrs,
		Body:    []byte(params.Encode()),
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	text := string(bodyBytes)

	parsed, parseErr := jsonx.Parse(bodyBytes)
	if parseErr == nil {
		if obj, ok := parsed.(*jsonx.Object); ok && obj != nil {
			if errVal, hasErr := obj.Get("error"); hasErr {
				if errCode, isStr := errVal.(string); isStr {
					desc := errCode
					if dv, ok := obj.Get("error_description"); ok {
						if ds, isStr := dv.(string); isStr {
							desc = ds
						}
					}
					var errUri *string
					if uv, ok := obj.Get("error_uri"); ok {
						if us, isStr := uv.(string); isStr {
							errUri = &us
						}
					}
					return nil, NewOAuthError(errCode, desc, errUri)
				}
			}
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, NewOAuthError("server_error", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, text), nil)
	}
	if parseErr != nil {
		return nil, parseErr
	}
	return ParseOAuthTokens(parsed)
}

func flowResolvePath(baseRaw, relPath string) (*url.URL, error) {
	base, err := discParseUrl(baseRaw)
	if err != nil {
		return nil, err
	}
	ref := &url.URL{Path: relPath}
	return base.ResolveReference(ref), nil
}

func flowBaseClientInfo(info OAuthClientInformationMixed) *OAuthClientInformation {
	switch c := info.(type) {
	case *OAuthClientInformation:
		return c
	case *OAuthClientInformationFull:
		if c == nil {
			return nil
		}
		return &c.OAuthClientInformation
	default:
		return nil
	}
}

func flowWithScope(tokens *OAuthTokens, scope *string) *OAuthTokens {
	if tokens == nil {
		return nil
	}
	out := *tokens
	if out.Scope == nil && scope != nil && *scope != "" {
		s := *scope
		out.Scope = &s
	}
	return &out
}

func flowPickScope(optScope *string, rm *OAuthProtectedResourceMetadata, cm OAuthClientMetadata) *string {
	if optScope != nil && *optScope != "" {
		s := *optScope
		return &s
	}
	if rm != nil && len(rm.ScopesSupported) > 0 {
		s := strings.Join(rm.ScopesSupported, " ")
		if s != "" {
			return &s
		}
	}
	if cm.Scope != nil && *cm.Scope != "" {
		s := *cm.Scope
		return &s
	}
	return nil
}

func flowPtrStringEqual(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

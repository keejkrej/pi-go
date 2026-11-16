// Ported from packages/mcp/src/oauth/provider.ts (pi v1.5.2).

package oauth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"sync"
	"time"
)

// McpOAuthState is the persisted OAuth state for one MCP server URL.
// JSON field order is the TS interface order.
type McpOAuthState struct {
	ServerUrl         string                      `json:"serverUrl"`
	ClientInformation OAuthClientInformationMixed `json:"clientInformation,omitzero"`
	Tokens            *OAuthTokens                `json:"tokens,omitzero"`
	// TokensExpireAt is when the access token expires, in milliseconds since the epoch, from expires_in at the time it was saved.
	TokensExpireAt *int64               `json:"tokensExpireAt,omitzero"`
	CodeVerifier   *string              `json:"codeVerifier,omitzero"`
	OauthState     *string              `json:"oauthState,omitzero"`
	Discovery      *OAuthDiscoveryState `json:"discovery,omitzero"`
}

// McpOAuthStateStore loads and saves McpOAuthState. A nil state from Load means nothing is stored.
type McpOAuthStateStore interface {
	Load() (*McpOAuthState, error)
	Save(state *McpOAuthState) error
}

// McpOAuthProviderOptions are the options of NewMcpOAuthProvider.
// ClientMetadata.RedirectUris nil means the provider's redirect URL.
type McpOAuthProviderOptions struct {
	ServerUrl              string
	RedirectUrl            string
	ClientMetadata         OAuthClientMetadata
	ClientMetadataDocument func(metadata *AuthorizationServerMetadata) (*OAuthClientMetadataDocument, error)
	ClientId               *string
	ClientSecret           *string
	Store                  McpOAuthStateStore
	OnRedirect             func(u *url.URL) error
}

// MemoryOAuthStateStore is an in-memory McpOAuthStateStore.
type MemoryOAuthStateStore struct {
	mu    sync.Mutex
	value *McpOAuthState
}

// NewMemoryOAuthStateStore returns an empty store.
func NewMemoryOAuthStateStore() *MemoryOAuthStateStore {
	return &MemoryOAuthStateStore{}
}

// Load returns a copy of the stored state, or nil when nothing has been saved.
func (s *MemoryOAuthStateStore) Load() (*McpOAuthState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.value == nil {
		return nil, nil
	}
	return provCloneState(s.value), nil
}

// Save replaces the stored state.
func (s *MemoryOAuthStateStore) Save(state *McpOAuthState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if state == nil {
		s.value = nil
		return nil
	}
	s.value = provCloneState(state)
	return nil
}

// McpOAuthProvider is the default stateful provider for one exact MCP server URL.
// Applications inject durable storage if needed.
type McpOAuthProvider struct {
	mu                     sync.Mutex
	redirectUrl            string
	clientMetadata         OAuthClientMetadata
	clientMetadataDocument func(metadata *AuthorizationServerMetadata) (*OAuthClientMetadataDocument, error)
	serverUrl              string
	configuredClient       OAuthClientInformationMixed
	store                  McpOAuthStateStore
	onRedirect             func(*url.URL) error
}

// NewMcpOAuthProvider builds a provider for one MCP server URL.
func NewMcpOAuthProvider(options *McpOAuthProviderOptions) (*McpOAuthProvider, error) {
	if options == nil {
		return nil, fmt.Errorf("McpOAuthProviderOptions are required")
	}
	serverUrl, err := provNormalizeUrl(options.ServerUrl)
	if err != nil {
		return nil, err
	}
	redirectUrl := options.RedirectUrl

	cm := options.ClientMetadata
	if cm.RedirectUris == nil {
		cm.RedirectUris = []string{redirectUrl}
	} else {
		cm.RedirectUris = append([]string(nil), cm.RedirectUris...)
	}
	if cm.GrantTypes == nil {
		cm.GrantTypes = []string{"authorization_code", "refresh_token"}
	} else {
		cm.GrantTypes = append([]string(nil), cm.GrantTypes...)
	}
	if cm.ResponseTypes == nil {
		cm.ResponseTypes = []string{"code"}
	} else {
		cm.ResponseTypes = append([]string(nil), cm.ResponseTypes...)
	}
	if cm.TokenEndpointAuthMethod == nil {
		method := "none"
		if options.ClientSecret != nil && *options.ClientSecret != "" {
			method = "client_secret_post"
		}
		cm.TokenEndpointAuthMethod = &method
	}

	var configured OAuthClientInformationMixed
	if options.ClientId != nil && *options.ClientId != "" {
		configured = &OAuthClientInformation{
			ClientId:     *options.ClientId,
			ClientSecret: provCloneStringPtr(options.ClientSecret),
		}
	}

	store := options.Store
	if store == nil {
		store = NewMemoryOAuthStateStore()
	}

	return &McpOAuthProvider{
		redirectUrl:            redirectUrl,
		clientMetadata:         cm,
		clientMetadataDocument: options.ClientMetadataDocument,
		serverUrl:              serverUrl,
		configuredClient:       configured,
		store:                  store,
		onRedirect:             options.OnRedirect,
	}, nil
}

// RedirectUrl returns the redirect URL.
func (p *McpOAuthProvider) RedirectUrl() string { return p.redirectUrl }

// ClientMetadata returns the client metadata, with registration defaults applied.
func (p *McpOAuthProvider) ClientMetadata() OAuthClientMetadata { return p.clientMetadata }

// ClientMetadataDocument returns the Client ID Metadata Document to identify as, or nil.
func (p *McpOAuthProvider) ClientMetadataDocument(metadata *AuthorizationServerMetadata) (*OAuthClientMetadataDocument, error) {
	if p.clientMetadataDocument == nil {
		return nil, nil
	}
	return p.clientMetadataDocument(metadata)
}

// State returns the stored OAuth state parameter, creating one on first use.
func (p *McpOAuthProvider) State() (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	cur, err := p.provLoadLocked()
	if err != nil {
		return "", err
	}
	if cur.OauthState != nil && *cur.OauthState != "" {
		return *cur.OauthState, nil
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	state := hex.EncodeToString(buf)
	cur.OauthState = &state
	if err := p.store.Save(cur); err != nil {
		return "", err
	}
	return state, nil
}

// ClientInformation returns the configured client, or the client saved in the store.
func (p *McpOAuthProvider) ClientInformation() (OAuthClientInformationMixed, error) {
	if p.configuredClient != nil {
		return p.configuredClient, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	cur, err := p.provLoadLocked()
	if err != nil {
		return nil, err
	}
	return cur.ClientInformation, nil
}

// SaveClientInformation stores a dynamically registered client. A configured client id is left unchanged.
func (p *McpOAuthProvider) SaveClientInformation(information OAuthClientInformationMixed) error {
	if p.configuredClient != nil {
		return nil
	}
	return p.provUpdate(func(state *McpOAuthState) {
		state.ClientInformation = information
	})
}

// Tokens returns the stored tokens.
func (p *McpOAuthProvider) Tokens() (*OAuthTokens, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	cur, err := p.provLoadLocked()
	if err != nil {
		return nil, err
	}
	return cur.Tokens, nil
}

// SaveTokens stores tokens and records TokensExpireAt from expires_in.
func (p *McpOAuthProvider) SaveTokens(tokens *OAuthTokens) error {
	var expiresAt *int64
	if tokens != nil && tokens.ExpiresIn != nil {
		v := time.Now().UnixMilli() + int64(*tokens.ExpiresIn*1000)
		expiresAt = &v
	}
	return p.provUpdate(func(state *McpOAuthState) {
		state.Tokens = tokens
		state.TokensExpireAt = expiresAt
	})
}

// RedirectToAuthorization calls the provider's redirect hook.
func (p *McpOAuthProvider) RedirectToAuthorization(u *url.URL) error {
	if p.onRedirect == nil {
		return nil
	}
	return p.onRedirect(u)
}

// SaveCodeVerifier stores the PKCE verifier.
func (p *McpOAuthProvider) SaveCodeVerifier(verifier string) error {
	return p.provUpdate(func(state *McpOAuthState) {
		v := verifier
		state.CodeVerifier = &v
	})
}

// CodeVerifier returns the stored PKCE verifier.
func (p *McpOAuthProvider) CodeVerifier() (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	cur, err := p.provLoadLocked()
	if err != nil {
		return "", err
	}
	if cur.CodeVerifier == nil || *cur.CodeVerifier == "" {
		return "", fmt.Errorf("No OAuth PKCE code verifier is stored")
	}
	return *cur.CodeVerifier, nil
}

// InvalidateCredentials drops the selected stored credentials.
func (p *McpOAuthProvider) InvalidateCredentials(kind OAuthInvalidateCredentialsKind) error {
	return p.provUpdate(func(state *McpOAuthState) {
		if kind == OAuthInvalidateCredentialsKindAll || kind == OAuthInvalidateCredentialsKindClient {
			state.ClientInformation = nil
		}
		if kind == OAuthInvalidateCredentialsKindAll || kind == OAuthInvalidateCredentialsKindTokens {
			state.Tokens = nil
			state.TokensExpireAt = nil
		}
		if kind == OAuthInvalidateCredentialsKindAll || kind == OAuthInvalidateCredentialsKindVerifier {
			state.CodeVerifier = nil
		}
		if kind == OAuthInvalidateCredentialsKindAll || kind == OAuthInvalidateCredentialsKindDiscovery {
			state.Discovery = nil
		}
		if kind == OAuthInvalidateCredentialsKindAll {
			state.OauthState = nil
		}
	})
}

// SaveDiscoveryState stores the discovery cache.
func (p *McpOAuthProvider) SaveDiscoveryState(discovery *OAuthDiscoveryState) error {
	return p.provUpdate(func(state *McpOAuthState) {
		state.Discovery = discovery
	})
}

// DiscoveryState returns the stored discovery cache.
func (p *McpOAuthProvider) DiscoveryState() (*OAuthDiscoveryState, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	cur, err := p.provLoadLocked()
	if err != nil {
		return nil, err
	}
	return cur.Discovery, nil
}

func (p *McpOAuthProvider) provLoadLocked() (*McpOAuthState, error) {
	raw, err := p.store.Load()
	if err != nil {
		return nil, err
	}
	return p.provOwn(raw), nil
}

func (p *McpOAuthProvider) provUpdate(fn func(state *McpOAuthState)) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	cur, err := p.provLoadLocked()
	if err != nil {
		return err
	}
	fn(cur)
	return p.store.Save(cur)
}

func (p *McpOAuthProvider) provOwn(state *McpOAuthState) *McpOAuthState {
	if state != nil && state.ServerUrl == p.serverUrl {
		return state
	}
	return &McpOAuthState{ServerUrl: p.serverUrl}
}

func provNormalizeUrl(raw string) (string, error) {
	u, err := discParseUrl(raw)
	if err != nil {
		return "", err
	}
	if u.Path == "" {
		u.Path = "/"
	}
	return u.String(), nil
}

func provCloneStringPtr(p *string) *string {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func provCloneFloatPtr(p *float64) *float64 {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func provCloneInt64Ptr(p *int64) *int64 {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func provCloneBoolPtr(p *bool) *bool {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func provCloneStrings(s []string) []string {
	if s == nil {
		return nil
	}
	out := make([]string, len(s))
	copy(out, s)
	return out
}

func provCloneState(in *McpOAuthState) *McpOAuthState {
	if in == nil {
		return nil
	}
	out := &McpOAuthState{
		ServerUrl:      in.ServerUrl,
		TokensExpireAt: provCloneInt64Ptr(in.TokensExpireAt),
		CodeVerifier:   provCloneStringPtr(in.CodeVerifier),
		OauthState:     provCloneStringPtr(in.OauthState),
	}
	if in.ClientInformation != nil {
		switch c := in.ClientInformation.(type) {
		case *OAuthClientInformation:
			if c != nil {
				out.ClientInformation = &OAuthClientInformation{
					ClientId:              c.ClientId,
					ClientSecret:          provCloneStringPtr(c.ClientSecret),
					ClientIdIssuedAt:      provCloneFloatPtr(c.ClientIdIssuedAt),
					ClientSecretExpiresAt: provCloneFloatPtr(c.ClientSecretExpiresAt),
				}
			}
		case *OAuthClientInformationFull:
			if c != nil {
				out.ClientInformation = &OAuthClientInformationFull{
					OAuthClientInformation: OAuthClientInformation{
						ClientId:              c.ClientId,
						ClientSecret:          provCloneStringPtr(c.ClientSecret),
						ClientIdIssuedAt:      provCloneFloatPtr(c.ClientIdIssuedAt),
						ClientSecretExpiresAt: provCloneFloatPtr(c.ClientSecretExpiresAt),
					},
					OAuthClientMetadata: OAuthClientMetadata{
						RedirectUris:            provCloneStrings(c.RedirectUris),
						TokenEndpointAuthMethod: provCloneStringPtr(c.TokenEndpointAuthMethod),
						GrantTypes:              provCloneStrings(c.GrantTypes),
						ResponseTypes:           provCloneStrings(c.ResponseTypes),
						ApplicationType:         provCloneStringPtr(c.ApplicationType),
						ClientName:              provCloneStringPtr(c.ClientName),
						ClientUri:               provCloneStringPtr(c.ClientUri),
						LogoUri:                 provCloneStringPtr(c.LogoUri),
						Scope:                   provCloneStringPtr(c.Scope),
						Contacts:                provCloneStrings(c.Contacts),
						TosUri:                  provCloneStringPtr(c.TosUri),
						PolicyUri:               provCloneStringPtr(c.PolicyUri),
						JwksUri:                 provCloneStringPtr(c.JwksUri),
						Jwks:                    c.Jwks,
						SoftwareId:              provCloneStringPtr(c.SoftwareId),
						SoftwareVersion:         provCloneStringPtr(c.SoftwareVersion),
						SoftwareStatement:       provCloneStringPtr(c.SoftwareStatement),
					},
				}
			}
		}
	}
	if in.Tokens != nil {
		out.Tokens = &OAuthTokens{
			AccessToken:  in.Tokens.AccessToken,
			TokenType:    in.Tokens.TokenType,
			ExpiresIn:    provCloneFloatPtr(in.Tokens.ExpiresIn),
			Scope:        provCloneStringPtr(in.Tokens.Scope),
			RefreshToken: provCloneStringPtr(in.Tokens.RefreshToken),
			IdToken:      provCloneStringPtr(in.Tokens.IdToken),
		}
	}
	if in.Discovery != nil {
		d := &OAuthDiscoveryState{
			AuthorizationServerUrl: in.Discovery.AuthorizationServerUrl,
			ResourceMetadataUrl:    provCloneStringPtr(in.Discovery.ResourceMetadataUrl),
		}
		if in.Discovery.ResourceMetadata != nil {
			rm := in.Discovery.ResourceMetadata
			var rmExtra = rm.Extra
			if rmExtra != nil {
				rmExtra = rmExtra.Clone()
			}
			d.ResourceMetadata = &OAuthProtectedResourceMetadata{
				Resource:             rm.Resource,
				AuthorizationServers: provCloneStrings(rm.AuthorizationServers),
				ScopesSupported:      provCloneStrings(rm.ScopesSupported),
				Extra:                rmExtra,
			}
		}
		if in.Discovery.AuthorizationServerMetadata != nil {
			am := in.Discovery.AuthorizationServerMetadata
			amExtra := am.Extra
			if amExtra != nil {
				amExtra = amExtra.Clone()
			}
			d.AuthorizationServerMetadata = &AuthorizationServerMetadata{
				Issuer:                                     am.Issuer,
				AuthorizationEndpoint:                      am.AuthorizationEndpoint,
				TokenEndpoint:                              am.TokenEndpoint,
				RegistrationEndpoint:                       provCloneStringPtr(am.RegistrationEndpoint),
				ScopesSupported:                            provCloneStrings(am.ScopesSupported),
				ResponseTypesSupported:                     provCloneStrings(am.ResponseTypesSupported),
				GrantTypesSupported:                        provCloneStrings(am.GrantTypesSupported),
				TokenEndpointAuthMethodsSupported:          provCloneStrings(am.TokenEndpointAuthMethodsSupported),
				CodeChallengeMethodsSupported:              provCloneStrings(am.CodeChallengeMethodsSupported),
				ClientIdMetadataDocumentSupported:          provCloneBoolPtr(am.ClientIdMetadataDocumentSupported),
				AuthorizationResponseIssParameterSupported: provCloneBoolPtr(am.AuthorizationResponseIssParameterSupported),
				Extra: amExtra,
			}
		}
		out.Discovery = d
	}
	return out
}

var (
	_ McpOAuthStateStore                        = (*MemoryOAuthStateStore)(nil)
	_ OAuthClientProvider                       = (*McpOAuthProvider)(nil)
	_ OAuthClientProviderClientMetadataDocument = (*McpOAuthProvider)(nil)
	_ OAuthClientProviderState                  = (*McpOAuthProvider)(nil)
	_ OAuthClientProviderSaveClientInformation  = (*McpOAuthProvider)(nil)
	_ OAuthClientProviderInvalidateCredentials  = (*McpOAuthProvider)(nil)
	_ OAuthClientProviderSaveDiscoveryState     = (*McpOAuthProvider)(nil)
	_ OAuthClientProviderDiscoveryState         = (*McpOAuthProvider)(nil)
)

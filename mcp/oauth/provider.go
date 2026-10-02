// Ported from packages/mcp/src/oauth/provider.ts (pi v1.0.0).

package oauth

import (
	"net/url"
	"sync"
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
	ServerUrl      string
	RedirectUrl    string
	ClientMetadata OAuthClientMetadata
	ClientId       *string
	ClientSecret   *string
	Store          McpOAuthStateStore
	OnRedirect     func(u *url.URL) error
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
	panic("unported: MemoryOAuthStateStore.Load")
}

// Save replaces the stored state.
func (s *MemoryOAuthStateStore) Save(state *McpOAuthState) error {
	panic("unported: MemoryOAuthStateStore.Save")
}

// McpOAuthProvider is the default stateful provider for one exact MCP server URL.
// Applications inject durable storage if needed.
type McpOAuthProvider struct {
	// mu serializes store updates (TS writes promise chain).
	mu               sync.Mutex
	redirectUrl      string
	clientMetadata   OAuthClientMetadata
	serverUrl        string
	configuredClient OAuthClientInformationMixed
	store            McpOAuthStateStore
	onRedirect       func(*url.URL) error
}

// NewMcpOAuthProvider builds a provider for one MCP server URL.
func NewMcpOAuthProvider(options *McpOAuthProviderOptions) (*McpOAuthProvider, error) {
	panic("unported: NewMcpOAuthProvider")
}

// RedirectUrl returns the redirect URL.
func (p *McpOAuthProvider) RedirectUrl() string { return p.redirectUrl }

// ClientMetadata returns the client metadata, with registration defaults applied.
func (p *McpOAuthProvider) ClientMetadata() OAuthClientMetadata { return p.clientMetadata }

// State returns the stored OAuth state parameter, creating one on first use.
func (p *McpOAuthProvider) State() (string, error) {
	panic("unported: McpOAuthProvider.State")
}

// ClientInformation returns the configured client, or the client saved in the store.
func (p *McpOAuthProvider) ClientInformation() (OAuthClientInformationMixed, error) {
	panic("unported: McpOAuthProvider.ClientInformation")
}

// SaveClientInformation stores a dynamically registered client. A configured client id is left unchanged.
func (p *McpOAuthProvider) SaveClientInformation(information OAuthClientInformationMixed) error {
	panic("unported: McpOAuthProvider.SaveClientInformation")
}

// Tokens returns the stored tokens.
func (p *McpOAuthProvider) Tokens() (*OAuthTokens, error) {
	panic("unported: McpOAuthProvider.Tokens")
}

// SaveTokens stores tokens and records TokensExpireAt from expires_in.
func (p *McpOAuthProvider) SaveTokens(tokens *OAuthTokens) error {
	panic("unported: McpOAuthProvider.SaveTokens")
}

// RedirectToAuthorization calls the provider's redirect hook.
func (p *McpOAuthProvider) RedirectToAuthorization(u *url.URL) error {
	panic("unported: McpOAuthProvider.RedirectToAuthorization")
}

// SaveCodeVerifier stores the PKCE verifier.
func (p *McpOAuthProvider) SaveCodeVerifier(verifier string) error {
	panic("unported: McpOAuthProvider.SaveCodeVerifier")
}

// CodeVerifier returns the stored PKCE verifier.
func (p *McpOAuthProvider) CodeVerifier() (string, error) {
	panic("unported: McpOAuthProvider.CodeVerifier")
}

// InvalidateCredentials drops the selected stored credentials.
func (p *McpOAuthProvider) InvalidateCredentials(kind OAuthInvalidateCredentialsKind) error {
	panic("unported: McpOAuthProvider.InvalidateCredentials")
}

// SaveDiscoveryState stores the discovery cache.
func (p *McpOAuthProvider) SaveDiscoveryState(state *OAuthDiscoveryState) error {
	panic("unported: McpOAuthProvider.SaveDiscoveryState")
}

// DiscoveryState returns the stored discovery cache.
func (p *McpOAuthProvider) DiscoveryState() (*OAuthDiscoveryState, error) {
	panic("unported: McpOAuthProvider.DiscoveryState")
}

var (
	_ McpOAuthStateStore                       = (*MemoryOAuthStateStore)(nil)
	_ OAuthClientProvider                      = (*McpOAuthProvider)(nil)
	_ OAuthClientProviderState                 = (*McpOAuthProvider)(nil)
	_ OAuthClientProviderSaveClientInformation = (*McpOAuthProvider)(nil)
	_ OAuthClientProviderInvalidateCredentials = (*McpOAuthProvider)(nil)
	_ OAuthClientProviderSaveDiscoveryState    = (*McpOAuthProvider)(nil)
	_ OAuthClientProviderDiscoveryState        = (*McpOAuthProvider)(nil)
)

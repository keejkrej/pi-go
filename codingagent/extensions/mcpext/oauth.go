// Ported from packages/coding-agent/src/extensions/mcp/oauth.ts (pi v1.5.2).

package mcpext

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/lockfile"
	"github.com/keejkrej/pi-go/mcp"
	mcpoauth "github.com/keejkrej/pi-go/mcp/oauth"
)

const (
	oauthRefreshLockStaleMs int64 = 20_000
	oauthRefreshLockWaitMs  int64 = 25_000
	oauthRefreshLockRetryMs int64 = 100
)

type oauthStoredStates map[string]*mcpoauth.McpOAuthState

func oauthDecodeClientInformation(raw any) mcpoauth.OAuthClientInformationMixed {
	obj, ok := raw.(*jsonx.Object)
	if !ok || obj == nil {
		return nil
	}
	if obj.Has("token_endpoint_auth_method") {
		var full mcpoauth.OAuthClientInformationFull
		if err := jsonx.Decode(obj, &full); err == nil {
			return &full
		}
	}
	var basic mcpoauth.OAuthClientInformation
	if err := jsonx.Decode(obj, &basic); err == nil {
		return &basic
	}
	return nil
}

func oauthDecodeState(raw any) (*mcpoauth.McpOAuthState, bool) {
	obj, ok := raw.(*jsonx.Object)
	if !ok || obj == nil {
		return nil, false
	}
	var shell struct {
		ServerUrl      string                        `json:"serverUrl"`
		Tokens         *mcpoauth.OAuthTokens         `json:"tokens,omitzero"`
		TokensExpireAt *int64                        `json:"tokensExpireAt,omitzero"`
		CodeVerifier   *string                       `json:"codeVerifier,omitzero"`
		OauthState     *string                       `json:"oauthState,omitzero"`
		Discovery      *mcpoauth.OAuthDiscoveryState `json:"discovery,omitzero"`
	}
	if err := jsonx.Decode(obj, &shell); err != nil {
		return nil, false
	}
	var clientInfo mcpoauth.OAuthClientInformationMixed
	if ciRaw, hasCI := obj.Get("clientInformation"); hasCI && ciRaw != nil {
		clientInfo = oauthDecodeClientInformation(ciRaw)
	}
	return &mcpoauth.McpOAuthState{
		ServerUrl:         shell.ServerUrl,
		ClientInformation: clientInfo,
		Tokens:            shell.Tokens,
		TokensExpireAt:    shell.TokensExpireAt,
		CodeVerifier:      shell.CodeVerifier,
		OauthState:        shell.OauthState,
		Discovery:         shell.Discovery,
	}, true
}

func oauthParseStates(content *string) oauthStoredStates {
	out := make(oauthStoredStates)
	if content == nil || strings.TrimSpace(*content) == "" {
		return out
	}
	parsedAny, err := jsonx.ParseString(*content)
	if err != nil {
		return out
	}
	obj, ok := parsedAny.(*jsonx.Object)
	if !ok || obj == nil {
		return out
	}
	for k, v := range obj.All() {
		if st, ok := oauthDecodeState(v); ok {
			out[k] = st
		}
	}
	return out
}

func oauthSerializeStates(states oauthStoredStates) string {
	b, err := jsonx.MarshalIndent(states, "  ")
	if err != nil {
		return "{}\n"
	}
	return string(b) + "\n"
}

func oauthNormalizeUrlString(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	// Match JS `String(new URL(serverUrl))`: origin URLs with empty path serialize with "/".
	if parsed.Path == "" && (parsed.Scheme == "http" || parsed.Scheme == "https") {
		parsed.Path = "/"
	}
	return parsed.String()
}

func oauthStoreKeys(name, serverUrl string) (key, legacyKey string) {
	legacyKey = oauthNormalizeUrlString(serverUrl)
	return mcp.McpNamespace(name) + "|" + legacyKey, legacyKey
}

// McpOAuthCredentialStore stores per-server OAuth state in mcp-auth.json and coordinates
// cross-process token refreshes via lock files.
type McpOAuthCredentialStore struct {
	backend core.AuthStorageBackend
	lockDir string
}

// NewMcpOAuthCredentialStore constructs a credential store backed by backend (or
// `<agentDir>/mcp-auth.json` when omitted/nil). When a custom backend is provided,
// refreshes serialize in-process only unless WithLockDir is used.
func NewMcpOAuthCredentialStore(backend ...core.AuthStorageBackend) *McpOAuthCredentialStore {
	if len(backend) > 0 && backend[0] != nil {
		return &McpOAuthCredentialStore{backend: backend[0]}
	}
	agentDir := utils.GetAgentDir()
	return &McpOAuthCredentialStore{
		backend: core.NewFileAuthStorageBackend(filepath.Join(agentDir, "mcp-auth.json")),
		lockDir: agentDir,
	}
}

// NewMcpOAuthCredentialStoreWithLockDir constructs a credential store using a specific lock directory.
func NewMcpOAuthCredentialStoreWithLockDir(backend core.AuthStorageBackend, lockDir string) *McpOAuthCredentialStore {
	if backend == nil {
		return &McpOAuthCredentialStore{
			backend: core.NewFileAuthStorageBackend(filepath.Join(utils.GetAgentDir(), "mcp-auth.json")),
			lockDir: lockDir,
		}
	}
	return &McpOAuthCredentialStore{
		backend: backend,
		lockDir: lockDir,
	}
}

type oauthServerStore struct {
	store     *McpOAuthCredentialStore
	key       string
	legacyKey string
}

func (s *oauthServerStore) Load() (*mcpoauth.McpOAuthState, error) {
	res, err := s.store.backend.WithLock(func(current *string) (core.AuthStorageLockResult, error) {
		states := oauthParseStates(current)
		if existing, ok := states[s.key]; ok && existing != nil {
			return core.AuthStorageLockResult{Result: existing}, nil
		}
		legacy, hasLegacy := states[s.legacyKey]
		if !hasLegacy || legacy == nil {
			return core.AuthStorageLockResult{Result: (*mcpoauth.McpOAuthState)(nil)}, nil
		}
		states[s.key] = legacy
		delete(states, s.legacyKey)
		next := oauthSerializeStates(states)
		return core.AuthStorageLockResult{Result: legacy, Next: &next}, nil
	})
	if err != nil {
		return nil, err
	}
	st, _ := res.(*mcpoauth.McpOAuthState)
	return st, nil
}

func (s *oauthServerStore) Save(state *mcpoauth.McpOAuthState) error {
	return s.store.write(func(states oauthStoredStates) {
		states[s.key] = state
	})
}

func (s *oauthServerStore) WithRefreshLock(fn func() error) error {
	return s.store.withRefreshLock(s.key, fn)
}

var _ mcpoauth.McpOAuthServerStore = (*oauthServerStore)(nil)

// ForServer returns the scoped OAuth store for server name and URL.
func (s *McpOAuthCredentialStore) ForServer(name, serverUrl string) mcpoauth.McpOAuthServerStore {
	key, legacyKey := oauthStoreKeys(name, serverUrl)
	return &oauthServerStore{
		store:     s,
		key:       key,
		legacyKey: legacyKey,
	}
}

func (s *McpOAuthCredentialStore) withRefreshLock(key string, fn func() error) error {
	if s.lockDir == "" {
		return fn()
	}
	if err := os.MkdirAll(s.lockDir, 0o700); err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(key))
	hashPrefix := hex.EncodeToString(sum[:])[:16]
	lockTarget := filepath.Join(s.lockDir, "mcp-auth-refresh-"+hashPrefix)

	realpathFalse := false
	stale := oauthRefreshLockStaleMs
	retries := int(oauthRefreshLockWaitMs / oauthRefreshLockRetryMs)
	factor := 1.0
	retryMs := oauthRefreshLockRetryMs

	release, err := lockfile.Lock(context.Background(), lockTarget, &lockfile.Options{
		Realpath: &realpathFalse,
		Stale:    &stale,
		Retries: &lockfile.RetryOptions{
			Retries:    &retries,
			Factor:     &factor,
			MinTimeout: &retryMs,
			MaxTimeout: &retryMs,
		},
		OnCompromised: func(error) {},
	})
	if err != nil {
		var lockErr *lockfile.Error
		if errors.As(err, &lockErr) && lockErr.Code == "ELOCKED" {
			return mcpoauth.NewMcpRefreshLockBusyError()
		}
		return err
	}
	defer func() {
		if release != nil {
			_ = release()
		}
	}()
	return fn()
}

// Tokens returns the stored tokens for name/serverUrl without migrating legacy entries.
func (s *McpOAuthCredentialStore) Tokens(name, serverUrl string) *mcpoauth.OAuthTokens {
	key, legacyKey := oauthStoreKeys(name, serverUrl)
	res, err := s.backend.WithLock(func(current *string) (core.AuthStorageLockResult, error) {
		return core.AuthStorageLockResult{Result: oauthParseStates(current)}, nil
	})
	if err != nil {
		return nil
	}
	states, _ := res.(oauthStoredStates)
	if st, ok := states[key]; ok && st != nil {
		return st.Tokens
	}
	if st, ok := states[legacyKey]; ok && st != nil {
		return st.Tokens
	}
	return nil
}

// Remove deletes stored credentials for name/serverUrl and reports whether anything was removed.
func (s *McpOAuthCredentialStore) Remove(name, serverUrl string) bool {
	key, legacyKey := oauthStoreKeys(name, serverUrl)
	res, err := s.backend.WithLock(func(current *string) (core.AuthStorageLockResult, error) {
		states := oauthParseStates(current)
		var storedKey string
		if _, ok := states[key]; ok {
			storedKey = key
		} else if _, ok := states[legacyKey]; ok {
			storedKey = legacyKey
		} else {
			return core.AuthStorageLockResult{Result: false}, nil
		}
		delete(states, storedKey)
		next := oauthSerializeStates(states)
		return core.AuthStorageLockResult{Result: true, Next: &next}, nil
	})
	if err != nil {
		return false
	}
	removed, _ := res.(bool)
	return removed
}

func (s *McpOAuthCredentialStore) write(update func(states oauthStoredStates)) error {
	_, err := s.backend.WithLock(func(current *string) (core.AuthStorageLockResult, error) {
		states := oauthParseStates(current)
		update(states)
		next := oauthSerializeStates(states)
		return core.AuthStorageLockResult{Next: &next}, nil
	})
	return err
}

var oauthPiClientMetadataDocuments = mcpoauth.McpClientMetadataDocuments{
	BaseUrl: "https://pi.dev/oauth",
	ListsRedirectUrl: func(redirectUrl string) bool {
		return IsClientMetadataDocumentCallback(&redirectUrl)
	},
}

func oauthPiSettings(settings mcpoauth.McpOAuthSettings) mcpoauth.McpOAuthSettings {
	reg := settings.ClientRegistration
	auto := reg == "" || reg == "auto"
	if auto && utils.AppName != "pi" {
		settings.ClientRegistration = "dcr"
	}
	return settings
}

// CreateMcpAuthProviderOptions configures CreateMcpAuthProvider.
type CreateMcpAuthProviderOptions struct {
	ServerUrl   string
	Store       mcpoauth.McpOAuthServerStore
	Settings    func() mcpoauth.McpOAuthSettings
	OnChallenge func(challenge mcpoauth.OAuthChallenge)
	HTTPClient  *http.Client
}

// CreateMcpAuthProvider wraps mcpoauth.CreateMcpAuthProvider with pi's client identity.
func CreateMcpAuthProvider(options CreateMcpAuthProviderOptions) mcpoauth.McpAuthProvider {
	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = ai.NodeHttpProxyDefaultClient()
	}
	settingsFn := options.Settings
	if settingsFn == nil {
		settingsFn = func() mcpoauth.McpOAuthSettings { return mcpoauth.McpOAuthSettings{} }
	}
	return mcpoauth.CreateMcpAuthProvider(mcpoauth.McpAuthProviderOptions{
		ServerUrl:               options.ServerUrl,
		Store:                   options.Store,
		ClientName:              utils.AppName,
		ClientMetadataDocuments: &oauthPiClientMetadataDocuments,
		HTTPClient:              httpClient,
		OnChallenge:             options.OnChallenge,
		Settings: func() mcpoauth.McpOAuthSettings {
			return oauthPiSettings(settingsFn())
		},
	})
}

// McpSignInPrompt drives the interactive side of SignInMcpServer.
type McpSignInPrompt interface {
	ShowAuthorizationUrl(url *url.URL)
	PromptForRedirectUrl(ctx context.Context) (*string, error)
}

func oauthRenderCallbackPage(page mcpoauth.OAuthCallbackPage) string {
	switch p := page.(type) {
	case *mcpoauth.OAuthCallbackPageOk:
		return ai.OauthSuccessHtml("Signed in to the MCP server. You may now close this page.")
	case *mcpoauth.OAuthCallbackPageFailed:
		if p.Details != nil {
			return ai.OauthErrorHtml(p.Message, *p.Details)
		}
		return ai.OauthErrorHtml(p.Message)
	default:
		return ai.OauthErrorHtml("Authentication failed")
	}
}

// SignInMcpServerOptions configures SignInMcpServer.
type SignInMcpServerOptions struct {
	ServerUrl  string
	Store      mcpoauth.McpOAuthServerStore
	Settings   mcpoauth.McpOAuthSettings
	Challenge  *mcpoauth.OAuthChallenge
	Prompt     McpSignInPrompt
	Context    context.Context
	HTTPClient *http.Client
}

// SignInMcpServer runs the MCP OAuth sign-in flow against a loopback callback or pasted redirect URL.
func SignInMcpServer(options SignInMcpServerOptions) error {
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = ai.NodeHttpProxyDefaultClient()
	}
	settings := oauthPiSettings(options.Settings)

	loopback, err := mcpoauth.ListenForLoopbackSignIn(mcpoauth.ListenForLoopbackSignInOptions{
		ServerUrl:               options.ServerUrl,
		Store:                   options.Store,
		Settings:                settings,
		ClientMetadataDocuments: &oauthPiClientMetadataDocuments,
		RenderPage:              oauthRenderCallbackPage,
	})
	if err != nil {
		return err
	}
	defer func() {
		if loopback.Callback != nil {
			_ = loopback.Callback.Close()
		}
	}()

	signInOpts := mcpoauth.McpSignInOptions{
		McpOAuthClientOptions: mcpoauth.McpOAuthClientOptions{
			ServerUrl:               options.ServerUrl,
			Store:                   options.Store,
			Settings:                settings,
			ClientName:              utils.AppName,
			ClientMetadataDocuments: &oauthPiClientMetadataDocuments,
			HTTPClient:              httpClient,
		},
		RedirectUrl: loopback.RedirectUrl,
		Challenge:   options.Challenge,
		Context:     ctx,
	}

	authorization, err := mcpoauth.StartMcpSignIn(signInOpts)
	if err != nil {
		return err
	}
	if authorization.Kind == "authorized" {
		return nil
	}
	authUrl, err := url.Parse(authorization.AuthorizationUrl)
	if err != nil {
		return err
	}
	if options.Prompt != nil {
		options.Prompt.ShowAuthorizationUrl(authUrl)
	}

	promptFn := func(waitCtx context.Context) (*string, error) {
		<-waitCtx.Done()
		return nil, nil
	}
	if options.Prompt != nil {
		promptFn = options.Prompt.PromptForRedirectUrl
	}

	response, err := mcpoauth.WaitForLoopbackSignIn(mcpoauth.WaitForLoopbackSignInOptions{
		Callback:             loopback.Callback,
		Authorization:        authorization.Redirect(),
		PromptForRedirectUrl: promptFn,
		Context:              ctx,
	})
	if err != nil {
		return err
	}
	return mcpoauth.CompleteMcpSignIn(mcpoauth.CompleteMcpSignInOptions{
		McpSignInOptions: signInOpts,
		Authorization:    authorization.Redirect(),
		Response:         *response,
	})
}

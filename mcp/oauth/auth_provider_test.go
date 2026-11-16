// Ported from packages/mcp/test/auth-provider.test.ts (pi v1.5.2).

package oauth_test

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/mcp"
	"github.com/keejkrej/pi-go/mcp/internal/testkit"
	mcpoauth "github.com/keejkrej/pi-go/mcp/oauth"
)

const toapRoute = "https://app.example/mcp/callback"
const toapLoopback = "http://127.0.0.1:1/callback"

func toapStartRedirect(t *testing.T, options mcpoauth.McpSignInOptions) mcpoauth.McpSignInRedirect {
	t.Helper()
	started, err := mcpoauth.StartMcpSignIn(options)
	if err != nil {
		t.Fatal(err)
	}
	if started.Kind != "redirect" {
		t.Fatalf("expected redirect, got %q", started.Kind)
	}
	return started.Redirect()
}

func toapBrowse(t *testing.T, authorization mcpoauth.McpSignInRedirect) mcpoauth.McpAuthorizationResponse {
	t.Helper()
	loc, err := testkit.AuthorizeInBrowser(authorization.AuthorizationUrl)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	var iss *string
	if v := q.Get("iss"); v != "" {
		iss = &v
	}
	return mcpoauth.McpAuthorizationResponse{
		Code:  q.Get("code"),
		State: q.Get("state"),
		Iss:   iss,
	}
}

func toapSignIn(t *testing.T, options mcpoauth.McpSignInOptions) {
	t.Helper()
	auth := toapStartRedirect(t, options)
	resp := toapBrowse(t, auth)
	if err := mcpoauth.CompleteMcpSignIn(mcpoauth.CompleteMcpSignInOptions{
		McpSignInOptions: options,
		Authorization:    auth,
		Response:         resp,
	}); err != nil {
		t.Fatal(err)
	}
}

func toapUnauthorized(server *testkit.AuthorizationServer, token string) *mcp.UnauthorizedContext {
	u, _ := url.Parse(server.ServerUrl)
	return &mcp.UnauthorizedContext{
		Response:  &http.Response{StatusCode: http.StatusUnauthorized},
		ServerUrl: u,
		Token:     &token,
	}
}

func TestAuthProvider_RefreshesRejectedToken(t *testing.T) {
	server := testkit.StartAuthorizationServer(t, testkit.AuthorizationServerOptions{})
	store := testkit.NewMemoryStore()
	route := toapRoute
	signInOpts := mcpoauth.McpSignInOptions{
		McpOAuthClientOptions: mcpoauth.McpOAuthClientOptions{
			ServerUrl:  server.ServerUrl,
			Store:      store,
			ClientName: "app",
		},
		RedirectUrl: route,
	}
	toapSignIn(t, signInOpts)

	provider := mcpoauth.CreateMcpAuthProvider(mcpoauth.McpAuthProviderOptions{
		ServerUrl:   server.ServerUrl,
		Store:       store,
		Settings:    func() mcpoauth.McpOAuthSettings { return mcpoauth.McpOAuthSettings{} },
		ClientName:  "app",
		RedirectUrl: &route,
		OnChallenge: func(mcpoauth.OAuthChallenge) {},
	})
	if err := provider.OnUnauthorized(toapUnauthorized(server, "access-1")); err != nil {
		t.Fatal(err)
	}
	tok, err := provider.Token()
	if err != nil {
		t.Fatal(err)
	}
	if tok == nil || *tok != "access-2" {
		t.Fatalf("token = %v, want access-2", tok)
	}
	reqs := server.TokenRequests()
	if len(reqs) < 2 || reqs[1].Get("client_id") != "client-1" {
		t.Fatalf("tokenRequests = %#v", reqs)
	}
}

func TestAuthProvider_LeavesPendingSignInUsableWhenRefreshFails(t *testing.T) {
	server := testkit.StartAuthorizationServer(t, testkit.AuthorizationServerOptions{})
	store := testkit.NewMemoryStore()
	route := toapRoute
	clientOpts := mcpoauth.McpOAuthClientOptions{
		ServerUrl:  server.ServerUrl,
		Store:      store,
		ClientName: "app",
	}
	signInOpts := mcpoauth.McpSignInOptions{
		McpOAuthClientOptions: clientOpts,
		RedirectUrl:           route,
	}
	toapSignIn(t, signInOpts)

	insufficient := "insufficient_scope"
	more := "more"
	stepUp := mcpoauth.McpSignInOptions{
		McpOAuthClientOptions: clientOpts,
		RedirectUrl:           route,
		Challenge:             &mcpoauth.OAuthChallenge{Error: &insufficient, Scope: &more},
	}
	authorization := toapStartRedirect(t, stepUp)
	pending, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}

	server.RevokeRefreshTokens()
	provider := mcpoauth.CreateMcpAuthProvider(mcpoauth.McpAuthProviderOptions{
		ServerUrl:   server.ServerUrl,
		Store:       store,
		Settings:    func() mcpoauth.McpOAuthSettings { return mcpoauth.McpOAuthSettings{} },
		ClientName:  "app",
		RedirectUrl: &route,
		OnChallenge: func(mcpoauth.OAuthChallenge) {},
	})
	err = provider.OnUnauthorized(toapUnauthorized(server, "access-1"))
	var authReq *mcpoauth.McpOAuthAuthorizationRequiredError
	if !errors.As(err, &authReq) {
		t.Fatalf("expected McpOAuthAuthorizationRequiredError, got %v", err)
	}

	state, _ := store.Load()
	if state == nil || state.OauthState == nil || pending.OauthState == nil || *state.OauthState != *pending.OauthState {
		t.Fatalf("oauthState lost: %#v vs %#v", state, pending)
	}
	if state.CodeVerifier == nil || pending.CodeVerifier == nil || *state.CodeVerifier != *pending.CodeVerifier {
		t.Fatalf("codeVerifier lost")
	}
	if len(server.Registrations()) != 1 {
		t.Fatalf("registrations = %d, want 1", len(server.Registrations()))
	}
	if err := mcpoauth.CompleteMcpSignIn(mcpoauth.CompleteMcpSignInOptions{
		McpSignInOptions: stepUp,
		Authorization:    authorization,
		Response:         toapBrowse(t, authorization),
	}); err != nil {
		t.Fatal(err)
	}
	finalState, _ := store.Load()
	if finalState == nil || finalState.Tokens == nil || finalState.Tokens.AccessToken != "access-2" {
		t.Fatalf("final tokens = %#v", finalState)
	}
}

func TestAuthProvider_DoesNotRegisterClientToRefresh(t *testing.T) {
	server := testkit.StartAuthorizationServer(t, testkit.AuthorizationServerOptions{Cimd: true, IssParameter: true})
	store := testkit.NewMemoryStore()
	loopback := toapLoopback
	docs := &mcpoauth.McpClientMetadataDocuments{
		BaseUrl:          "https://app.example/oauth",
		ListsRedirectUrl: func(string) bool { return true },
	}
	toapSignIn(t, mcpoauth.McpSignInOptions{
		McpOAuthClientOptions: mcpoauth.McpOAuthClientOptions{
			ServerUrl:               server.ServerUrl,
			Store:                   store,
			ClientName:              "app",
			ClientMetadataDocuments: docs,
		},
		RedirectUrl: loopback,
	})
	st, _ := store.Load()
	if st.ClientInformation != nil {
		t.Fatalf("expected nil clientInformation with CIMD, got %#v", st.ClientInformation)
	}

	provider := mcpoauth.CreateMcpAuthProvider(mcpoauth.McpAuthProviderOptions{
		ServerUrl:   server.ServerUrl,
		Store:       store,
		Settings:    func() mcpoauth.McpOAuthSettings { return mcpoauth.McpOAuthSettings{} },
		ClientName:  "app",
		RedirectUrl: &loopback,
		OnChallenge: func(mcpoauth.OAuthChallenge) {},
	})
	err := provider.OnUnauthorized(toapUnauthorized(server, "access-1"))
	var authReq *mcpoauth.McpOAuthAuthorizationRequiredError
	if !errors.As(err, &authReq) {
		t.Fatalf("expected McpOAuthAuthorizationRequiredError, got %v", err)
	}
	if len(server.Registrations()) != 0 {
		t.Fatalf("registrations = %d, want 0", len(server.Registrations()))
	}
	if len(server.Authorizations()) != 1 {
		t.Fatalf("authorizations = %d, want 1", len(server.Authorizations()))
	}
}

func TestAuthProvider_RefreshesDocumentListingOnlyPassedRedirectUri(t *testing.T) {
	for _, clientReg := range []string{"auto", "cimd"} {
		t.Run(clientReg, func(t *testing.T) {
			server := testkit.StartAuthorizationServer(t, testkit.AuthorizationServerOptions{Cimd: true, IssParameter: true})
			store := testkit.NewMemoryStore()
			route := toapRoute
			docs := &mcpoauth.McpClientMetadataDocuments{
				BaseUrl:          "https://app.example/oauth",
				ListsRedirectUrl: func(redirectUrl string) bool { return redirectUrl == route },
			}
			settings := mcpoauth.McpOAuthSettings{ClientRegistration: clientReg}
			clientOpts := mcpoauth.McpOAuthClientOptions{
				ServerUrl:               server.ServerUrl,
				Store:                   store,
				Settings:                settings,
				ClientName:              "app",
				ClientMetadataDocuments: docs,
			}
			toapSignIn(t, mcpoauth.McpSignInOptions{McpOAuthClientOptions: clientOpts, RedirectUrl: route})
			st, _ := store.Load()
			if st.ClientInformation != nil {
				t.Fatalf("expected nil clientInformation, got %#v", st.ClientInformation)
			}

			provider := mcpoauth.CreateMcpAuthProvider(mcpoauth.McpAuthProviderOptions{
				ServerUrl:               server.ServerUrl,
				Store:                   store,
				Settings:                func() mcpoauth.McpOAuthSettings { return settings },
				ClientName:              "app",
				ClientMetadataDocuments: docs,
				RedirectUrl:             &route,
				OnChallenge:             func(mcpoauth.OAuthChallenge) {},
			})
			if err := provider.OnUnauthorized(toapUnauthorized(server, "access-1")); err != nil {
				t.Fatal(err)
			}
			tok, _ := provider.Token()
			if tok == nil || *tok != "access-2" {
				t.Fatalf("token = %v, want access-2", tok)
			}
			reqs := server.TokenRequests()
			if len(reqs) < 2 || reqs[1].Get("client_id") != "https://app.example/oauth/client.json" {
				t.Fatalf("tokenRequests = %#v", reqs)
			}
			if len(server.Authorizations()) != 1 {
				t.Fatalf("authorizations = %d, want 1", len(server.Authorizations()))
			}
		})
	}
}

func toapPendingSignIn(t *testing.T) (
	*testkit.AuthorizationServer,
	*testkit.MemoryStore,
	*mcpoauth.McpOAuthState,
	mcpoauth.McpAuthProvider,
) {
	t.Helper()
	server := testkit.StartAuthorizationServer(t, testkit.AuthorizationServerOptions{})
	store := testkit.NewMemoryStore()
	route := toapRoute
	clientOpts := mcpoauth.McpOAuthClientOptions{
		ServerUrl:  server.ServerUrl,
		Store:      store,
		ClientName: "app",
	}
	toapSignIn(t, mcpoauth.McpSignInOptions{McpOAuthClientOptions: clientOpts, RedirectUrl: route})
	insufficient := "insufficient_scope"
	more := "more"
	_ = toapStartRedirect(t, mcpoauth.McpSignInOptions{
		McpOAuthClientOptions: clientOpts,
		RedirectUrl:           route,
		Challenge:             &mcpoauth.OAuthChallenge{Error: &insufficient, Scope: &more},
	})
	pending, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	provider := mcpoauth.CreateMcpAuthProvider(mcpoauth.McpAuthProviderOptions{
		ServerUrl:   server.ServerUrl,
		Store:       store,
		Settings:    func() mcpoauth.McpOAuthSettings { return mcpoauth.McpOAuthSettings{} },
		ClientName:  "app",
		RedirectUrl: &route,
		OnChallenge: func(mcpoauth.OAuthChallenge) {},
	})
	return server, store, pending, provider
}

func TestAuthProvider_DropsRejectedClientAndTokensButKeepsPendingSignIn(t *testing.T) {
	server, store, pending, provider := toapPendingSignIn(t)
	server.FailNextRefresh("invalid_client", nil)
	err := provider.OnUnauthorized(toapUnauthorized(server, "access-1"))
	var authReq *mcpoauth.McpOAuthAuthorizationRequiredError
	if !errors.As(err, &authReq) {
		t.Fatalf("expected McpOAuthAuthorizationRequiredError, got %v", err)
	}
	state, _ := store.Load()
	if state == nil || state.OauthState == nil || *state.OauthState != *pending.OauthState {
		t.Fatalf("oauthState = %#v", state)
	}
	if state.CodeVerifier == nil || *state.CodeVerifier != *pending.CodeVerifier {
		t.Fatalf("codeVerifier = %#v", state)
	}
	if state.ClientInformation != nil || state.Tokens != nil {
		t.Fatalf("expected clientInformation and tokens cleared: %#v", state)
	}
	if len(server.Registrations()) != 1 {
		t.Fatalf("registrations = %d", len(server.Registrations()))
	}
}

func TestAuthProvider_KeepsClientStoredWhileRejectedRefreshRan(t *testing.T) {
	server, store, _, provider := toapPendingSignIn(t)
	server.FailNextRefresh("invalid_client", func() {
		state, _ := store.Load()
		if state != nil {
			state.ClientInformation = &mcpoauth.OAuthClientInformationFull{
				OAuthClientInformation: mcpoauth.OAuthClientInformation{ClientId: "client-new"},
				OAuthClientMetadata:    mcpoauth.OAuthClientMetadata{RedirectUris: []string{toapRoute}},
			}
			_ = store.Save(state)
		}
	})
	if err := provider.OnUnauthorized(toapUnauthorized(server, "access-1")); err != nil {
		t.Fatal(err)
	}
	state, _ := store.Load()
	info, ok := state.ClientInformation.(*mcpoauth.OAuthClientInformationFull)
	if !ok || info.ClientId != "client-new" {
		t.Fatalf("clientInformation = %#v", state.ClientInformation)
	}
	reqs := server.TokenRequests()
	if len(reqs) == 0 || reqs[len(reqs)-1].Get("client_id") != "client-new" {
		t.Fatalf("tokenRequests = %#v", reqs)
	}
	tok, _ := provider.Token()
	if tok == nil || *tok != "access-2" {
		t.Fatalf("token = %v", tok)
	}
}

func TestAuthProvider_KeepsTokensAndPendingSignInAfterServerError(t *testing.T) {
	server, store, pending, provider := toapPendingSignIn(t)
	server.FailNextRefresh("server_error", nil)
	err := provider.OnUnauthorized(toapUnauthorized(server, "access-1"))
	var authReq *mcpoauth.McpOAuthAuthorizationRequiredError
	if !errors.As(err, &authReq) {
		t.Fatalf("expected McpOAuthAuthorizationRequiredError, got %v", err)
	}
	state, _ := store.Load()
	if state.OauthState == nil || *state.OauthState != *pending.OauthState {
		t.Fatalf("oauthState mismatch")
	}
	if state.CodeVerifier == nil || *state.CodeVerifier != *pending.CodeVerifier {
		t.Fatalf("codeVerifier mismatch")
	}
	if state.Tokens == nil || state.Tokens.AccessToken != "access-1" {
		t.Fatalf("tokens = %#v", state.Tokens)
	}
}

func TestAuthProvider_KeepsTokensAndPendingSignInAfterNetworkError(t *testing.T) {
	server, store, pending, provider := toapPendingSignIn(t)
	unauth := toapUnauthorized(server, "access-1")
	unauth.Fetch = func(ctx context.Context, input string, init *mcp.McpRequestInit) (*http.Response, error) {
		if strings.HasSuffix(input, "/token") {
			return nil, errors.New("fetch failed")
		}
		var bodyReader = http.NoBody
		if init != nil && init.Body != nil {
			req, err := http.NewRequestWithContext(ctx, init.Method, input, strings.NewReader(string(init.Body)))
			if err != nil {
				return nil, err
			}
			if init.Headers != nil {
				req.Header = init.Headers.Clone()
			}
			return http.DefaultClient.Do(req)
		}
		_ = bodyReader
		method := http.MethodGet
		if init != nil && init.Method != "" {
			method = init.Method
		}
		req, err := http.NewRequestWithContext(ctx, method, input, nil)
		if err != nil {
			return nil, err
		}
		if init != nil && init.Headers != nil {
			req.Header = init.Headers.Clone()
		}
		return http.DefaultClient.Do(req)
	}
	err := provider.OnUnauthorized(unauth)
	var authReq *mcpoauth.McpOAuthAuthorizationRequiredError
	if !errors.As(err, &authReq) {
		t.Fatalf("expected McpOAuthAuthorizationRequiredError, got %v", err)
	}
	state, _ := store.Load()
	if state.OauthState == nil || *state.OauthState != *pending.OauthState {
		t.Fatalf("oauthState mismatch")
	}
	if state.CodeVerifier == nil || *state.CodeVerifier != *pending.CodeVerifier {
		t.Fatalf("codeVerifier mismatch")
	}
	if state.Tokens == nil || state.Tokens.AccessToken != "access-1" {
		t.Fatalf("tokens = %#v", state.Tokens)
	}
}

// Ported from packages/mcp/test/oauth.test.ts (pi v1.5.2).

package oauth_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/mcp"
	"github.com/keejkrej/pi-go/mcp/internal/testkit"
	mcpoauth "github.com/keejkrej/pi-go/mcp/oauth"
	mcpprotocol "github.com/keejkrej/pi-go/mcp/protocol"
)

func toauthWriteJson(w http.ResponseWriter, v any) {
	b, _ := jsonx.Marshal(v)
	_, _ = w.Write(b)
}

type toauthProvider struct {
	redirectUrl      string
	clientMetadata   mcpoauth.OAuthClientMetadata
	client           mcpoauth.OAuthClientInformationMixed
	tokenSet         *mcpoauth.OAuthTokens
	verifier         string
	discovery        *mcpoauth.OAuthDiscoveryState
	authorizationUrl *url.URL
}

func toauthNewProvider(redirectUrl string) *toauthProvider {
	authMethod := "none"
	clientName := "pi-mcp-test"
	return &toauthProvider{
		redirectUrl: redirectUrl,
		clientMetadata: mcpoauth.OAuthClientMetadata{
			RedirectUris:            []string{redirectUrl},
			ClientName:              &clientName,
			GrantTypes:              []string{"authorization_code", "refresh_token"},
			ResponseTypes:           []string{"code"},
			TokenEndpointAuthMethod: &authMethod,
		},
	}
}

func (p *toauthProvider) RedirectUrl() string                          { return p.redirectUrl }
func (p *toauthProvider) ClientMetadata() mcpoauth.OAuthClientMetadata { return p.clientMetadata }
func (p *toauthProvider) State() (string, error)                       { return "expected-state", nil }
func (p *toauthProvider) ClientInformation() (mcpoauth.OAuthClientInformationMixed, error) {
	return p.client, nil
}
func (p *toauthProvider) SaveClientInformation(info mcpoauth.OAuthClientInformationMixed) error {
	p.client = info
	return nil
}
func (p *toauthProvider) Tokens() (*mcpoauth.OAuthTokens, error)   { return p.tokenSet, nil }
func (p *toauthProvider) SaveTokens(t *mcpoauth.OAuthTokens) error { p.tokenSet = t; return nil }
func (p *toauthProvider) RedirectToAuthorization(u *url.URL) error {
	p.authorizationUrl = u
	return nil
}
func (p *toauthProvider) SaveCodeVerifier(v string) error { p.verifier = v; return nil }
func (p *toauthProvider) CodeVerifier() (string, error) {
	if p.verifier == "" {
		return "", errors.New("Missing code verifier")
	}
	return p.verifier, nil
}
func (p *toauthProvider) InvalidateCredentials(kind mcpoauth.OAuthInvalidateCredentialsKind) error {
	switch kind {
	case mcpoauth.OAuthInvalidateCredentialsKindAll:
		p.client = nil
		p.tokenSet = nil
		p.verifier = ""
		p.discovery = nil
	case mcpoauth.OAuthInvalidateCredentialsKindClient:
		p.client = nil
	case mcpoauth.OAuthInvalidateCredentialsKindTokens:
		p.tokenSet = nil
	case mcpoauth.OAuthInvalidateCredentialsKindVerifier:
		p.verifier = ""
	case mcpoauth.OAuthInvalidateCredentialsKindDiscovery:
		p.discovery = nil
	}
	return nil
}
func (p *toauthProvider) SaveDiscoveryState(st *mcpoauth.OAuthDiscoveryState) error {
	p.discovery = st
	return nil
}
func (p *toauthProvider) DiscoveryState() (*mcpoauth.OAuthDiscoveryState, error) {
	return p.discovery, nil
}

func TestOAuth_DiscoversRegistersAuthorizesWithPkceAndRefreshesOn401(t *testing.T) {
	var mu sync.Mutex
	var expectedChallenge string
	var refreshes int

	origin := testkit.Listen(t, func(w http.ResponseWriter, r *http.Request, serverOrigin string) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource/mcp":
			w.Header().Set("Content-Type", "application/json")
			toauthWriteJson(w, map[string]any{
				"resource":              serverOrigin + "/mcp",
				"authorization_servers": []string{serverOrigin},
				"scopes_supported":      []string{"org:read"},
			})
			return
		case "/.well-known/oauth-authorization-server":
			w.Header().Set("Content-Type", "application/json")
			toauthWriteJson(w, map[string]any{
				"issuer":                                serverOrigin,
				"authorization_endpoint":                serverOrigin + "/authorize",
				"token_endpoint":                        serverOrigin + "/token",
				"registration_endpoint":                 serverOrigin + "/register",
				"response_types_supported":              []string{"code"},
				"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
				"token_endpoint_auth_methods_supported": []string{"none"},
				"code_challenge_methods_supported":      []string{"S256"},
			})
			return
		case "/register":
			body, _ := testkit.ReadBody(r)
			meta, _ := jsonx.ParseString(body)
			obj := meta.(*jsonx.Object)
			obj.Set("client_id", "test-client")
			obj.Set("client_secret", "")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			raw, _ := jsonx.Marshal(obj)
			_, _ = w.Write(raw)
			return
		case "/authorize":
			q := r.URL.Query()
			mu.Lock()
			expectedChallenge = q.Get("code_challenge")
			mu.Unlock()
			redir, _ := url.Parse(q.Get("redirect_uri"))
			rq := redir.Query()
			rq.Set("code", "test-code")
			rq.Set("state", q.Get("state"))
			redir.RawQuery = rq.Encode()
			w.Header().Set("Location", redir.String())
			w.WriteHeader(http.StatusFound)
			return
		case "/token":
			body, _ := testkit.ReadBody(r)
			form, _ := url.ParseQuery(body)
			if form.Get("grant_type") == "refresh_token" {
				mu.Lock()
				refreshes++
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"access_token":"refreshed-token","token_type":"Bearer","refresh_token":"","expires_in":null}`))
				return
			}
			sum := sha256.Sum256([]byte(form.Get("code_verifier")))
			chal := base64.RawURLEncoding.EncodeToString(sum[:])
			mu.Lock()
			exp := expectedChallenge
			mu.Unlock()
			if form.Get("code") != "test-code" || chal != exp {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"first-token","refresh_token":"refresh-token","token_type":"Bearer","scope":""}`))
			return
		}
		if r.URL.Path != "/mcp" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusOK)
			return
		}
		auth := r.Header.Get("Authorization")
		if auth != "Bearer first-token" && auth != "Bearer refreshed-token" {
			_, _ = testkit.ReadBody(r)
			w.Header().Set("Www-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s/.well-known/oauth-protected-resource/mcp", scope=""`, serverOrigin))
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte("Unauthorized"))
			return
		}
		body, _ := testkit.ReadBody(r)
		v, _ := jsonx.ParseString(body)
		msg := v.(*jsonx.Object)
		idVal, hasId := msg.Get("id")
		if !hasId {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		mVal, _ := msg.Get("method")
		var res any
		if mVal == "initialize" {
			res = jsonx.ObjectOf(
				"protocolVersion", "2025-06-18",
				"capabilities", jsonx.ObjectOf("tools", jsonx.ObjectOf()),
				"serverInfo", jsonx.ObjectOf("name", "oauth-test", "version", "1.0.0"),
			)
		} else {
			res = jsonx.ObjectOf("tools", []any{jsonx.ObjectOf("name", "issues", "inputSchema", jsonx.ObjectOf("type", "object"))})
		}
		w.Header().Set("Content-Type", "application/json")
		raw, _ := jsonx.Marshal(jsonx.ObjectOf("jsonrpc", "2.0", "id", idVal, "result", res))
		_, _ = w.Write(raw)
	})

	cb, err := mcpoauth.OAuthCallbackServerListen(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cb.Close() }()

	provider := toauthNewProvider(cb.RedirectUrl)
	staleHeaders := toauthHeaders("Authorization", "Bearer caller-supplied-stale-token")
	openGet := false

	tr1, err := mcp.NewStreamableHttpTransport(&mcp.StreamableHttpTransportOptions{
		Url:           origin + "/mcp",
		Headers:       staleHeaders,
		AuthProvider:  mcpoauth.AdaptOAuthProvider(provider),
		OpenGetStream: &openGet,
	})
	if err != nil {
		t.Fatal(err)
	}
	firstClient := mcp.NewMcpClient(&mcp.McpClientOptions{Implementation: mcpprotocol.Implementation{Name: "oauth-test", Version: "1.0.0"}})
	_, err = firstClient.Connect(tr1)
	var authReq *mcpoauth.McpOAuthAuthorizationRequiredError
	if !errors.As(err, &authReq) {
		t.Fatalf("expected McpOAuthAuthorizationRequiredError, got %v", err)
	}
	if provider.authorizationUrl == nil || provider.authorizationUrl.Query().Get("scope") != "org:read" || provider.authorizationUrl.Query().Get("resource") != origin+"/mcp" {
		t.Fatalf("authorizationUrl = %v", provider.authorizationUrl)
	}

	cbCh := make(chan *mcpoauth.OAuthCallback, 1)
	go func() {
		c, _ := cb.WaitForCallback("expected-state")
		cbCh <- c
	}()
	time.Sleep(15 * time.Millisecond)
	loc, err := testkit.AuthorizeInBrowser(provider.authorizationUrl.String())
	if err != nil {
		t.Fatal(err)
	}
	r2, err := http.Get(loc)
	if err != nil {
		t.Fatal(err)
	}
	_ = r2.Body.Close()
	cbRes := <-cbCh

	flowRes, err := mcpoauth.AuthorizeMcp(provider, &mcpoauth.OAuthFlowOptions{
		ServerUrl:         origin + "/mcp",
		AuthorizationCode: &cbRes.Code,
	})
	if err != nil || flowRes != mcpoauth.OAuthFlowResultAuthorized {
		t.Fatalf("flowRes = %q, err = %v", flowRes, err)
	}

	tr2, _ := mcp.NewStreamableHttpTransport(&mcp.StreamableHttpTransportOptions{
		Url:           origin + "/mcp",
		Headers:       staleHeaders,
		AuthProvider:  mcpoauth.AdaptOAuthProvider(provider),
		OpenGetStream: &openGet,
	})
	client := mcp.NewMcpClient(&mcp.McpClientOptions{Implementation: mcpprotocol.Implementation{Name: "oauth-test", Version: "1.0.0"}})
	if _, err := client.Connect(tr2); err != nil {
		t.Fatal(err)
	}
	tools, err := client.ListTools(t.Context(), nil)
	if err != nil || len(tools) != 1 || tools[0].Name != "issues" {
		t.Fatalf("tools = %#v, err = %v", tools, err)
	}
	_ = client.Close()

	provider.tokenSet.AccessToken = "stale-token"
	tr3, _ := mcp.NewStreamableHttpTransport(&mcp.StreamableHttpTransportOptions{
		Url:           origin + "/mcp",
		Headers:       staleHeaders,
		AuthProvider:  mcpoauth.AdaptOAuthProvider(provider),
		OpenGetStream: &openGet,
	})
	refreshedClient := mcp.NewMcpClient(&mcp.McpClientOptions{Implementation: mcpprotocol.Implementation{Name: "oauth-test", Version: "1.0.0"}})
	if _, err := refreshedClient.Connect(tr3); err != nil {
		t.Fatal(err)
	}
	_ = refreshedClient.Close()

	wantRT := "refresh-token"
	wantScope := "org:read"
	wantTokens := &mcpoauth.OAuthTokens{
		AccessToken:  "refreshed-token",
		RefreshToken: &wantRT,
		TokenType:    "Bearer",
		Scope:        &wantScope,
	}
	if !reflect.DeepEqual(provider.tokenSet, wantTokens) {
		t.Fatalf("tokenSet = %#v, want %#v", provider.tokenSet, wantTokens)
	}
	mu.Lock()
	gotRef := refreshes
	mu.Unlock()
	if gotRef != 1 {
		t.Fatalf("refreshes = %d, want 1", gotRef)
	}
}

func TestOAuth_SharesOneRefreshBetweenConcurrent401sWithRotatingRefreshTokens(t *testing.T) {
	var mu sync.Mutex
	var grants []string
	origin := testkit.Listen(t, func(w http.ResponseWriter, r *http.Request, serverOrigin string) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource/mcp":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"resource":"` + serverOrigin + `/mcp","authorization_servers":["not a url"]}`))
			return
		case "/.well-known/oauth-authorization-server":
			w.Header().Set("Content-Type", "application/json")
			toauthWriteJson(w, map[string]any{
				"issuer":                   serverOrigin,
				"authorization_endpoint":   serverOrigin + "/authorize",
				"token_endpoint":           serverOrigin + "/token",
				"response_types_supported": []string{"code"},
			})
			return
		case "/token":
			body, _ := testkit.ReadBody(r)
			form, _ := url.ParseQuery(body)
			rt := form.Get("refresh_token")
			mu.Lock()
			grants = append(grants, rt)
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			if rt != "r1" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
				return
			}
			time.Sleep(20 * time.Millisecond)
			_, _ = w.Write([]byte(`{"access_token":"a2","refresh_token":"r2","token_type":"Bearer","expires_in":3600}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})

	store := mcpoauth.NewMemoryOAuthStateStore()
	cid := "client"
	provider, err := mcpoauth.NewMcpOAuthProvider(&mcpoauth.McpOAuthProviderOptions{
		ServerUrl:      origin + "/mcp",
		RedirectUrl:    "http://127.0.0.1/callback",
		ClientMetadata: mcpoauth.OAuthClientMetadata{ClientName: toauthPtr("test")},
		ClientId:       &cid,
		Store:          store,
		OnRedirect:     func(*url.URL) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	rt1 := "r1"
	if err := provider.SaveTokens(&mcpoauth.OAuthTokens{AccessToken: "a1", RefreshToken: &rt1, TokenType: "Bearer"}); err != nil {
		t.Fatal(err)
	}
	auth := mcpoauth.AdaptOAuthProvider(provider)
	authUnauth := auth.(mcp.AuthProviderUnauthorized)
	u, _ := url.Parse(origin + "/mcp")
	mkUnauth := func() *mcp.UnauthorizedContext {
		tok := "a1"
		return &mcp.UnauthorizedContext{
			Response:  &http.Response{StatusCode: http.StatusUnauthorized, Header: http.Header{"Www-Authenticate": {"Bearer"}}},
			ServerUrl: u,
			Token:     &tok,
		}
	}

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- authUnauth.OnUnauthorized(mkUnauth())
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := authUnauth.OnUnauthorized(mkUnauth()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	gotGrants := append([]string(nil), grants...)
	mu.Unlock()
	if !reflect.DeepEqual(gotGrants, []string{"r1"}) {
		t.Fatalf("grants = %#v, want [r1]", gotGrants)
	}
	tok, _ := auth.Token()
	if tok == nil || *tok != "a2" {
		t.Fatalf("token = %v, want a2", tok)
	}
	st, _ := store.Load()
	if st == nil || st.Tokens == nil || st.Tokens.RefreshToken == nil || *st.Tokens.RefreshToken != "r2" {
		t.Fatalf("state = %#v", st)
	}
	if st.TokensExpireAt == nil || *st.TokensExpireAt <= time.Now().UnixMilli()+3_500_000 {
		t.Fatalf("tokensExpireAt = %v", st.TokensExpireAt)
	}
}

func TestOAuth_AsksForAuthorizationInsteadOfRefreshingOnInsufficientScope(t *testing.T) {
	provider := toauthNewProvider("http://127.0.0.1/callback")
	provider.client = &mcpoauth.OAuthClientInformation{ClientId: "client"}
	rt := "r1"
	sc := "repo read:org"
	provider.tokenSet = &mcpoauth.OAuthTokens{AccessToken: "a1", RefreshToken: &rt, TokenType: "Bearer", Scope: &sc}

	origin := testkit.Listen(t, func(w http.ResponseWriter, r *http.Request, serverOrigin string) {
		if r.URL.Path == "/.well-known/oauth-authorization-server" {
			w.Header().Set("Content-Type", "application/json")
			toauthWriteJson(w, map[string]any{
				"issuer":                   serverOrigin,
				"authorization_endpoint":   serverOrigin + "/authorize",
				"token_endpoint":           serverOrigin + "/token",
				"response_types_supported": []string{"code"},
			})
			return
		}
		if r.URL.Path == "/token" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	auth := mcpoauth.AdaptOAuthProvider(provider).(mcp.AuthProviderUnauthorized)
	u, _ := url.Parse(origin + "/mcp")
	tok := "a1"
	err := auth.OnUnauthorized(&mcp.UnauthorizedContext{
		Response: &http.Response{
			StatusCode: http.StatusForbidden,
			Header:     http.Header{"Www-Authenticate": {`Bearer error="insufficient_scope", scope="repo admin"`}},
		},
		ServerUrl: u,
		Token:     &tok,
	})
	var authReq *mcpoauth.McpOAuthAuthorizationRequiredError
	if !errors.As(err, &authReq) {
		t.Fatalf("expected McpOAuthAuthorizationRequiredError, got %v", err)
	}
	if provider.authorizationUrl == nil || provider.authorizationUrl.Query().Get("scope") != "repo read:org admin" {
		t.Fatalf("scope = %v", provider.authorizationUrl)
	}
	if provider.tokenSet == nil || provider.tokenSet.AccessToken != "a1" {
		t.Fatalf("expected working token kept, got %#v", provider.tokenSet)
	}
}

func TestOAuth_BindsPersistedCredentialsToExactMcpServerUrl(t *testing.T) {
	store := mcpoauth.NewMemoryOAuthStateStore()
	first, _ := mcpoauth.NewMcpOAuthProvider(&mcpoauth.McpOAuthProviderOptions{
		ServerUrl:      "https://one.example/mcp",
		RedirectUrl:    "http://127.0.0.1/callback",
		ClientMetadata: mcpoauth.OAuthClientMetadata{ClientName: toauthPtr("test")},
		Store:          store,
		OnRedirect:     func(*url.URL) error { return nil },
	})
	_ = first.SaveTokens(&mcpoauth.OAuthTokens{AccessToken: "secret", TokenType: "Bearer"})
	tok, _ := first.Tokens()
	if tok == nil || tok.AccessToken != "secret" {
		t.Fatalf("first tokens = %#v", tok)
	}

	second, _ := mcpoauth.NewMcpOAuthProvider(&mcpoauth.McpOAuthProviderOptions{
		ServerUrl:      "https://two.example/mcp",
		RedirectUrl:    "http://127.0.0.1/callback",
		ClientMetadata: mcpoauth.OAuthClientMetadata{ClientName: toauthPtr("test")},
		Store:          store,
		OnRedirect:     func(*url.URL) error { return nil },
	})
	tok2, _ := second.Tokens()
	if tok2 != nil {
		t.Fatalf("expected nil tokens for different serverUrl, got %#v", tok2)
	}
}

func TestOAuth_RegistersWithApplicationTypeDerivedFromRedirectUris(t *testing.T) {
	var mu sync.Mutex
	var bodies []*jsonx.Object
	origin := testkit.Listen(t, func(w http.ResponseWriter, r *http.Request, _ string) {
		body, _ := testkit.ReadBody(r)
		v, _ := jsonx.ParseString(body)
		obj := v.(*jsonx.Object)
		mu.Lock()
		bodies = append(bodies, obj)
		mu.Unlock()
		obj.Set("client_id", "client")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		raw, _ := jsonx.Marshal(obj)
		_, _ = w.Write(raw)
	})
	register := func(uris []string, appType *string) {
		_, err := mcpoauth.RegisterClient(origin, &mcpoauth.RegisterClientOptions{
			ClientMetadata: mcpoauth.OAuthClientMetadata{
				RedirectUris:    uris,
				ApplicationType: appType,
			},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	register([]string{"http://127.0.0.1:1234/callback"}, nil)
	register([]string{"http://[::1]/callback"}, nil)
	register([]string{"com.example.app:/callback"}, nil)
	register([]string{"https://app.example/callback"}, nil)
	register([]string{"http://localhost/callback"}, toauthPtr("web"))

	var got []string
	for _, b := range bodies {
		v, _ := b.Get("application_type")
		got = append(got, fmt.Sprint(v))
	}
	want := []string{"native", "native", "native", "web", "web"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("application_types = %#v, want %#v", got, want)
	}
}

func TestOAuth_RejectsAuthorizationMetadataWithMismatchedIssuer(t *testing.T) {
	origin := testkit.Listen(t, func(w http.ResponseWriter, r *http.Request, serverOrigin string) {
		if r.URL.Path == "/.well-known/oauth-authorization-server" {
			w.Header().Set("Content-Type", "application/json")
			toauthWriteJson(w, map[string]any{
				"issuer":                   "https://attacker.example",
				"authorization_endpoint":   serverOrigin + "/authorize",
				"token_endpoint":           serverOrigin + "/token",
				"response_types_supported": []string{"code"},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	_, err := mcpoauth.DiscoverAuthorizationServerMetadata(origin, nil)
	var mismatch *mcpoauth.OAuthIssuerMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("expected OAuthIssuerMismatchError, got %v", err)
	}
}

func TestOAuth_RejectsNonHttpAuthorizationEndpoints(t *testing.T) {
	var mu sync.Mutex
	var endpoint string
	origin := testkit.Listen(t, func(w http.ResponseWriter, _ *http.Request, serverOrigin string) {
		mu.Lock()
		ep := endpoint
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		toauthWriteJson(w, map[string]any{
			"issuer":                   serverOrigin,
			"authorization_endpoint":   ep,
			"token_endpoint":           serverOrigin + "/token",
			"response_types_supported": []string{"code"},
		})
	})
	for _, val := range []string{"file://attacker.example/share/x.exe", "search-ms:query=x", "ms-msdt:/id"} {
		mu.Lock()
		endpoint = val
		mu.Unlock()
		_, err := mcpoauth.DiscoverAuthorizationServerMetadata(origin, nil)
		if err == nil || !strings.Contains(err.Error(), "Invalid authorization endpoint") {
			t.Fatalf("discover(%q) err = %v", val, err)
		}
		_, _, err = mcpoauth.StartAuthorization(origin, &mcpoauth.StartAuthorizationOptions{
			Metadata: &mcpoauth.AuthorizationServerMetadata{
				Issuer:                 origin,
				AuthorizationEndpoint:  val,
				TokenEndpoint:          origin + "/token",
				ResponseTypesSupported: []string{"code"},
			},
			ClientInformation: &mcpoauth.OAuthClientInformation{ClientId: "client"},
			RedirectUrl:       "http://127.0.0.1/callback",
		})
		if err == nil || !strings.Contains(err.Error(), "Invalid authorization endpoint") {
			t.Fatalf("startAuthorization(%q) err = %v", val, err)
		}
	}
}

func TestOAuth_UsesConfiguredAuthorizationServerMetadataDocumentAsIs(t *testing.T) {
	origin := testkit.Listen(t, func(w http.ResponseWriter, r *http.Request, serverOrigin string) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource/mcp":
			toauthWriteJson(w, map[string]any{
				"resource":              serverOrigin + "/mcp",
				"authorization_servers": []string{serverOrigin},
			})
		case "/idp/metadata.json":
			toauthWriteJson(w, map[string]any{
				"issuer":                   "https://idp.example",
				"authorization_endpoint":   serverOrigin + "/idp/authorize",
				"token_endpoint":           serverOrigin + "/idp/token",
				"response_types_supported": []string{"code"},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	provider := toauthNewProvider("http://127.0.0.1/callback")
	provider.client = &mcpoauth.OAuthClientInformation{ClientId: "client"}
	metaURL, _ := url.Parse(origin + "/idp/metadata.json")
	opts := &mcpoauth.OAuthFlowOptions{
		ServerUrl:                      origin + "/mcp",
		AuthorizationServerMetadataUrl: metaURL,
	}
	res, err := mcpoauth.AuthorizeMcp(provider, opts)
	if err != nil || res != mcpoauth.OAuthFlowResultRedirect {
		t.Fatalf("res=%q err=%v", res, err)
	}
	if provider.authorizationUrl.Scheme+"://"+provider.authorizationUrl.Host+provider.authorizationUrl.Path != origin+"/idp/authorize" {
		t.Fatalf("authUrl = %v", provider.authorizationUrl)
	}
	if provider.authorizationUrl.Query().Get("resource") != origin+"/mcp" {
		t.Fatalf("resource = %s", provider.authorizationUrl.Query().Get("resource"))
	}

	insecureURL, _ := url.Parse("http://idp.example/metadata.json")
	_, err = mcpoauth.AuthorizeMcp(provider, &mcpoauth.OAuthFlowOptions{
		ServerUrl:                      origin + "/mcp",
		AuthorizationServerMetadataUrl: insecureURL,
	})
	var insecureErr *mcpoauth.OAuthInsecureEndpointError
	if !errors.As(err, &insecureErr) {
		t.Fatalf("expected OAuthInsecureEndpointError, got %v", err)
	}
}

func TestOAuth_ExchangesCodeOnlyWhenIssMatchesAuthorizationServer(t *testing.T) {
	var mu sync.Mutex
	var codes []string
	origin := testkit.Listen(t, func(w http.ResponseWriter, r *http.Request, _ string) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		body, _ := testkit.ReadBody(r)
		form, _ := url.ParseQuery(body)
		mu.Lock()
		codes = append(codes, form.Get("code"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"token","token_type":"Bearer"}`))
	})
	exchange := func(code string, iss *string, issParamSupported bool, hasMeta bool) (mcpoauth.OAuthFlowResult, error) {
		p := toauthNewProvider("http://127.0.0.1/callback")
		p.client = &mcpoauth.OAuthClientInformation{ClientId: "client"}
		p.verifier = "verifier"
		var md *mcpoauth.AuthorizationServerMetadata
		if hasMeta {
			md = &mcpoauth.AuthorizationServerMetadata{
				Issuer:                 origin,
				AuthorizationEndpoint:  origin + "/authorize",
				TokenEndpoint:          origin + "/token",
				ResponseTypesSupported: []string{"code"},
				AuthorizationResponseIssParameterSupported: &issParamSupported,
			}
		}
		p.discovery = &mcpoauth.OAuthDiscoveryState{
			AuthorizationServerUrl:      origin,
			AuthorizationServerMetadata: md,
		}
		return mcpoauth.AuthorizeMcp(p, &mcpoauth.OAuthFlowOptions{
			ServerUrl:         origin + "/mcp",
			AuthorizationCode: &code,
			Iss:               iss,
		})
	}

	var mismatch *mcpoauth.OAuthIssuerMismatchError
	if _, err := exchange("other", toauthPtr("https://attacker.example"), false, true); !errors.As(err, &mismatch) {
		t.Fatalf("expected OAuthIssuerMismatchError, got %v", err)
	}
	if _, err := exchange("missing", nil, true, true); !errors.As(err, &mismatch) {
		t.Fatalf("expected OAuthIssuerMismatchError, got %v", err)
	}
	if res, err := exchange("matching", &origin, true, true); err != nil || res != mcpoauth.OAuthFlowResultAuthorized {
		t.Fatalf("matching: res=%q err=%v", res, err)
	}
	if res, err := exchange("omitted", nil, false, true); err != nil || res != mcpoauth.OAuthFlowResultAuthorized {
		t.Fatalf("omitted: res=%q err=%v", res, err)
	}
	if _, err := exchange("unverifiable", toauthPtr("https://attacker.example"), false, false); !errors.As(err, &mismatch) {
		t.Fatalf("expected OAuthIssuerMismatchError, got %v", err)
	}
	if res, err := exchange("no-metadata", nil, false, false); err != nil || res != mcpoauth.OAuthFlowResultAuthorized {
		t.Fatalf("no-metadata: res=%q err=%v", res, err)
	}
	if !reflect.DeepEqual(codes, []string{"matching", "omitted", "no-metadata"}) {
		t.Fatalf("codes = %#v", codes)
	}
}

func TestOAuth_StopsWhenContextCancelsWithoutFallbackRedirect(t *testing.T) {
	var mu sync.Mutex
	var stalled []string
	hitCh := make(chan struct{}, 10)
	done := make(chan struct{})
	defer close(done)

	origin := testkit.Listen(t, func(_ http.ResponseWriter, r *http.Request, _ string) {
		mu.Lock()
		stalled = append(stalled, r.URL.Path)
		mu.Unlock()
		hitCh <- struct{}{}
		select {
		case <-r.Context().Done():
		case <-done:
		}
	})
	run := func(p *toauthProvider) string {
		ctx, cancel := context.WithCancel(t.Context())
		errCh := make(chan error, 1)
		go func() {
			_, err := mcpoauth.AuthorizeMcp(p, &mcpoauth.OAuthFlowOptions{
				ServerUrl: origin + "/mcp",
				Context:   ctx,
			})
			errCh <- err
		}()
		<-hitCh
		cancel()
		err := <-errCh
		if err == nil || (!errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "Abort")) {
			t.Fatalf("expected cancellation error, got %v", err)
		}
		if p.authorizationUrl != nil {
			t.Fatalf("expected no redirect on cancel, got %v", p.authorizationUrl)
		}
		mu.Lock()
		last := stalled[len(stalled)-1]
		mu.Unlock()
		return last
	}

	if got := run(toauthNewProvider("http://127.0.0.1/callback")); got != "/.well-known/oauth-protected-resource/mcp" {
		t.Fatalf("discovery stalled path = %q", got)
	}

	refreshing := toauthNewProvider("http://127.0.0.1/callback")
	refreshing.client = &mcpoauth.OAuthClientInformation{ClientId: "client"}
	refreshing.tokenSet = &mcpoauth.OAuthTokens{AccessToken: "a1", RefreshToken: toauthPtr("r1"), TokenType: "Bearer"}
	refreshing.discovery = &mcpoauth.OAuthDiscoveryState{
		AuthorizationServerUrl: origin,
		AuthorizationServerMetadata: &mcpoauth.AuthorizationServerMetadata{
			Issuer:                 origin,
			AuthorizationEndpoint:  origin + "/authorize",
			TokenEndpoint:          origin + "/token",
			ResponseTypesSupported: []string{"code"},
		},
	}
	if got := run(refreshing); got != "/token" {
		t.Fatalf("refresh stalled path = %q", got)
	}
}

func TestOAuth_CallbackServerPages(t *testing.T) {
	t.Run("renders plain text by default", func(t *testing.T) {
		cb, err := mcpoauth.OAuthCallbackServerListen(nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = cb.Close() }()
		pendingCh := make(chan *mcpoauth.OAuthCallback, 1)
		go func() {
			c, _ := cb.WaitForCallback("s1")
			pendingCh <- c
		}()
		time.Sleep(15 * time.Millisecond)
		resp, err := http.Get(cb.RedirectUrl + "?code=abc&state=s1")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		if got := resp.Header.Get("Content-Type"); got != "text/plain; charset=utf-8" {
			t.Fatalf("content-type = %q", got)
		}
		body, _ := testkit.ReadBody(&http.Request{Body: resp.Body})
		if body != "Authorization complete. You may close this window." {
			t.Fatalf("body = %q", body)
		}
		got := <-pendingCh
		if got == nil || got.Code != "abc" {
			t.Fatalf("callback = %#v", got)
		}
	})

	t.Run("closes while browser keeps unused connection open", func(t *testing.T) {
		cb, err := mcpoauth.OAuthCallbackServerListen(nil)
		if err != nil {
			t.Fatal(err)
		}
		u, _ := url.Parse(cb.RedirectUrl)
		conn, err := net.Dial("tcp", u.Host)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = conn.Close() }()

		pendingCh := make(chan *mcpoauth.OAuthCallback, 1)
		go func() {
			c, _ := cb.WaitForCallback("s1")
			pendingCh <- c
		}()
		time.Sleep(15 * time.Millisecond)
		resp, err := http.Get(cb.RedirectUrl + "?code=abc&state=s1")
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		<-pendingCh

		closed := make(chan error, 1)
		go func() { closed <- cb.Close() }()
		select {
		case err := <-closed:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("cb.Close timed out with open spare connection")
		}
	})

	t.Run("rejects response on another path than expected", func(t *testing.T) {
		cb, err := mcpoauth.OAuthCallbackServerListen(&mcpoauth.OAuthCallbackServerOptions{
			ExtraPaths: []string{"/callback/server-id"},
		})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = cb.Close() }()
		u, _ := url.Parse(cb.RedirectUrl)
		srvOrigin := u.Scheme + "://" + u.Host

		errCh := make(chan error, 1)
		go func() {
			_, e := cb.WaitForCallbackOnPath("s1", "/callback/server-id")
			errCh <- e
		}()
		time.Sleep(15 * time.Millisecond)
		wrong, err := http.Get(srvOrigin + "/callback?code=abc&state=s1")
		if err != nil {
			t.Fatal(err)
		}
		_ = wrong.Body.Close()
		if wrong.StatusCode != http.StatusBadRequest {
			t.Fatalf("wrong status = %d, want 400", wrong.StatusCode)
		}
		waitErr := <-errCh
		if waitErr == nil || !strings.Contains(waitErr.Error(), "arrived on another redirect URI") {
			t.Fatalf("waitErr = %v", waitErr)
		}

		okCh := make(chan *mcpoauth.OAuthCallback, 1)
		go func() {
			c, _ := cb.WaitForCallbackOnPath("s2", "/callback/server-id")
			okCh <- c
		}()
		time.Sleep(15 * time.Millisecond)
		right, err := http.Get(srvOrigin + "/callback/server-id?code=abc&state=s2")
		if err != nil {
			t.Fatal(err)
		}
		_ = right.Body.Close()
		if right.StatusCode != http.StatusOK {
			t.Fatalf("right status = %d", right.StatusCode)
		}
		got := <-okCh
		if got == nil || got.Code != "abc" {
			t.Fatalf("got = %#v", got)
		}
	})

	t.Run("renders pages through RenderPage", func(t *testing.T) {
		var mu sync.Mutex
		var pages []mcpoauth.OAuthCallbackPage
		cb, err := mcpoauth.OAuthCallbackServerListen(&mcpoauth.OAuthCallbackServerOptions{
			RenderPage: func(page mcpoauth.OAuthCallbackPage) string {
				mu.Lock()
				pages = append(pages, page)
				mu.Unlock()
				switch p := page.(type) {
				case *mcpoauth.OAuthCallbackPageOk:
					return "<p>ok</p>"
				case *mcpoauth.OAuthCallbackPageFailed:
					return "<p>" + p.Message + "</p>"
				default:
					return ""
				}
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = cb.Close() }()

		deniedErr := make(chan error, 1)
		go func() {
			_, e := cb.WaitForCallback("s1")
			deniedErr <- e
		}()
		time.Sleep(15 * time.Millisecond)
		failResp, err := http.Get(cb.RedirectUrl + "?error=access_denied&error_description=Denied&state=s1")
		if err != nil {
			t.Fatal(err)
		}
		_ = failResp.Body.Close()
		if ct := failResp.Header.Get("Content-Type"); ct != "text/html; charset=utf-8" {
			t.Fatalf("content-type = %q", ct)
		}
		if e := <-deniedErr; e == nil || !strings.Contains(e.Error(), "Denied") {
			t.Fatalf("deniedErr = %v", e)
		}
		mu.Lock()
		lastPage := pages[len(pages)-1].(*mcpoauth.OAuthCallbackPageFailed)
		mu.Unlock()
		if lastPage.Ok || lastPage.Message != "Authorization failed. You may close this window." || lastPage.Details == nil || *lastPage.Details != "Denied" {
			t.Fatalf("lastPage = %#v", lastPage)
		}

		okCh := make(chan *mcpoauth.OAuthCallback, 1)
		go func() {
			c, _ := cb.WaitForCallback("s2")
			okCh <- c
		}()
		time.Sleep(15 * time.Millisecond)
		okResp, err := http.Get(cb.RedirectUrl + "?code=abc&state=s2")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = okResp.Body.Close() }()
		body, _ := testkit.ReadBody(&http.Request{Body: okResp.Body})
		if body != "<p>ok</p>" {
			t.Fatalf("body = %q", body)
		}
		if got := <-okCh; got == nil || got.Code != "abc" {
			t.Fatalf("got = %#v", got)
		}
	})
}

func toauthHeaders(kv ...string) *omap.Map[string, string] {
	m := omap.NewMap[string, string]()
	for i := 0; i+1 < len(kv); i += 2 {
		m.Set(kv[i], kv[i+1])
	}
	return m
}

func toauthPtr[T any](v T) *T { return &v }

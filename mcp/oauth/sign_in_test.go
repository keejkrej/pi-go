// Ported from packages/mcp/test/sign-in.test.ts (pi v1.5.2).

package oauth_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/mcp/internal/testkit"
	mcpoauth "github.com/keejkrej/pi-go/mcp/oauth"
)

const (
	tsigninRoute      = "https://app.example/mcp/callback"
	tsigninOtherRoute = "https://relay.example/mcp/callback"
	tsigninLoopback   = "http://127.0.0.1:1/callback"
)

var tsigninDocuments = &mcpoauth.McpClientMetadataDocuments{
	BaseUrl: "https://app.example/oauth",
	ListsRedirectUrl: func(redirectUrl string) bool {
		u, err := url.Parse(redirectUrl)
		if err != nil {
			return false
		}
		return u.Hostname() == "127.0.0.1" && u.Path == "/callback"
	},
}

func tsigninOptions(server *testkit.AuthorizationServer, store mcpoauth.McpOAuthServerStore, settings mcpoauth.McpOAuthSettings) mcpoauth.McpSignInOptions {
	if store == nil {
		store = testkit.NewMemoryStore()
	}
	return mcpoauth.McpSignInOptions{
		McpOAuthClientOptions: mcpoauth.McpOAuthClientOptions{
			ServerUrl:  server.ServerUrl,
			Store:      store,
			Settings:   settings,
			ClientName: "app",
		},
		RedirectUrl: tsigninRoute,
	}
}

func tsigninStartRedirect(t *testing.T, opts mcpoauth.McpSignInOptions) mcpoauth.McpSignInRedirect {
	t.Helper()
	started, err := mcpoauth.StartMcpSignIn(opts)
	if err != nil {
		t.Fatal(err)
	}
	if started.Kind != "redirect" {
		t.Fatalf("expected redirect, got %q", started.Kind)
	}
	return started.Redirect()
}

func tsigninRouteResponse(t *testing.T, loc string) mcpoauth.McpAuthorizationResponse {
	t.Helper()
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

func tsigninDo(t *testing.T, opts mcpoauth.McpSignInOptions) mcpoauth.McpSignInRedirect {
	t.Helper()
	auth := tsigninStartRedirect(t, opts)
	loc, err := testkit.AuthorizeInBrowser(auth.AuthorizationUrl)
	if err != nil {
		t.Fatal(err)
	}
	resp := tsigninRouteResponse(t, loc)
	if err := mcpoauth.CompleteMcpSignIn(mcpoauth.CompleteMcpSignInOptions{
		McpSignInOptions: opts,
		Authorization:    auth,
		Response:         resp,
	}); err != nil {
		t.Fatal(err)
	}
	return auth
}

func TestSignIn_CompletesInSecondCallWithPersistedRedirect(t *testing.T) {
	server := testkit.StartAuthorizationServer(t, testkit.AuthorizationServerOptions{})
	scope := "issues:read"
	opts := tsigninOptions(server, nil, mcpoauth.McpOAuthSettings{Scope: &scope})

	started := tsigninStartRedirect(t, opts)
	raw, err := jsonx.Marshal(started)
	if err != nil {
		t.Fatal(err)
	}
	var authorization mcpoauth.McpSignInRedirect
	if err := jsonx.Unmarshal(raw, &authorization); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(authorization, started) {
		t.Fatalf("roundtrip = %#v, want %#v", authorization, started)
	}
	if authorization.RedirectUrl != tsigninRoute || authorization.Scope == nil || *authorization.Scope != "issues:read" {
		t.Fatalf("authorization = %#v", authorization)
	}
	authURL, _ := url.Parse(authorization.AuthorizationUrl)
	if authURL.Query().Get("state") != authorization.State {
		t.Fatalf("state mismatch: %s vs %s", authURL.Query().Get("state"), authorization.State)
	}
	regs := server.Registrations()
	if len(regs) != 1 {
		t.Fatalf("registrations = %d", len(regs))
	}
	appType, _ := regs[0].Get("application_type")
	if appType != "web" {
		t.Fatalf("application_type = %v", appType)
	}

	loc, err := testkit.AuthorizeInBrowser(authorization.AuthorizationUrl)
	if err != nil {
		t.Fatal(err)
	}
	if err := mcpoauth.CompleteMcpSignIn(mcpoauth.CompleteMcpSignInOptions{
		McpSignInOptions: opts,
		Authorization:    authorization,
		Response:         tsigninRouteResponse(t, loc),
	}); err != nil {
		t.Fatal(err)
	}
	st, _ := opts.Store.Load()
	if st == nil || st.Tokens == nil || st.Tokens.AccessToken != "access-1" || st.Tokens.Scope == nil || *st.Tokens.Scope != "issues:read" {
		t.Fatalf("tokens = %#v", st)
	}
	tokReqs := server.TokenRequests()
	if len(tokReqs) != 1 || tokReqs[0].Get("redirect_uri") != tsigninRoute || tokReqs[0].Get("client_id") != "client-1" {
		t.Fatalf("tokenRequests = %#v", tokReqs)
	}
}

func TestSignIn_RefreshesStoredTokensInsteadOfRedirecting(t *testing.T) {
	server := testkit.StartAuthorizationServer(t, testkit.AuthorizationServerOptions{})
	opts := tsigninOptions(server, nil, mcpoauth.McpOAuthSettings{})
	tsigninDo(t, opts)

	started, err := mcpoauth.StartMcpSignIn(opts)
	if err != nil {
		t.Fatal(err)
	}
	if started.Kind != "authorized" {
		t.Fatalf("expected authorized, got %#v", started)
	}
	if len(server.Authorizations()) != 1 {
		t.Fatalf("authorizations = %d, want 1", len(server.Authorizations()))
	}
	reqs := server.TokenRequests()
	if len(reqs) < 2 || reqs[1].Get("grant_type") != "refresh_token" {
		t.Fatalf("tokenRequests = %#v", reqs)
	}
	st, _ := opts.Store.Load()
	if st.Tokens == nil || st.Tokens.AccessToken != "access-2" {
		t.Fatalf("tokens = %#v", st)
	}
}

func TestSignIn_RefreshesUnderRefreshLock(t *testing.T) {
	server := testkit.StartAuthorizationServer(t, testkit.AuthorizationServerOptions{})
	rec := testkit.NewRecordingStore(nil)
	opts := tsigninOptions(server, rec, mcpoauth.McpOAuthSettings{})
	tsigninDo(t, opts)

	started, err := mcpoauth.StartMcpSignIn(opts)
	if err != nil || started.Kind != "authorized" {
		t.Fatalf("started = %#v, err = %v", started, err)
	}
	var matching []testkit.RecordingSave
	for _, s := range rec.Saves() {
		if s.AccessToken != nil && *s.AccessToken == "access-2" {
			matching = append(matching, s)
		}
	}
	tok2 := "access-2"
	if !reflect.DeepEqual(matching, []testkit.RecordingSave{{Locked: true, AccessToken: &tok2}}) {
		t.Fatalf("matching saves = %#v", matching)
	}
}

func TestSignIn_RedirectsWhenRefreshLockBusy(t *testing.T) {
	server := testkit.StartAuthorizationServer(t, testkit.AuthorizationServerOptions{})
	// Allow initial sign-in on an unlocked store, then simulate lock busy on StartMcpSignIn.
	rec := testkit.NewRecordingStore(nil)
	opts := tsigninOptions(server, rec, mcpoauth.McpOAuthSettings{})
	tsigninDo(t, opts)

	busyRec := testkit.NewRecordingStore(&mcpoauth.McpRefreshLockBusyError{Message: "busy"})
	st, _ := rec.Load()
	_ = busyRec.Save(st)
	opts.Store = busyRec
	started, err := mcpoauth.StartMcpSignIn(opts)
	if err != nil {
		t.Fatal(err)
	}
	if started.Kind != "redirect" {
		t.Fatalf("expected redirect when refresh lock is busy, got %#v", started)
	}
	reqs := server.TokenRequests()
	if len(reqs) != 1 || reqs[0].Get("grant_type") != "authorization_code" {
		t.Fatalf("tokenRequests = %#v", reqs)
	}
}

func TestSignIn_FailsWhenRefreshLockFailsForOtherReason(t *testing.T) {
	server := testkit.StartAuthorizationServer(t, testkit.AuthorizationServerOptions{})
	lockErr := errors.New("EACCES: permission denied")
	rec := testkit.NewRecordingStore(lockErr)
	opts := tsigninOptions(server, rec, mcpoauth.McpOAuthSettings{})
	_, err := mcpoauth.StartMcpSignIn(opts)
	if !errors.Is(err, lockErr) {
		t.Fatalf("expected %v, got %v", lockErr, err)
	}
	if len(server.Registrations()) != 0 || len(server.Authorizations()) != 0 {
		t.Fatalf("expected no registrations or authorizations")
	}
}

func TestSignIn_RefusesToCompleteWithOtherRedirectUri(t *testing.T) {
	server := testkit.StartAuthorizationServer(t, testkit.AuthorizationServerOptions{Cimd: true})
	opts := tsigninOptions(server, nil, mcpoauth.McpOAuthSettings{})
	opts.RedirectUrl = tsigninLoopback
	opts.ClientMetadataDocuments = tsigninDocuments
	authorization := tsigninStartRedirect(t, opts)
	loc, err := testkit.AuthorizeInBrowser(authorization.AuthorizationUrl)
	if err != nil {
		t.Fatal(err)
	}
	wrong := opts
	wrong.RedirectUrl = "http://127.0.0.1:2/callback"
	err = mcpoauth.CompleteMcpSignIn(mcpoauth.CompleteMcpSignInOptions{
		McpSignInOptions: wrong,
		Authorization:    authorization,
		Response:         tsigninRouteResponse(t, loc),
	})
	if err == nil || !strings.Contains(err.Error(), "but the authorization request used "+authorization.RedirectUrl) {
		t.Fatalf("err = %v", err)
	}
	if len(server.TokenRequests()) != 0 {
		t.Fatalf("tokenRequests = %d, want 0", len(server.TokenRequests()))
	}
}

func TestSignIn_FreshStateAndRefusesSupersededOrWrongState(t *testing.T) {
	server := testkit.StartAuthorizationServer(t, testkit.AuthorizationServerOptions{})
	opts := tsigninOptions(server, nil, mcpoauth.McpOAuthSettings{})
	first := tsigninStartRedirect(t, opts)
	loc1, err := testkit.AuthorizeInBrowser(first.AuthorizationUrl)
	if err != nil {
		t.Fatal(err)
	}
	firstResp := tsigninRouteResponse(t, loc1)
	second := tsigninStartRedirect(t, opts)
	if second.State == first.State {
		t.Fatalf("expected fresh state on second start")
	}

	err = mcpoauth.CompleteMcpSignIn(mcpoauth.CompleteMcpSignInOptions{
		McpSignInOptions: opts,
		Authorization:    first,
		Response:         firstResp,
	})
	if err == nil || !strings.Contains(err.Error(), "Another sign-in to this MCP server started since") {
		t.Fatalf("err = %v", err)
	}

	wrongState := firstResp
	wrongState.State = "other"
	err = mcpoauth.CompleteMcpSignIn(mcpoauth.CompleteMcpSignInOptions{
		McpSignInOptions: opts,
		Authorization:    first,
		Response:         wrongState,
	})
	if err == nil || !strings.Contains(err.Error(), "belongs to a different sign-in") {
		t.Fatalf("err = %v", err)
	}
	if len(server.TokenRequests()) != 0 {
		t.Fatalf("tokenRequests = %d, want 0", len(server.TokenRequests()))
	}
}

func TestSignIn_StoredClientLifecycle(t *testing.T) {
	t.Run("refreshes stored client from another redirect URI", func(t *testing.T) {
		server := testkit.StartAuthorizationServer(t, testkit.AuthorizationServerOptions{})
		opts := tsigninOptions(server, nil, mcpoauth.McpOAuthSettings{})
		tsigninDo(t, opts)

		other := opts
		other.RedirectUrl = tsigninOtherRoute
		started, err := mcpoauth.StartMcpSignIn(other)
		if err != nil || started.Kind != "authorized" {
			t.Fatalf("started = %#v, err = %v", started, err)
		}
		if len(server.Registrations()) != 1 || server.TokenRequests()[1].Get("client_id") != "client-1" {
			t.Fatalf("unexpected registrations/tokens")
		}
	})

	t.Run("keeps client for browser step on same redirect URI", func(t *testing.T) {
		server := testkit.StartAuthorizationServer(t, testkit.AuthorizationServerOptions{})
		opts := tsigninOptions(server, nil, mcpoauth.McpOAuthSettings{})
		tsigninDo(t, opts)
		server.RevokeRefreshTokens()

		auth := tsigninStartRedirect(t, opts)
		u, _ := url.Parse(auth.AuthorizationUrl)
		if u.Query().Get("client_id") != "client-1" || len(server.Registrations()) != 1 {
			t.Fatalf("authUrl=%s registrations=%d", auth.AuthorizationUrl, len(server.Registrations()))
		}
	})

	t.Run("drops client and tokens for browser step on another redirect URI", func(t *testing.T) {
		server := testkit.StartAuthorizationServer(t, testkit.AuthorizationServerOptions{})
		opts := tsigninOptions(server, nil, mcpoauth.McpOAuthSettings{})
		tsigninDo(t, opts)
		server.RevokeRefreshTokens()

		other := opts
		other.RedirectUrl = tsigninOtherRoute
		auth := tsigninStartRedirect(t, other)
		u, _ := url.Parse(auth.AuthorizationUrl)
		if u.Query().Get("client_id") != "client-2" {
			t.Fatalf("client_id = %s, want client-2", u.Query().Get("client_id"))
		}
		st, _ := opts.Store.Load()
		if info, ok := st.ClientInformation.(*mcpoauth.OAuthClientInformationFull); !ok || info.ClientId != "client-2" || st.Tokens != nil {
			t.Fatalf("state = %#v", st)
		}
	})

	t.Run("does not refresh with registered client replaced by cimd", func(t *testing.T) {
		server := testkit.StartAuthorizationServer(t, testkit.AuthorizationServerOptions{Cimd: true, IssParameter: true})
		opts := tsigninOptions(server, nil, mcpoauth.McpOAuthSettings{ClientRegistration: "dcr"})
		opts.RedirectUrl = tsigninLoopback
		opts.ClientMetadataDocuments = tsigninDocuments
		tsigninDo(t, opts)

		cimdOpts := opts
		cimdOpts.Settings.ClientRegistration = "cimd"
		auth := tsigninStartRedirect(t, cimdOpts)
		u, _ := url.Parse(auth.AuthorizationUrl)
		if u.Query().Get("client_id") != "https://app.example/oauth/client.json" {
			t.Fatalf("client_id = %s", u.Query().Get("client_id"))
		}
		if len(server.TokenRequests()) != 1 {
			t.Fatalf("tokenRequests = %d, want 1", len(server.TokenRequests()))
		}
		st, _ := opts.Store.Load()
		if st.ClientInformation != nil || st.Tokens != nil {
			t.Fatalf("state = %#v", st)
		}
	})

	t.Run("step-up combines granted and challenged scopes without refreshing", func(t *testing.T) {
		server := testkit.StartAuthorizationServer(t, testkit.AuthorizationServerOptions{})
		sRead := "issues:read"
		opts := tsigninOptions(server, nil, mcpoauth.McpOAuthSettings{})
		opts.Challenge = &mcpoauth.OAuthChallenge{Scope: &sRead}
		tsigninDo(t, opts)

		errStr := "insufficient_scope"
		sWrite := "issues:write"
		stepUp := opts
		stepUp.Challenge = &mcpoauth.OAuthChallenge{Error: &errStr, Scope: &sWrite}
		auth := tsigninDo(t, stepUp)
		if auth.Scope == nil || *auth.Scope != "issues:read issues:write" {
			t.Fatalf("scope = %v", auth.Scope)
		}
		st, _ := opts.Store.Load()
		if st.Tokens == nil || st.Tokens.Scope == nil || *st.Tokens.Scope != "issues:read issues:write" {
			t.Fatalf("tokens = %#v", st.Tokens)
		}
	})
}

func TestSignIn_ClientMetadataDocuments(t *testing.T) {
	loopback := func(server *testkit.AuthorizationServer, settings mcpoauth.McpOAuthSettings) mcpoauth.McpSignInOptions {
		o := tsigninOptions(server, nil, settings)
		o.RedirectUrl = tsigninLoopback
		o.ClientMetadataDocuments = tsigninDocuments
		return o
	}

	t.Run("uses document when server supports it and docs list redirect URI", func(t *testing.T) {
		server := testkit.StartAuthorizationServer(t, testkit.AuthorizationServerOptions{Cimd: true, IssParameter: true})
		opts := loopback(server, mcpoauth.McpOAuthSettings{})
		auth := tsigninDo(t, opts)
		u, _ := url.Parse(auth.AuthorizationUrl)
		if u.Query().Get("client_id") != "https://app.example/oauth/client.json" {
			t.Fatalf("client_id = %s", u.Query().Get("client_id"))
		}
		if len(server.Registrations()) != 0 {
			t.Fatalf("expected no registrations")
		}
		st, _ := opts.Store.Load()
		if st.ClientInformation != nil {
			t.Fatalf("expected nil clientInformation")
		}
	})

	t.Run("uses server-specific document and redirect URI without iss parameter", func(t *testing.T) {
		server := testkit.StartAuthorizationServer(t, testkit.AuthorizationServerOptions{Cimd: true})
		auth := tsigninDo(t, loopback(server, mcpoauth.McpOAuthSettings{}))
		sum := sha256.Sum256([]byte(server.ServerUrl))
		id := base64.RawURLEncoding.EncodeToString(sum[:9])
		u, _ := url.Parse(auth.AuthorizationUrl)
		if u.Query().Get("client_id") != "https://app.example/oauth/"+id+"/client.json" {
			t.Fatalf("client_id = %s", u.Query().Get("client_id"))
		}
		if auth.RedirectUrl != tsigninLoopback+"/"+id {
			t.Fatalf("redirectUrl = %s", auth.RedirectUrl)
		}
	})

	t.Run("appends server-specific path without doubling trailing slash", func(t *testing.T) {
		server := testkit.StartAuthorizationServer(t, testkit.AuthorizationServerOptions{Cimd: true})
		opts := loopback(server, mcpoauth.McpOAuthSettings{})
		opts.RedirectUrl = tsigninLoopback + "/"
		opts.ClientMetadataDocuments = &mcpoauth.McpClientMetadataDocuments{
			BaseUrl:          "https://app.example/oauth",
			ListsRedirectUrl: func(string) bool { return true },
		}
		auth := tsigninStartRedirect(t, opts)
		sum := sha256.Sum256([]byte(server.ServerUrl))
		id := base64.RawURLEncoding.EncodeToString(sum[:9])
		if auth.RedirectUrl != tsigninLoopback+"/"+id {
			t.Fatalf("redirectUrl = %s", auth.RedirectUrl)
		}
	})

	t.Run("registers dynamically when server does not support documents", func(t *testing.T) {
		server := testkit.StartAuthorizationServer(t, testkit.AuthorizationServerOptions{IssParameter: true})
		tsigninDo(t, loopback(server, mcpoauth.McpOAuthSettings{}))
		if len(server.Registrations()) != 1 {
			t.Fatalf("registrations = %d, want 1", len(server.Registrations()))
		}
	})

	t.Run("registers dynamically when documents do not list redirect URI", func(t *testing.T) {
		server := testkit.StartAuthorizationServer(t, testkit.AuthorizationServerOptions{Cimd: true, IssParameter: true})
		opts := loopback(server, mcpoauth.McpOAuthSettings{})
		opts.RedirectUrl = tsigninRoute
		tsigninDo(t, opts)
		if len(server.Registrations()) != 1 {
			t.Fatalf("registrations = %d, want 1", len(server.Registrations()))
		}
	})

	t.Run("registers dynamically without documents or with custom client name", func(t *testing.T) {
		server := testkit.StartAuthorizationServer(t, testkit.AuthorizationServerOptions{Cimd: true, IssParameter: true})
		noDocs := loopback(server, mcpoauth.McpOAuthSettings{})
		noDocs.ClientMetadataDocuments = nil
		tsigninDo(t, noDocs)
		otherName := "Other"
		tsigninDo(t, loopback(server, mcpoauth.McpOAuthSettings{ClientName: &otherName}))
		regs := server.Registrations()
		if len(regs) != 2 {
			t.Fatalf("registrations = %d, want 2", len(regs))
		}
		n0, _ := regs[0].Get("client_name")
		n1, _ := regs[1].Get("client_name")
		if n0 != "app" || n1 != "Other" {
			t.Fatalf("client names = %v, %v", n0, n1)
		}
	})

	t.Run("refreshes tokens of document listing only https route", func(t *testing.T) {
		routeDocs := &mcpoauth.McpClientMetadataDocuments{
			BaseUrl:          "https://app.example/oauth",
			ListsRedirectUrl: func(u string) bool { return u == tsigninRoute },
		}
		for _, reg := range []string{"auto", "cimd"} {
			server := testkit.StartAuthorizationServer(t, testkit.AuthorizationServerOptions{Cimd: true, IssParameter: true})
			opts := tsigninOptions(server, nil, mcpoauth.McpOAuthSettings{ClientRegistration: reg})
			opts.ClientMetadataDocuments = routeDocs
			auth := tsigninDo(t, opts)
			u, _ := url.Parse(auth.AuthorizationUrl)
			if u.Query().Get("client_id") != "https://app.example/oauth/client.json" {
				t.Fatalf("%s: client_id=%s", reg, u.Query().Get("client_id"))
			}
			started, err := mcpoauth.StartMcpSignIn(opts)
			if err != nil || started.Kind != "authorized" {
				t.Fatalf("%s: started=%#v err=%v", reg, started, err)
			}
			if len(server.TokenRequests()) < 2 || server.TokenRequests()[1].Get("client_id") != "https://app.example/oauth/client.json" {
				t.Fatalf("%s: tokenRequests=%#v", reg, server.TokenRequests())
			}
		}
	})

	t.Run("fails with cimd when server does not support documents or documents omit redirect URI", func(t *testing.T) {
		s1 := testkit.StartAuthorizationServer(t, testkit.AuthorizationServerOptions{IssParameter: true})
		_, err := mcpoauth.StartMcpSignIn(loopback(s1, mcpoauth.McpOAuthSettings{ClientRegistration: "cimd"}))
		if err == nil || !strings.Contains(err.Error(), "does not support Client ID Metadata Documents") {
			t.Fatalf("err1 = %v", err)
		}
		s2 := testkit.StartAuthorizationServer(t, testkit.AuthorizationServerOptions{Cimd: true, IssParameter: true})
		opts2 := loopback(s2, mcpoauth.McpOAuthSettings{ClientRegistration: "cimd"})
		opts2.RedirectUrl = tsigninRoute
		_, err = mcpoauth.StartMcpSignIn(opts2)
		if err == nil || !strings.Contains(err.Error(), "needs Client ID Metadata Documents that list the redirect URI") {
			t.Fatalf("err2 = %v", err)
		}
	})
}

func TestSignIn_Cancellation(t *testing.T) {
	t.Run("cancels start whose request stalls", func(t *testing.T) {
		server := testkit.StartAuthorizationServer(t, testkit.AuthorizationServerOptions{})
		server.AddStall("/register")
		ctx, cancel := context.WithCancel(t.Context())
		opts := tsigninOptions(server, nil, mcpoauth.McpOAuthSettings{})
		opts.Context = ctx
		time.AfterFunc(50*time.Millisecond, cancel)
		_, err := mcpoauth.StartMcpSignIn(opts)
		var cancelErr *mcpoauth.McpSignInCancelledError
		if !errors.As(err, &cancelErr) {
			t.Fatalf("expected McpSignInCancelledError, got %v", err)
		}
	})

	t.Run("cancels completion whose token request stalls", func(t *testing.T) {
		server := testkit.StartAuthorizationServer(t, testkit.AuthorizationServerOptions{})
		opts := tsigninOptions(server, nil, mcpoauth.McpOAuthSettings{})
		auth := tsigninStartRedirect(t, opts)
		loc, _ := testkit.AuthorizeInBrowser(auth.AuthorizationUrl)
		resp := tsigninRouteResponse(t, loc)
		server.AddStall("/token")
		ctx, cancel := context.WithCancel(t.Context())
		opts.Context = ctx
		time.AfterFunc(50*time.Millisecond, cancel)
		err := mcpoauth.CompleteMcpSignIn(mcpoauth.CompleteMcpSignInOptions{
			McpSignInOptions: opts,
			Authorization:    auth,
			Response:         resp,
		})
		var cancelErr *mcpoauth.McpSignInCancelledError
		if !errors.As(err, &cancelErr) {
			t.Fatalf("expected McpSignInCancelledError, got %v", err)
		}
	})

	t.Run("cancels immediately while another process holds refresh lock", func(t *testing.T) {
		server := testkit.StartAuthorizationServer(t, testkit.AuthorizationServerOptions{})
		rec := testkit.NewRecordingStore(nil)
		opts := tsigninOptions(server, rec, mcpoauth.McpOAuthSettings{})
		tsigninDo(t, opts)

		release := rec.Hold()
		defer release()
		heldCh := rec.HeldCh()
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		opts.Context = ctx
		done := make(chan struct{})
		go func() {
			defer close(done)
			timer := time.NewTimer(5 * time.Second)
			defer timer.Stop()
			select {
			case <-heldCh:
				cancel()
			case <-timer.C:
				cancel()
			case <-ctx.Done():
			}
		}()
		_, err := mcpoauth.StartMcpSignIn(opts)
		cancel()
		<-done
		var cancelErr *mcpoauth.McpSignInCancelledError
		if !errors.As(err, &cancelErr) {
			t.Fatalf("expected McpSignInCancelledError, got %v", err)
		}
		release()
		rec.WaitLockRuns()
		if rec.IsLocked() {
			t.Fatal("expected lock to be released")
		}
		if len(server.TokenRequests()) != 1 || len(server.Authorizations()) != 1 {
			t.Fatalf("expected no refresh/redirect after cancel")
		}
	})

	t.Run("fails at once with pre-canceled context", func(t *testing.T) {
		server := testkit.StartAuthorizationServer(t, testkit.AuthorizationServerOptions{})
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		opts := tsigninOptions(server, nil, mcpoauth.McpOAuthSettings{})
		opts.Context = ctx
		_, err := mcpoauth.StartMcpSignIn(opts)
		var cancelErr *mcpoauth.McpSignInCancelledError
		if !errors.As(err, &cancelErr) {
			t.Fatalf("expected McpSignInCancelledError, got %v", err)
		}
		if len(server.Registrations()) != 0 {
			t.Fatalf("expected no registrations")
		}
	})
}

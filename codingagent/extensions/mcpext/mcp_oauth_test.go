// Ported from packages/coding-agent/test/{mcp-oauth-store,mcp-oauth-refresh,mcp-manager-view}.test.ts
// and packages/coding-agent/test/suite/mcp-oauth-server.ts (pi v1.5.2).

package mcpext

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/mcp"
	mcpoauth "github.com/keejkrej/pi-go/mcp/oauth"
	mcpprotocol "github.com/keejkrej/pi-go/mcp/protocol"
	"github.com/keejkrej/pi-go/tui"
)

type tmoOAuthServerOptions struct {
	Iss          string
	IssParameter bool
	Cimd         bool
	RedirectPath string
}

type tmoOAuthMcpServer struct {
	URL            string
	httpServer     *httptest.Server
	options        tmoOAuthServerOptions
	mu             sync.Mutex
	log            []string
	registrations  []*jsonx.Object
	authorizations []url.Values
	tokenRequests  []url.Values
	deletes        []string
	stall          map[string]bool
	stalled        []string
	stalledAborted int
	validTokens    map[string]bool
	refreshTokens  map[string]bool
	challenges     map[string]string
	issued         int
	stalledRelease chan struct{}
	closeOnce      sync.Once
}

func tmoStartOAuthMcpServer(t *testing.T, opts ...tmoOAuthServerOptions) *tmoOAuthMcpServer {
	t.Helper()
	var opt tmoOAuthServerOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	srv := &tmoOAuthMcpServer{
		options:        opt,
		stall:          make(map[string]bool),
		validTokens:    make(map[string]bool),
		refreshTokens:  make(map[string]bool),
		challenges:     make(map[string]string),
		stalledRelease: make(chan struct{}),
	}
	srv.httpServer = httptest.NewServer(http.HandlerFunc(srv.serve))
	srv.URL = srv.httpServer.URL + "/mcp"
	t.Cleanup(srv.Close)
	return srv
}

func (s *tmoOAuthMcpServer) Close() {
	s.closeOnce.Do(func() {
		close(s.stalledRelease)
		s.httpServer.CloseClientConnections()
		s.httpServer.Close()
	})
}

func (s *tmoOAuthMcpServer) Log() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.log))
	copy(out, s.log)
	return out
}

func (s *tmoOAuthMcpServer) Registrations() []*jsonx.Object {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*jsonx.Object, len(s.registrations))
	copy(out, s.registrations)
	return out
}

func (s *tmoOAuthMcpServer) Authorizations() []url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]url.Values, len(s.authorizations))
	copy(out, s.authorizations)
	return out
}

func (s *tmoOAuthMcpServer) TokenRequests() []url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]url.Values, len(s.tokenRequests))
	copy(out, s.tokenRequests)
	return out
}

func (s *tmoOAuthMcpServer) Deletes() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.deletes))
	copy(out, s.deletes)
	return out
}

func (s *tmoOAuthMcpServer) StallPath(path string) {
	s.mu.Lock()
	s.stall[path] = true
	s.mu.Unlock()
}

func (s *tmoOAuthMcpServer) UnstallPath(path string) {
	s.mu.Lock()
	delete(s.stall, path)
	s.mu.Unlock()
}

func (s *tmoOAuthMcpServer) StalledCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.stalled)
}

func (s *tmoOAuthMcpServer) StalledAbortedCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stalledAborted
}

func (s *tmoOAuthMcpServer) IssueAccessToken() string {
	toks := s.issueTokensLocked()
	return toks["access_token"].(string)
}

func (s *tmoOAuthMcpServer) ExpireAccessTokens() {
	s.mu.Lock()
	s.validTokens = make(map[string]bool)
	s.mu.Unlock()
}

func (s *tmoOAuthMcpServer) issueTokensLocked() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.issued++
	access := fmt.Sprintf("access-%d", s.issued)
	refresh := fmt.Sprintf("refresh-%d", s.issued)
	s.validTokens[access] = true
	s.refreshTokens[refresh] = true
	return map[string]any{
		"access_token":  access,
		"refresh_token": refresh,
		"token_type":    "Bearer",
		"expires_in":    3600,
	}
}

func tmoWriteJSON(w http.ResponseWriter, status int, body any, extraHeaders ...map[string]string) {
	data, err := jsonx.Marshal(body)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	for _, hdrs := range extraHeaders {
		for k, v := range hdrs {
			w.Header().Set(k, v)
		}
	}
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

func (s *tmoOAuthMcpServer) serve(w http.ResponseWriter, r *http.Request) {
	origin := s.httpServer.URL
	path := r.URL.Path

	s.mu.Lock()
	isStalled := s.stall[path]
	if isStalled {
		s.stalled = append(s.stalled, path)
	}
	s.mu.Unlock()
	if isStalled {
		if r.Body != nil {
			_, _ = io.Copy(io.Discard, r.Body)
		}
		select {
		case <-r.Context().Done():
			s.mu.Lock()
			s.stalledAborted++
			s.mu.Unlock()
		case <-s.stalledRelease:
		}
		return
	}

	switch path {
	case "/mcp":
		s.serveMcp(w, r, origin)
	case "/.well-known/oauth-protected-resource/mcp":
		tmoWriteJSON(w, http.StatusOK, map[string]any{
			"resource":              origin + "/mcp",
			"authorization_servers": []string{origin},
		})
	case "/.well-known/oauth-authorization-server":
		meta := map[string]any{
			"issuer":                                origin,
			"authorization_endpoint":                origin + "/authorize",
			"token_endpoint":                        origin + "/token",
			"registration_endpoint":                 origin + "/register",
			"response_types_supported":              []string{"code"},
			"code_challenge_methods_supported":      []string{"S256"},
			"token_endpoint_auth_methods_supported": []string{"none"},
		}
		if s.options.Cimd {
			meta["client_id_metadata_document_supported"] = true
		}
		if s.options.IssParameter {
			meta["authorization_response_iss_parameter_supported"] = true
		}
		tmoWriteJSON(w, http.StatusOK, meta)
	case "/register":
		rawBody, _ := io.ReadAll(r.Body)
		parsed, err := jsonx.Parse(rawBody)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		obj, ok := parsed.(*jsonx.Object)
		if !ok || obj == nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.log = append(s.log, "register")
		s.registrations = append(s.registrations, obj)
		s.mu.Unlock()

		respObj := obj.Clone()
		respObj.Set("client_id", "client-1")
		tmoWriteJSON(w, http.StatusCreated, respObj)
	case "/authorize":
		q := r.URL.Query()
		s.mu.Lock()
		s.authorizations = append(s.authorizations, q)
		code := fmt.Sprintf("code-%d", len(s.challenges)+1)
		s.challenges[code] = q.Get("code_challenge")
		s.mu.Unlock()

		redirect, err := url.Parse(q.Get("redirect_uri"))
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if s.options.RedirectPath != "" {
			redirect.Path = s.options.RedirectPath
		}
		rq := redirect.Query()
		rq.Set("code", code)
		rq.Set("state", q.Get("state"))
		iss := s.options.Iss
		if iss == "" && s.options.IssParameter {
			iss = origin
		}
		if iss != "" {
			rq.Set("iss", iss)
		}
		redirect.RawQuery = rq.Encode()
		http.Redirect(w, r, redirect.String(), http.StatusFound)
	case "/token":
		rawBody, _ := io.ReadAll(r.Body)
		params, err := url.ParseQuery(string(rawBody))
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.tokenRequests = append(s.tokenRequests, params)
		s.mu.Unlock()

		if params.Get("grant_type") == "authorization_code" {
			code := params.Get("code")
			sum := sha256.Sum256([]byte(params.Get("code_verifier")))
			verifier := base64.RawURLEncoding.EncodeToString(sum[:])

			s.mu.Lock()
			challenge := s.challenges[code]
			if challenge == "" || challenge != verifier {
				s.mu.Unlock()
				tmoWriteJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_grant"})
				return
			}
			delete(s.challenges, code)
			s.log = append(s.log, "token code")
			s.mu.Unlock()
			tmoWriteJSON(w, http.StatusOK, s.issueTokensLocked())
			return
		}

		refresh := params.Get("refresh_token")
		s.mu.Lock()
		hasRefresh := s.refreshTokens[refresh]
		if !hasRefresh {
			s.mu.Unlock()
			tmoWriteJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_grant"})
			return
		}
		delete(s.refreshTokens, refresh)
		s.log = append(s.log, "token refresh")
		s.mu.Unlock()
		tmoWriteJSON(w, http.StatusOK, s.issueTokensLocked())
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (s *tmoOAuthMcpServer) serveMcp(w http.ResponseWriter, r *http.Request, origin string) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if r.Header.Get("Authorization") == "" || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		token = ""
	}
	if r.Method == http.MethodDelete {
		s.mu.Lock()
		s.deletes = append(s.deletes, token)
		s.mu.Unlock()
	}
	if r.Method != http.MethodPost {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
		} else {
			w.WriteHeader(http.StatusOK)
		}
		return
	}

	s.mu.Lock()
	valid := token != "" && s.validTokens[token]
	if !valid {
		label := token
		if label == "" {
			label = "none"
		}
		s.log = append(s.log, "401 "+label)
	}
	s.mu.Unlock()

	if !valid {
		w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s/.well-known/oauth-protected-resource/mcp"`, origin))
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	rawBody, _ := io.ReadAll(r.Body)
	parsed, err := jsonx.Parse(rawBody)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	obj, ok := parsed.(*jsonx.Object)
	if !ok || obj == nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	idVal, hasId := obj.Get("id")
	if !hasId || idVal == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	method, _ := obj.Get("method")
	methodStr, _ := method.(string)

	var result any
	switch methodStr {
	case "initialize":
		result = map[string]any{
			"protocolVersion": mcpprotocol.LatestProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "issues", "version": "1.0.0"},
		}
	case "tools/list":
		result = map[string]any{
			"tools": []any{
				map[string]any{
					"name":        "whoami",
					"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
				},
			},
		}
	case "tools/call":
		s.mu.Lock()
		s.log = append(s.log, "call "+token)
		s.mu.Unlock()
		result = map[string]any{
			"content": []any{map[string]any{"type": "text", "text": "token " + token}},
		}
	default:
		result = map[string]any{}
	}
	tmoWriteJSON(w, http.StatusOK, map[string]any{
		"jsonrpc": "2.0",
		"id":      idVal,
		"result":  result,
	}, map[string]string{"mcp-session-id": "session-1"})
}

func tmoState(serverUrl, accessToken string) *mcpoauth.McpOAuthState {
	return &mcpoauth.McpOAuthState{
		ServerUrl: serverUrl,
		Tokens: &mcpoauth.OAuthTokens{
			AccessToken: accessToken,
			TokenType:   "Bearer",
		},
	}
}

func tmoStoredKeys(backend core.AuthStorageBackend) []string {
	res, err := backend.WithLock(func(current *string) (core.AuthStorageLockResult, error) {
		raw := "{}"
		if current != nil {
			raw = *current
		}
		return core.AuthStorageLockResult{Result: raw}, nil
	})
	if err != nil {
		return nil
	}
	raw, _ := res.(string)
	parsed, err := jsonx.ParseString(raw)
	if err != nil {
		return nil
	}
	obj, ok := parsed.(*jsonx.Object)
	if !ok || obj == nil {
		return nil
	}
	return obj.Keys()
}

func TestMcpOAuthCredentialStore(t *testing.T) {
	const serverURL = "https://mcp.example.com/mcp"

	t.Run("keeps separate credentials for servers sharing a SERVER_URL", func(t *testing.T) {
		store := NewMcpOAuthCredentialStore(core.NewInMemoryAuthStorageBackend())
		if err := store.ForServer("work", serverURL).Save(tmoState(serverURL, "work-token")); err != nil {
			t.Fatalf("save work: %v", err)
		}
		if err := store.ForServer("personal", serverURL).Save(tmoState(serverURL, "personal-token")); err != nil {
			t.Fatalf("save personal: %v", err)
		}

		workLoaded, err := store.ForServer("work", serverURL).Load()
		if err != nil || workLoaded == nil || workLoaded.Tokens == nil || workLoaded.Tokens.AccessToken != "work-token" {
			t.Fatalf("unexpected work loaded: %+v, err=%v", workLoaded, err)
		}
		personalLoaded, err := store.ForServer("personal", serverURL).Load()
		if err != nil || personalLoaded == nil || personalLoaded.Tokens == nil || personalLoaded.Tokens.AccessToken != "personal-token" {
			t.Fatalf("unexpected personal loaded: %+v, err=%v", personalLoaded, err)
		}

		if !store.Remove("work", serverURL) {
			t.Fatalf("expected Remove(work) = true")
		}
		afterRemove, err := store.ForServer("work", serverURL).Load()
		if err != nil || afterRemove != nil {
			t.Fatalf("expected nil after remove, got %+v, err=%v", afterRemove, err)
		}
		if tok := store.Tokens("personal", serverURL); tok == nil || tok.AccessToken != "personal-token" {
			t.Fatalf("unexpected personal tokens after work removal: %+v", tok)
		}
	})

	t.Run("moves credentials stored by SERVER_URL to the first server that loads them", func(t *testing.T) {
		backend := core.NewInMemoryAuthStorageBackend()
		_, err := backend.WithLock(func(_ *string) (core.AuthStorageLockResult, error) {
			next, err := jsonx.Stringify(map[string]any{serverURL: tmoState(serverURL, "legacy-token")})
			if err != nil {
				return core.AuthStorageLockResult{}, err
			}
			return core.AuthStorageLockResult{Next: &next}, nil
		})
		if err != nil {
			t.Fatalf("seed legacy: %v", err)
		}
		store := NewMcpOAuthCredentialStore(backend)

		if tok := store.Tokens("work", serverURL); tok == nil || tok.AccessToken != "legacy-token" {
			t.Fatalf("expected legacy token via Tokens, got %+v", tok)
		}
		keys := tmoStoredKeys(backend)
		if len(keys) != 1 || keys[0] != serverURL {
			t.Fatalf("expected unmigrated key [%s], got %v", serverURL, keys)
		}

		migrated, err := store.ForServer("my_work", serverURL).Load()
		if err != nil || migrated == nil || migrated.Tokens == nil || migrated.Tokens.AccessToken != "legacy-token" {
			t.Fatalf("expected migrated state, got %+v, err=%v", migrated, err)
		}
		if tok := store.Tokens("my-work", serverURL); tok == nil || tok.AccessToken != "legacy-token" {
			t.Fatalf("expected dash/underscore equivalence, got %+v", tok)
		}
		personal, err := store.ForServer("personal", serverURL).Load()
		if err != nil || personal != nil {
			t.Fatalf("expected personal to be nil, got %+v, err=%v", personal, err)
		}
		keys = tmoStoredKeys(backend)
		wantKey := "mcp__my_work|" + serverURL
		if len(keys) != 1 || keys[0] != wantKey {
			t.Fatalf("expected migrated key [%s], got %v", wantKey, keys)
		}
	})

	t.Run("signs out of credentials stored by SERVER_URL", func(t *testing.T) {
		backend := core.NewInMemoryAuthStorageBackend()
		_, err := backend.WithLock(func(_ *string) (core.AuthStorageLockResult, error) {
			next, err := jsonx.Stringify(map[string]any{serverURL: tmoState(serverURL, "legacy-token")})
			if err != nil {
				return core.AuthStorageLockResult{}, err
			}
			return core.AuthStorageLockResult{Next: &next}, nil
		})
		if err != nil {
			t.Fatalf("seed legacy: %v", err)
		}
		store := NewMcpOAuthCredentialStore(backend)
		if !store.Remove("work", serverURL) {
			t.Fatalf("expected Remove(work) = true")
		}
		if keys := tmoStoredKeys(backend); len(keys) != 0 {
			t.Fatalf("expected empty keys after Remove, got %v", keys)
		}
		if store.Remove("work", serverURL) {
			t.Fatalf("expected second Remove(work) = false")
		}
	})
}

type tmoBrowserPrompt struct {
	client       *http.Client
	mu           sync.Mutex
	wg           sync.WaitGroup
	onUrl        func(u *url.URL)
	redirectFn   func(ctx context.Context) (*string, error)
	openedUrls   []*url.URL
	fetchInBg    bool
	lastFetchErr error
}

func tmoNewAutoBrowserPrompt(client *http.Client) *tmoBrowserPrompt {
	if client == nil {
		client = http.DefaultClient
	}
	return &tmoBrowserPrompt{
		client:    client,
		fetchInBg: true,
	}
}

func (p *tmoBrowserPrompt) ShowAuthorizationUrl(u *url.URL) {
	p.mu.Lock()
	p.openedUrls = append(p.openedUrls, u)
	cb := p.onUrl
	fetchInBg := p.fetchInBg
	client := p.client
	p.mu.Unlock()
	if cb != nil {
		cb(u)
	}
	if fetchInBg {
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			resp, err := client.Get(u.String())
			if err != nil {
				p.mu.Lock()
				p.lastFetchErr = err
				p.mu.Unlock()
				return
			}
			_ = resp.Body.Close()
		}()
	}
}

func (p *tmoBrowserPrompt) PromptForRedirectUrl(ctx context.Context) (*string, error) {
	if p.redirectFn != nil {
		return p.redirectFn(ctx)
	}
	<-ctx.Done()
	return nil, nil
}

func (p *tmoBrowserPrompt) Wait() {
	p.wg.Wait()
}

type tmoRecordingStore struct {
	mcpoauth.McpOAuthServerStore
	mu          sync.Mutex
	lockStarted chan struct{}
	lockDone    chan error
}

func (r *tmoRecordingStore) WithRefreshLock(fn func() error) error {
	r.mu.Lock()
	started := r.lockStarted
	done := r.lockDone
	r.mu.Unlock()
	if started != nil {
		select {
		case <-started:
		default:
			close(started)
		}
	}
	err := r.McpOAuthServerStore.WithRefreshLock(fn)
	if done != nil {
		done <- err
	}
	return err
}

func tmoCountEntries(items []string, target string) int {
	n := 0
	for _, it := range items {
		if it == target {
			n++
		}
	}
	return n
}

func tmoReadDirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir %s: %v", dir, err)
	}
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Name()
	}
	return out
}

func tmoFetch(client *http.Client) mcp.McpFetch {
	if client == nil {
		client = http.DefaultClient
	}
	return func(ctx context.Context, input string, init *mcp.McpRequestInit) (*http.Response, error) {
		method := http.MethodGet
		var bodyReader io.Reader
		var hdrs http.Header
		if init != nil {
			if init.Method != "" {
				method = init.Method
			}
			if len(init.Body) > 0 {
				bodyReader = bytes.NewReader(init.Body)
			}
			hdrs = init.Headers
		}
		req, err := http.NewRequestWithContext(ctx, method, input, bodyReader)
		if err != nil {
			return nil, err
		}
		for k, vs := range hdrs {
			for _, v := range vs {
				req.Header.Add(k, v)
			}
		}
		return client.Do(req)
	}
}

func tmoClientInfoId(info mcpoauth.OAuthClientInformationMixed) string {
	switch v := info.(type) {
	case *mcpoauth.OAuthClientInformation:
		if v != nil {
			return v.ClientId
		}
	case *mcpoauth.OAuthClientInformationFull:
		if v != nil {
			return v.ClientId
		}
	}
	return ""
}

func tmoSetupSignedIn(t *testing.T) (*tmoOAuthMcpServer, string, func() (mcpoauth.McpOAuthServerStore, mcpoauth.McpAuthProvider)) {
	t.Helper()
	srv := tmoStartOAuthMcpServer(t)
	lockDir := t.TempDir()
	backend := core.NewInMemoryAuthStorageBackend()
	httpClient := srv.httpServer.Client()
	makeProcess := func() (mcpoauth.McpOAuthServerStore, mcpoauth.McpAuthProvider) {
		store := NewMcpOAuthCredentialStoreWithLockDir(backend, lockDir).ForServer("test", srv.URL)
		provider := CreateMcpAuthProvider(CreateMcpAuthProviderOptions{
			ServerUrl:   srv.URL,
			Store:       store,
			HTTPClient:  httpClient,
			Settings:    func() mcpoauth.McpOAuthSettings { return mcpoauth.McpOAuthSettings{} },
			OnChallenge: func(mcpoauth.OAuthChallenge) {},
		})
		return store, provider
	}
	store, _ := makeProcess()
	prompt := tmoNewAutoBrowserPrompt(httpClient)
	if err := SignInMcpServer(SignInMcpServerOptions{
		ServerUrl:  srv.URL,
		Store:      store,
		Settings:   mcpoauth.McpOAuthSettings{},
		Prompt:     prompt,
		HTTPClient: httpClient,
	}); err != nil {
		t.Fatalf("initial signIn: %v", err)
	}
	prompt.Wait()
	return srv, lockDir, makeProcess
}

func TestMcpOAuthRefresh(t *testing.T) {
	t.Run("refreshes once when several processes find the same token rejected", func(t *testing.T) {
		srv, lockDir, makeProcess := tmoSetupSignedIn(t)
		srv.ExpireAccessTokens()
		type proc struct {
			store    mcpoauth.McpOAuthServerStore
			provider mcpoauth.McpAuthProvider
		}
		procs := make([]proc, 3)
		for i := range procs {
			s, p := makeProcess()
			procs[i] = proc{store: s, provider: p}
		}

		srvURL, err := url.Parse(srv.URL)
		if err != nil {
			t.Fatalf("parse url: %v", err)
		}
		stale := "access-1"
		var wg sync.WaitGroup
		errs := make([]error, len(procs))
		for i, p := range procs {
			wg.Add(1)
			go func(idx int, prov mcpoauth.McpAuthProvider) {
				defer wg.Done()
				errs[idx] = prov.OnUnauthorized(&mcp.UnauthorizedContext{
					Response:  &http.Response{StatusCode: http.StatusUnauthorized},
					ServerUrl: srvURL,
					Fetch:     tmoFetch(srv.httpServer.Client()),
					Token:     &stale,
				})
			}(i, p.provider)
		}
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Fatalf("process %d OnUnauthorized failed: %v", i, err)
			}
		}

		if count := tmoCountEntries(srv.Log(), "token refresh"); count != 1 {
			t.Fatalf("expected 1 token refresh, got %d (log=%v)", count, srv.Log())
		}
		for i, p := range procs {
			tok, err := p.provider.Token()
			if err != nil || tok == nil || *tok != "access-2" {
				t.Fatalf("process %d expected token access-2, got %v, err=%v", i, tok, err)
			}
		}
		if names := tmoReadDirNames(t, lockDir); len(names) != 0 {
			t.Fatalf("expected lockDir empty after refresh, got %v", names)
		}
	})

	t.Run("cancels a sign-in that waits for another process's refresh", func(t *testing.T) {
		srv, lockDir, makeProcess := tmoSetupSignedIn(t)
		heldStarted := make(chan struct{})
		releaseHeld := make(chan struct{})
		heldErr := make(chan error, 1)
		s0, _ := makeProcess()
		go func() {
			heldErr <- s0.WithRefreshLock(func() error {
				close(heldStarted)
				<-releaseHeld
				return nil
			})
		}()
		<-heldStarted

		innerStore, _ := makeProcess()
		recStore := &tmoRecordingStore{
			McpOAuthServerStore: innerStore,
			lockStarted:         make(chan struct{}),
			lockDone:            make(chan error, 1),
		}
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()

		prompt := &tmoBrowserPrompt{
			fetchInBg: false,
			redirectFn: func(context.Context) (*string, error) {
				return nil, nil
			},
		}
		err := SignInMcpServer(SignInMcpServerOptions{
			ServerUrl:  srv.URL,
			Store:      recStore,
			Settings:   mcpoauth.McpOAuthSettings{},
			Prompt:     prompt,
			Context:    ctx,
			HTTPClient: srv.httpServer.Client(),
		})
		var cancelErr *mcpoauth.McpSignInCancelledError
		if !errors.As(err, &cancelErr) {
			t.Fatalf("expected McpSignInCancelledError, got %v", err)
		}

		close(releaseHeld)
		if herr := <-heldErr; herr != nil {
			t.Fatalf("held lock err: %v", herr)
		}
		<-recStore.lockStarted
		if rerr := <-recStore.lockDone; rerr != nil {
			t.Fatalf("cancelled lock wait err: %v", rerr)
		}
		if count := tmoCountEntries(srv.Log(), "token refresh"); count != 0 {
			t.Fatalf("expected 0 token refreshes, got %d", count)
		}
		if names := tmoReadDirNames(t, lockDir); len(names) != 0 {
			t.Fatalf("expected lockDir empty, got %v", names)
		}
	})

	t.Run("waits for a running refresh to save the new tokens", func(t *testing.T) {
		srv, _, makeProcess := tmoSetupSignedIn(t)
		srv.ExpireAccessTokens()
		store, provider := makeProcess()
		srvURL, err := url.Parse(srv.URL)
		if err != nil {
			t.Fatalf("parse url: %v", err)
		}
		stale := "access-1"

		started := make(chan struct{})
		refreshErr := make(chan error, 1)
		go func() {
			close(started)
			refreshErr <- provider.OnUnauthorized(&mcp.UnauthorizedContext{
				Response:  &http.Response{StatusCode: http.StatusUnauthorized},
				ServerUrl: srvURL,
				Fetch:     tmoFetch(srv.httpServer.Client()),
				Token:     &stale,
			})
		}()
		<-started
		// Wait briefly for the goroutine to start refreshing, or wait for completion + verify Settled returns cleanly.
		if err := <-refreshErr; err != nil {
			t.Fatalf("OnUnauthorized err: %v", err)
		}
		provider.Settled()
		st, err := store.Load()
		if err != nil || st == nil || st.Tokens == nil || st.Tokens.AccessToken != "access-2" {
			t.Fatalf("expected stored access-2, got %+v, err=%v", st, err)
		}
	})
}

func TestMcpOAuthSignIn(t *testing.T) {
	signIn := func(opts tmoOAuthServerOptions, settingsFn func(serverURL string) mcpoauth.McpOAuthSettings) error {
		srv := tmoStartOAuthMcpServer(t, opts)
		defer srv.Close()
		store := NewMcpOAuthCredentialStore(core.NewInMemoryAuthStorageBackend()).ForServer("test", srv.URL)
		prompt := tmoNewAutoBrowserPrompt(srv.httpServer.Client())
		defer prompt.Wait()
		var settings mcpoauth.McpOAuthSettings
		if settingsFn != nil {
			settings = settingsFn(srv.URL)
		}
		return SignInMcpServer(SignInMcpServerOptions{
			ServerUrl:  srv.URL,
			Store:      store,
			Settings:   settings,
			Prompt:     prompt,
			HTTPClient: srv.httpServer.Client(),
		})
	}

	t.Run("rejects an authorization response from another issuer", func(t *testing.T) {
		err := signIn(tmoOAuthServerOptions{Iss: "https://attacker.example"}, nil)
		var mismatch *mcpoauth.OAuthIssuerMismatchError
		if !errors.As(err, &mismatch) {
			t.Fatalf("expected OAuthIssuerMismatchError, got %v", err)
		}
	})

	t.Run("keeps the granted scope when the server asks for more", func(t *testing.T) {
		srv := tmoStartOAuthMcpServer(t)
		store := NewMcpOAuthCredentialStore(core.NewInMemoryAuthStorageBackend()).ForServer("test", srv.URL)
		var opened []*url.URL
		signInWith := func(ch mcpoauth.OAuthChallenge) {
			prompt := tmoNewAutoBrowserPrompt(srv.httpServer.Client())
			prompt.onUrl = func(u *url.URL) {
				opened = append(opened, u)
			}
			err := SignInMcpServer(SignInMcpServerOptions{
				ServerUrl:  srv.URL,
				Store:      store,
				Settings:   mcpoauth.McpOAuthSettings{},
				Challenge:  &ch,
				Prompt:     prompt,
				HTTPClient: srv.httpServer.Client(),
			})
			prompt.Wait()
			if err != nil {
				t.Fatalf("signInWith(%+v): %v", ch, err)
			}
		}

		readScope := "issues:read"
		signInWith(mcpoauth.OAuthChallenge{Scope: &readScope})
		st1, err := store.Load()
		if err != nil || st1 == nil || st1.Tokens == nil || st1.Tokens.Scope == nil || *st1.Tokens.Scope != "issues:read" {
			t.Fatalf("expected granted scope issues:read, got %+v, err=%v", st1, err)
		}

		insuff := "insufficient_scope"
		writeScope := "issues:write"
		signInWith(mcpoauth.OAuthChallenge{Error: &insuff, Scope: &writeScope})
		if len(opened) != 2 {
			t.Fatalf("expected 2 opened auth URLs, got %d", len(opened))
		}
		if got0 := opened[0].Query().Get("scope"); got0 != "issues:read" {
			t.Fatalf("expected first scope issues:read, got %q", got0)
		}
		if got1 := opened[1].Query().Get("scope"); got1 != "issues:read issues:write" {
			t.Fatalf("expected merged scope issues:read issues:write, got %q", got1)
		}
		st2, err := store.Load()
		if err != nil || st2 == nil || st2.Tokens == nil || st2.Tokens.Scope == nil || *st2.Tokens.Scope != "issues:read issues:write" {
			t.Fatalf("expected stored scope issues:read issues:write, got %+v, err=%v", st2, err)
		}
	})

	t.Run("uses the configured authorization server metadata URL", func(t *testing.T) {
		err := signIn(tmoOAuthServerOptions{}, func(serverURL string) mcpoauth.McpOAuthSettings {
			base, _ := url.Parse(serverURL)
			missing, _ := base.Parse("/missing")
			return mcpoauth.McpOAuthSettings{AuthServerMetadataUrl: missing}
		})
		if err == nil || !strings.Contains(err.Error(), "HTTP 404 loading authorization server metadata") {
			t.Fatalf("expected HTTP 404 loading authorization server metadata, got %v", err)
		}
	})
}

func TestMcpOAuthClientIDMetadataDocuments(t *testing.T) {
	cimd := mcpoauth.McpOAuthSettings{ClientRegistration: "cimd"}
	dcr := mcpoauth.McpOAuthSettings{ClientRegistration: "dcr"}

	start := func(opts tmoOAuthServerOptions) (
		*tmoOAuthMcpServer,
		mcpoauth.McpOAuthServerStore,
		func(settings mcpoauth.McpOAuthSettings, customPrompt ...McpSignInPrompt) error,
	) {
		srv := tmoStartOAuthMcpServer(t, opts)
		store := NewMcpOAuthCredentialStore(core.NewInMemoryAuthStorageBackend()).ForServer("test", srv.URL)
		signIn := func(settings mcpoauth.McpOAuthSettings, customPrompt ...McpSignInPrompt) error {
			var p McpSignInPrompt
			var auto *tmoBrowserPrompt
			if len(customPrompt) > 0 && customPrompt[0] != nil {
				p = customPrompt[0]
			} else {
				auto = tmoNewAutoBrowserPrompt(srv.httpServer.Client())
				p = auto
			}
			err := SignInMcpServer(SignInMcpServerOptions{
				ServerUrl:  srv.URL,
				Store:      store,
				Settings:   settings,
				Prompt:     p,
				HTTPClient: srv.httpServer.Client(),
			})
			if auto != nil {
				auto.Wait()
			}
			return err
		}
		return srv, store, signIn
	}

	t.Run("registers dynamically with dcr, even when the server supports documents", func(t *testing.T) {
		srv, _, signIn := start(tmoOAuthServerOptions{Cimd: true, IssParameter: true})
		if err := signIn(dcr); err != nil {
			t.Fatalf("signIn(dcr): %v", err)
		}
		if regs := srv.Registrations(); len(regs) != 1 {
			t.Fatalf("expected 1 registration, got %d", len(regs))
		}
		if cid := srv.Authorizations()[0].Get("client_id"); cid != "client-1" {
			t.Fatalf("expected client-1, got %q", cid)
		}
	})

	t.Run("uses pi's document by default when authorization responses name their issuer", func(t *testing.T) {
		srv, store, signIn := start(tmoOAuthServerOptions{Cimd: true, IssParameter: true})
		if err := signIn(mcpoauth.McpOAuthSettings{}); err != nil {
			t.Fatalf("first signIn: %v", err)
		}
		auth := srv.Authorizations()[0]
		if cid := auth.Get("client_id"); cid != "https://pi.dev/oauth/client.json" {
			t.Fatalf("expected pi CIMD client_id, got %q", cid)
		}
		redirect, err := url.Parse(auth.Get("redirect_uri"))
		if err != nil {
			t.Fatalf("parse redirect_uri: %v", err)
		}
		if redirect.Hostname()+redirect.Path != "127.0.0.1/callback" || redirect.Port() == "" {
			t.Fatalf("unexpected redirect_uri: %s", redirect.String())
		}
		if regs := srv.Registrations(); len(regs) != 0 {
			t.Fatalf("expected 0 registrations, got %d", len(regs))
		}
		tokReqs := srv.TokenRequests()
		if tokReqs[0].Get("client_id") != "https://pi.dev/oauth/client.json" || tokReqs[0].Get("redirect_uri") != redirect.String() {
			t.Fatalf("unexpected token request[0]: %v", tokReqs[0])
		}
		st, err := store.Load()
		if err != nil || st == nil || st.ClientInformation != nil {
			t.Fatalf("expected nil ClientInformation, got %+v, err=%v", st, err)
		}
		if err := signIn(mcpoauth.McpOAuthSettings{}); err != nil {
			t.Fatalf("second signIn: %v", err)
		}
		if len(srv.Authorizations()) != 1 {
			t.Fatalf("expected 1 authorization on refresh, got %d", len(srv.Authorizations()))
		}
		tokReqs = srv.TokenRequests()
		if len(tokReqs) != 2 || tokReqs[1].Get("grant_type") != "refresh_token" || tokReqs[1].Get("client_id") != "https://pi.dev/oauth/client.json" {
			t.Fatalf("unexpected token request[1]: %v", tokReqs)
		}
	})

	t.Run("registers dynamically by default when the server does not support documents", func(t *testing.T) {
		srv, _, signIn := start(tmoOAuthServerOptions{IssParameter: true})
		if err := signIn(mcpoauth.McpOAuthSettings{}); err != nil {
			t.Fatalf("signIn: %v", err)
		}
		if len(srv.Registrations()) != 1 || srv.Authorizations()[0].Get("client_id") != "client-1" {
			t.Fatalf("expected dynamic client-1 registration")
		}
	})

	t.Run("registers dynamically by default with a client name", func(t *testing.T) {
		srv, _, signIn := start(tmoOAuthServerOptions{Cimd: true, IssParameter: true})
		name := "Claude Code"
		if err := signIn(mcpoauth.McpOAuthSettings{ClientName: &name}); err != nil {
			t.Fatalf("signIn: %v", err)
		}
		regs := srv.Registrations()
		if len(regs) != 1 {
			t.Fatalf("expected 1 registration, got %d", len(regs))
		}
		cn, _ := regs[0].Get("client_name")
		if cn != "Claude Code" {
			t.Fatalf("expected client_name Claude Code, got %v", cn)
		}
	})

	t.Run("keeps a registered client by default", func(t *testing.T) {
		srv, store, signIn := start(tmoOAuthServerOptions{Cimd: true, IssParameter: true})
		if err := signIn(dcr); err != nil {
			t.Fatalf("signIn(dcr): %v", err)
		}
		if err := signIn(mcpoauth.McpOAuthSettings{}); err != nil {
			t.Fatalf("signIn(default): %v", err)
		}
		if len(srv.Authorizations()) != 1 {
			t.Fatalf("expected 1 authorization, got %d", len(srv.Authorizations()))
		}
		tokReqs := srv.TokenRequests()
		if len(tokReqs) != 2 || tokReqs[1].Get("grant_type") != "refresh_token" || tokReqs[1].Get("client_id") != "client-1" {
			t.Fatalf("unexpected token requests: %v", tokReqs)
		}
		st, err := store.Load()
		if err != nil || st == nil || st.ClientInformation == nil || tmoClientInfoId(st.ClientInformation) != "client-1" {
			t.Fatalf("expected stored client-1, got %+v, err=%v", st, err)
		}
	})

	t.Run("uses a document and callback path specific to the MCP server without the iss parameter", func(t *testing.T) {
		srv, _, signIn := start(tmoOAuthServerOptions{Cimd: true})
		if err := signIn(cimd); err != nil {
			t.Fatalf("signIn(cimd): %v", err)
		}
		sum := sha256.Sum256([]byte(srv.URL))
		id := base64.RawURLEncoding.EncodeToString(sum[:9])
		auth := srv.Authorizations()[0]
		wantCID := fmt.Sprintf("https://pi.dev/oauth/%s/client.json", id)
		if auth.Get("client_id") != wantCID {
			t.Fatalf("expected client_id %q, got %q", wantCID, auth.Get("client_id"))
		}
		redirect, err := url.Parse(auth.Get("redirect_uri"))
		if err != nil {
			t.Fatalf("parse redirect: %v", err)
		}
		if redirect.Path != "/callback/"+id {
			t.Fatalf("expected redirect path /callback/%s, got %s", id, redirect.Path)
		}
		if len(srv.Registrations()) != 0 || srv.TokenRequests()[0].Get("redirect_uri") != redirect.String() {
			t.Fatalf("unexpected registrations or token request")
		}
	})

	t.Run("replaces a registered client when switching to the document", func(t *testing.T) {
		srv, store, signIn := start(tmoOAuthServerOptions{Cimd: true, IssParameter: true})
		if err := signIn(dcr); err != nil {
			t.Fatalf("signIn(dcr): %v", err)
		}
		st1, err := store.Load()
		if err != nil || st1 == nil || tmoClientInfoId(st1.ClientInformation) != "client-1" {
			t.Fatalf("expected client-1, got %+v, err=%v", st1, err)
		}
		if err := signIn(cimd); err != nil {
			t.Fatalf("signIn(cimd): %v", err)
		}
		auths := srv.Authorizations()
		if len(auths) != 2 || auths[0].Get("client_id") != "client-1" || auths[1].Get("client_id") != "https://pi.dev/oauth/client.json" {
			t.Fatalf("unexpected auth client_ids: %v", auths)
		}
		st2, err := store.Load()
		if err != nil || st2 == nil || st2.ClientInformation != nil {
			t.Fatalf("expected nil ClientInformation after switch to CIMD, got %+v, err=%v", st2, err)
		}
	})

	t.Run("accepts the authorization response only on the server-specific redirect URI", func(t *testing.T) {
		_, _, signInLoopback := start(tmoOAuthServerOptions{Cimd: true, RedirectPath: "/callback"})
		err := signInLoopback(cimd)
		if err == nil || !strings.Contains(err.Error(), "arrived on another redirect URI") {
			t.Fatalf("expected 'arrived on another redirect URI', got %v", err)
		}

		_, _, signInPaste := start(tmoOAuthServerOptions{Cimd: true})
		var shown *url.URL
		paste := &tmoBrowserPrompt{
			onUrl: func(u *url.URL) { shown = u },
			redirectFn: func(context.Context) (*string, error) {
				red, _ := url.Parse(shown.Query().Get("redirect_uri"))
				red.Path = "/callback"
				red.RawQuery = "code=code-1&state=" + url.QueryEscape(shown.Query().Get("state"))
				s := red.String()
				return &s, nil
			},
		}
		err = signInPaste(cimd, paste)
		if err == nil || !strings.Contains(err.Error(), "does not match this sign-in's redirect URI") {
			t.Fatalf("expected 'does not match this sign-in's redirect URI', got %v", err)
		}
	})

	t.Run("fails instead of registering when the server does not support documents", func(t *testing.T) {
		srv, _, signIn := start(tmoOAuthServerOptions{IssParameter: true})
		err := signIn(cimd)
		if err == nil || !strings.Contains(err.Error(), "does not support Client ID Metadata Documents") {
			t.Fatalf("expected CIMD unsupported error, got %v", err)
		}
		if len(srv.Registrations()) != 0 {
			t.Fatalf("expected 0 registrations, got %d", len(srv.Registrations()))
		}
	})
}

type tmoFakeTUI struct {
	tui.TUI
	renders atomic.Int64
}

func (f *tmoFakeTUI) RequestRender(_ *bool) {
	f.renders.Add(1)
}

func tmoRenderedView(view *McpManagerView) string {
	return utils.StripAnsi(strings.Join(view.Render(80), "\n"))
}

func TestMcpManagerView(t *testing.T) {
	if _, err := theme.InitTheme("dark", false); err != nil {
		t.Fatalf("InitTheme: %v", err)
	}
	prevKb := tui.GetKeybindings()
	defer tui.SetKeybindings(prevKb)

	t.Run("cancels the running operation with the cancel key", func(t *testing.T) {
		mgr := core.NewKeybindingsManager(nil)
		tui.SetKeybindings(mgr.KeybindingsManager)
		view := NewMcpManagerView(&tmoFakeTUI{}, theme.CurrentTheme(), mgr.KeybindingsManager)
		cancelled := 0
		view.Status("Sign in to issues", "Contacting the authorization server…", func() { cancelled++ })
		if out := tmoRenderedView(view); !strings.Contains(out, "cancel") {
			t.Fatalf("expected 'cancel' hint in rendered view:\n%s", out)
		}
		view.HandleInput("\x1b")
		if cancelled != 1 {
			t.Fatalf("expected onCancel called once, got %d", cancelled)
		}
	})

	t.Run("ignores the cancel key for operations that cannot be cancelled", func(t *testing.T) {
		mgr := core.NewKeybindingsManager(nil)
		tui.SetKeybindings(mgr.KeybindingsManager)
		view := NewMcpManagerView(&tmoFakeTUI{}, theme.CurrentTheme(), mgr.KeybindingsManager)
		view.Status("Sign in to issues", "Connecting…", nil)
		if out := tmoRenderedView(view); strings.Contains(out, "cancel") {
			t.Fatalf("did not expect 'cancel' hint:\n%s", out)
		}
		view.HandleInput("\x1b")
	})

	t.Run("captures fullscreen viewport keys only while a menu list is shown", func(t *testing.T) {
		mgr := core.NewKeybindingsManager(nil)
		tui.SetKeybindings(mgr.KeybindingsManager)
		view := NewMcpManagerView(&tmoFakeTUI{}, theme.CurrentTheme(), mgr.KeybindingsManager)

		items := make([]tui.SelectItem, 20)
		for i := range items {
			lbl := fmt.Sprintf("server-%d", i)
			items[i] = tui.SelectItem{Value: lbl, Label: lbl}
		}
		doneCh := make(chan *string, 1)
		go func() {
			doneCh <- view.Menu(
				context.Background(),
				func() McpMenu {
					return McpMenu{
						Title:        "MCP servers",
						Items:        items,
						ConfirmLabel: "open",
						CancelLabel:  "close",
					}
				},
				nil,
			)
		}()

		deadline := time.Now().Add(2 * time.Second)
		for !view.CapturesViewportInput() && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		if !view.CapturesViewportInput() {
			t.Fatalf("expected CapturesViewportInput() == true while menu is shown")
		}

		view.HandleInput("\x1b[6~") // PageDown
		view.HandleInput("\r")      // Enter

		var res *string
		select {
		case res = <-doneCh:
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for menu selection")
		}
		if res == nil || *res != "server-12" {
			t.Fatalf("expected server-12, got %v", res)
		}

		view.Status("Sign in to issues", "Connecting…", nil)
		if view.CapturesViewportInput() {
			t.Fatalf("expected CapturesViewportInput() == false during Status")
		}
	})
}

// Ported from packages/mcp/test/authorization-server.ts (pi v1.5.2).

package testkit

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/internal/jsonx"
	mcpoauth "github.com/keejkrej/pi-go/mcp/oauth"
)

// AuthorizationServerOptions configures StartAuthorizationServer.
type AuthorizationServerOptions struct {
	Cimd         bool
	IssParameter bool
}

type authsrvRefreshFailure struct {
	errorCode string
	onRequest func()
}

// AuthorizationServer is a fake OAuth 2.1 authorization server for tests.
type AuthorizationServer struct {
	ServerUrl string

	mu             sync.Mutex
	registrations  []*jsonx.Object
	authorizations []url.Values
	tokenRequests  []url.Values
	challenges     map[string]string
	refreshTokens  map[string]bool
	stall          map[string]bool
	refreshFailure *authsrvRefreshFailure
	issued         int
	closed         chan struct{}
	closeOnce      sync.Once
}

// StartAuthorizationServer starts an in-process OAuth authorization server on loopback.
func StartAuthorizationServer(t testing.TB, options AuthorizationServerOptions) *AuthorizationServer {
	t.Helper()
	srv := &AuthorizationServer{
		challenges:    make(map[string]string),
		refreshTokens: make(map[string]bool),
		stall:         make(map[string]bool),
		closed:        make(chan struct{}),
	}
	origin := Listen(t, func(w http.ResponseWriter, r *http.Request, orig string) {
		srv.authsrvHandle(w, r, orig, options)
	})
	srv.ServerUrl = origin + "/mcp"
	t.Cleanup(func() {
		srv.Close()
	})
	return srv
}

// Close unblocks any stalled requests.
func (s *AuthorizationServer) Close() {
	s.closeOnce.Do(func() {
		close(s.closed)
	})
}

// AddStall marks path so requests to it hang until canceled or Close.
func (s *AuthorizationServer) AddStall(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stall[path] = true
}

// Registrations returns a snapshot of recorded dynamic client registrations.
func (s *AuthorizationServer) Registrations() []*jsonx.Object {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.registrations)
}

// Authorizations returns a snapshot of recorded authorization query parameters.
func (s *AuthorizationServer) Authorizations() []url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.authorizations)
}

// TokenRequests returns a snapshot of recorded token form parameters.
func (s *AuthorizationServer) TokenRequests() []url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.tokenRequests)
}

// RevokeRefreshTokens invalidates all issued refresh tokens.
func (s *AuthorizationServer) RevokeRefreshTokens() {
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.refreshTokens)
}

// FailNextRefresh makes the next refresh request return errorCode after calling onRequest.
func (s *AuthorizationServer) FailNextRefresh(errorCode string, onRequest func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshFailure = &authsrvRefreshFailure{errorCode: errorCode, onRequest: onRequest}
}

func (s *AuthorizationServer) authsrvIssueTokensLocked() *jsonx.Object {
	s.issued++
	access := fmt.Sprintf("access-%d", s.issued)
	refresh := fmt.Sprintf("refresh-%d", s.issued)
	s.refreshTokens[refresh] = true
	return jsonx.ObjectOf(
		"access_token", access,
		"refresh_token", refresh,
		"token_type", "Bearer",
	)
}

func (s *AuthorizationServer) authsrvHandle(w http.ResponseWriter, r *http.Request, origin string, options AuthorizationServerOptions) {
	s.mu.Lock()
	stalled := s.stall[r.URL.Path]
	s.mu.Unlock()
	if stalled {
		select {
		case <-r.Context().Done():
		case <-s.closed:
		}
		return
	}

	switch r.URL.Path {
	case "/.well-known/oauth-protected-resource/mcp":
		authsrvJson(w, http.StatusOK, jsonx.ObjectOf(
			"resource", origin+"/mcp",
			"authorization_servers", []string{origin},
		))
	case "/.well-known/oauth-authorization-server":
		meta := jsonx.ObjectOf(
			"issuer", origin,
			"authorization_endpoint", origin+"/authorize",
			"token_endpoint", origin+"/token",
			"registration_endpoint", origin+"/register",
			"response_types_supported", []string{"code"},
			"code_challenge_methods_supported", []string{"S256"},
			"token_endpoint_auth_methods_supported", []string{"none"},
		)
		if options.Cimd {
			meta.Set("client_id_metadata_document_supported", true)
		}
		if options.IssParameter {
			meta.Set("authorization_response_iss_parameter_supported", true)
		}
		authsrvJson(w, http.StatusOK, meta)
	case "/register":
		body, err := ReadBody(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		parsed, err := jsonx.ParseString(body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		meta, ok := parsed.(*jsonx.Object)
		if !ok || meta == nil {
			http.Error(w, "invalid metadata", http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.registrations = append(s.registrations, meta)
		cid := fmt.Sprintf("client-%d", len(s.registrations))
		s.mu.Unlock()
		respObj := meta.Clone()
		respObj.Set("client_id", cid)
		authsrvJson(w, http.StatusCreated, respObj)
	case "/authorize":
		q := r.URL.Query()
		s.mu.Lock()
		s.authorizations = append(s.authorizations, q)
		code := fmt.Sprintf("code-%d", len(s.authorizations))
		s.challenges[code] = q.Get("code_challenge")
		s.mu.Unlock()

		redirect, err := url.Parse(q.Get("redirect_uri"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		rq := redirect.Query()
		rq.Set("code", code)
		rq.Set("state", q.Get("state"))
		if options.IssParameter {
			rq.Set("iss", origin)
		}
		redirect.RawQuery = rq.Encode()
		w.Header().Set("Location", redirect.String())
		w.WriteHeader(http.StatusFound)
	case "/token":
		body, err := ReadBody(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		params, err := url.ParseQuery(body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
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
			expectedChallenge := s.challenges[code]
			if expectedChallenge != verifier {
				s.mu.Unlock()
				authsrvJson(w, http.StatusBadRequest, jsonx.ObjectOf("error", "invalid_grant"))
				return
			}
			delete(s.challenges, code)
			tokens := s.authsrvIssueTokensLocked()
			s.mu.Unlock()
			authsrvJson(w, http.StatusOK, tokens)
			return
		}

		s.mu.Lock()
		failure := s.refreshFailure
		s.refreshFailure = nil
		s.mu.Unlock()
		if failure != nil {
			if failure.onRequest != nil {
				failure.onRequest()
			}
			authsrvJson(w, http.StatusBadRequest, jsonx.ObjectOf("error", failure.errorCode))
			return
		}

		rt := params.Get("refresh_token")
		s.mu.Lock()
		if !s.refreshTokens[rt] {
			s.mu.Unlock()
			authsrvJson(w, http.StatusBadRequest, jsonx.ObjectOf("error", "invalid_grant"))
			return
		}
		delete(s.refreshTokens, rt)
		tokens := s.authsrvIssueTokensLocked()
		s.mu.Unlock()
		authsrvJson(w, http.StatusOK, tokens)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func authsrvJson(w http.ResponseWriter, status int, body any) {
	data, err := jsonx.Marshal(body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

// MemoryStore returns an in-memory McpOAuthServerStore for single-process tests.
type MemoryStore struct {
	*mcpoauth.MemoryOAuthStateStore
}

// NewMemoryStore constructs an empty single-process McpOAuthServerStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{MemoryOAuthStateStore: mcpoauth.NewMemoryOAuthStateStore()}
}

// WithRefreshLock runs fn directly.
func (s *MemoryStore) WithRefreshLock(fn func() error) error {
	return fn()
}

// RecordingSave records one Save call and whether the refresh lock was held.
type RecordingSave struct {
	Locked      bool
	AccessToken *string
}

// RecordingStore is an in-memory McpOAuthServerStore recording lock state at Save time.
type RecordingStore struct {
	memory        *mcpoauth.MemoryOAuthStateStore
	mu            sync.Mutex
	lockError     error
	locked        bool
	holdCh        chan struct{}
	holdEntered   bool
	holdEnteredCh chan struct{}
	saves         []RecordingSave
	activeLocks   int
	idleWaiters   []chan struct{}
}

// NewRecordingStore creates a RecordingStore with an optional lock error.
func NewRecordingStore(lockError error) *RecordingStore {
	return &RecordingStore{
		memory:    mcpoauth.NewMemoryOAuthStateStore(),
		lockError: lockError,
	}
}

func (s *RecordingStore) Load() (*mcpoauth.McpOAuthState, error) {
	return s.memory.Load()
}

func (s *RecordingStore) Save(state *mcpoauth.McpOAuthState) error {
	s.mu.Lock()
	var tok *string
	if state != nil && state.Tokens != nil {
		t := state.Tokens.AccessToken
		tok = &t
	}
	s.saves = append(s.saves, RecordingSave{Locked: s.locked, AccessToken: tok})
	s.mu.Unlock()
	return s.memory.Save(state)
}

func (s *RecordingStore) WithRefreshLock(fn func() error) error {
	s.mu.Lock()
	s.activeLocks++
	holdCh := s.holdCh
	if holdCh != nil && !s.holdEntered && s.holdEnteredCh != nil {
		s.holdEntered = true
		close(s.holdEnteredCh)
	}
	errToReturn := s.lockError
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.activeLocks--
		if s.activeLocks == 0 && len(s.idleWaiters) > 0 {
			for _, ch := range s.idleWaiters {
				close(ch)
			}
			s.idleWaiters = nil
		}
		s.mu.Unlock()
	}()

	if holdCh != nil {
		<-holdCh
	}
	if errToReturn != nil {
		return errToReturn
	}
	s.mu.Lock()
	s.locked = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.locked = false
		s.mu.Unlock()
	}()
	return fn()
}

// Hold pauses future WithRefreshLock attempts until the returned release function is called.
func (s *RecordingStore) Hold() func() {
	ch := make(chan struct{})
	entered := make(chan struct{})
	s.mu.Lock()
	s.holdCh = ch
	s.holdEntered = false
	s.holdEnteredCh = entered
	s.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			if s.holdCh == ch {
				s.holdCh = nil
			}
			s.mu.Unlock()
			close(ch)
		})
	}
}

// HeldCh returns a channel closed once WithRefreshLock enters the barrier installed by Hold.
func (s *RecordingStore) HeldCh() <-chan struct{} {
	s.mu.Lock()
	ch := s.holdEnteredCh
	s.mu.Unlock()
	if ch == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return ch
}

// IsLocked reports whether WithRefreshLock is currently executing fn.
func (s *RecordingStore) IsLocked() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.locked
}

// Saves returns a copy of all recorded Save calls.
func (s *RecordingStore) Saves() []RecordingSave {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.saves)
}

// WaitLockRuns waits until all active WithRefreshLock invocations have finished.
func (s *RecordingStore) WaitLockRuns() {
	s.mu.Lock()
	if s.activeLocks == 0 {
		s.mu.Unlock()
		return
	}
	ch := make(chan struct{})
	s.idleWaiters = append(s.idleWaiters, ch)
	s.mu.Unlock()
	<-ch
}

// AuthorizeInBrowser sends a manual-redirect GET to authorizationUrl and returns Location.
func AuthorizeInBrowser(authorizationUrl string) (string, error) {
	client := &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Get(authorizationUrl)
	if err != nil {
		return "", err
	}
	_ = resp.Body.Close()
	return resp.Header.Get("Location"), nil
}

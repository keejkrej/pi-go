package oauth

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/keejkrej/pi-go/ai"
)

type tkcoRoundTripFunc func(*http.Request) (*http.Response, error)

func (f tkcoRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type tkcoInteraction struct {
	mu     sync.Mutex
	events []ai.AuthEvent
}

func (i *tkcoInteraction) Notify(event ai.AuthEvent) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.events = append(i.events, event)
}

func (i *tkcoInteraction) Prompt(_ context.Context, _ ai.AuthPrompt) (string, error) {
	return "", errors.New("unexpected prompt")
}

func (i *tkcoInteraction) snapshotEvents() []ai.AuthEvent {
	i.mu.Lock()
	defer i.mu.Unlock()
	out := make([]ai.AuthEvent, len(i.events))
	copy(out, i.events)
	return out
}

func tkcoResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestKimiCodingOAuth_LoginWithCustomHostAndWaitBeforeFirstPoll(t *testing.T) {
	t.Setenv("KIMI_CODE_OAUTH_HOST", "https://custom.kimi.test/")

	synctest.Test(t, func(t *testing.T) {
		var mu sync.Mutex
		var requestedUrls []string
		pollCount := 0

		client := &http.Client{
			Transport: tkcoRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				raw, _ := io.ReadAll(req.Body)
				form, _ := url.ParseQuery(string(raw))

				mu.Lock()
				requestedUrls = append(requestedUrls, req.URL.String())
				mu.Unlock()

				switch req.URL.String() {
				case "https://custom.kimi.test/api/oauth/device_authorization":
					if got := form.Get("client_id"); got != kcClientId {
						t.Fatalf("device auth client_id = %q, want %q", got, kcClientId)
					}
					return tkcoResponse(http.StatusOK, `{
						"device_code": "kc-device",
						"user_code": "KIMI-123",
						"verification_uri": "https://custom.kimi.test/verify",
						"verification_uri_complete": "https://custom.kimi.test/verify?user_code=KIMI-123",
						"interval": 2,
						"expires_in": 60
					}`), nil
				case "https://custom.kimi.test/api/oauth/token":
					mu.Lock()
					pollCount += 1
					attempt := pollCount
					mu.Unlock()
					if got := form.Get("device_code"); got != "kc-device" {
						t.Fatalf("poll device_code = %q, want kc-device", got)
					}
					if attempt == 1 {
						return tkcoResponse(http.StatusBadRequest, `{"error":"authorization_pending"}`), nil
					}
					return tkcoResponse(http.StatusOK, `{"access_token":"kc-access","refresh_token":"kc-refresh","expires_in":1800}`), nil
				default:
					t.Fatalf("unexpected URL: %s", req.URL.String())
					return nil, nil
				}
			}),
		}

		interaction := &tkcoInteraction{}
		type loginRes struct {
			cred *ai.OAuthCredential
			err  error
		}
		done := make(chan loginRes, 1)
		go func() {
			cred, err := KimiCodingOAuth.Login(tuWithHttpClient(context.Background(), client), interaction, nil)
			done <- loginRes{cred, err}
		}()

		synctest.Wait()
		mu.Lock()
		pollsAtStart := pollCount
		mu.Unlock()
		if pollsAtStart != 0 {
			t.Fatalf("expected WaitBeforeFirstPoll=true (0 polls immediately), got %d", pollsAtStart)
		}

		events := interaction.snapshotEvents()
		if len(events) != 1 {
			t.Fatalf("expected 1 device-code event, got %d", len(events))
		}
		devEv, ok := events[0].(*ai.AuthEventDeviceCode)
		if !ok {
			t.Fatalf("expected *ai.AuthEventDeviceCode, got %T", events[0])
		}
		if devEv.UserCode != "KIMI-123" || devEv.VerificationUri != "https://custom.kimi.test/verify?user_code=KIMI-123" {
			t.Fatalf("unexpected device event: %+v", devEv)
		}
		if devEv.IntervalSeconds == nil || *devEv.IntervalSeconds != 2 {
			t.Fatalf("IntervalSeconds = %v, want 2", devEv.IntervalSeconds)
		}

		time.Sleep(1999 * time.Millisecond)
		synctest.Wait()
		mu.Lock()
		pollsBeforeFirst := pollCount
		mu.Unlock()
		if pollsBeforeFirst != 0 {
			t.Fatalf("expected 0 polls at 1999ms, got %d", pollsBeforeFirst)
		}

		time.Sleep(1 * time.Millisecond)
		synctest.Wait()
		mu.Lock()
		pollsAfterFirst := pollCount
		mu.Unlock()
		if pollsAfterFirst != 1 {
			t.Fatalf("expected 1 poll at 2000ms, got %d", pollsAfterFirst)
		}

		time.Sleep(2000 * time.Millisecond)
		synctest.Wait()

		res := <-done
		if res.err != nil {
			t.Fatalf("Login() failed: %v", res.err)
		}
		if res.cred.Access != "kc-access" || res.cred.Refresh != "kc-refresh" {
			t.Fatalf("unexpected credential: %+v", res.cred)
		}

		modelAuth, err := KimiCodingOAuth.ToAuth(context.Background(), res.cred)
		if err != nil {
			t.Fatalf("ToAuth() failed: %v", err)
		}
		if modelAuth.ApiKey != nil {
			t.Fatalf("expected nil ApiKey for header-based auth, got %v", *modelAuth.ApiKey)
		}
		authHeader, ok := modelAuth.Headers.Get("Authorization")
		if !ok || authHeader == nil || *authHeader != "Bearer kc-access" {
			t.Fatalf("Authorization header = %v (ok=%v), want Bearer kc-access", authHeader, ok)
		}
	})
}

func TestKimiCodingOAuth_FallbackHostEnvAndRejectsUntrustedVerificationUri(t *testing.T) {
	t.Setenv("KIMI_CODE_OAUTH_HOST", "")
	t.Setenv("KIMI_OAUTH_HOST", "https://fallback.kimi.test")

	var seenHost string
	client := &http.Client{
		Transport: tkcoRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			seenHost = req.URL.Host
			return tkcoResponse(http.StatusOK, `{
				"device_code": "kc-device",
				"user_code": "KIMI-BAD",
				"verification_uri": "javascript:alert(1)",
				"verification_uri_complete": "https://fallback.kimi.test/ok"
			}`), nil
		}),
	}

	_, err := KimiCodingOAuth.Login(tuWithHttpClient(context.Background(), client), &tkcoInteraction{}, nil)
	if seenHost != "fallback.kimi.test" {
		t.Fatalf("requested host = %q, want fallback.kimi.test", seenHost)
	}
	if err == nil || !strings.Contains(err.Error(), "Invalid Kimi Code device authorization response") {
		t.Fatalf("expected invalid device authorization response error, got %v", err)
	}
}

func TestKimiCodingOAuth_RefreshRetriesWithBackoffAndStopsOnUnauthorized(t *testing.T) {
	// Retry transient 500 errors twice then succeed on attempt 3 (1s + 2s backoff).
	synctest.Test(t, func(t *testing.T) {
		var mu sync.Mutex
		attempts := 0

		client := &http.Client{
			Transport: tkcoRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.String() != "https://auth.kimi.com/api/oauth/token" {
					t.Fatalf("unexpected refresh URL: %s", req.URL.String())
				}
				mu.Lock()
				attempts += 1
				n := attempts
				mu.Unlock()
				if n < 3 {
					return tkcoResponse(http.StatusInternalServerError, "temporary error"), nil
				}
				return tkcoResponse(http.StatusOK, `{"access_token":"new-access","refresh_token":"new-refresh","expires_in":3600}`), nil
			}),
		}

		type refreshRes struct {
			cred *ai.OAuthCredential
			err  error
		}
		done := make(chan refreshRes, 1)
		go func() {
			cred, err := KimiCodingOAuth.Refresh(tuWithHttpClient(context.Background(), client), &ai.OAuthCredential{
				OAuthCredentials: ai.OAuthCredentials{Refresh: "old-refresh"},
			})
			done <- refreshRes{cred, err}
		}()

		synctest.Wait()
		mu.Lock()
		n1 := attempts
		mu.Unlock()
		if n1 != 1 {
			t.Fatalf("attempts immediately = %d, want 1", n1)
		}

		time.Sleep(1000 * time.Millisecond)
		synctest.Wait()
		mu.Lock()
		n2 := attempts
		mu.Unlock()
		if n2 != 2 {
			t.Fatalf("attempts after 1s = %d, want 2", n2)
		}

		time.Sleep(2000 * time.Millisecond)
		synctest.Wait()

		res := <-done
		if res.err != nil {
			t.Fatalf("Refresh() failed: %v", res.err)
		}
		if res.cred.Access != "new-access" || res.cred.Refresh != "new-refresh" {
			t.Fatalf("unexpected refreshed credential: %+v", res.cred)
		}
	})

	// Do not retry 401/403/invalid_grant.
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{name: "401", status: http.StatusUnauthorized, body: `{"error":"invalid_token","error_description":"revoked"}`},
		{name: "403", status: http.StatusForbidden, body: `{"error":"forbidden"}`},
		{name: "invalid_grant", status: http.StatusBadRequest, body: `{"error":"invalid_grant"}`},
	} {
		attempts := 0
		client := &http.Client{
			Transport: tkcoRoundTripFunc(func(_ *http.Request) (*http.Response, error) {
				attempts += 1
				return tkcoResponse(tc.status, tc.body), nil
			}),
		}
		_, err := KimiCodingOAuth.Refresh(tuWithHttpClient(context.Background(), client), &ai.OAuthCredential{
			OAuthCredentials: ai.OAuthCredentials{Refresh: "bad-refresh"},
		})
		if err == nil {
			t.Fatalf("%s: expected error", tc.name)
		}
		if attempts != 1 {
			t.Fatalf("%s: attempts = %d, want 1 (no retry)", tc.name, attempts)
		}
	}
}

func TestKimiCodingOAuth_RefreshRealHttpServerParses200AndErrorBodies(t *testing.T) {
	var (
		mu       sync.Mutex
		status   int
		respBody string
		attempts int
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/oauth/token" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(raw))
		if form.Get("grant_type") != "refresh_token" || form.Get("refresh_token") != "live-refresh" {
			t.Errorf("unexpected form payload: %v", form)
		}
		mu.Lock()
		attempts += 1
		st := status
		body := respBody
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(st)
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()
	t.Setenv("KIMI_CODE_OAUTH_HOST", srv.URL)

	// Case 1: 200 JSON decode over real net/http transport.
	mu.Lock()
	status = http.StatusOK
	respBody = `{"access_token":"real-http-access","refresh_token":"real-http-rot","expires_in":3600}`
	attempts = 0
	mu.Unlock()

	cred, err := KimiCodingOAuth.Refresh(context.Background(), &ai.OAuthCredential{
		OAuthCredentials: ai.OAuthCredentials{Refresh: "live-refresh"},
	})
	if err != nil {
		t.Fatalf("200 Refresh() over real http failed: %v", err)
	}
	if cred.Access != "real-http-access" || cred.Refresh != "real-http-rot" || cred.Expires <= 0 {
		t.Fatalf("unexpected 200 credential: %+v", cred)
	}

	// Case 2: 400 invalid_grant with error_description over real net/http transport stops after 1 attempt.
	mu.Lock()
	status = http.StatusBadRequest
	respBody = `{"error":"invalid_grant","error_description":"refresh token revoked"}`
	attempts = 0
	mu.Unlock()

	_, err = KimiCodingOAuth.Refresh(context.Background(), &ai.OAuthCredential{
		OAuthCredentials: ai.OAuthCredentials{Refresh: "live-refresh"},
	})
	if err == nil || !strings.Contains(err.Error(), "Kimi Code token refresh unauthorized (status 400): refresh token revoked") {
		t.Fatalf("expected parsed unauthorized error over real http, got %v", err)
	}
	mu.Lock()
	gotAttempts := attempts
	mu.Unlock()
	if gotAttempts != 1 {
		t.Fatalf("attempts on invalid_grant = %d, want 1", gotAttempts)
	}

	// Case 3: Non-retryable 404 JSON error body preserves serialized JSON error detail.
	mu.Lock()
	status = http.StatusNotFound
	respBody = `{"error":"not_found","error_description":"missing client"}`
	attempts = 0
	mu.Unlock()

	_, err = KimiCodingOAuth.Refresh(context.Background(), &ai.OAuthCredential{
		OAuthCredentials: ai.OAuthCredentials{Refresh: "live-refresh"},
	})
	if err == nil || !strings.Contains(err.Error(), "status 404:") || !strings.Contains(err.Error(), "missing client") {
		t.Fatalf("expected serialized 404 error detail over real http, got %v", err)
	}
}

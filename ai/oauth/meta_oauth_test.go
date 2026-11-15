package oauth

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/keejkrej/pi-go/ai"
)

type tmoRoundTripFunc func(*http.Request) (*http.Response, error)

func (f tmoRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type tmoInteraction struct {
	mu     sync.Mutex
	events []ai.AuthEvent
}

func (i *tmoInteraction) Notify(event ai.AuthEvent) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.events = append(i.events, event)
}

func (i *tmoInteraction) Prompt(_ context.Context, _ ai.AuthPrompt) (string, error) {
	return "", errors.New("unexpected prompt")
}

func (i *tmoInteraction) snapshotEvents() []ai.AuthEvent {
	i.mu.Lock()
	defer i.mu.Unlock()
	out := make([]ai.AuthEvent, len(i.events))
	copy(out, i.events)
	return out
}

func tmoResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestMetaOAuth_LoginMintsApiKeyWithDeviceFlow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var mu sync.Mutex
		pollCount := 0
		var mintAuthHeader, mintVersionHeader string

		client := &http.Client{
			Transport: tmoRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				switch req.URL.String() {
				case metaDeviceAuthorizationUrl:
					raw, _ := io.ReadAll(req.Body)
					form, _ := url.ParseQuery(string(raw))
					if got := form.Get("client_id"); got != metaClientId {
						t.Fatalf("client_id = %q, want %q", got, metaClientId)
					}
					return tmoResponse(http.StatusOK, `{
						"device_code": "meta-dev",
						"user_code": "META-CODE",
						"verification_uri": "https://auth.meta.com/device",
						"interval": 2,
						"expires_in": 120
					}`), nil
				case metaDeviceTokenUrl:
					raw, _ := io.ReadAll(req.Body)
					form, _ := url.ParseQuery(string(raw))
					if got := form.Get("grant_type"); got != "urn:ietf:params:oauth:grant-type:device_code" {
						t.Fatalf("grant_type = %q", got)
					}
					mu.Lock()
					pollCount += 1
					n := pollCount
					mu.Unlock()
					if n == 1 {
						return tmoResponse(http.StatusBadRequest, `{"error":"authorization_pending"}`), nil
					}
					return tmoResponse(http.StatusOK, `{"access_token":"meta-identity-token"}`), nil
				case metaApiKeyMintUrl:
					mintAuthHeader = req.Header.Get("Authorization")
					mintVersionHeader = req.Header.Get("x-api-version")
					return tmoResponse(http.StatusOK, `{"api_key":"meta-api-key"}`), nil
				default:
					t.Fatalf("unexpected URL: %s", req.URL.String())
					return nil, nil
				}
			}),
		}

		interaction := &tmoInteraction{}
		type resT struct {
			cred *ai.OAuthCredential
			err  error
		}
		done := make(chan resT, 1)
		go func() {
			cred, err := MetaOAuth.Login(tuWithHttpClient(context.Background(), client), interaction, nil)
			done <- resT{cred, err}
		}()

		synctest.Wait()
		events := interaction.snapshotEvents()
		if len(events) < 1 {
			t.Fatal("expected device-code event")
		}
		devEv, ok := events[0].(*ai.AuthEventDeviceCode)
		if !ok || devEv.UserCode != "META-CODE" || devEv.VerificationUri != "https://auth.meta.com/device" {
			t.Fatalf("unexpected device event: %+v", events[0])
		}

		time.Sleep(2000 * time.Millisecond)
		synctest.Wait()

		res := <-done
		if res.err != nil {
			t.Fatalf("Login() failed: %v", res.err)
		}
		if mintAuthHeader != "Bearer meta-identity-token" {
			t.Fatalf("mint Authorization header = %q", mintAuthHeader)
		}
		if mintVersionHeader != "1.0.0" {
			t.Fatalf("mint x-api-version header = %q", mintVersionHeader)
		}
		if res.cred.Access != "meta-api-key" || res.cred.Refresh != "meta-identity-token" {
			t.Fatalf("unexpected credential: %+v", res.cred)
		}
		if res.cred.Expires <= 0 {
			t.Fatalf("expected positive expires, got %v", res.cred.Expires)
		}

		modelAuth, err := MetaOAuth.ToAuth(context.Background(), res.cred)
		if err != nil {
			t.Fatalf("ToAuth() failed: %v", err)
		}
		if modelAuth.ApiKey == nil || *modelAuth.ApiKey != "meta-api-key" {
			t.Fatalf("ToAuth().ApiKey = %v, want meta-api-key", modelAuth.ApiKey)
		}
	})
}

func TestMetaOAuth_MintSetupHintAndSessionExpired(t *testing.T) {
	// Missing api_key with trusted action_url includes setup URL.
	setupClient := &http.Client{
		Transport: tmoRoundTripFunc(func(_ *http.Request) (*http.Response, error) {
			return tmoResponse(http.StatusOK, `{"action_url":"https://meta.ai/setup"}`), nil
		}),
	}
	_, err := MetaOAuth.Refresh(tuWithHttpClient(context.Background(), setupClient), &ai.OAuthCredential{
		OAuthCredentials: ai.OAuthCredentials{Refresh: "id-tok"},
	})
	if err == nil || !strings.Contains(err.Error(), "Complete setup at https://meta.ai/setup") {
		t.Fatalf("expected setup hint error, got %v", err)
	}

	// Missing api_key with untrusted action_url omits URL.
	untrustedClient := &http.Client{
		Transport: tmoRoundTripFunc(func(_ *http.Request) (*http.Response, error) {
			return tmoResponse(http.StatusOK, `{"action_url":"javascript:alert(1)"}`), nil
		}),
	}
	_, err = MetaOAuth.Refresh(tuWithHttpClient(context.Background(), untrustedClient), &ai.OAuthCredential{
		OAuthCredentials: ai.OAuthCredentials{Refresh: "id-tok"},
	})
	if err == nil || err.Error() != "Meta did not issue an API key." {
		t.Fatalf("expected untrusted setup omission error, got %v", err)
	}

	// 401/403 triggers session expired message.
	expiredClient := &http.Client{
		Transport: tmoRoundTripFunc(func(_ *http.Request) (*http.Response, error) {
			return tmoResponse(http.StatusUnauthorized, `{"detail":"token revoked"}`), nil
		}),
	}
	_, err = MetaOAuth.Refresh(tuWithHttpClient(context.Background(), expiredClient), &ai.OAuthCredential{
		OAuthCredentials: ai.OAuthCredentials{Refresh: "expired-id"},
	})
	if err == nil || !strings.Contains(err.Error(), "Meta session expired (status 401). Run `/login meta` to sign in again.: token revoked") {
		t.Fatalf("expected session expired error, got %v", err)
	}
}

func TestMetaOAuth_RefreshReMintsUsingRefreshToken(t *testing.T) {
	var seenAuth string
	client := &http.Client{
		Transport: tmoRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			seenAuth = req.Header.Get("Authorization")
			return tmoResponse(http.StatusOK, `{"api_key":"fresh-api-key"}`), nil
		}),
	}
	cred, err := MetaOAuth.Refresh(tuWithHttpClient(context.Background(), client), &ai.OAuthCredential{
		OAuthCredentials: ai.OAuthCredentials{Refresh: "stored-identity", Access: "old-key"},
	})
	if err != nil {
		t.Fatalf("Refresh() failed: %v", err)
	}
	if seenAuth != "Bearer stored-identity" {
		t.Fatalf("Authorization = %q, want Bearer stored-identity", seenAuth)
	}
	if cred.Access != "fresh-api-key" || cred.Refresh != "stored-identity" {
		t.Fatalf("unexpected refreshed credential: %+v", cred)
	}
}

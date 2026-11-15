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
	"github.com/keejkrej/pi-go/internal/js"
)

type txoRoundTripFunc func(*http.Request) (*http.Response, error)

func (f txoRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type txoInteraction struct {
	mu     sync.Mutex
	events []ai.AuthEvent
}

func (i *txoInteraction) Notify(event ai.AuthEvent) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.events = append(i.events, event)
}

func (i *txoInteraction) Prompt(_ context.Context, _ ai.AuthPrompt) (string, error) {
	return "", errors.New("unexpected prompt")
}

func (i *txoInteraction) snapshotEvents() []ai.AuthEvent {
	i.mu.Lock()
	defer i.mu.Unlock()
	out := make([]ai.AuthEvent, len(i.events))
	copy(out, i.events)
	return out
}

func txoResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestXaiOAuth_MetadataAndDeviceCodeLogin(t *testing.T) {
	if !XaiOAuth.IsSubscription {
		t.Fatal("expected IsSubscription=true")
	}
	if XaiOAuth.LoginLabel == nil || *XaiOAuth.LoginLabel != "Sign in with SuperGrok or X Premium" {
		t.Fatalf("unexpected LoginLabel: %v", XaiOAuth.LoginLabel)
	}

	synctest.Test(t, func(t *testing.T) {
		var mu sync.Mutex
		pollCount := 0
		var seenDeviceForm url.Values

		client := &http.Client{
			Transport: txoRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				raw, _ := io.ReadAll(req.Body)
				form, _ := url.ParseQuery(string(raw))
				switch req.URL.String() {
				case xaiDeviceCodeUrl:
					seenDeviceForm = form
					return txoResponse(http.StatusOK, `{
						"device_code": "xai-dev",
						"user_code": "XAI-999",
						"verification_uri": "https://x.ai/device",
						"verification_uri_complete": "https://x.ai/device?code=XAI-999",
						"interval": 2,
						"expires_in": 300
					}`), nil
				case xaiTokenUrl:
					mu.Lock()
					pollCount += 1
					n := pollCount
					mu.Unlock()
					switch n {
					case 1:
						return txoResponse(http.StatusBadRequest, `{"error":"authorization_pending"}`), nil
					case 2:
						return txoResponse(http.StatusBadRequest, `{"error":"slow_down","interval":3}`), nil
					default:
						// Omit expires_in to verify default 3600s lifetime minus 5m skew.
						return txoResponse(http.StatusOK, `{"access_token":"xai-access","refresh_token":"xai-refresh"}`), nil
					}
				default:
					t.Fatalf("unexpected URL: %s", req.URL.String())
					return nil, nil
				}
			}),
		}

		interaction := &txoInteraction{}
		startMs := float64(js.DateNow())
		type resT struct {
			cred *ai.OAuthCredential
			err  error
		}
		done := make(chan resT, 1)
		go func() {
			cred, err := XaiOAuth.Login(tuWithHttpClient(context.Background(), client), interaction, nil)
			done <- resT{cred, err}
		}()

		synctest.Wait()
		if seenDeviceForm.Get("referrer") != "pi" || seenDeviceForm.Get("client_id") != xaiClientId {
			t.Fatalf("unexpected device form: %v", seenDeviceForm)
		}
		mu.Lock()
		p0 := pollCount
		mu.Unlock()
		if p0 != 0 {
			t.Fatalf("expected 0 polls before initial interval, got %d", p0)
		}

		events := interaction.snapshotEvents()
		if len(events) != 1 {
			t.Fatalf("expected 1 device-code event, got %d", len(events))
		}
		devEv, ok := events[0].(*ai.AuthEventDeviceCode)
		if !ok || devEv.VerificationUri != "https://x.ai/device?code=XAI-999" || devEv.UserCode != "XAI-999" {
			t.Fatalf("unexpected device event: %+v", events[0])
		}

		time.Sleep(2000 * time.Millisecond)
		synctest.Wait()
		time.Sleep(2000 * time.Millisecond)
		synctest.Wait()
		time.Sleep(3000 * time.Millisecond)
		synctest.Wait()

		res := <-done
		if res.err != nil {
			t.Fatalf("Login() failed: %v", res.err)
		}
		if res.cred.Access != "xai-access" || res.cred.Refresh != "xai-refresh" {
			t.Fatalf("unexpected credential: %+v", res.cred)
		}
		expectedApprox := startMs + 7000 + float64(xaiDefaultTokenLifetimeSeconds*1000-xaiRefreshSkewMs)
		if res.cred.Expires < expectedApprox-1000 || res.cred.Expires > expectedApprox+1000 {
			t.Fatalf("Expires = %v, expected near %v", res.cred.Expires, expectedApprox)
		}

		modelAuth, err := XaiOAuth.ToAuth(context.Background(), res.cred)
		if err != nil {
			t.Fatalf("ToAuth() failed: %v", err)
		}
		if modelAuth.ApiKey == nil || *modelAuth.ApiKey != "xai-access" {
			t.Fatalf("ToAuth().ApiKey = %v, want xai-access", modelAuth.ApiKey)
		}
	})
}

func TestXaiOAuth_RejectsNonHttpsVerificationUri(t *testing.T) {
	for _, uri := range []string{"http://auth.x.ai/device", "javascript:alert(1)", "not-a-url"} {
		client := &http.Client{
			Transport: txoRoundTripFunc(func(_ *http.Request) (*http.Response, error) {
				return txoResponse(http.StatusOK, `{"device_code":"d","user_code":"U","verification_uri":"`+uri+`","expires_in":60}`), nil
			}),
		}
		_, err := XaiOAuth.Login(tuWithHttpClient(context.Background(), client), &txoInteraction{}, nil)
		if err == nil || !strings.Contains(err.Error(), "Untrusted verification URI") {
			t.Fatalf("uri %q: expected Untrusted verification URI error, got %v", uri, err)
		}
	}
}

func TestXaiOAuth_RefreshPreservesRefreshTokenAndHandlesDenial(t *testing.T) {
	client := &http.Client{
		Transport: txoRoundTripFunc(func(_ *http.Request) (*http.Response, error) {
			return txoResponse(http.StatusOK, `{"access_token":"xai-new-access","expires_in":120}`), nil
		}),
	}
	cred, err := XaiOAuth.Refresh(tuWithHttpClient(context.Background(), client), &ai.OAuthCredential{
		OAuthCredentials: ai.OAuthCredentials{Refresh: "keep-my-refresh", Access: "old"},
	})
	if err != nil {
		t.Fatalf("Refresh() failed: %v", err)
	}
	if cred.Refresh != "keep-my-refresh" || cred.Access != "xai-new-access" {
		t.Fatalf("expected unrotated refresh token to be preserved, got %+v", cred)
	}

	synctest.Test(t, func(t *testing.T) {
		deniedClient := &http.Client{
			Transport: txoRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.String() == xaiDeviceCodeUrl {
					return txoResponse(http.StatusOK, `{"device_code":"d","user_code":"U","verification_uri":"https://x.ai/dev","interval":1,"expires_in":60}`), nil
				}
				return txoResponse(http.StatusBadRequest, `{"error":"access_denied"}`), nil
			}),
		}
		type resT struct {
			err error
		}
		done := make(chan resT, 1)
		go func() {
			_, err := XaiOAuth.Login(tuWithHttpClient(context.Background(), deniedClient), &txoInteraction{}, nil)
			done <- resT{err}
		}()

		synctest.Wait()
		time.Sleep(1000 * time.Millisecond)
		synctest.Wait()

		res := <-done
		if res.err == nil || !strings.Contains(res.err.Error(), "xAI device authorization was denied") {
			t.Fatalf("expected denial error, got %v", res.err)
		}
	})
}

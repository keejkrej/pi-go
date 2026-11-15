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

type troRoundTripFunc func(*http.Request) (*http.Response, error)

func (f troRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type troInteraction struct {
	mu      sync.Mutex
	events  []ai.AuthEvent
	handler func(context.Context, ai.AuthPrompt) (string, error)
}

func (i *troInteraction) Notify(event ai.AuthEvent) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.events = append(i.events, event)
}

func (i *troInteraction) Prompt(ctx context.Context, prompt ai.AuthPrompt) (string, error) {
	if i.handler != nil {
		return i.handler(ctx, prompt)
	}
	return "", errors.New("unexpected prompt")
}

func (i *troInteraction) snapshotEvents() []ai.AuthEvent {
	i.mu.Lock()
	defer i.mu.Unlock()
	out := make([]ai.AuthEvent, len(i.events))
	copy(out, i.events)
	return out
}

func troResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestRadiusOAuth_BrowserLoginDiscoveryCallbackAndScope(t *testing.T) {
	flow := CreateRadiusOAuth(RadiusOAuthOptions{
		Name:    "Radius Gateway",
		Gateway: "https://radius.example/",
	})
	if flow.Name != "Radius Gateway" {
		t.Fatalf("flow.Name = %q, want Radius Gateway", flow.Name)
	}

	var tokenForm url.Values
	client := &http.Client{
		Transport: troRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch req.URL.String() {
			case "https://radius.example/v1/oauth":
				return troResponse(http.StatusOK, `{"authorizationEndpoint":"https://radius.example/oauth/authorize"}`), nil
			case "https://radius.example/v1/oauth/token":
				raw, _ := io.ReadAll(req.Body)
				tokenForm, _ = url.ParseQuery(string(raw))
				return troResponse(http.StatusOK, `{
					"access_token": "rad-access",
					"refresh_token": "rad-refresh",
					"expires_in": 3600,
					"scope": "gateway offline_access"
				}`), nil
			default:
				t.Fatalf("unexpected request URL: %s", req.URL.String())
				return nil, nil
			}
		}),
	}

	interaction := &troInteraction{
		handler: func(_ context.Context, prompt ai.AuthPrompt) (string, error) {
			sel, ok := prompt.(*ai.AuthPromptSelect)
			if !ok {
				return "", errors.New("unexpected prompt")
			}
			if sel.Message != "Sign in to Radius Gateway:" {
				t.Fatalf("select message = %q", sel.Message)
			}
			return "browser", nil
		},
	}

	ctx, cancel := context.WithTimeout(tuWithHttpClient(context.Background(), client), 5*time.Second)
	defer cancel()

	type resT struct {
		cred *ai.OAuthCredential
		err  error
	}
	done := make(chan resT, 1)
	go func() {
		cred, err := flow.Login(ctx, interaction, nil)
		done <- resT{cred, err}
	}()

	var authUrl string
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, ev := range interaction.snapshotEvents() {
			if u, ok := ev.(*ai.AuthEventAuthUrl); ok {
				authUrl = u.Url
			}
		}
		if authUrl != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if authUrl == "" {
		t.Fatal("timed out waiting for AuthEventAuthUrl")
	}

	parsedAuthUrl, err := url.Parse(authUrl)
	if err != nil {
		t.Fatal(err)
	}
	q := parsedAuthUrl.Query()
	if q.Get("redirect_uri") != radiusRedirectUri || q.Get("client_id") != radiusOauthClientId || q.Get("handoff") != "url" || q.Get("code_challenge_method") != "S256" {
		t.Fatalf("unexpected authorize query params: %v", q)
	}

	resp, err := http.Get(radiusRedirectUri + "?code=rad-code&state=" + url.QueryEscape(q.Get("state")))
	if err != nil {
		t.Fatalf("GET callback failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("callback status = %d, want 200", resp.StatusCode)
	}

	res := <-done
	if res.err != nil {
		t.Fatalf("Login() failed: %v", res.err)
	}
	if tokenForm.Get("grant_type") != "authorization_code" || tokenForm.Get("code") != "rad-code" {
		t.Fatalf("unexpected token form: %v", tokenForm)
	}
	if res.cred.Access != "rad-access" || res.cred.Refresh != "rad-refresh" {
		t.Fatalf("unexpected credential: %+v", res.cred)
	}
	if res.cred.Extra == nil {
		t.Fatal("expected Extra map with scope")
	}
	if scopeVal, ok := res.cred.Extra.GetString("scope"); !ok || scopeVal != "gateway offline_access" {
		t.Fatalf("Extra.scope = %q (ok=%v), want gateway offline_access", scopeVal, ok)
	}

	modelAuth, err := flow.ToAuth(context.Background(), res.cred)
	if err != nil {
		t.Fatalf("ToAuth() failed: %v", err)
	}
	if modelAuth.ApiKey == nil || *modelAuth.ApiKey != "rad-access" {
		t.Fatalf("ToAuth().ApiKey = %v, want rad-access", modelAuth.ApiKey)
	}
}

func TestRadiusOAuth_DeviceCodeLoginAndRefresh(t *testing.T) {
	flow := CreateRadiusOAuth(RadiusOAuthOptions{
		Name:    "Radius",
		Gateway: "https://radius.example",
	})

	synctest.Test(t, func(t *testing.T) {
		var mu sync.Mutex
		pollCount := 0
		var refreshGrant string

		client := &http.Client{
			Transport: troRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				raw, _ := io.ReadAll(req.Body)
				form, _ := url.ParseQuery(string(raw))
				switch req.URL.String() {
				case "https://radius.example/v1/oauth/device":
					return troResponse(http.StatusOK, `{
						"device_code": "rad-dev",
						"user_code": "RAD-123",
						"verification_uri": "https://radius.example/device",
						"interval": 1,
						"expires_in": 60
					}`), nil
				case "https://radius.example/v1/oauth/token":
					if form.Get("grant_type") == "refresh_token" {
						refreshGrant = form.Get("refresh_token")
						return troResponse(http.StatusOK, `{"access_token":"rad-refreshed","refresh_token":"rad-rot","expires_in":3600}`), nil
					}
					mu.Lock()
					pollCount += 1
					n := pollCount
					mu.Unlock()
					switch n {
					case 1:
						return troResponse(http.StatusBadRequest, `{"error":"authorization_pending"}`), nil
					case 2:
						return troResponse(http.StatusBadRequest, `{"error":"slow_down"}`), nil
					default:
						return troResponse(http.StatusOK, `{"access_token":"rad-dev-access","refresh_token":"rad-dev-refresh","expires_in":3600}`), nil
					}
				default:
					t.Fatalf("unexpected URL: %s", req.URL.String())
					return nil, nil
				}
			}),
		}

		interaction := &troInteraction{
			handler: func(_ context.Context, _ ai.AuthPrompt) (string, error) {
				return "device-code", nil
			},
		}

		type resT struct {
			cred *ai.OAuthCredential
			err  error
		}
		done := make(chan resT, 1)
		go func() {
			cred, err := flow.Login(tuWithHttpClient(context.Background(), client), interaction, nil)
			done <- resT{cred, err}
		}()

		synctest.Wait()
		events := interaction.snapshotEvents()
		if len(events) < 1 {
			t.Fatal("expected device-code event")
		}
		if devEv, ok := events[0].(*ai.AuthEventDeviceCode); !ok || devEv.UserCode != "RAD-123" {
			t.Fatalf("unexpected device event: %+v", events[0])
		}

		time.Sleep(1000 * time.Millisecond)
		synctest.Wait()
		// After slow_down, interval bumps by 5s -> 6s.
		time.Sleep(6000 * time.Millisecond)
		synctest.Wait()

		res := <-done
		if res.err != nil {
			t.Fatalf("device-code Login() failed: %v", res.err)
		}
		if res.cred.Access != "rad-dev-access" || res.cred.Refresh != "rad-dev-refresh" {
			t.Fatalf("unexpected device cred: %+v", res.cred)
		}

		refreshed, err := flow.Refresh(tuWithHttpClient(context.Background(), client), res.cred)
		if err != nil {
			t.Fatalf("Refresh() failed: %v", err)
		}
		if refreshGrant != "rad-dev-refresh" || refreshed.Access != "rad-refreshed" {
			t.Fatalf("unexpected refresh result: grant=%q cred=%+v", refreshGrant, refreshed)
		}
	})
}

func TestRadiusOAuth_RejectsUntrustedEndpointsAndDeviceErrors(t *testing.T) {
	flow := CreateRadiusOAuth(RadiusOAuthOptions{
		Name:    "Radius",
		Gateway: "https://radius.example",
	})

	untrustedDiscoveryClient := &http.Client{
		Transport: troRoundTripFunc(func(_ *http.Request) (*http.Response, error) {
			return troResponse(http.StatusOK, `{"authorizationEndpoint":"javascript:alert(1)"}`), nil
		}),
	}
	interactionBrowser := &troInteraction{
		handler: func(_ context.Context, _ ai.AuthPrompt) (string, error) { return "browser", nil },
	}
	_, err := flow.Login(tuWithHttpClient(context.Background(), untrustedDiscoveryClient), interactionBrowser, nil)
	if err == nil || !strings.Contains(err.Error(), "Invalid Radius OAuth config") {
		t.Fatalf("expected invalid discovery error, got %v", err)
	}

	untrustedDeviceClient := &http.Client{
		Transport: troRoundTripFunc(func(_ *http.Request) (*http.Response, error) {
			return troResponse(http.StatusOK, `{
				"device_code": "d",
				"user_code": "U",
				"verification_uri": "file:///etc/passwd",
				"expires_in": 60
			}`), nil
		}),
	}
	interactionDevice := &troInteraction{
		handler: func(_ context.Context, _ ai.AuthPrompt) (string, error) { return "device-code", nil },
	}
	_, err = flow.Login(tuWithHttpClient(context.Background(), untrustedDeviceClient), interactionDevice, nil)
	if err == nil || !strings.Contains(err.Error(), "Untrusted verification_uri") {
		t.Fatalf("expected untrusted verification_uri error, got %v", err)
	}

	// expired_token stops polling with clear message.
	deniedClient := &http.Client{
		Transport: troRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			if strings.HasSuffix(req.URL.String(), "/v1/oauth/device") {
				return troResponse(http.StatusOK, `{"device_code":"d","user_code":"U","verification_uri":"https://radius.example/dev","expires_in":60}`), nil
			}
			return troResponse(http.StatusBadRequest, `{"error":"access_denied"}`), nil
		}),
	}
	_, err = flow.Login(tuWithHttpClient(context.Background(), deniedClient), interactionDevice, nil)
	if err == nil || !strings.Contains(err.Error(), "Device authorization was denied.") {
		t.Fatalf("expected access_denied error, got %v", err)
	}
}

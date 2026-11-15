package oauth

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

type tocoaRoundTripFunc func(*http.Request) (*http.Response, error)

func (f tocoaRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type tocoaInteraction struct {
	mu      sync.Mutex
	events  []ai.AuthEvent
	handler func(context.Context, ai.AuthPrompt) (string, error)
}

func (i *tocoaInteraction) Notify(event ai.AuthEvent) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.events = append(i.events, event)
}

func (i *tocoaInteraction) Prompt(ctx context.Context, prompt ai.AuthPrompt) (string, error) {
	if i.handler != nil {
		return i.handler(ctx, prompt)
	}
	return "", errors.New("unexpected prompt")
}

func (i *tocoaInteraction) snapshotEvents() []ai.AuthEvent {
	i.mu.Lock()
	defer i.mu.Unlock()
	out := make([]ai.AuthEvent, len(i.events))
	copy(out, i.events)
	return out
}

func tocoaResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func tocoaFakeJwt(accountId string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	claimsObj := jsonx.NewObject()
	if accountId != "" {
		authObj := jsonx.NewObject()
		authObj.Set("chatgpt_account_id", accountId)
		claimsObj.Set(ocoJwtClaimPath, authObj)
	}
	payloadBytes, _ := jsonx.Marshal(claimsObj)
	payload := base64.RawURLEncoding.EncodeToString(payloadBytes)
	return header + "." + payload + ".sig"
}

func TestOpenAICodexOAuth_BrowserFallbackToManualWhenPort1455Busy(t *testing.T) {
	blocker, err := net.Listen("tcp", "127.0.0.1:1455")
	if err != nil {
		t.Fatalf("failed to bind blocker on 127.0.0.1:1455: %v", err)
	}
	defer blocker.Close()

	jwt := tocoaFakeJwt("acct_codex_manual")
	var tokenForm url.Values
	client := &http.Client{
		Transport: tocoaRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.String() != ocoTokenUrl {
				t.Fatalf("unexpected URL: %s", req.URL.String())
			}
			raw, _ := io.ReadAll(req.Body)
			tokenForm, _ = url.ParseQuery(string(raw))
			return tocoaResponse(http.StatusOK, `{"access_token":"`+jwt+`","refresh_token":"codex-refresh","expires_in":3600}`), nil
		}),
	}

	var authUrl string
	interaction := &tocoaInteraction{}
	interaction.handler = func(_ context.Context, prompt ai.AuthPrompt) (string, error) {
		switch prompt.(type) {
		case *ai.AuthPromptSelect:
			return "browser", nil
		case *ai.AuthPromptManualCode:
			for _, ev := range interaction.snapshotEvents() {
				if u, ok := ev.(*ai.AuthEventAuthUrl); ok {
					authUrl = u.Url
				}
			}
			parsedUrl, err := url.Parse(authUrl)
			if err != nil {
				t.Fatal(err)
			}
			state := parsedUrl.Query().Get("state")
			return ocoRedirectUri + "?code=codex-manual-code&state=" + url.QueryEscape(state), nil
		default:
			return "", errors.New("unexpected prompt")
		}
	}

	customAgent := "custom-agent"
	cred, err := OpenaiCodexOAuth.Login(
		tuWithHttpClient(context.Background(), client),
		interaction,
		&ai.LoginOptions{AgentName: &customAgent},
	)
	if err != nil {
		t.Fatalf("Login() failed: %v", err)
	}

	parsedAuthUrl, err := url.Parse(authUrl)
	if err != nil {
		t.Fatal(err)
	}
	q := parsedAuthUrl.Query()
	if q.Get("client_id") != ocoClientId ||
		q.Get("redirect_uri") != ocoRedirectUri ||
		q.Get("code_challenge_method") != "S256" ||
		q.Get("id_token_add_organizations") != "true" ||
		q.Get("codex_cli_simplified_flow") != "true" ||
		q.Get("originator") != "custom-agent" {
		t.Fatalf("unexpected authorize params: %v", q)
	}
	if tokenForm.Get("grant_type") != "authorization_code" || tokenForm.Get("code") != "codex-manual-code" {
		t.Fatalf("unexpected token form: %v", tokenForm)
	}
	if cred.Extra == nil {
		t.Fatal("expected Extra map with accountId")
	}
	if acct, ok := cred.Extra.GetString("accountId"); !ok || acct != "acct_codex_manual" {
		t.Fatalf("Extra.accountId = %q (ok=%v), want acct_codex_manual", acct, ok)
	}

	modelAuth, err := OpenaiCodexOAuth.ToAuth(context.Background(), cred)
	if err != nil {
		t.Fatalf("ToAuth() failed: %v", err)
	}
	if modelAuth.ApiKey == nil || *modelAuth.ApiKey != jwt {
		t.Fatalf("unexpected ToAuth().ApiKey")
	}
}

func TestOpenAICodexOAuth_BrowserCallbackCompletesOnPort1455(t *testing.T) {
	jwt := tocoaFakeJwt("acct_codex_cb")
	client := &http.Client{
		Transport: tocoaRoundTripFunc(func(_ *http.Request) (*http.Response, error) {
			return tocoaResponse(http.StatusOK, `{"access_token":"`+jwt+`","refresh_token":"codex-cb-refresh","expires_in":3600}`), nil
		}),
	}

	manualStarted := make(chan context.Context, 1)
	interaction := &tocoaInteraction{
		handler: func(ctx context.Context, prompt ai.AuthPrompt) (string, error) {
			switch p := prompt.(type) {
			case *ai.AuthPromptSelect:
				return "browser", nil
			case *ai.AuthPromptManualCode:
				promptCtx := p.PromptContext
				if promptCtx == nil {
					promptCtx = ctx
				}
				manualStarted <- promptCtx
				<-promptCtx.Done()
				return "", promptCtx.Err()
			default:
				return "", errors.New("unexpected prompt")
			}
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
		cred, err := OpenaiCodexOAuth.Login(ctx, interaction, nil)
		done <- resT{cred, err}
	}()

	var manualCtx context.Context
	select {
	case manualCtx = <-manualStarted:
	case res := <-done:
		t.Fatalf("login finished prematurely: %+v %v", res.cred, res.err)
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for manual prompt")
	}

	var authUrl string
	for _, ev := range interaction.snapshotEvents() {
		if u, ok := ev.(*ai.AuthEventAuthUrl); ok {
			authUrl = u.Url
		}
	}
	parsedAuthUrl, err := url.Parse(authUrl)
	if err != nil {
		t.Fatal(err)
	}
	if parsedAuthUrl.Query().Get("originator") != "pi" {
		t.Fatalf("default originator = %q, want pi", parsedAuthUrl.Query().Get("originator"))
	}
	state := parsedAuthUrl.Query().Get("state")
	resp, err := http.Get("http://127.0.0.1:1455/auth/callback?code=cb-code&state=" + url.QueryEscape(state))
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
	if acct, _ := res.cred.Extra.GetString("accountId"); acct != "acct_codex_cb" {
		t.Fatalf("Extra.accountId = %q, want acct_codex_cb", acct)
	}
	if manualCtx.Err() == nil {
		t.Fatal("expected manual prompt context cancellation after callback")
	}
}

func TestOpenAICodexOAuth_DeviceCodeLoginAndPolling(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		jwt := tocoaFakeJwt("acct_codex_device")
		var mu sync.Mutex
		pollCount := 0
		var tokenForm url.Values

		client := &http.Client{
			Transport: tocoaRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				switch req.URL.String() {
				case ocoDeviceUserCodeUrl:
					// Exercise string interval parsing as supported by OpenAI endpoint.
					return tocoaResponse(http.StatusOK, `{"device_auth_id":"dev-auth-1","user_code":"CDX-789","interval":"2"}`), nil
				case ocoDeviceTokenUrl:
					mu.Lock()
					pollCount += 1
					n := pollCount
					mu.Unlock()
					switch n {
					case 1:
						return tocoaResponse(http.StatusForbidden, `{}`), nil
					case 2:
						return tocoaResponse(http.StatusNotFound, `{}`), nil
					default:
						return tocoaResponse(http.StatusOK, `{"authorization_code":"dev-auth-code","code_verifier":"dev-verifier"}`), nil
					}
				case ocoTokenUrl:
					raw, _ := io.ReadAll(req.Body)
					tokenForm, _ = url.ParseQuery(string(raw))
					return tocoaResponse(http.StatusOK, `{"access_token":"`+jwt+`","refresh_token":"dev-refresh","expires_in":3600}`), nil
				default:
					t.Fatalf("unexpected URL: %s", req.URL.String())
					return nil, nil
				}
			}),
		}

		interaction := &tocoaInteraction{
			handler: func(_ context.Context, _ ai.AuthPrompt) (string, error) {
				return "device_code", nil
			},
		}

		type resT struct {
			cred *ai.OAuthCredential
			err  error
		}
		done := make(chan resT, 1)
		go func() {
			cred, err := OpenaiCodexOAuth.Login(tuWithHttpClient(context.Background(), client), interaction, nil)
			done <- resT{cred, err}
		}()

		synctest.Wait()
		events := interaction.snapshotEvents()
		if len(events) < 1 {
			t.Fatal("expected device-code event")
		}
		devEv, ok := events[0].(*ai.AuthEventDeviceCode)
		if !ok || devEv.VerificationUri != ocoDeviceVerificationUri || devEv.UserCode != "CDX-789" {
			t.Fatalf("unexpected device event: %+v", events[0])
		}

		// OpenAI device poll does not wait before first poll; 2 pending polls take 2s + 2s.
		time.Sleep(2000 * time.Millisecond)
		synctest.Wait()
		time.Sleep(2000 * time.Millisecond)
		synctest.Wait()

		res := <-done
		if res.err != nil {
			t.Fatalf("device Login() failed: %v", res.err)
		}
		if tokenForm.Get("redirect_uri") != ocoDeviceRedirectUri || tokenForm.Get("code_verifier") != "dev-verifier" {
			t.Fatalf("unexpected token form: %v", tokenForm)
		}
		if acct, _ := res.cred.Extra.GetString("accountId"); acct != "acct_codex_device" {
			t.Fatalf("accountId = %q, want acct_codex_device", acct)
		}
	})

	// 404 on /usercode returns friendly disabled message.
	disabledClient := &http.Client{
		Transport: tocoaRoundTripFunc(func(_ *http.Request) (*http.Response, error) {
			return tocoaResponse(http.StatusNotFound, `not found`), nil
		}),
	}
	_, err := OpenaiCodexOAuth.Login(tuWithHttpClient(context.Background(), disabledClient), &tocoaInteraction{
		handler: func(_ context.Context, _ ai.AuthPrompt) (string, error) { return "device_code", nil },
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "device code login is not enabled") {
		t.Fatalf("expected disabled device code error, got %v", err)
	}
}

func TestOpenAICodexOAuth_RefreshAndErrors(t *testing.T) {
	jwtNew := tocoaFakeJwt("acct_refreshed")
	client := &http.Client{
		Transport: tocoaRoundTripFunc(func(_ *http.Request) (*http.Response, error) {
			return tocoaResponse(http.StatusOK, `{"access_token":"`+jwtNew+`","refresh_token":"rot-refresh","expires_in":3600}`), nil
		}),
	}
	cred, err := OpenaiCodexOAuth.Refresh(tuWithHttpClient(context.Background(), client), &ai.OAuthCredential{
		OAuthCredentials: ai.OAuthCredentials{Refresh: "old-refresh"},
	})
	if err != nil {
		t.Fatalf("Refresh() failed: %v", err)
	}
	if cred.Refresh != "rot-refresh" {
		t.Fatalf("Refresh = %q, want rot-refresh", cred.Refresh)
	}
	if acct, _ := cred.Extra.GetString("accountId"); acct != "acct_refreshed" {
		t.Fatalf("Extra.accountId = %q, want acct_refreshed", acct)
	}

	// Missing accountId claim in JWT fails cleanly.
	jwtMissingClaim := tocoaFakeJwt("")
	badClaimClient := &http.Client{
		Transport: tocoaRoundTripFunc(func(_ *http.Request) (*http.Response, error) {
			return tocoaResponse(http.StatusOK, `{"access_token":"`+jwtMissingClaim+`","refresh_token":"r","expires_in":3600}`), nil
		}),
	}
	_, err = OpenaiCodexOAuth.Refresh(tuWithHttpClient(context.Background(), badClaimClient), &ai.OAuthCredential{
		OAuthCredentials: ai.OAuthCredentials{Refresh: "old"},
	})
	if err == nil || !strings.Contains(err.Error(), "Failed to extract accountId from token") {
		t.Fatalf("expected missing accountId error, got %v", err)
	}

	// Unknown method errors.
	_, err = OpenaiCodexOAuth.Login(context.Background(), &tocoaInteraction{
		handler: func(_ context.Context, _ ai.AuthPrompt) (string, error) { return "bogus", nil },
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "Unknown OpenAI Codex login method") {
		t.Fatalf("expected unknown login method error, got %v", err)
	}
}

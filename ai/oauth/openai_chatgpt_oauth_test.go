package oauth

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

type tocoRoundTripFunc func(*http.Request) (*http.Response, error)

func (f tocoRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type tocoInteraction struct {
	mu      sync.Mutex
	events  []ai.AuthEvent
	handler func(context.Context, ai.AuthPrompt) (string, error)
}

func (i *tocoInteraction) Notify(event ai.AuthEvent) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.events = append(i.events, event)
}

func (i *tocoInteraction) Prompt(ctx context.Context, prompt ai.AuthPrompt) (string, error) {
	if i.handler != nil {
		return i.handler(ctx, prompt)
	}
	return "", errors.New("unexpected prompt")
}

func (i *tocoInteraction) snapshotEvents() []ai.AuthEvent {
	i.mu.Lock()
	defer i.mu.Unlock()
	out := make([]ai.AuthEvent, len(i.events))
	copy(out, i.events)
	return out
}

func tocoResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestOpenAIChatGPTOAuth_DynamicClientBrowserLoginAndRefresh(t *testing.T) {
	if !OpenaiChatGPTOAuth.IsSubscription {
		t.Fatal("expected IsSubscription=true")
	}
	if OpenaiChatGPTOAuth.LoginLabel == nil || *OpenaiChatGPTOAuth.LoginLabel != "Sign in with ChatGPT" {
		t.Fatalf("unexpected LoginLabel: %v", OpenaiChatGPTOAuth.LoginLabel)
	}

	var forms []url.Values
	client := &http.Client{
		Transport: tocoRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.String() != ocTokenUrl {
				t.Fatalf("unexpected URL: %s", req.URL.String())
			}
			raw, _ := io.ReadAll(req.Body)
			form, _ := url.ParseQuery(string(raw))
			forms = append(forms, form)
			if form.Get("grant_type") == "refresh_token" {
				return tocoResponse(http.StatusOK, `{
					"access_token": "oc-refreshed",
					"refresh_token": "oc-rot-refresh",
					"expires_in": 3600,
					"scope": "openid resource.invoke chatgpt.tokens.use.direct"
				}`), nil
			}
			return tocoResponse(http.StatusOK, `{
				"access_token": "oc-access",
				"refresh_token": "oc-refresh",
				"id_token": "eyJhbGciOiJub25lIn0.e30.",
				"expires_in": 3600,
				"scope": "openid profile resource.invoke chatgpt.tokens.use.direct"
			}`), nil
		}),
	}

	runOnce := func(agentName *string, issuedClient string, wantHostId string) *ai.OAuthCredential {
		forms = nil
		manualStarted := make(chan context.Context, 1)
		interaction := &tocoInteraction{
			handler: func(ctx context.Context, prompt ai.AuthPrompt) (string, error) {
				p := prompt.(*ai.AuthPromptManualCode)
				pCtx := p.PromptContext
				if pCtx == nil {
					pCtx = ctx
				}
				manualStarted <- pCtx
				<-pCtx.Done()
				return "", pCtx.Err()
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
			cred, err := OpenaiChatGPTOAuth.Login(ctx, interaction, &ai.LoginOptions{
				GetDeviceId: func() string { return "A1B2C3D4-E5F6-7890-ABCD-EF1234567890" },
				AgentName:   agentName,
			})
			done <- resT{cred, err}
		}()

		var manualCtx context.Context
		select {
		case manualCtx = <-manualStarted:
		case res := <-done:
			t.Fatalf("login exited prematurely: %+v %v", res.cred, res.err)
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for manual prompt")
		}

		var authUrl string
		for _, ev := range interaction.snapshotEvents() {
			if u, ok := ev.(*ai.AuthEventAuthUrl); ok {
				authUrl = u.Url
			}
		}
		parsedUrl, err := url.Parse(authUrl)
		if err != nil {
			t.Fatal(err)
		}
		q := parsedUrl.Query()
		if q.Get("client_id") != ocDynamicClientId ||
			q.Get("ext_agent_host_id") != wantHostId ||
			q.Get("redirect_uri") != ocRedirectUri ||
			q.Get("resource") != ocResource ||
			q.Get("code_challenge_method") != "S256" ||
			q.Get("nonce") == "" {
			t.Fatalf("unexpected authorize query params: %v", q)
		}
		wantHint := ocAgentNameHint
		if agentName != nil {
			wantHint = *agentName
		}
		if q.Get("agent_name_hint") != wantHint {
			t.Fatalf("agent_name_hint = %q, want %q", q.Get("agent_name_hint"), wantHint)
		}

		cbUrl := ocRedirectUri + "?code=oc-code&state=" + url.QueryEscape(q.Get("state")) + "&client_id=" + url.QueryEscape(issuedClient)
		resp, err := http.Get(cbUrl)
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
		if manualCtx.Err() == nil {
			t.Fatal("expected manual prompt cancellation on callback")
		}
		return res.cred
	}

	customName := "Pi Custom"
	before := float64(js.DateNow())
	cred1 := runOnce(&customName, "issued-client-1", "urn:uuid:a1b2c3d4-e5f6-7890-abcd-ef1234567890")
	// Immediately run a second login to verify port 1455 server closed cleanly.
	cred2 := runOnce(nil, "issued-client-2", "urn:uuid:a1b2c3d4-e5f6-7890-abcd-ef1234567890")

	if cred1.Extra == nil {
		t.Fatal("expected Extra map")
	}
	if cid, _ := cred1.Extra.GetString("clientId"); cid != "issued-client-1" {
		t.Fatalf("cred1 clientId = %q, want issued-client-1", cid)
	}
	if cid2, _ := cred2.Extra.GetString("clientId"); cid2 != "issued-client-2" {
		t.Fatalf("cred2 clientId = %q, want issued-client-2", cid2)
	}
	scopesVal, ok := cred1.Extra.GetArray("scopes")
	if !ok || len(scopesVal) != 4 {
		t.Fatalf("unexpected Extra.scopes: %v (ok=%v)", scopesVal, ok)
	}
	expectedMinExpires := before + 3600*1000 - float64(ocExpiryMarginMs) - 5000
	if cred1.Expires < expectedMinExpires {
		t.Fatalf("Expires = %v, expected >= %v (with 180s expiry margin)", cred1.Expires, expectedMinExpires)
	}

	// Refresh sends stored issued clientId and resource.
	forms = nil
	refreshed, err := OpenaiChatGPTOAuth.Refresh(tuWithHttpClient(context.Background(), client), cred1)
	if err != nil {
		t.Fatalf("Refresh() failed: %v", err)
	}
	if len(forms) != 1 || forms[0].Get("client_id") != "issued-client-1" || forms[0].Get("resource") != ocResource {
		t.Fatalf("unexpected refresh form: %v", forms)
	}
	if refreshed.Access != "oc-refreshed" || refreshed.Refresh != "oc-rot-refresh" {
		t.Fatalf("unexpected refreshed cred: %+v", refreshed)
	}

	modelAuth, err := OpenaiChatGPTOAuth.ToAuth(context.Background(), refreshed)
	if err != nil {
		t.Fatalf("ToAuth() failed: %v", err)
	}
	if modelAuth.ApiKey == nil || *modelAuth.ApiKey != "oc-refreshed" {
		t.Fatalf("ToAuth().ApiKey = %v, want oc-refreshed", modelAuth.ApiKey)
	}
}

func TestOpenAIChatGPTOAuth_ManualFallbackUrlValidationAndErrors(t *testing.T) {
	client := &http.Client{
		Transport: tocoRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			return tocoResponse(http.StatusOK, `{
				"access_token": "oc-manual-access",
				"refresh_token": "oc-manual-refresh",
				"id_token": "id-tok",
				"expires_in": 3600,
				"scope": "openid chatgpt.tokens.use.direct"
			}`), nil
		}),
	}

	// Manual redirect URL paste succeeds.
	var authUrl string
	interaction := &tocoInteraction{}
	interaction.handler = func(_ context.Context, _ ai.AuthPrompt) (string, error) {
		for _, ev := range interaction.snapshotEvents() {
			if u, ok := ev.(*ai.AuthEventAuthUrl); ok {
				authUrl = u.Url
			}
		}
		parsedUrl, _ := url.Parse(authUrl)
		state := parsedUrl.Query().Get("state")
		return ocRedirectUri + "?code=man-code&state=" + url.QueryEscape(state) + "&client_id=man-client", nil
	}
	cred, err := OpenaiChatGPTOAuth.Login(tuWithHttpClient(context.Background(), client), interaction, &ai.LoginOptions{
		GetDeviceId: func() string { return "00000000-0000-0000-0000-000000000001" },
	})
	if err != nil {
		t.Fatalf("manual Login() failed: %v", err)
	}
	if cid, _ := cred.Extra.GetString("clientId"); cid != "man-client" {
		t.Fatalf("manual clientId = %q, want man-client", cid)
	}

	// Validate manual input helper checks.
	if _, err := ocAuthorizationResultFromManualInput("not-a-url", "st"); err == nil || !strings.Contains(err.Error(), "Paste the full callback URL") {
		t.Fatalf("expected full callback URL error, got %v", err)
	}
	if _, err := ocAuthorizationResultFromManualInput("https://evil.example/auth/callback?code=c&state=st&client_id=cid", "st"); err == nil || !strings.Contains(err.Error(), "must start with "+ocRedirectUri) {
		t.Fatalf("expected prefix check error, got %v", err)
	}
	if _, err := ocAuthorizationResultFromManualInput(ocRedirectUri+"?code=c&state=st", "st"); err == nil || !strings.Contains(err.Error(), "did not contain an issued client ID") {
		t.Fatalf("expected missing client_id error, got %v", err)
	}

	// Missing device UUID error.
	_, err = OpenaiChatGPTOAuth.Login(context.Background(), &tocoInteraction{}, &ai.LoginOptions{
		GetDeviceId: func() string { return "not-a-uuid" },
	})
	if err == nil || !strings.Contains(err.Error(), "requires a device ID (UUID)") {
		t.Fatalf("expected device ID UUID error, got %v", err)
	}

	// Missing id_token fails authorization exchange.
	noIdTokenClient := &http.Client{
		Transport: tocoRoundTripFunc(func(_ *http.Request) (*http.Response, error) {
			return tocoResponse(http.StatusOK, `{"access_token":"a","refresh_token":"r","expires_in":3600,"scope":"chatgpt.tokens.use.direct"}`), nil
		}),
	}
	_, err = OpenaiChatGPTOAuth.Login(tuWithHttpClient(context.Background(), noIdTokenClient), interaction, &ai.LoginOptions{
		GetDeviceId: func() string { return "00000000-0000-0000-0000-000000000001" },
	})
	if err == nil || !strings.Contains(err.Error(), "did not contain an ID token") {
		t.Fatalf("expected missing id_token error, got %v", err)
	}

	// Missing chatgpt.tokens.use.direct scope fails token validation.
	missingScopeClient := &http.Client{
		Transport: tocoRoundTripFunc(func(_ *http.Request) (*http.Response, error) {
			return tocoResponse(http.StatusOK, `{"access_token":"a","refresh_token":"r","id_token":"id","expires_in":3600,"scope":"openid profile"}`), nil
		}),
	}
	_, err = OpenaiChatGPTOAuth.Login(tuWithHttpClient(context.Background(), missingScopeClient), interaction, &ai.LoginOptions{
		GetDeviceId: func() string { return "00000000-0000-0000-0000-000000000001" },
	})
	if err == nil || !strings.Contains(err.Error(), ocDirectTokenScope) {
		t.Fatalf("expected missing direct token scope error, got %v", err)
	}

	// Missing clientId on Refresh errors cleanly.
	_, err = OpenaiChatGPTOAuth.Refresh(context.Background(), &ai.OAuthCredential{
		OAuthCredentials: ai.OAuthCredentials{Refresh: "r", Extra: jsonx.NewObject()},
	})
	if err == nil || !strings.Contains(err.Error(), "does not contain an issued client ID") {
		t.Fatalf("expected missing clientId refresh error, got %v", err)
	}

	// Port 1455 occupied produces explicit EADDRINUSE message.
	blocker, listenErr := net.Listen("tcp", "127.0.0.1:1455")
	if listenErr != nil {
		t.Fatalf("failed to bind blocker on 127.0.0.1:1455: %v", listenErr)
	}
	defer blocker.Close()

	_, err = OpenaiChatGPTOAuth.Login(context.Background(), &tocoInteraction{}, &ai.LoginOptions{
		GetDeviceId: func() string { return "00000000-0000-0000-0000-000000000001" },
	})
	if err == nil || !strings.Contains(err.Error(), "Port 1455 is in use") {
		t.Fatalf("expected port 1455 in use error, got %v", err)
	}
}

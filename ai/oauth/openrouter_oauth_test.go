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
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

type toroRoundTripFunc func(*http.Request) (*http.Response, error)

func (f toroRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type toroInteraction struct {
	mu      sync.Mutex
	events  []ai.AuthEvent
	handler func(context.Context, ai.AuthPrompt) (string, error)
}

func (i *toroInteraction) Notify(event ai.AuthEvent) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.events = append(i.events, event)
}

func (i *toroInteraction) Prompt(ctx context.Context, prompt ai.AuthPrompt) (string, error) {
	if i.handler != nil {
		return i.handler(ctx, prompt)
	}
	return "", errors.New("unexpected prompt")
}

func (i *toroInteraction) snapshotEvents() []ai.AuthEvent {
	i.mu.Lock()
	defer i.mu.Unlock()
	out := make([]ai.AuthEvent, len(i.events))
	copy(out, i.events)
	return out
}

func toroResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestOpenRouterOAuth_CallbackUrlLoginWithPkceAndInfinityExpiry(t *testing.T) {
	if OpenRouterOAuth.LoginLabel == nil || *OpenRouterOAuth.LoginLabel != "Sign in with OpenRouter" {
		t.Fatalf("unexpected LoginLabel: %v", OpenRouterOAuth.LoginLabel)
	}

	var exchangeBody *jsonx.Object
	client := &http.Client{
		Transport: toroRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.String() != openTokenUrl {
				t.Fatalf("unexpected URL: %s", req.URL.String())
			}
			raw, _ := io.ReadAll(req.Body)
			parsed, err := jsonx.Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			exchangeBody, _ = parsed.(*jsonx.Object)
			return toroResponse(http.StatusOK, `{"key":"sk-or-v1-test-key"}`), nil
		}),
	}

	manualStarted := make(chan context.Context, 1)
	interaction := &toroInteraction{
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
		cred, err := OpenRouterOAuth.Login(ctx, interaction, nil)
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
	parsedAuthUrl, err := url.Parse(authUrl)
	if err != nil {
		t.Fatal(err)
	}
	if parsedAuthUrl.Scheme != "https" || parsedAuthUrl.Host != "openrouter.ai" || parsedAuthUrl.Path != "/auth" {
		t.Fatalf("unexpected authorize URL: %s", authUrl)
	}
	callbackUrl := parsedAuthUrl.Query().Get("callback_url")
	if callbackUrl == "" || parsedAuthUrl.Query().Get("code_challenge_method") != "S256" {
		t.Fatalf("missing PKCE/callback params in %s", authUrl)
	}

	resp, err := http.Get(callbackUrl + "?code=or-cb-code")
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
		t.Fatal("expected manual prompt cancellation after callback")
	}
	if got, _ := exchangeBody.GetString("code"); got != "or-cb-code" {
		t.Fatalf("exchanged code = %q, want or-cb-code", got)
	}
	if got, _ := exchangeBody.GetString("code_challenge_method"); got != "S256" {
		t.Fatalf("code_challenge_method = %q, want S256", got)
	}
	if res.cred.Access != "sk-or-v1-test-key" || res.cred.Refresh != "" || res.cred.Expires != openMaxSafeInteger {
		t.Fatalf("unexpected OpenRouter cred: %+v", res.cred)
	}

	// Refresh returns stored credential unchanged; ToAuth emits ApiKey.
	refreshed, err := OpenRouterOAuth.Refresh(context.Background(), res.cred)
	if err != nil || refreshed != res.cred {
		t.Fatalf("Refresh() = (%+v, %v), want identity", refreshed, err)
	}
	modelAuth, err := OpenRouterOAuth.ToAuth(context.Background(), res.cred)
	if err != nil {
		t.Fatalf("ToAuth() failed: %v", err)
	}
	if modelAuth.ApiKey == nil || *modelAuth.ApiKey != "sk-or-v1-test-key" {
		t.Fatalf("ToAuth().ApiKey = %v, want sk-or-v1-test-key", modelAuth.ApiKey)
	}
}

func TestOpenRouterOAuth_ManualInputFallbackAndErrors(t *testing.T) {
	t.Setenv("PI_OAUTH_CALLBACK_HOST", "127.0.0.1")

	var exchangedCode string
	client := &http.Client{
		Transport: toroRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			raw, _ := io.ReadAll(req.Body)
			parsed, _ := jsonx.Parse(raw)
			if obj, ok := parsed.(*jsonx.Object); ok {
				exchangedCode, _ = obj.GetString("code")
			}
			return toroResponse(http.StatusOK, `{"key":"sk-or-manual"}`), nil
		}),
	}

	// Supports full URL with query param or bare code string.
	for _, tc := range []struct {
		input string
		want  string
	}{
		{input: "http://127.0.0.1:12345/cb?code=from-url", want: "from-url"},
		{input: "  bare-code-123  ", want: "bare-code-123"},
	} {
		exchangedCode = ""
		interaction := &toroInteraction{
			handler: func(_ context.Context, _ ai.AuthPrompt) (string, error) {
				return tc.input, nil
			},
		}
		cred, err := OpenRouterOAuth.Login(tuWithHttpClient(context.Background(), client), interaction, nil)
		if err != nil {
			t.Fatalf("%q: Login() failed: %v", tc.input, err)
		}
		if exchangedCode != tc.want || cred.Access != "sk-or-manual" {
			t.Fatalf("%q: exchanged=%q cred=%+v", tc.input, exchangedCode, cred)
		}
	}

	// Missing code rejects cleanly.
	_, err := OpenRouterOAuth.Login(tuWithHttpClient(context.Background(), client), &toroInteraction{
		handler: func(_ context.Context, _ ai.AuthPrompt) (string, error) { return "   ", nil },
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "Missing authorization code") {
		t.Fatalf("expected missing code error, got %v", err)
	}

	// HTTP error includes detail; missing "key" field rejects cleanly.
	for _, tc := range []struct {
		status int
		body   string
		substr string
	}{
		{status: http.StatusBadRequest, body: `{"error_description":"bad code"}`, substr: "OpenRouter OAuth key exchange failed (HTTP 400): bad code"},
		{status: http.StatusOK, body: `not-json`, substr: "OpenRouter OAuth returned invalid JSON"},
		{status: http.StatusOK, body: `{}`, substr: `OpenRouter OAuth response carries no "key"`},
	} {
		errClient := &http.Client{
			Transport: toroRoundTripFunc(func(_ *http.Request) (*http.Response, error) {
				return toroResponse(tc.status, tc.body), nil
			}),
		}
		_, err := OpenRouterOAuth.Login(tuWithHttpClient(context.Background(), errClient), &toroInteraction{
			handler: func(_ context.Context, _ ai.AuthPrompt) (string, error) { return "code-1", nil },
		}, nil)
		if err == nil || !strings.Contains(err.Error(), tc.substr) {
			t.Fatalf("body=%s: got %v, want %q", tc.body, err, tc.substr)
		}
	}
}

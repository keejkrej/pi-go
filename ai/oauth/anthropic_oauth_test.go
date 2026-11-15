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
	"github.com/keejkrej/pi-go/internal/jsonx"
)

type taoRoundTripFunc func(*http.Request) (*http.Response, error)

func (f taoRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type taoInteraction struct {
	mu      sync.Mutex
	events  []ai.AuthEvent
	prompts []ai.AuthPrompt
	handler func(context.Context, ai.AuthPrompt) (string, error)
}

func (i *taoInteraction) Notify(event ai.AuthEvent) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.events = append(i.events, event)
}

func (i *taoInteraction) Prompt(ctx context.Context, prompt ai.AuthPrompt) (string, error) {
	i.mu.Lock()
	i.prompts = append(i.prompts, prompt)
	handler := i.handler
	i.mu.Unlock()
	if handler != nil {
		return handler(ctx, prompt)
	}
	return "", errors.New("unexpected prompt")
}

func (i *taoInteraction) snapshotEvents() []ai.AuthEvent {
	i.mu.Lock()
	defer i.mu.Unlock()
	out := make([]ai.AuthEvent, len(i.events))
	copy(out, i.events)
	return out
}

func (i *taoInteraction) snapshotPrompts() []ai.AuthPrompt {
	i.mu.Lock()
	defer i.mu.Unlock()
	out := make([]ai.AuthPrompt, len(i.prompts))
	copy(out, i.prompts)
	return out
}

func taoResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestAnthropicOAuth_SelectMethodBrowserAndCopyCode(t *testing.T) {
	var seenPrompt *ai.AuthPromptSelect
	var capturedBody *jsonx.Object

	client := &http.Client{
		Transport: taoRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.String() != anthTokenUrl {
				t.Fatalf("unexpected request URL: %s", req.URL.String())
			}
			raw, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := jsonx.Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			obj, _ := parsed.(*jsonx.Object)
			capturedBody = obj
			return taoResponse(http.StatusOK, `{"access_token":"anth-access","refresh_token":"anth-refresh","expires_in":3600}`), nil
		}),
	}

	var authUrl string
	interaction := &taoInteraction{}
	interaction.handler = func(_ context.Context, prompt ai.AuthPrompt) (string, error) {
		switch p := prompt.(type) {
		case *ai.AuthPromptSelect:
			seenPrompt = p
			return "copy_code", nil
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
			return "copied-code#" + parsedUrl.Query().Get("state"), nil
		default:
			return "", errors.New("unexpected prompt kind")
		}
	}

	ctx := tuWithHttpClient(context.Background(), client)
	cred, err := AnthropicOAuth.Login(ctx, interaction, nil)
	if err != nil {
		t.Fatalf("Login() failed: %v", err)
	}
	if seenPrompt == nil || seenPrompt.Message != "Select Anthropic login method:" {
		t.Fatalf("unexpected select prompt: %+v", seenPrompt)
	}
	if len(seenPrompt.Options) != 2 || seenPrompt.Options[0].Id != "browser" || seenPrompt.Options[1].Id != "copy_code" {
		t.Fatalf("unexpected select options: %+v", seenPrompt.Options)
	}

	parsedAuthUrl, err := url.Parse(authUrl)
	if err != nil {
		t.Fatal(err)
	}
	if got := parsedAuthUrl.Query().Get("redirect_uri"); got != anthCopyCodeRedirectUri {
		t.Fatalf("copy_code redirect_uri = %q, want %q", got, anthCopyCodeRedirectUri)
	}
	if got := parsedAuthUrl.Query().Get("code_challenge_method"); got != "S256" {
		t.Fatalf("code_challenge_method = %q, want S256", got)
	}
	if capturedBody == nil {
		t.Fatal("expected token exchange payload")
	}
	if got, _ := capturedBody.GetString("grant_type"); got != "authorization_code" {
		t.Fatalf("grant_type = %q, want authorization_code", got)
	}
	if got, _ := capturedBody.GetString("code"); got != "copied-code" {
		t.Fatalf("code = %q, want copied-code", got)
	}
	if got, _ := capturedBody.GetString("redirect_uri"); got != anthCopyCodeRedirectUri {
		t.Fatalf("payload redirect_uri = %q, want %q", got, anthCopyCodeRedirectUri)
	}
	if cred.Access != "anth-access" || cred.Refresh != "anth-refresh" {
		t.Fatalf("unexpected credential: %+v", cred)
	}
	if cred.Expires <= 0 {
		t.Fatalf("expected positive expires, got %v", cred.Expires)
	}
	modelAuth, err := AnthropicOAuth.ToAuth(context.Background(), cred)
	if err != nil {
		t.Fatalf("ToAuth() failed: %v", err)
	}
	if modelAuth.ApiKey == nil || *modelAuth.ApiKey != "anth-access" {
		t.Fatalf("ToAuth().ApiKey = %v, want anth-access", modelAuth.ApiKey)
	}

	// Verify state mismatch and empty code error paths.
	interactionMismatch := &taoInteraction{
		handler: func(_ context.Context, prompt ai.AuthPrompt) (string, error) {
			if _, ok := prompt.(*ai.AuthPromptSelect); ok {
				return "copy_code", nil
			}
			return "copied-code#wrong-state", nil
		},
	}
	if _, err := AnthropicOAuth.Login(ctx, interactionMismatch, nil); err == nil || !strings.Contains(err.Error(), "OAuth state mismatch") {
		t.Fatalf("expected state mismatch error, got %v", err)
	}

	interactionEmpty := &taoInteraction{
		handler: func(_ context.Context, prompt ai.AuthPrompt) (string, error) {
			if _, ok := prompt.(*ai.AuthPromptSelect); ok {
				return "copy_code", nil
			}
			return "   ", nil
		},
	}
	if _, err := AnthropicOAuth.Login(ctx, interactionEmpty, nil); err == nil || !strings.Contains(err.Error(), "Missing authorization code") {
		t.Fatalf("expected missing code error, got %v", err)
	}
}

func TestAnthropicOAuth_KeepsLocalhostRedirectUriForManualLogin(t *testing.T) {
	t.Setenv("PI_OAUTH_CALLBACK_HOST", "0.0.0.0")

	var capturedBody *jsonx.Object
	client := &http.Client{
		Transport: taoRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			raw, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := jsonx.Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			capturedBody, _ = parsed.(*jsonx.Object)
			return taoResponse(http.StatusOK, `{"access_token":"manual-access","refresh_token":"manual-refresh","expires_in":3600}`), nil
		}),
	}

	var authUrl string
	interaction := &taoInteraction{}
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
			redirectUri := parsedUrl.Query().Get("redirect_uri")
			state := parsedUrl.Query().Get("state")
			return redirectUri + "?code=manual-code&state=" + url.QueryEscape(state), nil
		default:
			return "", errors.New("unexpected prompt kind")
		}
	}

	ctx := tuWithHttpClient(context.Background(), client)
	cred, err := AnthropicOAuth.Login(ctx, interaction, nil)
	if err != nil {
		t.Fatalf("Login() failed: %v", err)
	}

	parsedAuthUrl, err := url.Parse(authUrl)
	if err != nil {
		t.Fatal(err)
	}
	redirectParsed, err := url.Parse(parsedAuthUrl.Query().Get("redirect_uri"))
	if err != nil {
		t.Fatal(err)
	}
	if redirectParsed.Hostname() != "localhost" {
		t.Fatalf("redirect hostname = %q, want localhost", redirectParsed.Hostname())
	}
	if redirectParsed.Path != "/callback" {
		t.Fatalf("redirect path = %q, want /callback", redirectParsed.Path)
	}
	if got, _ := capturedBody.GetString("code"); got != "manual-code" {
		t.Fatalf("exchanged code = %q, want manual-code", got)
	}
	if cred.Access != "manual-access" {
		t.Fatalf("Access = %q, want manual-access", cred.Access)
	}
}

func TestAnthropicOAuth_FallsBackToEphemeralCallbackPortWhenBusy(t *testing.T) {
	blocker, err := net.Listen("tcp", "127.0.0.1:53692")
	if err != nil {
		t.Fatalf("failed to bind blocker on 127.0.0.1:53692: %v", err)
	}
	defer blocker.Close()

	client := &http.Client{
		Transport: taoRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			raw, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := jsonx.Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			obj, _ := parsed.(*jsonx.Object)
			if code, _ := obj.GetString("code"); code != "cb-code" {
				t.Fatalf("unexpected exchanged code: %q", code)
			}
			return taoResponse(http.StatusOK, `{"access_token":"cb-access","refresh_token":"cb-refresh","expires_in":1800}`), nil
		}),
	}

	manualPromptStarted := make(chan context.Context, 1)
	interaction := &taoInteraction{
		handler: func(ctx context.Context, prompt ai.AuthPrompt) (string, error) {
			switch p := prompt.(type) {
			case *ai.AuthPromptSelect:
				return "browser", nil
			case *ai.AuthPromptManualCode:
				promptCtx := p.PromptContext
				if promptCtx == nil {
					promptCtx = ctx
				}
				manualPromptStarted <- promptCtx
				<-promptCtx.Done()
				return "", promptCtx.Err()
			default:
				return "", errors.New("unexpected prompt kind")
			}
		},
	}

	ctx, cancel := context.WithTimeout(tuWithHttpClient(context.Background(), client), 5*time.Second)
	defer cancel()

	type loginResult struct {
		cred *ai.OAuthCredential
		err  error
	}
	done := make(chan loginResult, 1)
	go func() {
		cred, err := AnthropicOAuth.Login(ctx, interaction, nil)
		done <- loginResult{cred, err}
	}()

	var manualPromptCtx context.Context
	select {
	case manualPromptCtx = <-manualPromptStarted:
	case res := <-done:
		t.Fatalf("login finished prematurely before callback: cred=%+v err=%v", res.cred, res.err)
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
	redirectUri := parsedAuthUrl.Query().Get("redirect_uri")
	redirectParsed, err := url.Parse(redirectUri)
	if err != nil {
		t.Fatal(err)
	}
	if redirectParsed.Hostname() != "localhost" {
		t.Fatalf("fallback redirect hostname = %q, want localhost", redirectParsed.Hostname())
	}
	if redirectParsed.Port() == "53692" || redirectParsed.Port() == "" {
		t.Fatalf("expected non-53692 ephemeral port in redirect_uri %q", redirectUri)
	}

	cbUrl := "http://127.0.0.1:" + redirectParsed.Port() + redirectParsed.Path + "?code=cb-code&state=" + url.QueryEscape(parsedAuthUrl.Query().Get("state"))
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
		t.Fatalf("Login() error = %v", res.err)
	}
	if res.cred == nil || res.cred.Access != "cb-access" {
		t.Fatalf("unexpected credential: %+v", res.cred)
	}
	if manualPromptCtx.Err() == nil {
		t.Fatal("expected manual prompt context to be cancelled after browser callback")
	}
}

func TestAnthropicOAuth_RefreshAndErrorDetails(t *testing.T) {
	var seenRefreshBody *jsonx.Object
	client := &http.Client{
		Transport: taoRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			raw, _ := io.ReadAll(req.Body)
			parsed, _ := jsonx.Parse(raw)
			seenRefreshBody, _ = parsed.(*jsonx.Object)
			return taoResponse(http.StatusOK, `{"access_token":"refreshed-access","refresh_token":"rotated-refresh","expires_in":7200}`), nil
		}),
	}
	ctx := tuWithHttpClient(context.Background(), client)
	cred, err := AnthropicOAuth.Refresh(ctx, &ai.OAuthCredential{
		Type:             ai.AuthTypeOAuth,
		OAuthCredentials: ai.OAuthCredentials{Refresh: "old-refresh", Access: "old-access", Expires: 1},
	})
	if err != nil {
		t.Fatalf("Refresh() failed: %v", err)
	}
	if got, _ := seenRefreshBody.GetString("grant_type"); got != "refresh_token" {
		t.Fatalf("refresh grant_type = %q, want refresh_token", got)
	}
	if got, _ := seenRefreshBody.GetString("refresh_token"); got != "old-refresh" {
		t.Fatalf("refresh_token = %q, want old-refresh", got)
	}
	if cred.Access != "refreshed-access" || cred.Refresh != "rotated-refresh" {
		t.Fatalf("unexpected refreshed cred: %+v", cred)
	}

	// Non-2xx status includes status, URL, and response body.
	errClient := &http.Client{
		Transport: taoRoundTripFunc(func(_ *http.Request) (*http.Response, error) {
			return taoResponse(http.StatusBadRequest, `{"error":"invalid_grant"}`), nil
		}),
	}
	_, err = AnthropicOAuth.Refresh(tuWithHttpClient(context.Background(), errClient), &ai.OAuthCredential{
		OAuthCredentials: ai.OAuthCredentials{Refresh: "bad"},
	})
	if err == nil || !strings.Contains(err.Error(), "status=400") || !strings.Contains(err.Error(), "invalid_grant") {
		t.Fatalf("expected detailed refresh error, got %v", err)
	}

	// Invalid JSON body reports descriptive error.
	badJsonClient := &http.Client{
		Transport: taoRoundTripFunc(func(_ *http.Request) (*http.Response, error) {
			return taoResponse(http.StatusOK, `not-json`), nil
		}),
	}
	_, err = AnthropicOAuth.Refresh(tuWithHttpClient(context.Background(), badJsonClient), &ai.OAuthCredential{
		OAuthCredentials: ai.OAuthCredentials{Refresh: "r"},
	})
	if err == nil || !strings.Contains(err.Error(), "Anthropic token refresh returned invalid JSON") {
		t.Fatalf("expected invalid JSON refresh error, got %v", err)
	}
}

// Ported from packages/ai/test/oauth-callback-server.test.ts (pi v1.5.2).

package oauth

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/ai"
)

func tocsPtr[T any](v T) *T {
	return &v
}

type tocsInteraction struct {
	prompt func(ctx context.Context, prompt ai.AuthPrompt) (string, error)
	notify func(event ai.AuthEvent)
}

func (i tocsInteraction) Prompt(ctx context.Context, prompt ai.AuthPrompt) (string, error) {
	if i.prompt == nil {
		<-ctx.Done()
		return "", ctx.Err()
	}
	return i.prompt(ctx, prompt)
}

func (i tocsInteraction) Notify(event ai.AuthEvent) {
	if i.notify != nil {
		i.notify(event)
	}
}

func tocsGet(t *testing.T, url string) (int, http.Header, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s failed: %v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body failed: %v", err)
	}
	return resp.StatusCode, resp.Header, string(body)
}

func TestOAuthCallbackServer_CompletesAndValidatesRoutesAndState(t *testing.T) {
	srv, err := StartOAuthCallbackServer(t.Context(), OAuthCallbackServerOptions[string]{
		ProviderName: "TestProvider",
		Host:         "127.0.0.1",
		Port:         0,
		Path:         "/auth/callback",
		RedirectHost: tocsPtr("localhost"),
		State:        tocsPtr("expected-state"),
		Complete: func(ctx context.Context, code string) (string, error) {
			return "exchanged-" + code, nil
		},
	})
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	defer srv.Close()

	if !strings.HasPrefix(srv.RedirectUri, "http://localhost:") || !strings.HasSuffix(srv.RedirectUri, "/auth/callback") {
		t.Fatalf("unexpected RedirectUri: %s", srv.RedirectUri)
	}
	localBase := strings.Replace(srv.RedirectUri, "http://localhost:", "http://127.0.0.1:", 1)

	status, hdr, body := tocsGet(t, strings.TrimSuffix(localBase, "/auth/callback")+"/other")
	if status != http.StatusNotFound || !strings.Contains(body, "Callback route not found.") {
		t.Fatalf("wrong route: status=%d body=%s", status, body)
	}
	if hdr.Get("cache-control") != "no-store" {
		t.Fatalf("expected cache-control no-store, got %q", hdr.Get("cache-control"))
	}

	status, _, body = tocsGet(t, localBase+"?code=abc&state=wrong")
	if status != http.StatusBadRequest || !strings.Contains(body, "State mismatch.") {
		t.Fatalf("state mismatch: status=%d body=%s", status, body)
	}

	status, _, body = tocsGet(t, localBase+"?state=expected-state")
	if status != http.StatusBadRequest || !strings.Contains(body, "Missing authorization code.") {
		t.Fatalf("missing code: status=%d body=%s", status, body)
	}

	status, hdr, body = tocsGet(t, localBase+"?code=good-code&state=expected-state")
	if status != http.StatusOK || !strings.Contains(body, "Signed in to TestProvider") {
		t.Fatalf("success callback: status=%d body=%s", status, body)
	}
	if hdr.Get("cache-control") != "no-store" {
		t.Fatalf("expected cache-control no-store, got %q", hdr.Get("cache-control"))
	}

	val, ok, waitErr := srv.Wait(t.Context())
	if waitErr != nil || !ok || val != "exchanged-good-code" {
		t.Fatalf("Wait got (%q, %v, %v), want (exchanged-good-code, true, nil)", val, ok, waitErr)
	}

	status, _, body = tocsGet(t, localBase+"?code=again&state=expected-state")
	if status != http.StatusConflict || !strings.Contains(body, "This sign-in has already been handled.") {
		t.Fatalf("duplicate callback: status=%d body=%s", status, body)
	}
}

func TestOAuthCallbackServer_ProviderErrorAndCompleteFailure(t *testing.T) {
	srv1, err := StartOAuthCallbackServer(t.Context(), OAuthCallbackServerOptions[string]{
		ProviderName: "TestProvider",
		Host:         "127.0.0.1",
		Port:         0,
		Path:         "/cb",
		Complete: func(ctx context.Context, code string) (string, error) {
			return code, nil
		},
	})
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	defer srv1.Close()

	status, _, body := tocsGet(t, srv1.RedirectUri+"?error=access_denied&error_description=User%20denied")
	if status != http.StatusBadRequest || !strings.Contains(body, "TestProvider authorization failed.") {
		t.Fatalf("error callback: status=%d body=%s", status, body)
	}
	_, _, waitErr := srv1.Wait(t.Context())
	if waitErr == nil || waitErr.Error() != "TestProvider authorization failed: User denied" {
		t.Fatalf("Wait error got %v", waitErr)
	}

	srv2, err := StartOAuthCallbackServer(t.Context(), OAuthCallbackServerOptions[string]{
		ProviderName: "TestProvider",
		Host:         "127.0.0.1",
		Port:         0,
		Path:         "/cb",
		Complete: func(ctx context.Context, code string) (string, error) {
			return "", errors.New("token exchange boom")
		},
	})
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	defer srv2.Close()

	status, _, body = tocsGet(t, srv2.RedirectUri+"?code=abc")
	if status != http.StatusBadGateway || !strings.Contains(body, "TestProvider sign-in failed.") {
		t.Fatalf("502 callback: status=%d body=%s", status, body)
	}
	_, _, waitErr = srv2.Wait(t.Context())
	if waitErr == nil || waitErr.Error() != "token exchange boom" {
		t.Fatalf("Wait error got %v, want token exchange boom", waitErr)
	}
}

func TestOAuthCallbackServer_CancelClaimedTimeoutAndClose(t *testing.T) {
	claimed := make(chan struct{})
	release := make(chan struct{})
	srv, err := StartOAuthCallbackServer(t.Context(), OAuthCallbackServerOptions[string]{
		ProviderName: "TestProvider",
		Host:         "127.0.0.1",
		Port:         0,
		Path:         "/cb",
		Complete: func(ctx context.Context, code string) (string, error) {
			close(claimed)
			<-release
			return "finished-" + code, nil
		},
	})
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	defer srv.Close()

	httpDone := make(chan int, 1)
	go func() {
		status, _, _ := tocsGet(t, srv.RedirectUri+"?code=c1")
		httpDone <- status
	}()

	<-claimed
	// Cancel after claim must NOT drop the in-flight completion.
	srv.Cancel()
	close(release)

	if st := <-httpDone; st != http.StatusOK {
		t.Fatalf("expected 200 after release, got %d", st)
	}
	val, ok, waitErr := srv.Wait(t.Context())
	if waitErr != nil || !ok || val != "finished-c1" {
		t.Fatalf("Wait got (%q, %v, %v), want (finished-c1, true, nil)", val, ok, waitErr)
	}

	// Cancel before claim resolves (zero, false, nil)
	srvCancel, err := StartOAuthCallbackServer(t.Context(), OAuthCallbackServerOptions[string]{
		ProviderName: "TestProvider",
		Host:         "127.0.0.1",
		Port:         0,
		Path:         "/cb",
		Complete:     func(ctx context.Context, code string) (string, error) { return code, nil },
	})
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	srvCancel.Cancel()
	srvCancel.Close()
	_, ok, waitErr = srvCancel.Wait(t.Context())
	if waitErr != nil || ok {
		t.Fatalf("expected cancelled Wait (false, nil), got (%v, %v)", ok, waitErr)
	}

	// Close rejects Wait
	srvClose, err := StartOAuthCallbackServer(t.Context(), OAuthCallbackServerOptions[string]{
		ProviderName: "TestProvider",
		Host:         "127.0.0.1",
		Port:         0,
		Path:         "/cb",
		Complete:     func(ctx context.Context, code string) (string, error) { return code, nil },
	})
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	srvClose.Close()
	_, _, waitErr = srvClose.Wait(t.Context())
	if waitErr == nil || waitErr.Error() != "OAuth callback server closed" {
		t.Fatalf("expected closed error, got %v", waitErr)
	}

	// Timeout rejects Wait
	srvTimeout, err := StartOAuthCallbackServer(t.Context(), OAuthCallbackServerOptions[string]{
		ProviderName: "TestProvider",
		Host:         "127.0.0.1",
		Port:         0,
		Path:         "/cb",
		TimeoutMs:    tocsPtr(int64(25)),
		Complete:     func(ctx context.Context, code string) (string, error) { return code, nil },
	})
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	defer srvTimeout.Close()
	_, _, waitErr = srvTimeout.Wait(t.Context())
	if waitErr == nil || waitErr.Error() != "TestProvider sign-in timed out" {
		t.Fatalf("expected timeout error, got %v", waitErr)
	}
}

func TestOAuthCallbackServer_WaitForCallbackOrManualInput(t *testing.T) {
	// Callback wins
	srv, err := StartOAuthCallbackServer(t.Context(), OAuthCallbackServerOptions[string]{
		ProviderName: "TestProvider",
		Host:         "127.0.0.1",
		Port:         0,
		Path:         "/cb",
		Complete:     func(ctx context.Context, code string) (string, error) { return "val-" + code, nil },
	})
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	defer srv.Close()

	go func() {
		_, _, _ = tocsGet(t, srv.RedirectUri+"?code=xyz")
	}()
	res, err := WaitForCallbackOrManualInput(t.Context(), tocsInteraction{}, srv, ManualInputPrompt{
		Message:     "Paste code",
		Placeholder: "code",
	})
	if err != nil || res.Type != CallbackOrManualInputKindCallback || res.Value != "val-xyz" {
		t.Fatalf("got (%+v, %v), want callback val-xyz", res, err)
	}

	// Manual input wins when callback never arrives
	srv2, err := StartOAuthCallbackServer(t.Context(), OAuthCallbackServerOptions[string]{
		ProviderName: "TestProvider",
		Host:         "127.0.0.1",
		Port:         0,
		Path:         "/cb",
		Complete:     func(ctx context.Context, code string) (string, error) { return code, nil },
	})
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	defer srv2.Close()

	res, err = WaitForCallbackOrManualInput(t.Context(), tocsInteraction{
		prompt: func(ctx context.Context, p ai.AuthPrompt) (string, error) {
			time.Sleep(10 * time.Millisecond)
			return "manual-123", nil
		},
	}, srv2, ManualInputPrompt{Message: "Paste", Placeholder: "..."})
	if err != nil || res.Type != CallbackOrManualInputKindManual || res.Input != "manual-123" {
		t.Fatalf("got (%+v, %v), want manual manual-123", res, err)
	}

	// Prompt error aborts callback and surfaces prompt error
	srv3, err := StartOAuthCallbackServer(t.Context(), OAuthCallbackServerOptions[string]{
		ProviderName: "TestProvider",
		Host:         "127.0.0.1",
		Port:         0,
		Path:         "/cb",
		Complete:     func(ctx context.Context, code string) (string, error) { return code, nil },
	})
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	defer srv3.Close()

	_, err = WaitForCallbackOrManualInput(t.Context(), tocsInteraction{
		prompt: func(ctx context.Context, p ai.AuthPrompt) (string, error) {
			return "", errors.New("prompt aborted")
		},
	}, srv3, ManualInputPrompt{Message: "Paste", Placeholder: "..."})
	if err == nil || err.Error() != "prompt aborted" {
		t.Fatalf("got %v, want prompt aborted", err)
	}

	// Nil callback runs manual prompt directly
	res, err = WaitForCallbackOrManualInput[string](t.Context(), tocsInteraction{
		prompt: func(ctx context.Context, p ai.AuthPrompt) (string, error) {
			return "only-manual", nil
		},
	}, nil, ManualInputPrompt{Message: "Paste", Placeholder: "..."})
	if err != nil || res.Type != CallbackOrManualInputKindManual || res.Input != "only-manual" {
		t.Fatalf("got (%+v, %v), want manual only-manual", res, err)
	}
}

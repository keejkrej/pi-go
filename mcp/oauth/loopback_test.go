// Ported from packages/mcp/test/loopback.test.ts (pi v1.5.2).

package oauth_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/mcp/internal/testkit"
	mcpoauth "github.com/keejkrej/pi-go/mcp/oauth"
)

func tloopListenAt(t *testing.T, serverUrl string, documents bool) *mcpoauth.McpLoopbackCallback {
	t.Helper()
	var docs *mcpoauth.McpClientMetadataDocuments
	if documents {
		docs = &mcpoauth.McpClientMetadataDocuments{
			BaseUrl:          "https://app.example/oauth",
			ListsRedirectUrl: func(string) bool { return true },
		}
	}
	cb, err := mcpoauth.ListenForLoopbackSignIn(mcpoauth.ListenForLoopbackSignInOptions{
		ServerUrl:               serverUrl,
		Store:                   testkit.NewMemoryStore(),
		Settings:                mcpoauth.McpOAuthSettings{},
		ClientMetadataDocuments: docs,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cb.Callback.Close() })
	return cb
}

func tloopPending(redirectUrl string) mcpoauth.McpSignInRedirect {
	return mcpoauth.McpSignInRedirect{
		Kind:             "redirect",
		AuthorizationUrl: "https://auth.example/authorize",
		RedirectUrl:      redirectUrl,
		State:            "state-1",
	}
}

func TestLoopback_CompletesThroughCallbackServer(t *testing.T) {
	server := testkit.StartAuthorizationServer(t, testkit.AuthorizationServerOptions{})
	store := testkit.NewMemoryStore()
	loopback, err := mcpoauth.ListenForLoopbackSignIn(mcpoauth.ListenForLoopbackSignInOptions{
		ServerUrl: server.ServerUrl,
		Store:     store,
		Settings:  mcpoauth.McpOAuthSettings{},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = loopback.Callback.Close() }()

	if !regexp.MustCompile(`^http://127\.0\.0\.1:\d+/callback$`).MatchString(loopback.RedirectUrl) {
		t.Fatalf("redirectUrl = %q", loopback.RedirectUrl)
	}

	opts := mcpoauth.McpSignInOptions{
		McpOAuthClientOptions: mcpoauth.McpOAuthClientOptions{
			ServerUrl:  server.ServerUrl,
			Store:      store,
			ClientName: "app",
		},
		RedirectUrl: loopback.RedirectUrl,
	}
	started, err := mcpoauth.StartMcpSignIn(opts)
	if err != nil {
		t.Fatal(err)
	}
	if started.Kind != "redirect" {
		t.Fatalf("expected redirect, got %q", started.Kind)
	}
	authorization := started.Redirect()

	respCh := make(chan struct {
		resp *mcpoauth.McpAuthorizationResponse
		err  error
	}, 1)
	go func() {
		r, err := mcpoauth.WaitForLoopbackSignIn(mcpoauth.WaitForLoopbackSignInOptions{
			Callback:      loopback.Callback,
			Authorization: authorization,
		})
		respCh <- struct {
			resp *mcpoauth.McpAuthorizationResponse
			err  error
		}{r, err}
	}()

	time.Sleep(15 * time.Millisecond)
	loc, err := testkit.AuthorizeInBrowser(authorization.AuthorizationUrl)
	if err != nil {
		t.Fatal(err)
	}
	cbResp, err := http.Get(loc)
	if err != nil {
		t.Fatal(err)
	}
	_ = cbResp.Body.Close()

	waitOut := <-respCh
	if waitOut.err != nil {
		t.Fatal(waitOut.err)
	}
	if err := mcpoauth.CompleteMcpSignIn(mcpoauth.CompleteMcpSignInOptions{
		McpSignInOptions: opts,
		Authorization:    authorization,
		Response:         *waitOut.resp,
	}); err != nil {
		t.Fatal(err)
	}

	state, _ := store.Load()
	if state == nil || state.Tokens == nil || state.Tokens.AccessToken != "access-1" {
		t.Fatalf("tokens = %#v", state)
	}
	regs := server.Registrations()
	if len(regs) != 1 {
		t.Fatalf("registrations = %d", len(regs))
	}
	appType, _ := regs[0].Get("application_type")
	uris, _ := regs[0].Get("redirect_uris")
	if appType != "native" || !reflect.DeepEqual(uris, []any{loopback.RedirectUrl}) {
		t.Fatalf("registration = %#v", regs[0])
	}
}

func TestLoopback_RejectsResponseOnAnotherRedirectPath(t *testing.T) {
	serverUrl := "https://mcp.example/mcp"
	cb := tloopListenAt(t, serverUrl, true)
	sum := sha256.Sum256([]byte(serverUrl))
	id := base64.RawURLEncoding.EncodeToString(sum[:9])

	errCh := make(chan error, 1)
	go func() {
		_, err := mcpoauth.WaitForLoopbackSignIn(mcpoauth.WaitForLoopbackSignInOptions{
			Callback:      cb.Callback,
			Authorization: tloopPending(cb.RedirectUrl + "/" + id),
		})
		errCh <- err
	}()

	time.Sleep(20 * time.Millisecond)
	resp, err := http.Get(cb.RedirectUrl + "?code=c&state=state-1")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	waitErr := <-errCh
	if waitErr == nil || !strings.Contains(waitErr.Error(), "arrived on another redirect URI") {
		t.Fatalf("waitErr = %v, want arrived on another redirect URI", waitErr)
	}
}

func TestLoopback_TakesPastedRedirectUrlWhileWaiting(t *testing.T) {
	cb := tloopListenAt(t, "https://mcp.example/mcp", false)
	resp, err := mcpoauth.WaitForLoopbackSignIn(mcpoauth.WaitForLoopbackSignInOptions{
		Callback:      cb.Callback,
		Authorization: tloopPending(cb.RedirectUrl),
		PromptForRedirectUrl: func(context.Context) (*string, error) {
			v := cb.RedirectUrl + "?code=pasted&state=state-1"
			return &v, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := &mcpoauth.McpAuthorizationResponse{Code: "pasted", State: "state-1"}
	if !reflect.DeepEqual(resp, want) {
		t.Fatalf("resp = %#v, want %#v", resp, want)
	}
}

func TestLoopback_CancelsWaitAndEmptyPasteCancelsToo(t *testing.T) {
	cb := tloopListenAt(t, "https://mcp.example/mcp", false)
	ctx, cancel := context.WithCancel(t.Context())
	errCh := make(chan error, 1)
	go func() {
		_, err := mcpoauth.WaitForLoopbackSignIn(mcpoauth.WaitForLoopbackSignInOptions{
			Callback:      cb.Callback,
			Authorization: tloopPending(cb.RedirectUrl),
			Context:       ctx,
		})
		errCh <- err
	}()
	cancel()
	var cancelErr *mcpoauth.McpSignInCancelledError
	if err := <-errCh; !errors.As(err, &cancelErr) {
		t.Fatalf("expected McpSignInCancelledError, got %v", err)
	}

	p2 := tloopPending(cb.RedirectUrl)
	p2.State = "state-2"
	_, err := mcpoauth.WaitForLoopbackSignIn(mcpoauth.WaitForLoopbackSignInOptions{
		Callback:      cb.Callback,
		Authorization: p2,
		PromptForRedirectUrl: func(context.Context) (*string, error) {
			empty := ""
			return &empty, nil
		},
	})
	if !errors.As(err, &cancelErr) {
		t.Fatalf("expected McpSignInCancelledError for empty paste, got %v", err)
	}
}

func TestLoopback_ResponseFromRedirectUrl(t *testing.T) {
	authorization := tloopPending("http://127.0.0.1:1234/callback/server-id")
	redirectUrl := authorization.RedirectUrl

	resp, err := mcpoauth.ResponseFromRedirectUrl(" "+redirectUrl+"?code=c&state=state-1&iss=x ", authorization)
	if err != nil {
		t.Fatal(err)
	}
	issX := "x"
	want := &mcpoauth.McpAuthorizationResponse{Code: "c", State: "state-1", Iss: &issX}
	if !reflect.DeepEqual(resp, want) {
		t.Fatalf("resp = %#v, want %#v", resp, want)
	}

	cases := []struct {
		input string
		want  string
	}{
		{redirectUrl + "?code=c&state=other", "belongs to a different sign-in"},
		{"http://127.0.0.1:1234/callback?code=c&state=state-1", "does not match this sign-in's redirect URI"},
		{redirectUrl + "?error=access_denied", "access_denied"},
		{"code=c", "Expected the full redirect URL"},
	}
	for _, tc := range cases {
		_, err := mcpoauth.ResponseFromRedirectUrl(tc.input, authorization)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("input=%q: err=%v, want %q", tc.input, err, tc.want)
		}
	}
}

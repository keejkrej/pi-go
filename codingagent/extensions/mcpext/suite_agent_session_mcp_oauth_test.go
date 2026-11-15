// Ported from packages/coding-agent/test/suite/agent-session-mcp-oauth.test.ts (pi v1.5.2).

package mcpext

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/internal/testkit"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

type tsasmoSetupResult struct {
	harness       *testkit.SuiteHarness
	server        *tmoOAuthMcpServer
	notifications *[]string
	notifyMu      *sync.Mutex
	backend       *core.InMemoryAuthStorageBackend
	opened        *[]*url.URL
	openedMu      *sync.Mutex
}

func (r *tsasmoSetupResult) lastNotification() string {
	r.notifyMu.Lock()
	defer r.notifyMu.Unlock()
	if len(*r.notifications) == 0 {
		return ""
	}
	return (*r.notifications)[len(*r.notifications)-1]
}

func (r *tsasmoSetupResult) allNotifications() []string {
	r.notifyMu.Lock()
	defer r.notifyMu.Unlock()
	out := make([]string, len(*r.notifications))
	copy(out, *r.notifications)
	return out
}

func (r *tsasmoSetupResult) openedURLs() []*url.URL {
	r.openedMu.Lock()
	defer r.openedMu.Unlock()
	out := make([]*url.URL, len(*r.opened))
	copy(out, *r.opened)
	return out
}

func tsasmoWaitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !cond() {
		t.Fatalf("condition not met within %s", timeout)
	}
}

func tsasmoFreePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen 127.0.0.1:0: %v", err)
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().(*net.TCPAddr).Port
}

func tsasmoSetup(t *testing.T, browser string, oauthCfg *McpOAuthConfig) *tsasmoSetupResult {
	t.Helper()
	ctx := context.Background()
	srv := tmoStartOAuthMcpServer(t)
	backend := core.NewInMemoryAuthStorageBackend()
	expDirect := core.McpExposureDirect
	entry := McpServerEntry{
		Name: "issues",
		Config: McpServerConfig{
			Url:      &srv.URL,
			Exposure: &expDirect,
			OAuth:    oauthCfg,
		},
		Source: "test",
	}

	var notifyMu sync.Mutex
	var notifications []string
	var openedMu sync.Mutex
	var opened []*url.URL
	redirectCh := make(chan string, 1)

	extRes := core.LoadExtensionsWithOptions(ctx, nil, &core.LoadExtensionsOptions{
		Cwd: t.TempDir(),
		Factories: []core.InlineExtension{
			{
				Name: "mcp",
				Factory: CreateMcpExtension(McpExtensionOptions{
					LoadConfig: func(core.ExtensionContext) LoadedMcpConfig {
						return LoadedMcpConfig{Servers: []McpServerEntry{entry}}
					},
					Credentials: NewMcpOAuthCredentialStore(backend),
					HTTPClient:  srv.httpServer.Client(),
					OpenUrl: func(raw string) {
						parsed, err := url.Parse(raw)
						if err == nil {
							openedMu.Lock()
							opened = append(opened, parsed)
							openedMu.Unlock()
						}
						if browser == "follow" {
							go func() {
								resp, err := srv.httpServer.Client().Get(raw)
								if err == nil {
									_ = resp.Body.Close()
								}
							}()
						} else {
							go func() {
								noRedirect := &http.Client{
									Transport: srv.httpServer.Client().Transport,
									CheckRedirect: func(*http.Request, []*http.Request) error {
										return http.ErrUseLastResponse
									},
								}
								resp, err := noRedirect.Get(raw)
								if err != nil {
									return
								}
								loc := resp.Header.Get("Location")
								_ = resp.Body.Close()
								select {
								case redirectCh <- loc:
								default:
								}
							}()
						}
					},
				}),
			},
		},
	})
	if len(extRes.Errors) > 0 {
		t.Fatalf("LoadExtensionsWithOptions errors: %+v", extRes.Errors)
	}

	harness, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
		InitialActiveToolNames: []string{},
		ResourceLoader: testkit.CreateTestResourceLoader(&testkit.CreateTestResourceLoaderOptions{
			ExtensionsResult: &extRes,
		}),
	})
	if err != nil {
		t.Fatalf("CreateSuiteHarness: %v", err)
	}
	t.Cleanup(harness.Cleanup)

	uiCtx := testkit.CreateTestUiContext(&testkit.TestUiContextOverrides{
		Notify: func(message string, _ ...string) {
			notifyMu.Lock()
			notifications = append(notifications, message)
			notifyMu.Unlock()
		},
		Input: func(inCtx context.Context, _ string, _ *string, _ ...*core.ExtensionUIDialogOptions) (*string, error) {
			if browser == "paste" {
				select {
				case loc := <-redirectCh:
					return &loc, nil
				case <-inCtx.Done():
					return nil, inCtx.Err()
				}
			}
			<-inCtx.Done()
			return nil, nil
		},
	})
	if err := harness.Session.BindExtensions(ctx, core.ExtensionBindings{
		UIContext: uiCtx,
	}); err != nil {
		t.Fatalf("BindExtensions: %v", err)
	}

	return &tsasmoSetupResult{
		harness:       harness,
		server:        srv,
		notifications: &notifications,
		notifyMu:      &notifyMu,
		backend:       backend,
		opened:        &opened,
		openedMu:      &openedMu,
	}
}

func tsasmoCallWhoami(t *testing.T, harness *testkit.SuiteHarness) *ai.ToolResultMessage {
	t.Helper()
	stopTool := ai.StopReasonToolUse
	harness.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(
				providers.FauxAssistantBlock{
					Block: providers.FauxToolCall("mcp__issues__whoami", jsonx.NewObject(), nil),
				},
				&providers.FauxAssistantMessageOptions{StopReason: &stopTool},
			),
		},
		providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(providers.FauxAssistantText("done"), nil),
		},
	})
	msgs := harness.Session.Messages()
	before := len(msgs)
	if err := harness.Session.Prompt(context.Background(), "who am i"); err != nil {
		t.Fatalf("Prompt(who am i): %v", err)
	}
	after := harness.Session.Messages()
	for _, msg := range after[before:] {
		if tr, ok := msg.(*ai.ToolResultMessage); ok && tr != nil {
			return tr
		}
	}
	t.Fatalf("no tool result produced after prompt")
	return nil
}

func tsasmoShutdown(harness *testkit.SuiteHarness) time.Duration {
	start := time.Now()
	core.EmitSessionShutdownEvent(context.Background(), harness.Session.ExtensionRunner(), core.SessionShutdownEvent{
		Type:   "session_shutdown",
		Reason: "quit",
	})
	return time.Since(start)
}

func tsasmoExpireTokensAt(t *testing.T, backend *core.InMemoryAuthStorageBackend, expireAtMs int64) {
	t.Helper()
	_, err := backend.WithLock(func(current *string) (core.AuthStorageLockResult, error) {
		raw := "{}"
		if current != nil && *current != "" {
			raw = *current
		}
		parsed, err := jsonx.Parse([]byte(raw))
		if err != nil {
			return core.AuthStorageLockResult{}, err
		}
		obj, ok := parsed.(*jsonx.Object)
		if !ok || obj == nil {
			obj = jsonx.NewObject()
		}
		for _, val := range obj.All() {
			if stObj, ok := val.(*jsonx.Object); ok && stObj != nil {
				stObj.Set("tokensExpireAt", float64(expireAtMs))
			}
		}
		encoded, err := jsonx.Stringify(obj)
		if err != nil {
			return core.AuthStorageLockResult{}, err
		}
		return core.AuthStorageLockResult{Next: &encoded}, nil
	})
	if err != nil {
		t.Fatalf("WithLock(tokensExpireAt): %v", err)
	}
}

func tsasmoBackendSnapshot(t *testing.T, backend *core.InMemoryAuthStorageBackend) string {
	t.Helper()
	res, err := backend.WithLock(func(current *string) (core.AuthStorageLockResult, error) {
		val := ""
		if current != nil {
			val = *current
		}
		return core.AuthStorageLockResult{Result: val}, nil
	})
	if err != nil {
		t.Fatalf("WithLock(snapshot): %v", err)
	}
	s, _ := res.(string)
	return s
}

func TestAgentSessionMcpOAuth(t *testing.T) {
	ctx := context.Background()

	t.Run("signs in through the browser, refreshes expired tokens, and signs out", func(t *testing.T) {
		res := tsasmoSetup(t, "follow", nil)
		if err := res.harness.Session.Prompt(ctx, "/mcp"); err != nil {
			t.Fatalf("Prompt(/mcp): %v", err)
		}
		notes := res.allNotifications()
		if !slices.Contains(notes, "MCP servers need attention:\n  issues: needs sign-in\nRun /mcp to fix.") {
			t.Fatalf("expected startup problem notification, got %v", notes)
		}
		if got := res.lastNotification(); got != "issues: needs sign-in, run /mcp login issues (direct)" {
			t.Fatalf("expected /mcp status notification, got %q", got)
		}

		if err := res.harness.Session.Prompt(ctx, "/mcp login issues"); err != nil {
			t.Fatalf("Prompt(/mcp login issues): %v", err)
		}
		if got := res.lastNotification(); got != `Signed in to MCP server "issues" (1 tools).` {
			t.Fatalf("expected signed in notification, got %q", got)
		}
		if log := res.server.Log(); !slices.Equal(log, []string{"401 none", "register", "token code"}) {
			t.Fatalf("unexpected server log after login: %v", log)
		}
		if snap := tsasmoBackendSnapshot(t, res.backend); !strings.Contains(snap, `"access_token":"access-1"`) && !strings.Contains(snap, `"access_token": "access-1"`) {
			t.Fatalf("expected access-1 in credential store, got %s", snap)
		}
		if got := testkit.GetMessageText(tsasmoCallWhoami(t, res.harness)); got != "token access-1" {
			t.Fatalf("expected token access-1, got %q", got)
		}

		res.server.ExpireAccessTokens()
		if got := testkit.GetMessageText(tsasmoCallWhoami(t, res.harness)); got != "token access-2" {
			t.Fatalf("expected token access-2 after 401 refresh, got %q", got)
		}
		log := res.server.Log()
		if len(log) < 3 || !slices.Equal(log[len(log)-3:], []string{"401 access-1", "token refresh", "call access-2"}) {
			t.Fatalf("unexpected tail of server log after 401 refresh: %v", log)
		}

		tsasmoExpireTokensAt(t, res.backend, time.Now().UnixMilli()-1_000)
		if got := testkit.GetMessageText(tsasmoCallWhoami(t, res.harness)); got != "token access-3" {
			t.Fatalf("expected token access-3 after proactive refresh, got %q", got)
		}
		log = res.server.Log()
		if len(log) < 2 || !slices.Equal(log[len(log)-2:], []string{"token refresh", "call access-3"}) {
			t.Fatalf("unexpected tail of server log after proactive refresh: %v", log)
		}

		if err := res.harness.Session.Prompt(ctx, "/mcp logout issues"); err != nil {
			t.Fatalf("Prompt(/mcp logout issues): %v", err)
		}
		if got := res.lastNotification(); got != `Signed out of MCP server "issues".` {
			t.Fatalf("expected signed out notification, got %q", got)
		}
		tr := tsasmoCallWhoami(t, res.harness)
		if !tr.IsError {
			t.Fatalf("expected tool error after logout, got %+v", tr)
		}
		if got := testkit.GetMessageText(tr); got != `MCP server "issues" requires sign-in. Run /mcp to sign in.` {
			t.Fatalf("unexpected error text after logout: %q", got)
		}
	})

	t.Run("accepts a pasted redirect URL when the browser cannot reach the callback", func(t *testing.T) {
		res := tsasmoSetup(t, "paste", nil)
		if err := res.harness.Session.Prompt(ctx, "/mcp login"); err != nil {
			t.Fatalf("Prompt(/mcp login): %v", err)
		}
		if got := res.lastNotification(); got != `Signed in to MCP server "issues" (1 tools).` {
			t.Fatalf("expected signed in notification, got %q", got)
		}
		if got := testkit.GetMessageText(tsasmoCallWhoami(t, res.harness)); got != "token access-1" {
			t.Fatalf("expected token access-1, got %q", got)
		}
		if !slices.Contains(res.server.Log(), "token code") {
			t.Fatalf("expected token code in log, got %v", res.server.Log())
		}
	})

	for _, stallPath := range []string{"/.well-known/oauth-authorization-server", "/token"} {
		t.Run("cancels a sign-in waiting on "+stallPath+" when the session shuts down", func(t *testing.T) {
			res := tsasmoSetup(t, "follow", nil)
			if err := res.harness.Session.Prompt(ctx, "/mcp"); err != nil {
				t.Fatalf("Prompt(/mcp): %v", err)
			}
			res.server.StallPath(stallPath)

			loginDone := make(chan error, 1)
			go func() {
				loginDone <- res.harness.Session.Prompt(ctx, "/mcp login issues")
			}()

			tsasmoWaitFor(t, 5*time.Second, func() bool {
				return res.server.StalledCount() == 1
			})

			elapsed := tsasmoShutdown(res.harness)
			if elapsed >= 2*time.Second {
				t.Fatalf("expected shutdown within 2s, took %s", elapsed)
			}
			select {
			case err := <-loginDone:
				if err != nil {
					t.Fatalf("login prompt returned error: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("login prompt did not exit after shutdown")
			}

			tsasmoWaitFor(t, 5*time.Second, func() bool {
				return res.server.StalledAbortedCount() >= 1
			})
			for _, msg := range res.allNotifications() {
				if strings.HasPrefix(msg, "Sign-in") {
					t.Fatalf("ended session reported sign-in notification: %q", msg)
				}
			}
		})
	}

	t.Run("closes the session without refreshing an expiring token", func(t *testing.T) {
		res := tsasmoSetup(t, "follow", nil)
		if err := res.harness.Session.Prompt(ctx, "/mcp login issues"); err != nil {
			t.Fatalf("Prompt(/mcp login issues): %v", err)
		}
		if got := res.lastNotification(); got != `Signed in to MCP server "issues" (1 tools).` {
			t.Fatalf("expected signed in notification, got %q", got)
		}
		tsasmoExpireTokensAt(t, res.backend, time.Now().UnixMilli()+10_000)
		res.server.StallPath("/token")

		elapsed := tsasmoShutdown(res.harness)
		if elapsed >= 2*time.Second {
			t.Fatalf("expected shutdown within 2s, took %s", elapsed)
		}
		if res.server.StalledCount() != 0 {
			t.Fatalf("expected no stalled token refresh, got %d", res.server.StalledCount())
		}
		if dels := res.server.Deletes(); !slices.Equal(dels, []string{"access-1"}) {
			t.Fatalf("expected DELETE with access-1, got %v", dels)
		}
	})

	t.Run("uses the configured callback URL and scope", func(t *testing.T) {
		port := tsasmoFreePort(t)
		callbackUrl := fmt.Sprintf("http://127.0.0.1:%d/callback", port)
		res := tsasmoSetup(t, "follow", &McpOAuthConfig{
			CallbackUrl: callbackUrl,
			Scope:       "issues:read",
		})
		if err := res.harness.Session.Prompt(ctx, "/mcp login issues"); err != nil {
			t.Fatalf("Prompt(/mcp login issues): %v", err)
		}
		if got := res.lastNotification(); got != `Signed in to MCP server "issues" (1 tools).` {
			t.Fatalf("expected signed in notification, got %q", got)
		}
		urls := res.openedURLs()
		if len(urls) == 0 {
			t.Fatalf("expected authorization URL opened")
		}
		if got := urls[0].Query().Get("redirect_uri"); got != callbackUrl {
			t.Fatalf("expected redirect_uri %q, got %q", callbackUrl, got)
		}
		if got := urls[0].Query().Get("scope"); got != "issues:read" {
			t.Fatalf("expected scope issues:read, got %q", got)
		}
		if got := testkit.GetMessageText(tsasmoCallWhoami(t, res.harness)); got != "token access-1" {
			t.Fatalf("expected token access-1, got %q", got)
		}
	})

	t.Run("registers with the configured client name", func(t *testing.T) {
		res := tsasmoSetup(t, "follow", &McpOAuthConfig{ClientName: "Claude Code"})
		if err := res.harness.Session.Prompt(ctx, "/mcp login issues"); err != nil {
			t.Fatalf("Prompt(/mcp login issues): %v", err)
		}
		if got := res.lastNotification(); got != `Signed in to MCP server "issues" (1 tools).` {
			t.Fatalf("expected signed in notification, got %q", got)
		}
		regs := res.server.Registrations()
		if len(regs) != 1 {
			t.Fatalf("expected 1 registration, got %d", len(regs))
		}
		if cn, _ := regs[0].GetString("client_name"); cn != "Claude Code" {
			t.Fatalf("expected client_name Claude Code, got %q", cn)
		}

		fallback := tsasmoSetup(t, "follow", nil)
		if err := fallback.harness.Session.Prompt(ctx, "/mcp login issues"); err != nil {
			t.Fatalf("fallback Prompt(/mcp login issues): %v", err)
		}
		fbRegs := fallback.server.Registrations()
		if len(fbRegs) != 1 {
			t.Fatalf("expected 1 fallback registration, got %d", len(fbRegs))
		}
		if cn, _ := fbRegs[0].GetString("client_name"); cn != "pi" {
			t.Fatalf("expected fallback client_name pi, got %q", cn)
		}
		if appType, _ := fbRegs[0].GetString("application_type"); appType != "native" {
			t.Fatalf("expected application_type native, got %q", appType)
		}
	})

	t.Run("adds the listening port to a callback URL without one", func(t *testing.T) {
		res := tsasmoSetup(t, "follow", &McpOAuthConfig{CallbackUrl: "http://127.0.0.1/oauth/done"})
		if err := res.harness.Session.Prompt(ctx, "/mcp login issues"); err != nil {
			t.Fatalf("Prompt(/mcp login issues): %v", err)
		}
		if got := res.lastNotification(); got != `Signed in to MCP server "issues" (1 tools).` {
			t.Fatalf("expected signed in notification, got %q", got)
		}
		re := regexp.MustCompile(`^http://127\.0\.0\.1:\d+/oauth/done$`)
		if got := res.openedURLs()[0].Query().Get("redirect_uri"); !re.MatchString(got) {
			t.Fatalf("expected redirect_uri with listening port, got %q", got)
		}

		port := tsasmoFreePort(t)
		fixed := tsasmoSetup(t, "follow", &McpOAuthConfig{
			CallbackUrl:  "http://127.0.0.1/oauth/done",
			CallbackPort: &port,
		})
		if err := fixed.harness.Session.Prompt(ctx, "/mcp login issues"); err != nil {
			t.Fatalf("fixed Prompt(/mcp login issues): %v", err)
		}
		want := fmt.Sprintf("http://127.0.0.1:%d/oauth/done", port)
		if got := fixed.openedURLs()[0].Query().Get("redirect_uri"); got != want {
			t.Fatalf("expected redirect_uri %q, got %q", want, got)
		}
	})

	t.Run("gives up on pi mcp login after --timeout, also while the authorization server hangs", func(t *testing.T) {
		srv := tmoStartOAuthMcpServer(t)
		agentDir := t.TempDir()
		cfg := jsonx.NewObject()
		serversObj := jsonx.NewObject()
		issuesObj := jsonx.NewObject()
		issuesObj.Set("url", srv.URL)
		serversObj.Set("issues", issuesObj)
		cfg.Set("mcpServers", serversObj)
		raw, err := jsonx.Stringify(cfg)
		if err != nil {
			t.Fatalf("Stringify: %v", err)
		}
		if err := os.WriteFile(filepath.Join(agentDir, "mcp.json"), []byte(raw), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		login := func() (int, []string) {
			var output []string
			code := RunMcpCommand([]string{"login", "issues", "--timeout", "0.5"}, McpCommandOptions{
				Cwd:         agentDir,
				AgentDir:    agentDir,
				Credentials: NewMcpOAuthCredentialStore(core.NewInMemoryAuthStorageBackend()),
				HTTPClient:  srv.httpServer.Client(),
				OpenUrl:     func(string) {},
				Log:         func(line string) { output = append(output, line) },
				Error:       func(line string) { output = append(output, line) },
			})
			return code, output
		}

		code, out := login()
		if code != 1 || len(out) == 0 || !strings.Contains(out[len(out)-1], "was cancelled or not completed within") {
			t.Fatalf("expected timeout failure on unapproved login, code=%d out=%v", code, out)
		}

		srv.StallPath("/.well-known/oauth-authorization-server")
		code, out = login()
		if code != 1 || len(out) == 0 || !strings.Contains(out[len(out)-1], "was cancelled or not completed within") {
			t.Fatalf("expected timeout failure on stalled AS login, code=%d out=%v", code, out)
		}
	})

	t.Run("uses credentials from pi mcp login on the next turn", func(t *testing.T) {
		res := tsasmoSetup(t, "follow", nil)
		agentDir := t.TempDir()
		cfg := jsonx.NewObject()
		serversObj := jsonx.NewObject()
		issuesObj := jsonx.NewObject()
		issuesObj.Set("url", res.server.URL)
		serversObj.Set("issues", issuesObj)
		cfg.Set("mcpServers", serversObj)
		raw, err := jsonx.Stringify(cfg)
		if err != nil {
			t.Fatalf("Stringify: %v", err)
		}
		if err := os.WriteFile(filepath.Join(agentDir, "mcp.json"), []byte(raw), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		var output []string
		code := RunMcpCommand([]string{"login", "issues"}, McpCommandOptions{
			Cwd:         agentDir,
			AgentDir:    agentDir,
			Credentials: NewMcpOAuthCredentialStore(res.backend),
			HTTPClient:  res.server.httpServer.Client(),
			OpenUrl: func(rawUrl string) {
				go func() {
					resp, err := res.server.httpServer.Client().Get(rawUrl)
					if err == nil {
						_ = resp.Body.Close()
					}
				}()
			},
			Log:   func(line string) { output = append(output, line) },
			Error: func(line string) { output = append(output, line) },
		})
		if code != 0 {
			t.Fatalf("expected RunMcpCommand exit 0, got %d (%v)", code, output)
		}
		if len(output) == 0 || output[len(output)-1] != `Signed in to MCP server "issues" (1 tools).` {
			t.Fatalf("unexpected CLI output: %v", output)
		}

		if got := testkit.GetMessageText(tsasmoCallWhoami(t, res.harness)); got != "token access-1" {
			t.Fatalf("expected token access-1 on next turn, got %q", got)
		}
	})
}

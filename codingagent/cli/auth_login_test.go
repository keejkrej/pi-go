// Ported from packages/coding-agent/test/auth-login.test.ts (pi v1.5.2).

package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/js"
)

func talStrPtr(s string) *string { return &s }
func talBoolPtr(b bool) *bool    { return &b }

type talFakeTerminal struct {
	interactive bool
	questions   []string
	secrets     []string
	writes      []string
	errors      []string
	openedUrls  []string
	prompts     []string
}

func (f *talFakeTerminal) Interactive() bool { return f.interactive }
func (f *talFakeTerminal) Write(line string) { f.writes = append(f.writes, line) }
func (f *talFakeTerminal) Error(line string) { f.errors = append(f.errors, line) }
func (f *talFakeTerminal) OpenUrl(u string)  { f.openedUrls = append(f.openedUrls, u) }
func (f *talFakeTerminal) Question(ctx context.Context, msg string) (string, error) {
	if ctx != nil && ctx.Err() != nil {
		return "", &utils.AbortError{Message: "Login cancelled"}
	}
	f.prompts = append(f.prompts, msg)
	if len(f.questions) == 0 {
		return "", &utils.AbortError{Message: "Login cancelled"}
	}
	ans := f.questions[0]
	f.questions = f.questions[1:]
	return ans, nil
}
func (f *talFakeTerminal) Secret(ctx context.Context, msg string) (string, error) {
	if ctx != nil && ctx.Err() != nil {
		return "", &utils.AbortError{Message: "Login cancelled"}
	}
	f.prompts = append(f.prompts, msg)
	if len(f.secrets) == 0 {
		return "", &utils.AbortError{Message: "Login cancelled"}
	}
	ans := f.secrets[0]
	f.secrets = f.secrets[1:]
	return ans, nil
}

func talNewTestRuntime(t *testing.T) (*core.ModelRuntime, ai.CredentialStore) {
	t.Helper()
	t.Setenv("PI_OFFLINE", "1")
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	creds := ai.NewInMemoryCredentialStore()
	rt, err := core.CreateModelRuntime(t.Context(), &core.CreateModelRuntimeOptions{
		Credentials:       creds,
		ModelsPath:        talStrPtr(""),
		ModelsStore:       core.NewInMemoryCodingAgentModelsStore(),
		AllowModelNetwork: false,
		RefreshOnCreate:   talBoolPtr(false),
	})
	if err != nil {
		t.Fatalf("CreateModelRuntime: %v", err)
	}
	t.Cleanup(rt.Close)
	return rt, creds
}

func TestRunAuthLoginValidationAndOAuthBrowserFlows(t *testing.T) {
	rt, creds := talNewTestRuntime(t)

	err := rt.RegisterNativeProvider(&ai.Provider{
		Id:   "mock-oauth",
		Name: "Mock OAuth",
		Auth: ai.ProviderAuth{
			OAuth: &ai.OAuthAuth{
				Name: "Mock OAuth",
				Login: func(ctx context.Context, inter ai.AuthInteraction, _ *ai.LoginOptions) (*ai.OAuthCredential, error) {
					method, err := inter.Prompt(ctx, &ai.AuthPromptSelect{
						Type:    ai.AuthPromptKindSelect,
						Message: "Choose sign-in method",
						Options: []ai.AuthSelectOption{
							{Id: "browser", Label: "Browser OAuth"},
							{Id: "device", Label: "Device Code", Description: talStrPtr("Enter code on another device")},
							{Id: "manual", Label: "Manual Paste"},
						},
					})
					if err != nil {
						return nil, err
					}
					if method == "browser" {
						inter.Notify(&ai.AuthEventAuthUrl{
							Type:         ai.AuthEventKindAuthUrl,
							Url:          "https://auth.example.com/login",
							Instructions: talStrPtr("Finish login in browser."),
						})
					} else if method == "device" {
						inter.Notify(&ai.AuthEventDeviceCode{
							Type:            ai.AuthEventKindDeviceCode,
							UserCode:        "ABCD-1234",
							VerificationUri: "https://auth.example.com/device",
						})
					}
					return &ai.OAuthCredential{
						Type: ai.AuthTypeOAuth,
						OAuthCredentials: ai.OAuthCredentials{
							Refresh: "ref-" + method,
							Access:  "acc-" + method,
							Expires: float64(js.DateNow() + 3_600_000),
						},
					}, nil
				},
			},
		},
		Stream: func(context.Context, *ai.Model, *ai.TranscriptContext, ai.ProviderStreamOptions) *ai.AssistantMessageEventStream {
			panic("not used")
		},
		StreamSimple: func(context.Context, *ai.Model, *ai.TranscriptContext, *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
			panic("not used")
		},
	})
	if err != nil {
		t.Fatalf("RegisterNativeProvider(mock-oauth): %v", err)
	}

	// Missing arg & conflicting flags.
	term := &talFakeTerminal{interactive: true}
	if code := RunAuthLogin(t.Context(), nil, AuthLoginOptions{ModelRuntime: rt, Terminal: term}); code != 1 {
		t.Fatalf("expected exit 1 for empty args, got %d", code)
	}
	term = &talFakeTerminal{interactive: true}
	if code := RunAuthLogin(t.Context(), []string{"mock-oauth", "--api-key", "--no-browser"}, AuthLoginOptions{ModelRuntime: rt, Terminal: term}); code != 1 {
		t.Fatalf("expected exit 1 for --api-key --no-browser, got %d", code)
	}

	// Unknown provider.
	term = &talFakeTerminal{interactive: true}
	if code := RunAuthLogin(t.Context(), []string{"bogus-provider"}, AuthLoginOptions{ModelRuntime: rt, Terminal: term}); code != 1 {
		t.Fatalf("expected exit 1 for unknown provider, got %d", code)
	}
	if !strings.Contains(strings.Join(term.errors, "\n"), "mock-oauth") {
		t.Fatalf("expected provider list to include mock-oauth: %v", term.errors)
	}

	// Non-interactive terminal rejected.
	term = &talFakeTerminal{interactive: false}
	if code := RunAuthLogin(t.Context(), []string{"mock-oauth"}, AuthLoginOptions{ModelRuntime: rt, Terminal: term}); code != 1 {
		t.Fatalf("expected exit 1 on non-interactive terminal, got %d", code)
	}

	// Default browser OAuth login chooses first option, opens browser, mentions skipped options.
	term = &talFakeTerminal{interactive: true}
	if code := RunAuthLogin(t.Context(), []string{"mock-oauth"}, AuthLoginOptions{ModelRuntime: rt, Terminal: term}); code != 0 {
		t.Fatalf("expected exit 0 on browser login, got %d (%v)", code, term.errors)
	}
	if len(term.openedUrls) != 1 || term.openedUrls[0] != "https://auth.example.com/login" {
		t.Fatalf("expected browser URL opened, got %v", term.openedUrls)
	}
	allOut := strings.Join(term.writes, "\n")
	if !strings.Contains(allOut, "Other sign-in methods:") || !strings.Contains(allOut, "Logged in to Mock OAuth") {
		t.Fatalf("unexpected browser login writes:\n%s", allOut)
	}
	stored, err := creds.Read(t.Context(), "mock-oauth", nil)
	if err != nil {
		t.Fatalf("creds.Read: %v", err)
	}
	oc, ok := stored.(*ai.OAuthCredential)
	if !ok || oc.Access != "acc-browser" {
		t.Fatalf("stored credential = %#v, want acc-browser", stored)
	}

	// --no-browser asks user to pick from non-browser options and does NOT open browser.
	term = &talFakeTerminal{
		interactive: true,
		questions:   []string{"invalid", "1"},
	}
	if code := RunAuthLogin(t.Context(), []string{"mock-oauth", "--no-browser"}, AuthLoginOptions{ModelRuntime: rt, Terminal: term}); code != 0 {
		t.Fatalf("expected exit 0 on --no-browser login, got %d (%v)", code, term.errors)
	}
	if len(term.openedUrls) != 0 {
		t.Fatalf("expected no browser opened with --no-browser, got %v", term.openedUrls)
	}
	stored, _ = creds.Read(t.Context(), "mock-oauth", nil)
	if oc, ok = stored.(*ai.OAuthCredential); !ok || oc.Access != "acc-device" {
		t.Fatalf("stored credential = %#v, want acc-device", stored)
	}
}

func TestRunAuthLoginApiKeyAndCancelFlows(t *testing.T) {
	rt, creds := talNewTestRuntime(t)

	apiKeyCfg := ai.EnvApiKeyAuth("API Key", nil)
	apiKeyCfg.Login = func(ctx context.Context, inter ai.AuthInteraction) (*ai.ApiKeyCredential, error) {
		key, err := inter.Prompt(ctx, &ai.AuthPromptSecret{
			Type:    ai.AuthPromptKindSecret,
			Message: "Enter Mock API Key",
		})
		if err != nil {
			return nil, err
		}
		return &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Key: &key}, nil
	}

	err := rt.RegisterNativeProvider(&ai.Provider{
		Id:   "mock-apikey",
		Name: "Mock ApiKey",
		Auth: ai.ProviderAuth{
			ApiKey: apiKeyCfg,
		},
		Stream: func(context.Context, *ai.Model, *ai.TranscriptContext, ai.ProviderStreamOptions) *ai.AssistantMessageEventStream {
			panic("not used")
		},
		StreamSimple: func(context.Context, *ai.Model, *ai.TranscriptContext, *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
			panic("not used")
		},
	})
	if err != nil {
		t.Fatalf("RegisterNativeProvider(mock-apikey): %v", err)
	}

	term := &talFakeTerminal{interactive: true, secrets: []string{"sk-saved-99"}}
	if code := RunAuthLogin(t.Context(), []string{"mock-apikey", "--api-key"}, AuthLoginOptions{ModelRuntime: rt, Terminal: term}); code != 0 {
		t.Fatalf("expected exit 0 saving API key, got %d (%v)", code, term.errors)
	}
	if !strings.Contains(strings.Join(term.writes, "\n"), "Saved API key for Mock ApiKey") {
		t.Fatalf("unexpected API key writes: %v", term.writes)
	}
	stored, _ := creds.Read(t.Context(), "mock-apikey", nil)
	ak, ok := stored.(*ai.ApiKeyCredential)
	if !ok || ak.Key == nil || *ak.Key != "sk-saved-99" {
		t.Fatalf("stored = %#v, want sk-saved-99", stored)
	}

	// Cancel / abort secret prompt -> "Login cancelled." and exit 1.
	term = &talFakeTerminal{interactive: true}
	if code := RunAuthLogin(t.Context(), []string{"mock-apikey"}, AuthLoginOptions{ModelRuntime: rt, Terminal: term}); code != 1 {
		t.Fatalf("expected exit 1 on cancel, got %d", code)
	}
	if !strings.Contains(strings.Join(term.errors, "\n"), "Login cancelled.") {
		t.Fatalf("expected Login cancelled., got %v", term.errors)
	}
}

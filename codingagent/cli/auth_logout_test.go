// Ported from packages/coding-agent/test/auth-logout.test.ts (pi v1.5.2).

package cli

import (
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
)

func taloStrPtr(s string) *string { return &s }
func taloBoolPtr(b bool) *bool    { return &b }

type taloRecordedOutput struct {
	lines  []string
	errors []string
}

func (o *taloRecordedOutput) Write(line string) { o.lines = append(o.lines, line) }
func (o *taloRecordedOutput) Error(line string) { o.errors = append(o.errors, line) }

func TestRunAuthLogoutFlows(t *testing.T) {
	t.Setenv("PI_OFFLINE", "1")
	creds := ai.NewInMemoryCredentialStore()
	rt, err := core.CreateModelRuntime(t.Context(), &core.CreateModelRuntimeOptions{
		Credentials:       creds,
		ModelsPath:        taloStrPtr(""),
		ModelsStore:       core.NewInMemoryCodingAgentModelsStore(),
		AllowModelNetwork: false,
		RefreshOnCreate:   taloBoolPtr(false),
	})
	if err != nil {
		t.Fatalf("CreateModelRuntime: %v", err)
	}
	defer rt.Close()

	// Usage validation errors.
	out := &taloRecordedOutput{}
	if code := RunAuthLogout(t.Context(), nil, AuthLogoutOptions{ModelRuntime: rt, Terminal: out}); code != 1 {
		t.Fatalf("expected exit 1 on missing arg, got %d", code)
	}
	if len(out.errors) == 0 || !strings.Contains(out.errors[0], "Usage:") {
		t.Fatalf("expected Usage error, got %v", out.errors)
	}

	// Unknown provider with no stored credentials.
	out = &taloRecordedOutput{}
	if code := RunAuthLogout(t.Context(), []string{"nonexistent"}, AuthLogoutOptions{ModelRuntime: rt, Terminal: out}); code != 1 {
		t.Fatalf("expected exit 1 for unknown provider, got %d", code)
	}
	if !strings.Contains(strings.Join(out.errors, "\n"), "Stored credentials: none") {
		t.Fatalf("expected Stored credentials: none, got %v", out.errors)
	}

	// Known provider with no stored credentials succeeds with exit 0 and helpful note.
	out = &taloRecordedOutput{}
	if code := RunAuthLogout(t.Context(), []string{"openai"}, AuthLogoutOptions{ModelRuntime: rt, Terminal: out}); code != 0 {
		t.Fatalf("expected exit 0 when no stored creds, got %d (%v)", code, out.errors)
	}
	if len(out.lines) == 0 || !strings.Contains(out.lines[0], "No stored credentials") {
		t.Fatalf("unexpected output lines: %v", out.lines)
	}

	// Remove stored API key.
	_, _ = creds.Modify(t.Context(), "openai", func(ai.Credential) (ai.Credential, error) {
		return &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Key: taloStrPtr("sk-test")}, nil
	}, nil)
	out = &taloRecordedOutput{}
	if code := RunAuthLogout(t.Context(), []string{"openai"}, AuthLogoutOptions{ModelRuntime: rt, Terminal: out}); code != 0 {
		t.Fatalf("expected exit 0 removing API key, got %d (%v)", code, out.errors)
	}
	if len(out.lines) == 0 || !strings.Contains(out.lines[0], "Removed stored API key") {
		t.Fatalf("unexpected logout API key message: %v", out.lines)
	}
	if remaining, _ := creds.Read(t.Context(), "openai", nil); remaining != nil {
		t.Fatalf("expected stored openai key deleted, got %#v", remaining)
	}

	// Remove orphaned OAuth credential (unknown to provider list, present in store).
	_, _ = creds.Modify(t.Context(), "legacy-oauth", func(ai.Credential) (ai.Credential, error) {
		return &ai.OAuthCredential{
			Type:             ai.AuthTypeOAuth,
			OAuthCredentials: ai.OAuthCredentials{Refresh: "r", Access: "a", Expires: 9999999999999},
		}, nil
	}, nil)
	out = &taloRecordedOutput{}
	if code := RunAuthLogout(t.Context(), []string{"legacy-oauth"}, AuthLogoutOptions{ModelRuntime: rt, Terminal: out}); code != 0 {
		t.Fatalf("expected exit 0 logging out orphaned OAuth entry, got %d (%v)", code, out.errors)
	}
	if len(out.lines) == 0 || !strings.Contains(out.lines[0], "Logged out of legacy-oauth.") {
		t.Fatalf("unexpected logout OAuth message: %v", out.lines)
	}
	if remaining, _ := creds.Read(t.Context(), "legacy-oauth", nil); remaining != nil {
		t.Fatalf("expected stored legacy-oauth deleted, got %#v", remaining)
	}
}

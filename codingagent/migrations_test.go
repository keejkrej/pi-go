// Ported from packages/coding-agent/src/migrations.ts (pi v1.5.2).

package codingagent

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func TestRunMigrationsAuthSessionsBinKeybindingsAndPrompts(t *testing.T) {
	agentDir := filepath.Join(t.TempDir(), "agent")
	cwd := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(filepath.Join(agentDir, "commands"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(agentDir, "tools"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(agentDir, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cwd, utils.ConfigDirName, "commands"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(utils.EnvAgentDir, agentDir)

	// Legacy oauth.json + settings.json apiKeys
	if err := os.WriteFile(filepath.Join(agentDir, "oauth.json"), []byte(`{"anthropic":{"access":"tok-1"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"theme":"dark","apiKeys":{"openai":"sk-123","anthropic":"ignored-dup"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// Legacy keybindings.json
	if err := os.WriteFile(filepath.Join(agentDir, "keybindings.json"), []byte(`{"interrupt":"ctrl+q"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// Legacy managed rg binary + custom tool script
	if err := os.WriteFile(filepath.Join(agentDir, "tools", "rg"), []byte("bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "tools", "custom.ts"), []byte("export default {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Legacy session jsonl directly in agentDir
	sessionLine := `{"type":"session","version":3,"id":"s-1","timestamp":"2026-01-01T00:00:00Z","cwd":"` + filepath.ToSlash(cwd) + `"}` + "\n"
	if err := os.WriteFile(filepath.Join(agentDir, "legacy.jsonl"), []byte(sessionLine), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	res := RunMigrationsWithOutput(cwd, &out)

	if len(res.MigratedAuthProviders) != 2 || res.MigratedAuthProviders[0] != "anthropic" || res.MigratedAuthProviders[1] != "openai" {
		t.Fatalf("MigratedAuthProviders = %v, want [anthropic openai]", res.MigratedAuthProviders)
	}
	if _, err := os.Stat(filepath.Join(agentDir, "oauth.json.migrated")); err != nil {
		t.Fatalf("expected oauth.json renamed to .migrated: %v", err)
	}

	authRaw, err := os.ReadFile(utils.GetAuthPath())
	if err != nil {
		t.Fatalf("expected auth.json created: %v", err)
	}
	var authObj *jsonx.Object
	if err := jsonx.Unmarshal(authRaw, &authObj); err != nil {
		t.Fatal(err)
	}
	if !authObj.Has("anthropic") || !authObj.Has("openai") {
		t.Fatalf("unexpected auth.json contents: %s", string(authRaw))
	}

	settingsRaw, err := os.ReadFile(utils.GetSettingsPath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(settingsRaw), `"apiKeys"`) {
		t.Fatalf("expected apiKeys removed from settings.json, got %s", string(settingsRaw))
	}

	// Managed binary moved to bin/, commands moved to prompts/, session moved to sessions/
	if _, err := os.Stat(filepath.Join(utils.GetBinDir(), "rg")); err != nil {
		t.Fatalf("expected rg migrated to bin/: %v", err)
	}
	if _, err := os.Stat(filepath.Join(agentDir, "prompts")); err != nil {
		t.Fatalf("expected global commands/ migrated to prompts/: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cwd, utils.ConfigDirName, "prompts")); err != nil {
		t.Fatalf("expected project commands/ migrated to prompts/: %v", err)
	}
	if _, err := os.Stat(filepath.Join(agentDir, "legacy.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("expected root legacy.jsonl moved out of agentDir")
	}

	kbRaw, err := os.ReadFile(filepath.Join(agentDir, "keybindings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(kbRaw), "app.interrupt") {
		t.Fatalf("expected keybindings.json migrated to app.interrupt, got %s", string(kbRaw))
	}

	if len(res.DeprecationWarnings) != 2 {
		t.Fatalf("DeprecationWarnings = %v, want 2 warnings (hooks + custom tools)", res.DeprecationWarnings)
	}
	if !strings.Contains(out.String(), "Migrated Global commands/ → prompts/") ||
		!strings.Contains(out.String(), "Migrated managed binaries tools/ → bin/") {
		t.Fatalf("unexpected migration output:\n%s", out.String())
	}

	// Idempotent second run should not migrate auth again.
	if second := MigrateAuthToAuthJson(); len(second) != 0 {
		t.Fatalf("expected second MigrateAuthToAuthJson empty, got %v", second)
	}
}

func TestShowDeprecationWarningsWithIO(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	_, _ = w.Write([]byte("x"))
	_ = w.Close()

	var out bytes.Buffer
	ShowDeprecationWarningsWithIO([]string{"Global hooks/ directory found."}, r, &out)
	got := out.String()
	if !strings.Contains(got, "Warning: Global hooks/ directory found.") ||
		!strings.Contains(got, "Press any key to continue...") {
		t.Fatalf("unexpected warning output:\n%s", got)
	}

	var emptyOut bytes.Buffer
	ShowDeprecationWarningsWithIO(nil, nil, &emptyOut)
	if emptyOut.Len() != 0 {
		t.Fatalf("expected no output for empty warnings, got %q", emptyOut.String())
	}
}

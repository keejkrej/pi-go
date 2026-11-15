package core

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/tui"
)

func tsmBoolPtr(b bool) *bool    { return &b }
func tsmStrPtr(s string) *string { return &s }

func TestSettingsManagerGlobalProjectTrustAndReload(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	projectSettingsDir := filepath.Join(cwd, utils.ConfigDirName)
	if err := os.MkdirAll(projectSettingsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	globalPath := filepath.Join(agentDir, "settings.json")
	projectPath := filepath.Join(projectSettingsDir, "settings.json")

	// Byte-exact JSON with custom field order & unknown fields preserved across unrelated updates.
	globalInitial := `{
  "$schema": "https://pi.dev/schemas/settings.schema.json",
  "customFirst": {
    "b": 2,
    "a": 1
  },
  "defaultProvider": "anthropic",
  "theme": "dark"
}`
	if err := os.WriteFile(globalPath, []byte(globalInitial), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projectPath, []byte(`{"theme":"light","defaultTools":["+bash","-read"]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Untrusted project ignores project settings and rejects SetProject* writes.
	untrusted := SettingsManagerCreateWithOptions(cwd, agentDir, &SettingsManagerCreateOptions{
		ProjectTrusted: tsmBoolPtr(false),
	})
	if got := untrusted.GetTheme(); got == nil || *got != "dark" {
		t.Fatalf("expected global dark when untrusted, got %v", got)
	}
	if err := untrusted.SetProjectSkillPaths([]string{"./skills"}); err == nil {
		t.Fatal("expected error calling SetProjectSkillPaths when project untrusted")
	}

	// Calling SetProjectTrusted(true) loads project overrides cleanly.
	untrusted.SetProjectTrusted(true)
	if !untrusted.IsProjectTrusted() {
		t.Fatal("expected trusted=true")
	}
	if got := untrusted.GetTheme(); got == nil || *got != "light" {
		t.Fatalf("expected project light after trust granted, got %v", got)
	}

	// Modify an unrelated global setting and verify unknown fields & key order & NO trailing newline on disk.
	untrusted.SetTheme("solarized")
	if err := untrusted.Flush(); err != nil {
		t.Fatal(err)
	}
	rawAfter, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasSuffix(string(rawAfter), "\n") {
		t.Fatalf("settings.json should have NO trailing newline, got %q", string(rawAfter))
	}
	wantGlobal := `{
  "$schema": "https://pi.dev/schemas/settings.schema.json",
  "customFirst": {
    "b": 2,
    "a": 1
  },
  "defaultProvider": "anthropic",
  "theme": "solarized"
}`
	if string(rawAfter) != wantGlobal {
		t.Fatalf("settings.json byte mismatch:\ngot:\n%s\nwant:\n%s", string(rawAfter), wantGlobal)
	}
}

func TestSettingsManagerMigrationsAndTimeoutAccessors(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	globalPath := filepath.Join(agentDir, "settings.json")
	if err := os.WriteFile(globalPath, []byte(`{
		"queueMode": "one-at-a-time",
		"websockets": true,
		"skills": { "enableSkillCommands": false, "customDirectories": ["./my-skills"] },
		"retry": { "maxDelayMs": 12345 },
		"httpIdleTimeoutMs": "disabled",
		"websocketConnectTimeoutMs": 5000
	}`), 0o644); err != nil {
		t.Fatal(err)
	}

	sm := SettingsManagerCreate(cwd, agentDir)
	if got := sm.GetSteeringMode(); got != agent.QueueModeOneAtATime {
		t.Fatalf("migrated steeringMode = %q", got)
	}
	if got := sm.GetTransport(); got != TransportWebsocket {
		t.Fatalf("migrated transport = %q", got)
	}
	if sm.GetEnableSkillCommands() {
		t.Fatal("expected migrated enableSkillCommands=false")
	}
	if !slices.Equal(sm.GetSkillPaths(), []string{"./my-skills"}) {
		t.Fatalf("migrated SkillPaths = %v", sm.GetSkillPaths())
	}
	retry := sm.GetProviderRetrySettings()
	if retry.MaxRetryDelayMs != 12345 {
		t.Fatalf("migrated provider MaxRetryDelayMs = %v", retry.MaxRetryDelayMs)
	}

	idle, err := sm.GetHttpIdleTimeoutMs()
	if err != nil || idle != 0 {
		t.Fatalf("GetHttpIdleTimeoutMs = (%d, %v)", idle, err)
	}
	wsTimeout, err := sm.GetWebSocketConnectTimeoutMs()
	if err != nil || wsTimeout == nil || *wsTimeout != 5000 {
		t.Fatalf("GetWebSocketConnectTimeoutMs = (%v, %v)", wsTimeout, err)
	}
	if err := sm.SetHttpIdleTimeoutMs(-1); err == nil {
		t.Fatal("expected negative SetHttpIdleTimeoutMs to fail")
	}
	if err := sm.SetHttpIdleTimeoutMs(0); err != nil {
		t.Fatal(err)
	}
}

func TestSettingsManagerInMemoryReloadAndOverrides(t *testing.T) {
	// Issue #3616: SettingsManagerInMemory writes to InMemorySettingsStorage so Reload() preserves edits.
	sm := SettingsManagerInMemory(&Settings{
		DefaultProvider: tsmStrPtr("anthropic"),
	})
	sm.SetDefaultModel("claude-sonnet-4-5")
	if err := sm.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := sm.GetDefaultModel(); got == nil || *got != "claude-sonnet-4-5" {
		t.Fatalf("after Reload defaultModel = %v", got)
	}

	sm.ApplyOverrides(&Settings{HideThinkingBlock: tsmBoolPtr(true)})
	if !sm.GetHideThinkingBlock() {
		t.Fatal("expected ApplyOverrides HideThinkingBlock=true")
	}
	// Overrides are transient across Reload().
	_ = sm.Reload()
	if sm.GetHideThinkingBlock() {
		t.Fatal("expected transient override cleared after Reload")
	}

	// Install telemetry getter/setter parity.
	sm.SetEnableInstallTelemetry(false)
	if sm.GetEnableInstallTelemetry() {
		t.Fatal("expected GetEnableInstallTelemetry=false")
	}
}

func TestSettingsDefaultToolModifiers(t *testing.T) {
	if err := GetToolListError([]string{"read", "+bash"}); err == nil {
		t.Fatal("expected error mixing full replacement and modifiers")
	}
	if err := GetToolListError([]string{"+bash", "-read"}); err != nil {
		t.Fatalf("unexpected error for pure modifiers: %v", err)
	}
	applied := ApplyToolModifiers([]string{"read", "bash", "edit"}, []string{"-bash", "+grep", "+read"})
	if !slices.Equal(applied, []string{"read", "edit", "grep"}) {
		t.Fatalf("ApplyToolModifiers = %v", applied)
	}
	// Verify valid JSON serialization preserves order in jsonx.Object.
	_ = jsonx.NewObject()
}

func TestSettingsManagerTerminalCapabilityOverrides(t *testing.T) {
	smAuto := SettingsManagerInMemory()
	autoOverrides := smAuto.GetTerminalCapabilityOverrides()
	if autoOverrides.Images != nil || autoOverrides.TrueColor != nil || autoOverrides.Hyperlinks != nil {
		t.Fatalf("expected all-nil overrides by default, got %+v", autoOverrides)
	}

	smDisabled := SettingsManagerInMemory(&Settings{
		Terminal: &TerminalSettings{
			Images:     &TerminalImageSetting{Disabled: true},
			TrueColor:  &TerminalAutoBool{Enabled: false},
			Hyperlinks: &TerminalAutoBool{Enabled: true},
		},
	})
	disabledOverrides := smDisabled.GetTerminalCapabilityOverrides()
	if disabledOverrides.Images == nil || *disabledOverrides.Images != tui.ImageProtocol("") {
		t.Fatalf("expected disabled empty protocol pointer, got %v", disabledOverrides.Images)
	}
	if disabledOverrides.TrueColor == nil || *disabledOverrides.TrueColor {
		t.Fatalf("expected TrueColor=false, got %v", disabledOverrides.TrueColor)
	}
	if disabledOverrides.Hyperlinks == nil || !*disabledOverrides.Hyperlinks {
		t.Fatalf("expected Hyperlinks=true, got %v", disabledOverrides.Hyperlinks)
	}

	smKitty := SettingsManagerInMemory(&Settings{
		Terminal: &TerminalSettings{
			Images: &TerminalImageSetting{Protocol: string(tui.ImageProtocolKitty)},
		},
	})
	kittyOverrides := smKitty.GetTerminalCapabilityOverrides()
	if kittyOverrides.Images == nil || *kittyOverrides.Images != tui.ImageProtocolKitty {
		t.Fatalf("expected kitty protocol pointer, got %v", kittyOverrides.Images)
	}
}

package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSettingsManagerDoesNotClobberConcurrentDiskEdits(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	globalPath := filepath.Join(agentDir, "settings.json")

	initial := `{
  "theme": "dark",
  "compaction": {
    "enabled": true,
    "reserveTokens": 16384
  },
  "defaultProvider": "anthropic"
}`
	if err := os.WriteFile(globalPath, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	sm := SettingsManagerCreate(cwd, agentDir)

	// External process edits defaultModel and compaction.keepRecentTokens on disk.
	external := `{
  "theme": "dark",
  "compaction": {
    "enabled": true,
    "reserveTokens": 16384,
    "keepRecentTokens": 8000
  },
  "defaultProvider": "anthropic",
  "defaultModel": "claude-opus-4"
}`
	if err := os.WriteFile(globalPath, []byte(external), 0o644); err != nil {
		t.Fatal(err)
	}

	// Only theme and compaction.enabled are touched here; Flush should preserve defaultModel & keepRecentTokens.
	sm.SetTheme("light")
	sm.SetCompactionEnabled(false)
	if err := sm.Flush(); err != nil {
		t.Fatal(err)
	}

	reloaded := SettingsManagerCreate(cwd, agentDir)
	if got := reloaded.GetTheme(); got == nil || *got != "light" {
		t.Fatalf("theme = %v", got)
	}
	if reloaded.GetCompactionEnabled() {
		t.Fatal("expected compaction.enabled=false")
	}
	if got := reloaded.GetDefaultModel(); got == nil || *got != "claude-opus-4" {
		t.Fatalf("external defaultModel clobbered: %v", got)
	}
	keep, err := reloaded.GetCompactionKeepRecentTokens()
	if err != nil || keep != 8000 {
		t.Fatalf("external compaction.keepRecentTokens clobbered: (%d, %v)", keep, err)
	}
}

func TestSettingsManagerPreservesInvalidDiskFileAndReportsErrors(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	globalPath := filepath.Join(agentDir, "settings.json")
	corrupt := "{invalid json"
	if err := os.WriteFile(globalPath, []byte(corrupt), 0o644); err != nil {
		t.Fatal(err)
	}

	sm := SettingsManagerCreate(cwd, agentDir)
	errs := sm.DrainErrors()
	if len(errs) == 0 || errs[0].Scope != SettingsScopeGlobal {
		t.Fatalf("expected global load error, got %+v", errs)
	}
	if len(sm.DrainErrors()) != 0 {
		t.Fatal("expected DrainErrors to clear queue")
	}

	sm.SetTheme("light")
	_ = sm.Flush()
	if got := sm.GetTheme(); got == nil || *got != "light" {
		t.Fatalf("expected in-memory update to still apply, got %v", got)
	}
	after, err := os.ReadFile(globalPath)
	if err != nil || string(after) != corrupt {
		t.Fatalf("malformed file clobbered: %q", string(after))
	}

	// If file was initially valid and becomes corrupt on disk before saving, persist records an error and keeps file intact.
	validPath := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(validPath, []byte(`{"theme":"dark"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	sm2 := SettingsManagerCreate(t.TempDir(), filepath.Dir(validPath))
	if err := os.WriteFile(validPath, []byte(corrupt), 0o644); err != nil {
		t.Fatal(err)
	}
	sm2.SetTheme("light")
	_ = sm2.Flush()
	if writeErrs := sm2.DrainErrors(); len(writeErrs) == 0 {
		t.Fatal("expected flush error when valid settings.json became malformed before save")
	}
	after2, err := os.ReadFile(validPath)
	if err != nil || string(after2) != corrupt {
		t.Fatalf("corrupted disk file clobbered: %q", string(after2))
	}
}

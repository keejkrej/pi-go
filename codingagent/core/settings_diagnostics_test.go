package core

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type tsdFailingSettingsStorage struct {
	failScope SettingsScope
	err       error
}

func (s tsdFailingSettingsStorage) WithLock(scope SettingsScope, fn func(current *string) (next *string, err error)) error {
	if scope == s.failScope {
		return s.err
	}
	_, err := fn(nil)
	return err
}

func TestCollectSettingsDiagnosticsIncludesPathForFileStorage(t *testing.T) {
	tempDir := t.TempDir()
	agentDir := filepath.Join(tempDir, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(agentDir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}

	sm := SettingsManagerCreate(tempDir, agentDir)
	diags := CollectSettingsDiagnostics(sm)
	if len(diags) != 1 {
		t.Fatalf("len(diags) = %d, want 1 (%+v)", len(diags), diags)
	}
	if diags[0].Type != "warning" {
		t.Fatalf("diags[0].Type = %q, want warning", diags[0].Type)
	}
	wantPrefix := "Invalid settings file " + settingsPath + ":"
	if !strings.Contains(diags[0].Message, wantPrefix) {
		t.Fatalf("diags[0].Message = %q, want prefix %q", diags[0].Message, wantPrefix)
	}
	if again := CollectSettingsDiagnostics(sm); len(again) != 0 {
		t.Fatalf("expected drained diagnostics on second call, got %+v", again)
	}
}

func TestCollectSettingsDiagnosticsFallsBackToScopeWithoutPath(t *testing.T) {
	storage := tsdFailingSettingsStorage{
		failScope: SettingsScopeGlobal,
		err:       errors.New("backend failed"),
	}
	diags := CollectSettingsDiagnostics(SettingsManagerFromStorage(storage))
	want := []AgentSessionRuntimeDiagnostic{
		{Type: "warning", Message: "Invalid global settings: backend failed"},
	}
	if !reflect.DeepEqual(diags, want) {
		t.Fatalf("diags = %+v, want %+v", diags, want)
	}
}

func TestDeduplicateDiagnostics(t *testing.T) {
	warning := AgentSessionRuntimeDiagnostic{
		Type:    "warning",
		Message: "Invalid settings file /tmp/settings.json",
	}
	errDiag := AgentSessionRuntimeDiagnostic{
		Type:    "error",
		Message: warning.Message,
	}
	otherWarning := AgentSessionRuntimeDiagnostic{
		Type:    "warning",
		Message: "Other warning",
	}

	got := DeduplicateDiagnostics([]AgentSessionRuntimeDiagnostic{warning, warning, errDiag, otherWarning, errDiag})
	want := []AgentSessionRuntimeDiagnostic{warning, errDiag, otherWarning}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DeduplicateDiagnostics = %+v, want %+v", got, want)
	}
}

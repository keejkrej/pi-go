package core

import "testing"

func TestIsInstallTelemetryEnabled(t *testing.T) {
	sm := SettingsManagerInMemory()

	// Default setting is true when PI_TELEMETRY is unset.
	t.Setenv("PI_TELEMETRY", "")
	if !IsInstallTelemetryEnabled(sm, nil) {
		t.Fatal("expected true by default with nil env arg")
	}

	// Explicit env var cases matching JS boolean env flag behavior.
	for _, val := range []string{"1", "true", "TRUE", "yes", "YeS"} {
		v := val
		if !IsInstallTelemetryEnabled(sm, &v) {
			t.Fatalf("expected true for %q", val)
		}
	}
	for _, val := range []string{"0", "false", "no", "", "2"} {
		v := val
		if IsInstallTelemetryEnabled(sm, &v) {
			t.Fatalf("expected false for %q", val)
		}
	}

	// Process env fallback when variadic override is omitted.
	t.Setenv("PI_TELEMETRY", "0")
	if IsInstallTelemetryEnabled(sm) {
		t.Fatal("expected false when PI_TELEMETRY=0")
	}

	// Disabling via SettingsManager applies when PI_TELEMETRY is unset.
	smDisabled := SettingsManagerInMemory(&Settings{EnableInstallTelemetry: tteleBoolPtr(false)})
	if IsInstallTelemetryEnabled(smDisabled, nil) {
		t.Fatal("expected false when settings enableInstallTelemetry=false")
	}
}

func tteleBoolPtr(b bool) *bool { return &b }

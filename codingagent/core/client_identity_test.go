package core

import (
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func tciBoolPtr(b bool) *bool { return &b }

func TestGetClientIdentity(t *testing.T) {
	t.Run("telemetry enabled includes PiAttribution", func(t *testing.T) {
		sm := SettingsManagerInMemory(&Settings{EnableInstallTelemetry: tciBoolPtr(true)})
		id := GetClientIdentity(sm)
		if id.UserAgent.Val != ai.UserAgentModeDetailed {
			t.Fatalf("UserAgent = %v", id.UserAgent)
		}
		if id.Version == nil || *id.Version != utils.VERSION {
			t.Fatalf("Version = %v, want %q", id.Version, utils.VERSION)
		}
		if !id.Attribution.IsSome() || id.Attribution.IsNull() {
			t.Fatalf("expected non-null present Attribution, got %+v", id.Attribution)
		}
	})

	t.Run("telemetry disabled emits null Attribution", func(t *testing.T) {
		sm := SettingsManagerInMemory(&Settings{EnableInstallTelemetry: tciBoolPtr(false)})
		t.Setenv("PI_TELEMETRY", "0")
		id := GetClientIdentity(sm)
		if !id.Attribution.IsNull() {
			t.Fatalf("expected Null Attribution when telemetry disabled, got %+v", id.Attribution)
		}
		encoded, err := jsonx.Stringify(id)
		if err != nil {
			t.Fatal(err)
		}
		if encoded == "" {
			t.Fatal("unexpected empty JSON")
		}
	})
}

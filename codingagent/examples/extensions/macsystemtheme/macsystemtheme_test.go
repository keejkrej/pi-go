package macsystemtheme

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/keejkrej/pi-go/codingagent/core"
)

func TestMacSystemThemeExampleTS(t *testing.T) {
	ctx := context.Background()

	res := core.LoadExtensions(ctx, []string{filepath.Join(".", "mac-system-theme.ts")}, ".")
	if len(res.Errors) != 0 {
		t.Fatalf("LoadExtensions(mac-system-theme.ts) errors: %+v", res.Errors)
	}
	runner := core.NewExtensionRunner(res.Extensions, res.Runtime, ".", nil, nil)
	if !runner.HasHandlers("session_start") || !runner.HasHandlers("session_shutdown") {
		t.Fatalf("expected session_start and session_shutdown handlers")
	}
	runner.Emit(ctx, core.SessionShutdownEvent{Type: "session_shutdown"})
	runner.Shutdown()
}

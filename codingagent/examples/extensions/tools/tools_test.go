package tools

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/keejkrej/pi-go/codingagent/core"
)

func TestToolsExampleTS(t *testing.T) {
	ctx := context.Background()

	res := core.LoadExtensions(ctx, []string{filepath.Join(".", "tools.ts")}, ".")
	if len(res.Errors) != 0 {
		t.Fatalf("LoadExtensions(tools.ts) errors: %+v", res.Errors)
	}
	res.Runtime.GetAllTools = func() []core.ToolInfo {
		return []core.ToolInfo{{Name: "read"}, {Name: "bash"}}
	}
	res.Runtime.GetActiveTools = func() []string { return []string{"read"} }

	runner := core.NewExtensionRunner(res.Extensions, res.Runtime, ".", nil, nil)
	defer runner.Shutdown()

	if cmd := runner.GetCommand("tools"); cmd == nil {
		t.Fatalf("expected /tools command registered from tools.ts")
	}
	runner.Emit(ctx, core.SessionStartEvent{Type: "session_start"})
}

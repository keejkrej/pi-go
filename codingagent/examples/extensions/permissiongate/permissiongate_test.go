package permissiongate

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func TestPermissionGateTSAndGo(t *testing.T) {
	ctx := context.Background()

	res := core.LoadExtensions(ctx, []string{filepath.Join(".", "permission-gate.ts")}, ".")
	if len(res.Errors) != 0 {
		t.Fatalf("LoadExtensions(permission-gate.ts) errors: %+v", res.Errors)
	}
	tsRunner := core.NewExtensionRunner(res.Extensions, res.Runtime, ".", nil, nil)
	defer tsRunner.Shutdown()

	blocked := tsRunner.EmitToolCall(ctx, core.ToolCallEvent{
		Type:     "tool_call",
		ToolName: "bash",
		Input:    jsonx.ObjectOf("command", "rm -rf /tmp/foo"),
	})
	if blocked == nil || blocked.Block == nil || !*blocked.Block {
		t.Fatalf("expected rm -rf blocked in headless mode, got %+v", blocked)
	}
	allowed := tsRunner.EmitToolCall(ctx, core.ToolCallEvent{
		Type:     "tool_call",
		ToolName: "bash",
		Input:    jsonx.ObjectOf("command", "ls -la"),
	})
	if allowed != nil && allowed.Block != nil && *allowed.Block {
		t.Fatalf("expected ls -la allowed, got %+v", allowed)
	}

	goExt, err := core.LoadExtensionFromFactory(ctx, core.InlineExtension{Name: "pg-go", Factory: Extension}, ".")
	if err != nil {
		t.Fatalf("LoadExtensionFromFactory failed: %v", err)
	}
	goRunner := core.NewExtensionRunner([]core.Extension{*goExt}, nil, ".", nil, nil)
	defer goRunner.Shutdown()
	goBlocked := goRunner.EmitToolCall(ctx, core.ToolCallEvent{
		Type:     "tool_call",
		ToolName: "bash",
		Input:    jsonx.ObjectOf("command", "sudo id"),
	})
	if goBlocked == nil || goBlocked.Block == nil || !*goBlocked.Block {
		t.Fatalf("expected sudo blocked by Go Extension, got %+v", goBlocked)
	}
}

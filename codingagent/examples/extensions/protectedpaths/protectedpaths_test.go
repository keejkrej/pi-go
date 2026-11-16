package protectedpaths

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func TestProtectedPathsTSAndGo(t *testing.T) {
	ctx := context.Background()

	res := core.LoadExtensions(ctx, []string{filepath.Join(".", "protected-paths.ts")}, ".")
	if len(res.Errors) != 0 {
		t.Fatalf("LoadExtensions(protected-paths.ts) errors: %+v", res.Errors)
	}
	runner := core.NewExtensionRunner(res.Extensions, res.Runtime, ".", nil, nil)
	defer runner.Shutdown()

	blocked := runner.EmitToolCall(ctx, core.ToolCallEvent{
		Type:     "tool_call",
		ToolName: "write",
		Input:    jsonx.ObjectOf("path", ".env"),
	})
	if blocked == nil || blocked.Block == nil || !*blocked.Block {
		t.Fatalf("expected .env write blocked, got %+v", blocked)
	}
	allowed := runner.EmitToolCall(ctx, core.ToolCallEvent{
		Type:     "tool_call",
		ToolName: "write",
		Input:    jsonx.ObjectOf("path", "src/main.go"),
	})
	if allowed != nil && allowed.Block != nil && *allowed.Block {
		t.Fatalf("expected src/main.go allowed, got %+v", allowed)
	}
}

package dynamictools

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func TestDynamicToolsTS(t *testing.T) {
	ctx := context.Background()

	res := core.LoadExtensions(ctx, []string{filepath.Join(".", "dynamic-tools.ts")}, ".")
	if len(res.Errors) != 0 {
		t.Fatalf("LoadExtensions(dynamic-tools.ts) errors: %+v", res.Errors)
	}
	var refreshed int
	res.Runtime.RefreshTools = func() { refreshed++ }

	runner := core.NewExtensionRunner(res.Extensions, res.Runtime, ".", nil, nil)
	defer runner.Shutdown()

	runner.Emit(ctx, core.SessionStartEvent{Type: "session_start"})
	def := runner.GetToolDefinition("echo_session")
	if def == nil {
		t.Fatalf("expected echo_session registered on session_start")
	}
	if refreshed == 0 {
		t.Fatalf("expected RefreshTools invoked when dynamic tool registered after load")
	}
	cmd := runner.GetCommand("add-echo-tool")
	if cmd == nil {
		t.Fatalf("expected /add-echo-tool command")
	}
	if err := cmd.Handler(ctx, "echo_extra", runner.CreateCommandContext()); err != nil {
		t.Fatalf("command failed: %v", err)
	}
	extraDef := runner.GetToolDefinition("echo_extra")
	if extraDef == nil {
		t.Fatalf("expected echo_extra registered at runtime")
	}
	out, err := extraDef.Execute(ctx, "c1", jsonx.ObjectOf("message", "hi"), nil, runner.CreateToolContext("c1", ctx))
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if out.Content[0].(*ai.TextContent).Text != "[echo_extra] hi" {
		t.Fatalf("unexpected output: %+v", out)
	}
}

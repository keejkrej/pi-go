package planmode

import (
	"context"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/tui"
)

func TestPlanModeExampleTS(t *testing.T) {
	ctx := context.Background()

	res := core.LoadExtensions(ctx, []string{"."}, ".")
	if len(res.Errors) != 0 {
		t.Fatalf("LoadExtensions(.) errors: %+v", res.Errors)
	}
	var activeTools []string
	res.Runtime.GetActiveTools = func() []string { return []string{"read", "bash", "edit", "write"} }
	res.Runtime.SetActiveTools = func(names []string) { activeTools = append([]string(nil), names...) }

	runner := core.NewExtensionRunner(res.Extensions, res.Runtime, ".", nil, nil)
	defer runner.Shutdown()

	if sc := runner.GetShortcuts(nil); sc[tui.KeyId("ctrl+alt+p")].Handler == nil {
		t.Fatalf("expected ctrl+alt+p shortcut registered from planmode, got keys=%v", sc)
	}
	cmd := runner.GetCommand("plan")
	if cmd == nil {
		t.Fatalf("expected /plan command")
	}
	if err := cmd.Handler(ctx, "", runner.CreateCommandContext()); err != nil {
		t.Fatalf("/plan failed: %v", err)
	}
	if len(activeTools) == 0 {
		t.Fatalf("expected SetActiveTools called when entering plan mode")
	}

	// Destructive bash command blocked; read-only ls command allowed.
	blocked := runner.EmitToolCall(ctx, core.ToolCallEvent{
		Type:     "tool_call",
		ToolName: "bash",
		Input:    jsonx.ObjectOf("command", "rm -rf build"),
	})
	if blocked == nil || blocked.Block == nil || !*blocked.Block {
		t.Fatalf("expected destructive bash blocked in plan mode, got %+v", blocked)
	}
	allowed := runner.EmitToolCall(ctx, core.ToolCallEvent{
		Type:     "tool_call",
		ToolName: "bash",
		Input:    jsonx.ObjectOf("command", "ls -la"),
	})
	if allowed != nil && allowed.Block != nil && *allowed.Block {
		t.Fatalf("expected safe ls allowed in plan mode, got %+v", allowed)
	}

	base := "base"
	bas := runner.EmitBeforeAgentStart(ctx, core.BeforeAgentStartTrigger{MessageType: "user", Prompt: "plan"}, core.BuildSystemPromptOptions{
		CustomPrompt: &base,
	})
	if len(bas.Messages) == 0 || !strings.Contains(bas.Messages[0].CustomType, "plan-mode") {
		t.Fatalf("expected plan-mode-context injected on before_agent_start, got %+v", bas.Messages)
	}
}

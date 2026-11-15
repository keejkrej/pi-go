package commands

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/keejkrej/pi-go/codingagent/core"
)

func TestCommandsExampleTS(t *testing.T) {
	ctx := context.Background()

	res := core.LoadExtensions(ctx, []string{filepath.Join(".", "commands.ts")}, ".")
	if len(res.Errors) != 0 {
		t.Fatalf("LoadExtensions(commands.ts) errors: %+v", res.Errors)
	}
	res.Runtime.GetCommands = func() []core.SlashCommandInfo {
		return []core.SlashCommandInfo{
			{Name: "commands", Source: core.SlashCommandSourceExtension},
			{Name: "my-prompt", Source: core.SlashCommandSourcePrompt},
		}
	}
	runner := core.NewExtensionRunner(res.Extensions, res.Runtime, ".", nil, nil)
	defer runner.Shutdown()

	cmd := runner.GetCommand("commands")
	if cmd == nil {
		t.Fatalf("/commands not registered")
	}
	items, err := cmd.GetArgumentCompletions(ctx, "ex")
	if err != nil || len(items) != 1 || items[0].Value != "extension" {
		t.Fatalf("unexpected completions for 'ex': %+v err=%v", items, err)
	}
	if err := cmd.Handler(ctx, "extension", runner.CreateCommandContext()); err != nil {
		t.Fatalf("handler failed: %v", err)
	}
}

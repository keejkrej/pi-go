package pirate

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/codingagent/core"
)

func TestPirateExampleTSAndGo(t *testing.T) {
	ctx := context.Background()

	res := core.LoadExtensions(ctx, []string{filepath.Join(".", "pirate.ts")}, ".")
	if len(res.Errors) != 0 {
		t.Fatalf("LoadExtensions(pirate.ts) errors: %+v", res.Errors)
	}
	runner := core.NewExtensionRunner(res.Extensions, res.Runtime, ".", nil, nil)
	defer runner.Shutdown()

	cmd := runner.GetCommand("pirate")
	if cmd == nil {
		t.Fatalf("/pirate command not found")
	}
	base := "base system prompt"
	if err := cmd.Handler(ctx, "", runner.CreateCommandContext()); err != nil {
		t.Fatalf("command failed: %v", err)
	}
	out := runner.EmitBeforeAgentStart(ctx, core.BeforeAgentStartTrigger{MessageType: "user", Prompt: "hi"}, core.BuildSystemPromptOptions{
		CustomPrompt: &base,
	})
	if out.SystemPromptOptions.ForceSystemPrompt == nil || !strings.Contains(*out.SystemPromptOptions.ForceSystemPrompt, "PIRATE MODE") {
		t.Fatalf("expected PIRATE MODE in system prompt, got %+v", out.SystemPromptOptions)
	}
}

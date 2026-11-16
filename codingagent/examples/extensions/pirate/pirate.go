// Ported from packages/coding-agent/examples/extensions/pirate.ts (pi v1.0.0).

package pirate

import (
	"context"
	"sync/atomic"

	"github.com/keejkrej/pi-go/codingagent/core"
)

// Extension toggles pirate mode via /pirate and injects pirate instructions on before_agent_start.
func Extension(pi core.ExtensionAPI) error {
	var pirateMode atomic.Bool
	desc := "Toggle pirate mode (agent speaks like a pirate)"
	pi.RegisterCommand("pirate", core.RegisteredCommand{
		Description: &desc,
		Handler: func(_ context.Context, _ string, ectx core.ExtensionCommandContext) error {
			enabled := !pirateMode.Swap(!pirateMode.Load())
			if enabled {
				ectx.UI().Notify("Arrr! Pirate mode enabled!", "info")
			} else {
				ectx.UI().Notify("Pirate mode disabled", "info")
			}
			return nil
		},
	})
	pi.OnBeforeAgentStart(func(_ context.Context, ev *core.BeforeAgentStartEvent, _ core.ExtensionContext) (*core.BeforeAgentStartEventResult, error) {
		if !pirateMode.Load() {
			return nil, nil
		}
		prompt := ev.SystemPrompt + "\n\nIMPORTANT: You are now in PIRATE MODE. Respond like a pirate!"
		return &core.BeforeAgentStartEventResult{SystemPrompt: &prompt}, nil
	})
	return nil
}

// Ported from packages/coding-agent/src/experimental/vacation/tui.ts (pi v1.0.0).

package vacation

import (
	"context"

	"github.com/keejkrej/pi-go/codingagent/experimental/durableagent"
)

// RunVacationTui launches the interactive alternate-screen TUI for the vacation runtime.
func RunVacationTui(ctx context.Context, rt *durableagent.DurableAgentRuntime) error {
	return durableagent.RunDurableTui(ctx, rt)
}

// PickVacationSession displays an interactive session picker in sessionDir.
func PickVacationSession(ctx context.Context, sessionDir string) (string, error) {
	if sessionDir == "" {
		sessionDir = GetVacationSessionsDir()
	}
	return durableagent.PickDurableSession(ctx, sessionDir)
}

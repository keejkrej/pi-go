// Ported from packages/coding-agent/src/cli/setup.ts (pi v1.5.2).

package cli

import (
	"os"

	"github.com/keejkrej/pi-go/codingagent/core"
)

// SetupCli sets process environment flags and initializes the HTTP dispatcher
// before provider requests run.
func SetupCli() {
	_ = os.Setenv("PI_CODING_AGENT", "true")
	_ = os.Setenv("AI_AGENT", "pi")

	// Settings overrides are applied once SettingsManager has loaded global/project config.
	_ = core.ConfigureHttpDispatcher()
}

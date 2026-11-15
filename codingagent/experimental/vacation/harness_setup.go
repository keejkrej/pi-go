// Ported from packages/coding-agent/src/experimental/vacation/harness-setup.ts (pi v1.0.0).

package vacation

import (
	"context"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/codingagent/experimental/durableagent"
	"github.com/keejkrej/pi-go/durable"
)

// VacationHarnessOptions configures OpenVacationHarness.
type VacationHarnessOptions struct {
	Cwd               string
	AgentDir          string
	Storage           durable.Storage
	Provider          *string
	Model             *string
	Thinking          *agent.ThinkingLevel
	Instructions      *string
	AllowModelNetwork bool
	Extensions        []*durable.Extension
	ShellPath         *string
	OnReport          func(error)
}

// OpenVacationHarness initializes a durable agent harness installed with the vacation durable-sleep extension.
func OpenVacationHarness(ctx context.Context, options VacationHarnessOptions) (*durableagent.DurableHarnessBundle, error) {
	vacaExt := CreateVacationExtension()
	exts := make([]*durable.Extension, 0, len(options.Extensions)+1)
	exts = append(exts, vacaExt)
	exts = append(exts, options.Extensions...)

	return durableagent.OpenDurableHarness(ctx, durableagent.HarnessSetupOptions{
		Cwd:               options.Cwd,
		AgentDir:          options.AgentDir,
		Storage:           options.Storage,
		Provider:          options.Provider,
		Model:             options.Model,
		Thinking:          options.Thinking,
		Instructions:      options.Instructions,
		AllowModelNetwork: options.AllowModelNetwork,
		Extensions:        exts,
		ShellPath:         options.ShellPath,
		OnReport:          options.OnReport,
	})
}

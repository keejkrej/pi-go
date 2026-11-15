// Ported from packages/coding-agent/src/experimental/vacation/runtime.ts (pi v1.0.0).

package vacation

import (
	"context"
	"io"

	"github.com/keejkrej/pi-go/codingagent/experimental/durableagent"
	"github.com/keejkrej/pi-go/durable"
)

// VacationCliOptions aliases durableagent.DurableAgentCliOptions.
type VacationCliOptions = durableagent.DurableAgentCliOptions

// ParseVacationArgs parses CLI flags for the vacation command.
func ParseVacationArgs(args []string) (VacationCliOptions, error) {
	opts, err := durableagent.ParseDurableAgentArgs(args)
	if err != nil {
		return opts, err
	}
	if opts.SessionDir == "" {
		opts.SessionDir = GetVacationSessionsDir(opts.AgentDir)
	}
	return opts, nil
}

// StartVacationRuntime opens or creates a vacation session runtime with the vacation extension installed.
func StartVacationRuntime(ctx context.Context, opts VacationCliOptions, extraExtensions ...*durable.Extension) (*durableagent.DurableAgentRuntime, error) {
	if opts.SessionDir == "" {
		opts.SessionDir = GetVacationSessionsDir(opts.AgentDir)
	}
	exts := make([]*durable.Extension, 0, len(extraExtensions)+1)
	exts = append(exts, CreateVacationExtension())
	exts = append(exts, extraExtensions...)
	return durableagent.StartDurableAgentRuntime(ctx, opts, exts...)
}

// RunVacationPrint runs one prompt non-interactively using the vacation runtime.
func RunVacationPrint(ctx context.Context, rt *durableagent.DurableAgentRuntime, prompt string, out io.Writer) error {
	return durableagent.RunDurableAgentPrint(ctx, rt, prompt, out)
}

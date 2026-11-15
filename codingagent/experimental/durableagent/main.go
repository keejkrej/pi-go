// Ported from packages/coding-agent/src/experimental/durable/main.ts (pi v1.0.0).

package durableagent

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
)

// Usage returns CLI usage help text for the standalone durable agent.
func Usage() string {
	return `Usage: durable-pi [options] [prompt...]

Options:
  -p, --print              Run one prompt non-interactively and exit
  -c, --continue           Resume most recent unlocked durable session
  -r, --resume             Select a durable session interactively
      --session <path>     Open or create a specific session database file
      --session-dir <dir>  Override session directory
      --no-session         Run in-memory without persisting a session DB
      --provider <id>      Model provider ID
      --model <id>         Model ID
      --thinking <level>   Thinking level (minimal, low, medium, high, xhigh)
      --cwd <dir>          Working directory
      --instructions <txt> Append system instructions
  -h, --help               Show this help message
`
}

// Run executes the durableagent entry point with arguments and stdio streams.
func Run(ctx context.Context, args []string, out io.Writer, errOut io.Writer) error {
	if out == nil {
		out = os.Stdout
	}
	if errOut == nil {
		errOut = os.Stderr
	}
	opts, err := ParseDurableAgentArgs(args)
	if err != nil {
		_, _ = fmt.Fprintln(errOut, err)
		return err
	}
	if opts.Help {
		_, _ = fmt.Fprint(out, Usage())
		return nil
	}
	if opts.Resume && opts.Session == "" && !opts.PrintMode {
		dir := opts.SessionDir
		if dir == "" {
			dir = GetDurableSessionsDir(opts.AgentDir)
		}
		picked, pickErr := PickDurableSession(ctx, dir)
		if pickErr != nil {
			return pickErr
		}
		opts.Session = picked
	}

	rt, err := StartDurableAgentRuntime(ctx, opts)
	if err != nil {
		return err
	}
	defer func() {
		_ = rt.Close(context.Background())
	}()

	if opts.PrintMode || strings.TrimSpace(opts.Prompt) != "" {
		if strings.TrimSpace(opts.Prompt) == "" {
			return fmt.Errorf("prompt text is required in --print mode")
		}
		return RunDurableAgentPrint(ctx, rt, opts.Prompt, out)
	}
	return RunDurableTui(ctx, rt)
}

// Main runs the durableagent entry point and returns an OS exit code.
func Main(ctx context.Context, args []string) int {
	if err := Run(ctx, args, os.Stdout, os.Stderr); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "durableagent: %v\n", err)
		return 1
	}
	return 0
}

// Ported from packages/coding-agent/src/experimental/vacation/main.ts (pi v1.0.0).

package vacation

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
)

// Usage returns CLI usage help text for the vacation durable-sleep agent.
func Usage() string {
	return `Usage: vacation-pi [options] [prompt...]

Options:
  -p, --print              Run one prompt non-interactively and exit
  -c, --continue           Resume most recent unlocked vacation session
  -r, --resume             Select a vacation session interactively
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

// Run executes the vacation entry point with arguments and stdio streams.
func Run(ctx context.Context, args []string, out io.Writer, errOut io.Writer) error {
	if out == nil {
		out = os.Stdout
	}
	if errOut == nil {
		errOut = os.Stderr
	}
	opts, err := ParseVacationArgs(args)
	if err != nil {
		_, _ = fmt.Fprintln(errOut, err)
		return err
	}
	if opts.Help {
		_, _ = fmt.Fprint(out, Usage())
		return nil
	}
	if opts.Resume && opts.Session == "" && !opts.PrintMode {
		picked, pickErr := PickVacationSession(ctx, opts.SessionDir)
		if pickErr != nil {
			return pickErr
		}
		opts.Session = picked
	}

	rt, err := StartVacationRuntime(ctx, opts)
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
		return RunVacationPrint(ctx, rt, opts.Prompt, out)
	}
	return RunVacationTui(ctx, rt)
}

// Main runs the vacation entry point and returns an OS exit code.
func Main(ctx context.Context, args []string) int {
	if err := Run(ctx, args, os.Stdout, os.Stderr); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "vacation: %v\n", err)
		return 1
	}
	return 0
}

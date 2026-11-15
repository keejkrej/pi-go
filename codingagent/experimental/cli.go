// Ported from packages/coding-agent/src/experimental/cli.ts (pi v1.0.0).

package experimental

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/keejkrej/pi-go/codingagent/cli/experimentalcli"
	"github.com/keejkrej/pi-go/codingagent/core"
)

func cliTrimInvocationArgs(args []string) ([]string, bool) {
	if len(args) == 0 {
		return nil, false
	}
	if args[0] == "experimental" {
		return args[1:], true
	}
	if args[0] == "server" || args[0] == "client" {
		return args, true
	}
	return nil, false
}

// RunExperimentalCli dispatches experimental server/client invocations when PI_EXPERIMENTAL=1 is enabled.
func RunExperimentalCli(ctx context.Context, args []string, options ...CliCommandOptions) (bool, error) {
	if !core.AreExperimentalFeaturesEnabled() {
		return false, nil
	}
	execArgs, ok := cliTrimInvocationArgs(args)
	if !ok {
		return false, nil
	}

	opts := commNormalizeCommandOptions(options)
	opts.Context = ctx
	res, err := experimentalcli.Cli.Execute(execArgs, NewCliContext(opts))
	if err != nil {
		return true, err
	}
	if !res.Ok {
		return true, errors.New(strings.Join(res.Errors, "\n"))
	}
	return true, nil
}

// RunExperimentalCliMain runs internal subprocess entrypoints, experimental CLI dispatch, or opts.Fallback.
func RunExperimentalCliMain(ctx context.Context, args []string, options ...CliCommandOptions) (bool, int) {
	opts := commNormalizeCommandOptions(options)
	if handled, err := MaybeRunInternalProcess(args); handled {
		if err != nil {
			_, _ = fmt.Fprintln(opts.Stderr, err.Error())
			return true, 1
		}
		return true, 0
	}
	handled, err := RunExperimentalCli(ctx, args, opts)
	if !handled {
		if opts.Fallback != nil {
			return true, opts.Fallback(ctx, args)
		}
		return false, 0
	}
	if err != nil {
		_, _ = fmt.Fprintln(opts.Stderr, err.Error())
		return true, 1
	}
	return true, 0
}

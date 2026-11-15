// Ported from packages/coding-agent/src/experimental/durable/main.ts (pi v1.0.0).

package durableagent

import (
	"context"
	"fmt"
	"os"
)

// ParseArgs parses standalone durable CLI arguments (--continue / -c).
func ParseArgs(argv []string) (OpenDurableOptions, error) {
	continueSession := false
	for _, arg := range argv {
		if arg == "--continue" || arg == "-c" {
			continueSession = true
		} else {
			return OpenDurableOptions{}, fmt.Errorf("Unknown argument: %s", arg)
		}
	}
	return OpenDurableOptions{ContinueSession: continueSession}, nil
}

// Run opens the durable session and launches the interactive durable TUI.
func Run(ctx context.Context, args []string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	opts, err := ParseArgs(args)
	if err != nil {
		return err
	}
	d, err := OpenDurable(ctx, &opts)
	if err != nil {
		return err
	}
	defer func() {
		_ = d.Close()
	}()
	return RunDurableTui(ctx, d.View, d.Controller, d.Settings)
}

// Main runs the standalone durable entry point and returns an OS exit code.
func Main(ctx context.Context, args []string) int {
	if err := Run(ctx, args); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	return 0
}

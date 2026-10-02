//go:build !windows

package xspawn

import (
	"context"
	"os/exec"
)

func startCommand(ctx context.Context, dir string, env []string, name string, args []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if env != nil {
		cmd.Env = append([]string(nil), env...)
	}
	return cmd
}

func verifyMissing(_ *exec.Cmd, err error, _ string) error { return err }

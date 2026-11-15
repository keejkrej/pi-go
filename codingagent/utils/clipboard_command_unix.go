//go:build !windows

// Ported from packages/coding-agent/src/utils/clipboard-command.ts (pi v1.5.2).

package utils

import (
	"os/exec"
	"syscall"
)

func ccRootDir() string {
	return "/"
}

func ccConfigureCmd(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

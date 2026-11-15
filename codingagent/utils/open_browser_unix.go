//go:build !windows

// Ported from packages/coding-agent/src/utils/open-browser.ts (pi v1.5.2).

package utils

import (
	"os/exec"
	"syscall"
)

func obConfigureCmd(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

//go:build windows

// Ported from packages/coding-agent/src/utils/clipboard-command.ts (pi v1.5.2).

package utils

import (
	"os/exec"
	"path/filepath"
	"syscall"
)

func ccRootDir() string {
	abs, err := filepath.Abs(`\`)
	if err == nil && abs != "" {
		return abs
	}
	return `\`
}

func ccConfigureCmd(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}

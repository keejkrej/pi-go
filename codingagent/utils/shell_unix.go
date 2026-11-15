//go:build !windows

// Ported from packages/coding-agent/src/utils/shell.ts (pi v1.5.2).

package utils

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

func shellAccessFOK(p string) bool {
	return unix.Access(p, unix.F_OK) == nil
}

func shellKillSig(pid int, sig string) error {
	switch sig {
	case "SIGSTOP":
		return syscall.Kill(pid, syscall.SIGSTOP)
	case "SIGKILL":
		return syscall.Kill(pid, syscall.SIGKILL)
	default:
		return syscall.Kill(pid, syscall.SIGKILL)
	}
}

func shellDefaultSpawnTaskkill(command string, args []string) {
	cmd := exec.Command(command, args...)
	_ = cmd.Start()
}

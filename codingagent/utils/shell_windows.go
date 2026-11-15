//go:build windows

// Ported from packages/coding-agent/src/utils/shell.ts (pi v1.5.2).

package utils

import (
	"errors"
	"os/exec"
	"syscall"
)

func shellAccessFOK(p string) bool {
	ptr, err := syscall.UTF16PtrFromString(p)
	if err != nil {
		return false
	}
	_, err = syscall.GetFileAttributes(ptr)
	return err == nil
}

func shellKillSig(pid int, sig string) error {
	_ = sig
	return errors.New("process signal kill not supported on Windows")
}

func shellDefaultSpawnTaskkill(command string, args []string) {
	cmd := exec.Command(command, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	_ = cmd.Start()
}

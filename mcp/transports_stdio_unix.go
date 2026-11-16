//go:build !windows

package mcp

import (
	"os/exec"
	"syscall"
)

func tsConfigureProcessGroup(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func tsPlatformKillProcessTree(child *exec.Cmd, signal string) {
	if child == nil || child.Process == nil || child.Process.Pid <= 0 {
		return
	}
	pid := child.Process.Pid
	sig := syscall.SIGTERM
	if signal == "SIGKILL" {
		sig = syscall.SIGKILL
	}
	if tsUseProcessGroups() {
		if err := syscall.Kill(-pid, sig); err == nil {
			return
		}
	}
	_ = syscall.Kill(pid, sig)
}

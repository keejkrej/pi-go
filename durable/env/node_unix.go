// Ported from packages/durable/src/env/node.ts (pi v1.0.0).
//go:build unix

package env

import (
	"os"
	"os/exec"
	"syscall"
)

func nodeKillProcessTree(pid int) {
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

func nodeSetProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

func nodeSignalExitCode(state *os.ProcessState) (int, bool) {
	if state == nil {
		return 0, false
	}
	ws, ok := state.Sys().(syscall.WaitStatus)
	if !ok || !ws.Signaled() {
		return 0, false
	}
	return 128 + int(ws.Signal()), true
}

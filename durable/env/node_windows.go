// Ported from packages/durable/src/env/node.ts (pi v1.0.0).
//go:build windows

package env

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
)

func nodeKillProcessTree(pid int) {
	systemRoot := os.Getenv("SystemRoot")
	if systemRoot == "" {
		systemRoot = `C:\Windows`
	}
	cmd := exec.Command(
		filepath.Join(systemRoot, "System32", "taskkill.exe"),
		"/F",
		"/T",
		"/PID",
		strconv.Itoa(pid),
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	_ = cmd.Start()
}

func nodeSetProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
}

func nodeSignalExitCode(_ *os.ProcessState) (int, bool) {
	return 0, false
}

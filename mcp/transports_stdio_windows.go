//go:build windows

package mcp

import (
	"os/exec"
	"strconv"
)

func tsConfigureProcessGroup(_ *exec.Cmd) {}

func tsPlatformKillProcessTree(child *exec.Cmd, _ string) {
	if child == nil || child.Process == nil || child.Process.Pid <= 0 {
		return
	}
	if err := exec.Command("taskkill", "/pid", strconv.Itoa(child.Process.Pid), "/T", "/F").Run(); err == nil {
		return
	}
	_ = child.Process.Kill()
}

// Ported from packages/durable/src/env/node.ts (pi v1.0.0).
//go:build !unix && !windows

package env

import (
	"os"
	"os/exec"
)

func nodeKillProcessTree(pid int) {
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
}

func nodeSetProcessGroup(_ *exec.Cmd) {}

func nodeSignalExitCode(_ *os.ProcessState) (int, bool) {
	return 0, false
}

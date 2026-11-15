// Ported from packages/coding-agent/src/utils/wsl.ts (pi v1.5.2).

package utils

import (
	"os"
	"regexp"
)

var (
	wslProcVersionRe   = regexp.MustCompile(`(?i)microsoft|wsl`)
	wslReadProcVersion = func() ([]byte, error) {
		return os.ReadFile("/proc/version")
	}
)

// IsWSL reports whether execution is under Windows Subsystem for Linux.
// If an explicit env map is passed, WSL environment markers are read from it
// instead of process environment variables.
func IsWSL(env ...map[string]string) bool {
	if len(env) > 0 && env[0] != nil {
		if env[0]["WSL_DISTRO_NAME"] != "" || env[0]["WSLENV"] != "" {
			return true
		}
	} else {
		if os.Getenv("WSL_DISTRO_NAME") != "" || os.Getenv("WSLENV") != "" {
			return true
		}
	}

	release, err := wslReadProcVersion()
	if err != nil {
		return false
	}
	return wslProcVersionRe.Match(release)
}

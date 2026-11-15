// Ported from packages/coding-agent/src/utils/shell.ts (pi v1.5.2).

package utils

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/keejkrej/pi-go/internal/js"
)

// ShellConfig describes the shell executable, command flags, and optional stdin transport.
type ShellConfig struct {
	Shell            string
	Args             []string
	CommandTransport string
}

// PowershellArgs are the standard PowerShell invocation arguments.
var PowershellArgs = []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command"}

var (
	shellPlatform            = js.Platform
	shellPathExists          = shellAccessFOK
	shellSpawnSync           = shellExecSync
	shellProcessKill         = shellKillSig
	shellSpawnTaskkill       = shellDefaultSpawnTaskkill
	shellWinDriveRe          = regexp.MustCompile(`(?i)^[a-z]:[\\/]`)
	shellLegacyWslFallbackRe = regexp.MustCompile(`(?i)^[a-z]:\\windows\\(?:system32|sysnative)\\bash\.exe$`)

	shellTrackedMu   sync.Mutex
	shellTrackedPids = make(map[int]struct{})
)

func shellExecSync(command string, args []string) (int, string, error) {
	cmd := exec.Command(command, args...)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), string(out), nil
		}
		return -1, "", err
	}
	return 0, string(out), nil
}

func shellIsLegacyWslBashPath(p string) bool {
	normalized := strings.ToLower(pathsWin32Normalize(p))
	systemRoot := os.Getenv("SystemRoot")
	if systemRoot != "" {
		if !shellWinDriveRe.MatchString(systemRoot) {
			return false
		}
		for _, dir := range []string{"System32", "Sysnative"} {
			candidate := strings.ToLower(pathsWin32Join(systemRoot, dir, "bash.exe"))
			if normalized == candidate {
				return true
			}
		}
		return false
	}
	return shellLegacyWslFallbackRe.MatchString(normalized)
}

func shellGetBashConfig(shell string) ShellConfig {
	if shellIsLegacyWslBashPath(shell) {
		return ShellConfig{
			Shell:            shell,
			Args:             []string{"-s"},
			CommandTransport: "stdin",
		}
	}
	return ShellConfig{
		Shell: shell,
		Args:  []string{"-c"},
	}
}

func shellFindExecutableOnPath(executable string) string {
	if shellPlatform() == "win32" {
		status, stdout, err := shellSpawnSync("where", []string{executable})
		if err == nil && status == 0 && stdout != "" {
			lines := strings.Split(strings.ReplaceAll(strings.TrimSpace(stdout), "\r\n", "\n"), "\n")
			if len(lines) > 0 && lines[0] != "" && shellPathExists(lines[0]) {
				return lines[0]
			}
		}
		return ""
	}

	status, stdout, err := shellSpawnSync("which", []string{executable})
	if err == nil && status == 0 && stdout != "" {
		lines := strings.Split(strings.ReplaceAll(strings.TrimSpace(stdout), "\r\n", "\n"), "\n")
		if len(lines) > 0 && lines[0] != "" {
			return lines[0]
		}
	}
	return ""
}

// GetShellConfig resolves the bash shell configuration for the current platform
// or an explicit custom shell path.
func GetShellConfig(customShellPath ...string) (ShellConfig, error) {
	if len(customShellPath) > 0 && customShellPath[0] != "" {
		custom := customShellPath[0]
		if shellPathExists(custom) {
			return shellGetBashConfig(custom), nil
		}
		return ShellConfig{}, fmt.Errorf("Custom shell path not found: %s", custom)
	}

	if shellPlatform() == "win32" {
		var paths []string
		if programFiles := os.Getenv("ProgramFiles"); programFiles != "" {
			paths = append(paths, programFiles+`\Git\bin\bash.exe`)
		}
		if programFilesX86 := os.Getenv("ProgramFiles(x86)"); programFilesX86 != "" {
			paths = append(paths, programFilesX86+`\Git\bin\bash.exe`)
		}
		for _, p := range paths {
			if shellPathExists(p) {
				return shellGetBashConfig(p), nil
			}
		}

		if bashOnPath := shellFindExecutableOnPath("bash.exe"); bashOnPath != "" {
			return shellGetBashConfig(bashOnPath), nil
		}

		var searched strings.Builder
		for i, p := range paths {
			if i > 0 {
				searched.WriteByte('\n')
			}
			searched.WriteString("  ")
			searched.WriteString(p)
		}
		return ShellConfig{}, fmt.Errorf(
			"No bash shell found. Options:\n"+
				"  1. Install Git for Windows: https://git-scm.com/download/win\n"+
				"  2. Add your bash to PATH (Cygwin, MSYS2, etc.)\n"+
				"  3. Set shellPath in settings.json\n\n"+
				"Searched Git Bash in:\n%s",
			searched.String(),
		)
	}

	if shellPathExists("/bin/bash") {
		return shellGetBashConfig("/bin/bash"), nil
	}
	if bashOnPath := shellFindExecutableOnPath("bash"); bashOnPath != "" {
		return shellGetBashConfig(bashOnPath), nil
	}
	return ShellConfig{Shell: "sh", Args: []string{"-c"}}, nil
}

// GetPowerShellConfig resolves PowerShell on Windows, preferring PowerShell 7 when available.
func GetPowerShellConfig() (ShellConfig, error) {
	if shellPlatform() != "win32" {
		return ShellConfig{}, fmt.Errorf("The powershell tool is only available on Windows.")
	}

	shell := shellFindExecutableOnPath("pwsh.exe")
	if shell == "" {
		if programFiles := os.Getenv("ProgramFiles"); programFiles != "" {
			pwsh7 := pathsWin32Join(programFiles, "PowerShell", "7", "pwsh.exe")
			if shellPathExists(pwsh7) {
				shell = pwsh7
			}
		}
	}

	if shell == "" {
		shell = shellFindExecutableOnPath("powershell.exe")
	}
	if shell == "" {
		if systemRoot := os.Getenv("SystemRoot"); systemRoot != "" {
			windowsPS := pathsWin32Join(systemRoot, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
			if shellPathExists(windowsPS) {
				shell = windowsPS
			}
		}
	}

	if shell == "" {
		return ShellConfig{}, fmt.Errorf("No PowerShell executable found. Install PowerShell or add powershell.exe/pwsh.exe to PATH.")
	}

	args := make([]string, len(PowershellArgs))
	copy(args, PowershellArgs)
	return ShellConfig{Shell: shell, Args: args}, nil
}

// GetShellEnv returns the process environment with GetBinDir() prepended to PATH when absent.
func GetShellEnv() []string {
	binDir := GetBinDir()
	env := os.Environ()
	delim := string(os.PathListSeparator)

	pathIdx := -1
	pathKey := "PATH"
	currentPath := ""
	for i, entry := range env {
		key, val, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if strings.EqualFold(key, "PATH") {
			pathIdx = i
			pathKey = key
			currentPath = val
			break
		}
	}

	var entries []string
	hasBinDir := false
	for _, p := range strings.Split(currentPath, delim) {
		if p == "" {
			continue
		}
		if p == binDir {
			hasBinDir = true
		}
		entries = append(entries, p)
	}
	_ = entries

	updatedPath := currentPath
	if !hasBinDir {
		if currentPath != "" {
			updatedPath = binDir + delim + currentPath
		} else {
			updatedPath = binDir
		}
	}

	if pathIdx >= 0 {
		env[pathIdx] = pathKey + "=" + updatedPath
	} else {
		env = append(env, pathKey+"="+updatedPath)
	}
	return env
}

// SanitizeBinaryOutput removes control characters (except \t, \n, \r) and U+FFF9..U+FFFB.
func SanitizeBinaryOutput(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			b.WriteByte(s[i])
			i++
			continue
		}
		if (r >= 0x00 && r <= 0x08) ||
			r == 0x0B ||
			r == 0x0C ||
			(r >= 0x0E && r <= 0x1F) ||
			(r >= 0xFFF9 && r <= 0xFFFB) {
			i += size
			continue
		}
		b.WriteString(s[i : i+size])
		i += size
	}
	return b.String()
}

// TrackDetachedChildPid records a detached child PID for shutdown cleanup.
func TrackDetachedChildPid(pid int) {
	shellTrackedMu.Lock()
	shellTrackedPids[pid] = struct{}{}
	shellTrackedMu.Unlock()
}

// UntrackDetachedChildPid removes pid from detached child cleanup tracking.
func UntrackDetachedChildPid(pid int) {
	shellTrackedMu.Lock()
	delete(shellTrackedPids, pid)
	shellTrackedMu.Unlock()
}

// KillTrackedDetachedChildren kills all tracked detached child process trees and clears the set.
func KillTrackedDetachedChildren() {
	shellTrackedMu.Lock()
	pids := make([]int, 0, len(shellTrackedPids))
	for pid := range shellTrackedPids {
		pids = append(pids, pid)
	}
	clear(shellTrackedPids)
	shellTrackedMu.Unlock()

	for _, pid := range pids {
		KillProcessTree(pid)
	}
}

// KillProcessTree terminates pid and its descendants across platforms.
func KillProcessTree(pid int) {
	if shellPlatform() == "win32" {
		systemRoot := os.Getenv("SystemRoot")
		if systemRoot == "" {
			systemRoot = `C:\Windows`
		}
		taskkill := filepath.Join(systemRoot, "System32", "taskkill.exe")
		if strings.Contains(systemRoot, `\`) || strings.Contains(systemRoot, `:`) {
			taskkill = pathsWin32Join(systemRoot, "System32", "taskkill.exe")
		}
		shellSpawnTaskkill(taskkill, []string{"/F", "/T", "/PID", fmt.Sprintf("%d", pid)})
		return
	}

	if shellPlatform() == "darwin" {
		_ = shellProcessKill(-pid, "SIGSTOP")
	}
	if err := shellProcessKill(-pid, "SIGKILL"); err != nil {
		_ = shellProcessKill(pid, "SIGKILL")
	}
}

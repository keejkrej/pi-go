// Ported from packages/coding-agent/src/utils/windows-self-update.ts (pi v1.5.2).

package utils

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const wsuOldSuffix = ".old"

// CleanupStaleWindowsSelfUpdateArtifacts removes leftover renamed binary artifacts from a
// previous Windows self-update alongside execPath. No-op on non-Windows platforms.
func CleanupStaleWindowsSelfUpdateArtifacts(execPath string) {
	if runtime.GOOS != "windows" || execPath == "" {
		return
	}
	dir := filepath.Dir(execPath)
	base := filepath.Base(execPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	prefix := base + wsuOldSuffix
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), prefix) {
			_ = os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
}

// QuarantineWindowsBinaryForUpdate renames a currently running Windows executable aside so
// a package manager or updater can install the replacement binary.
func QuarantineWindowsBinaryForUpdate(execPath string) (string, error) {
	if runtime.GOOS != "windows" || execPath == "" {
		return "", nil
	}
	backup := fmt.Sprintf("%s%s.%d", execPath, wsuOldSuffix, os.Getpid())
	if err := os.Rename(execPath, backup); err != nil {
		return "", err
	}
	return backup, nil
}

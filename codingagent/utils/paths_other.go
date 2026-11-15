//go:build !darwin && !linux

// Ported from packages/coding-agent/src/utils/paths.ts (pi v1.5.2).

package utils

import (
	"fmt"
	"os"
)

func pathsGetFileRevision(p string) *string {
	info, err := os.Stat(p)
	if err != nil {
		return nil
	}
	ns := info.ModTime().UnixNano()
	rev := fmt.Sprintf("0:0:%d:%d:%d", info.Size(), ns, ns)
	return &rev
}

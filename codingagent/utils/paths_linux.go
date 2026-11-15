//go:build linux

// Ported from packages/coding-agent/src/utils/paths.ts (pi v1.5.2).

package utils

import (
	"fmt"
	"os"
	"syscall"
)

func pathsGetFileRevision(p string) *string {
	info, err := os.Stat(p)
	if err != nil {
		return nil
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st == nil {
		return nil
	}
	rev := fmt.Sprintf("%d:%d:%d:%d:%d",
		st.Dev, st.Ino, st.Size,
		st.Mtim.Nano(), st.Ctim.Nano(),
	)
	return &rev
}

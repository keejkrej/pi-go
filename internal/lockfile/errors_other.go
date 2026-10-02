// Ported from proper-lockfile@4.1.2 lib/lockfile.js (fallback for platforms without unix or Windows syscalls).

//go:build !unix && !windows

package lockfile

import (
	"io/fs"
	"os"
	"syscall"
)

func lfErrnoCode(errno syscall.Errno) string {
	return ""
}

// lfRmdir removes the lock directory; it refuses to remove anything that is not a directory.
func lfRmdir(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return &fs.PathError{Op: "rmdir", Path: path, Err: syscall.ENOTDIR}
	}
	return os.Remove(path)
}

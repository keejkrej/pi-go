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

// lfIsSymlink reports whether lstat saw a symbolic link (stats.isSymbolicLink()).
func lfIsSymlink(_ string, info fs.FileInfo) bool {
	return info.Mode()&fs.ModeSymlink != 0
}

// lfFileID is realpath's seenLinks key; dev/ino are unavailable here, so links are always re-read.
func lfFileID(fs.FileInfo) string {
	return ""
}

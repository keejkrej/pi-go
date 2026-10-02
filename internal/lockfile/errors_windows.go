// Ported from proper-lockfile@4.1.2 lib/lockfile.js (rmdir and libuv error mapping on Windows).

package lockfile

import (
	"io/fs"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

// lfWindowsErrnoCodes follows libuv's uv_translate_sys_error for the errors fs.mkdir/rmdir/stat/utimes produce.
var lfWindowsErrnoCodes = map[syscall.Errno]string{
	windows.ERROR_FILE_NOT_FOUND:        "ENOENT",
	windows.ERROR_PATH_NOT_FOUND:        "ENOENT",
	windows.ERROR_INVALID_NAME:          "ENOENT",
	windows.ERROR_INVALID_DRIVE:         "ENOENT",
	windows.ERROR_MOD_NOT_FOUND:         "ENOENT",
	windows.ERROR_BAD_PATHNAME:          "ENOENT",
	windows.ERROR_DIRECTORY:             "ENOTDIR",
	windows.ERROR_FILE_EXISTS:           "EEXIST",
	windows.ERROR_ALREADY_EXISTS:        "EEXIST",
	windows.ERROR_DIR_NOT_EMPTY:         "ENOTEMPTY",
	windows.ERROR_ACCESS_DENIED:         "EPERM",
	windows.ERROR_PRIVILEGE_NOT_HELD:    "EPERM",
	windows.ERROR_NOACCESS:              "EACCES",
	windows.ERROR_ELEVATION_REQUIRED:    "EACCES",
	windows.ERROR_CANT_ACCESS_FILE:      "EACCES",
	windows.ERROR_SHARING_VIOLATION:     "EBUSY",
	windows.ERROR_LOCK_VIOLATION:        "EBUSY",
	windows.ERROR_BUSY:                  "EBUSY",
	windows.ERROR_PATH_BUSY:             "EBUSY",
	windows.ERROR_WRITE_PROTECT:         "EROFS",
	windows.ERROR_DISK_FULL:             "ENOSPC",
	windows.ERROR_HANDLE_DISK_FULL:      "ENOSPC",
	windows.ERROR_FILENAME_EXCED_RANGE:  "ENAMETOOLONG",
	windows.ERROR_NOT_SAME_DEVICE:       "EXDEV",
	windows.ERROR_TOO_MANY_OPEN_FILES:   "EMFILE",
	windows.ERROR_INVALID_PARAMETER:     "EINVAL",
	windows.ERROR_CANT_RESOLVE_FILENAME: "ELOOP",
	windows.ERROR_INVALID_FUNCTION:      "EISDIR",
	windows.ERROR_NOT_SUPPORTED:         "ENOTSUP",
}

func lfErrnoCode(errno syscall.Errno) string {
	return lfWindowsErrnoCodes[errno]
}

// lfNamespacedPath is path.toNamespacedPath for an absolute Windows path, as Node passes to libuv.
func lfNamespacedPath(path string) string {
	if !filepath.IsAbs(path) || strings.HasPrefix(path, `\\?\`) || strings.HasPrefix(path, `\\.\`) {
		return path
	}
	if strings.HasPrefix(path, `\\`) {
		return `\\?\UNC\` + path[2:]
	}
	return `\\?\` + path
}

// lfRmdir is fs.rmdir: RemoveDirectoryW, which fails on anything but an empty directory.
func lfRmdir(path string) error {
	p, err := syscall.UTF16PtrFromString(lfNamespacedPath(filepath.Clean(path)))
	if err != nil {
		return &fs.PathError{Op: "rmdir", Path: path, Err: err}
	}
	if err := syscall.RemoveDirectory(p); err != nil {
		return &fs.PathError{Op: "rmdir", Path: path, Err: err}
	}
	return nil
}

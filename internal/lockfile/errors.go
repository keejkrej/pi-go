// Ported from proper-lockfile@4.1.2 lib/lockfile.js (fs errors surface as Node-style errors with a code).

package lockfile

import (
	"errors"
	"io/fs"
	"syscall"
)

// uvErrorMessages are libuv's uv_strerror texts, which Node uses in fs error messages.
var uvErrorMessages = map[string]string{
	"E2BIG":        "argument list too long",
	"EACCES":       "permission denied",
	"EAGAIN":       "resource temporarily unavailable",
	"EBADF":        "bad file descriptor",
	"EBUSY":        "resource busy or locked",
	"EEXIST":       "file already exists",
	"EFAULT":       "bad address in system call argument",
	"EFBIG":        "file too large",
	"EINTR":        "interrupted system call",
	"EINVAL":       "invalid argument",
	"EIO":          "i/o error",
	"EISDIR":       "illegal operation on a directory",
	"ELOOP":        "too many symbolic links encountered",
	"EMFILE":       "too many open files",
	"EMLINK":       "too many links",
	"ENAMETOOLONG": "name too long",
	"ENFILE":       "file table overflow",
	"ENODEV":       "no such device",
	"ENOENT":       "no such file or directory",
	"ENOMEM":       "not enough memory",
	"ENOSPC":       "no space left on device",
	"ENOSYS":       "function not implemented",
	"ENOTDIR":      "not a directory",
	"ENOTEMPTY":    "directory not empty",
	"ENOTSUP":      "operation not supported on socket",
	"EPERM":        "operation not permitted",
	"EROFS":        "read-only file system",
	"ESPIPE":       "invalid seek",
	"ETIMEDOUT":    "connection timed out",
	"ETXTBSY":      "text file is busy",
	"EXDEV":        "cross-device link not permitted",
}

// toNodeError converts a Go fs error into the Node fs error shape:
// "<CODE>: <uv message>, <syscall> '<path>'".
func toNodeError(err error, syscallName, path string) *Error {
	var lfErr *Error
	if errors.As(err, &lfErr) {
		return lfErr
	}
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) && pathErr.Path != "" {
		path = pathErr.Path
	}
	code := ""
	var errno syscall.Errno
	if errors.As(err, &errno) {
		code = lfErrnoCode(errno)
	}
	if code == "" {
		switch {
		case errors.Is(err, fs.ErrNotExist):
			code = "ENOENT"
		case errors.Is(err, fs.ErrExist):
			code = "EEXIST"
		case errors.Is(err, fs.ErrPermission):
			code = "EACCES"
		}
	}
	message, ok := uvErrorMessages[code]
	if !ok {
		code, message = "UNKNOWN", "unknown error"
	}
	return &Error{
		Code:    code,
		Message: code + ": " + message + ", " + syscallName + " '" + path + "'",
		Syscall: syscallName,
		Path:    path,
		Err:     err,
	}
}

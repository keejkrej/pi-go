// Ported from proper-lockfile@4.1.2 lib/lockfile.js (rmdir and errno codes on unix).

//go:build unix

package lockfile

import (
	"io/fs"
	"syscall"
)

var lfUnixErrnoCodes = map[syscall.Errno]string{
	syscall.E2BIG:        "E2BIG",
	syscall.EACCES:       "EACCES",
	syscall.EAGAIN:       "EAGAIN",
	syscall.EBADF:        "EBADF",
	syscall.EBUSY:        "EBUSY",
	syscall.EEXIST:       "EEXIST",
	syscall.EFAULT:       "EFAULT",
	syscall.EFBIG:        "EFBIG",
	syscall.EINTR:        "EINTR",
	syscall.EINVAL:       "EINVAL",
	syscall.EIO:          "EIO",
	syscall.EISDIR:       "EISDIR",
	syscall.ELOOP:        "ELOOP",
	syscall.EMFILE:       "EMFILE",
	syscall.EMLINK:       "EMLINK",
	syscall.ENAMETOOLONG: "ENAMETOOLONG",
	syscall.ENFILE:       "ENFILE",
	syscall.ENODEV:       "ENODEV",
	syscall.ENOENT:       "ENOENT",
	syscall.ENOMEM:       "ENOMEM",
	syscall.ENOSPC:       "ENOSPC",
	syscall.ENOSYS:       "ENOSYS",
	syscall.ENOTDIR:      "ENOTDIR",
	syscall.ENOTEMPTY:    "ENOTEMPTY",
	syscall.ENOTSUP:      "ENOTSUP",
	syscall.EPERM:        "EPERM",
	syscall.EROFS:        "EROFS",
	syscall.ESPIPE:       "ESPIPE",
	syscall.ETIMEDOUT:    "ETIMEDOUT",
	syscall.ETXTBSY:      "ETXTBSY",
	syscall.EXDEV:        "EXDEV",
}

func lfErrnoCode(errno syscall.Errno) string {
	return lfUnixErrnoCodes[errno]
}

// lfRmdir is fs.rmdir: it removes an empty directory and fails with ENOTDIR on anything else.
func lfRmdir(path string) error {
	for {
		err := syscall.Rmdir(path)
		if err == syscall.EINTR {
			continue
		}
		if err != nil {
			return &fs.PathError{Op: "rmdir", Path: path, Err: err}
		}
		return nil
	}
}

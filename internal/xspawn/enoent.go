package xspawn

import (
	"os/exec"
)

// NotFoundError is the ENOENT cross-spawn synthesizes when Windows cmd.exe
// exits 1 because the command was never resolved. Error() is
// "<syscall> ENOENT", for example "spawn git ENOENT".
type NotFoundError struct {
	// Command is the original command name (Node's err.path).
	Command string
	// Args are the original arguments, before shebang rewriting
	// (Node's err.spawnargs).
	Args []string
	// Syscall is "spawn <command>" or "spawnSync <command>".
	Syscall string
	// Path is the original command name (Node's err.path).
	Path string
}

func (e *NotFoundError) Error() string {
	if e == nil {
		return "spawn ENOENT"
	}
	syscall := e.Syscall
	if syscall == "" {
		syscall = "spawn " + e.Command
	}
	return syscall + " ENOENT"
}

// Unwrap lets errors.Is(err, exec.ErrNotFound) succeed.
func (e *NotFoundError) Unwrap() error { return exec.ErrNotFound }

// VerifyENOENT converts a Windows exit status 1 into a NotFoundError when
// cross-spawn would emit an error from its spawn hook. err is returned
// unchanged when a file was resolved or the process did not exit 1.
func VerifyENOENT(cmd *exec.Cmd, err error) error {
	return verifyMissing(cmd, err, "spawn")
}

// VerifyENOENTSync is VerifyENOENT with the syscall name cross-spawn's
// spawnSync path uses ("spawnSync <command>").
func VerifyENOENTSync(cmd *exec.Cmd, err error) error {
	return verifyMissing(cmd, err, "spawnSync")
}

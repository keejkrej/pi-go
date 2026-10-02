package xspawn

import (
	"context"
	"os/exec"
)

// Command returns a command that runs name with args the way cross-spawn
// 7.0.6's spawn(name, args) does. The process is not started.
//
// On Windows, call VerifyENOENT after Wait (or Run or Output) so a command
// cmd.exe could not resolve becomes a NotFoundError. A program that was
// resolved and exits 1 is left as an *exec.ExitError.
func Command(ctx context.Context, name string, args []string) *exec.Cmd {
	return CommandIn(ctx, "", nil, name, args)
}

// CommandIn is Command with cross-spawn's cwd and env options.
//
// An empty dir inherits the current directory. A nil env inherits the
// process environment; a non-nil slice is the child's entire environment,
// not a merge. PATH lookup uses env when that block contains a non-empty
// PATH entry and otherwise the process PATH (which treats "" as missing).
// PATHEXT always comes from the process environment.
func CommandIn(ctx context.Context, dir string, env []string, name string, args []string) *exec.Cmd {
	return startCommand(ctx, dir, env, name, cloneArgs(args))
}

func cloneArgs(args []string) []string {
	if len(args) == 0 {
		return []string{}
	}
	return append([]string(nil), args...)
}

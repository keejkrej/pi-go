// Ported from packages/coding-agent/src/utils/child-process.ts (pi v1.5.2).

package utils

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/internal/xspawn"
)

const cpExitStdioGrace = 100 * time.Millisecond

// SpawnSyncOptions configures synchronous child process execution.
type SpawnSyncOptions struct {
	Dir string
	Env []string
}

// SpawnSyncResult captures synchronous child process output and exit code.
type SpawnSyncResult struct {
	Stdout string
	Stderr string
	Status int
	Error  error
}

// SpawnProcess creates an unstarted command using cross-spawn semantics on Windows.
func SpawnProcess(command string, args []string) *exec.Cmd {
	return xspawn.Command(context.Background(), command, args)
}

// SpawnProcessIn creates an unstarted command with ctx, working directory, and optional environment.
func SpawnProcessIn(ctx context.Context, dir string, env []string, command string, args []string) *exec.Cmd {
	if ctx == nil {
		ctx = context.Background()
	}
	return xspawn.CommandIn(ctx, dir, env, command, args)
}

// SpawnProcessSync runs a command synchronously and returns captured stdout/stderr, status, and verified ENOENT error.
func SpawnProcessSync(command string, args []string, opts *SpawnSyncOptions) SpawnSyncResult {
	var dir string
	var env []string
	if opts != nil {
		dir = opts.Dir
		env = opts.Env
	}
	cmd := xspawn.CommandIn(context.Background(), dir, env, command, args)
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err := cmd.Run()
	err = xspawn.VerifyENOENTSync(cmd, err)

	status := -1
	if cmd.ProcessState != nil {
		status = cmd.ProcessState.ExitCode()
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ProcessState != nil {
			status = exitErr.ProcessState.ExitCode()
		}
	} else if status == -1 {
		status = 0
	}

	return SpawnSyncResult{
		Stdout: stdoutBuf.String(),
		Stderr: stderrBuf.String(),
		Status: status,
		Error:  err,
	}
}

type cpExitResult struct {
	code int
	err  error
}

type cpStreamDone struct {
	isStderr bool
}

// WaitForChildProcess waits for an already-started cmd to exit without hanging on inherited
// stdio handles kept open by detached descendants. After exit, an idle grace window of 100ms
// is armed and re-armed whenever stdout or stderr produces additional data.
func WaitForChildProcess(
	cmd *exec.Cmd,
	stdout io.ReadCloser,
	stderr io.ReadCloser,
	onStdout func([]byte),
	onStderr func([]byte),
) (int, error) {
	waitProc := func() (int, error) {
		state, err := cmd.Process.Wait()
		if err != nil {
			err = xspawn.VerifyENOENT(cmd, err)
			return -1, err
		}
		return state.ExitCode(), nil
	}
	return WaitForChildStreams(waitProc, stdout, stderr, onStdout, onStderr)
}

// WaitForChildStreams waits for waitProc and drains stdout/stderr with the post-exit idle grace timer.
func WaitForChildStreams(
	waitProc func() (int, error),
	stdout io.ReadCloser,
	stderr io.ReadCloser,
	onStdout func([]byte),
	onStderr func([]byte),
) (int, error) {
	stdoutEnded := stdout == nil
	stderrEnded := stderr == nil

	activityCh := make(chan struct{}, 1)
	streamDoneCh := make(chan cpStreamDone, 2)
	exitCh := make(chan cpExitResult, 1)

	var readerWG sync.WaitGroup
	startReader := func(r io.ReadCloser, isStderr bool, cb func([]byte)) {
		readerWG.Add(1)
		go func() {
			defer readerWG.Done()
			defer func() {
				streamDoneCh <- cpStreamDone{isStderr: isStderr}
			}()
			buf := make([]byte, 32*1024)
			for {
				n, err := r.Read(buf)
				if n > 0 {
					if cb != nil {
						chunk := make([]byte, n)
						copy(chunk, buf[:n])
						cb(chunk)
					}
					select {
					case activityCh <- struct{}{}:
					default:
					}
				}
				if err != nil {
					return
				}
			}
		}()
	}

	if stdout != nil {
		startReader(stdout, false, onStdout)
	}
	if stderr != nil {
		startReader(stderr, true, onStderr)
	}

	go func() {
		code, err := waitProc()
		exitCh <- cpExitResult{code: code, err: err}
	}()

	exited := false
	var exitRes cpExitResult
	var timer *time.Timer
	var timerCh <-chan time.Time

	stopTimer := func() {
		if timer != nil {
			timer.Stop()
			timerCh = nil
		}
	}
	armTimer := func() {
		if timer == nil {
			timer = time.NewTimer(cpExitStdioGrace)
		} else {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(cpExitStdioGrace)
		}
		timerCh = timer.C
	}

	defer stopTimer()

	for {
		if exited && stdoutEnded && stderrEnded {
			readerWG.Wait()
			return exitRes.code, exitRes.err
		}

		select {
		case res := <-exitCh:
			exited = true
			exitRes = res
			if res.err != nil && stdoutEnded && stderrEnded {
				readerWG.Wait()
				return res.code, res.err
			}
			if stdoutEnded && stderrEnded {
				readerWG.Wait()
				return res.code, res.err
			}
			armTimer()

		case done := <-streamDoneCh:
			if done.isStderr {
				stderrEnded = true
			} else {
				stdoutEnded = true
			}

		case <-activityCh:
			if exited {
				armTimer()
			}

		case <-timerCh:
			if stdout != nil {
				_ = stdout.Close()
			}
			if stderr != nil {
				_ = stderr.Close()
			}
			readerWG.Wait()
			return exitRes.code, exitRes.err
		}
	}
}

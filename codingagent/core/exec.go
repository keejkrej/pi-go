// Ported from packages/coding-agent/src/core/exec.ts (pi v1.0.0).

package core

import (
	"context"
	"fmt"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/xspawn"
)

const (
	execDefaultMaxBuffer = 256 * 1024 * 1024
	execForceKillDelay   = 5000 * time.Millisecond
)

// ExecOptions configures command execution via ExecCommand.
type ExecOptions struct {
	// Timeout in milliseconds before sending SIGTERM (and SIGKILL after 5s).
	Timeout *int64
	// Working directory override.
	Cwd string
	// Combined stdout+stderr byte limit; exceeding it terminates the command with Truncated=true.
	MaxBuffer *int
}

// ExecResult contains the output and exit status of ExecCommand.
type ExecResult struct {
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	Code      int    `json:"code"`
	Killed    bool   `json:"killed"`
	Truncated bool   `json:"truncated"`
}

type execSignaledWaitStatus interface {
	Signaled() bool
	Signal() syscall.Signal
}

// ExecCommand executes command with args in cwd without invoking a shell,
// supporting cancellation via ctx, millisecond timeouts, and a combined output buffer cap.
func ExecCommand(ctx context.Context, command string, args []string, cwd string, options ...*ExecOptions) (ExecResult, error) {
	var opts *ExecOptions
	if len(options) > 0 {
		opts = options[0]
	}

	maxBuffer := execDefaultMaxBuffer
	if opts != nil && opts.MaxBuffer != nil {
		maxBuffer = *opts.MaxBuffer
	}
	if maxBuffer < 0 || maxBuffer > execDefaultMaxBuffer {
		return ExecResult{}, fmt.Errorf("RangeError: maxBuffer must be an integer between 0 and %d bytes", execDefaultMaxBuffer)
	}

	workDir := cwd
	if opts != nil && opts.Cwd != "" {
		workDir = opts.Cwd
	}

	cmd := utils.SpawnProcessIn(context.Background(), workDir, nil, command, args)
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return ExecResult{Code: 1, Stderr: err.Error()}, nil
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return ExecResult{Code: 1, Stderr: err.Error()}, nil
	}

	if err := cmd.Start(); err != nil {
		err = xspawn.VerifyENOENT(cmd, err)
		return ExecResult{Code: 1, Stderr: err.Error()}, nil
	}

	var (
		mu            sync.Mutex
		stdoutText    string
		stderrText    string
		stdoutDecoder = js.NewUTF8StreamDecoder()
		stderrDecoder = js.NewUTF8StreamDecoder()
		outputBytes   int
		truncated     bool
		killed        bool
		forceKillTmr  *time.Timer
		procDone      bool
	)

	killProcess := func() {
		mu.Lock()
		if killed {
			mu.Unlock()
			return
		}
		killed = true
		doneAlready := procDone
		mu.Unlock()

		if doneAlready || cmd.Process == nil {
			return
		}
		if sigErr := cmd.Process.Signal(syscall.SIGTERM); sigErr != nil {
			_ = cmd.Process.Kill()
		}

		t := time.AfterFunc(execForceKillDelay, func() {
			mu.Lock()
			stillRunning := !procDone
			mu.Unlock()
			if stillRunning && cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
		})
		mu.Lock()
		if procDone {
			t.Stop()
		} else {
			forceKillTmr = t
		}
		mu.Unlock()
	}

	stopWatch := make(chan struct{})
	if ctx != nil {
		if ctx.Err() != nil {
			killProcess()
		} else {
			go func() {
				select {
				case <-ctx.Done():
					killProcess()
				case <-stopWatch:
				}
			}()
		}
	}

	var timeoutTmr *time.Timer
	if opts != nil && opts.Timeout != nil && *opts.Timeout > 0 {
		timeoutTmr = time.AfterFunc(time.Duration(*opts.Timeout)*time.Millisecond, killProcess)
	}

	takeWithinCap := func(data []byte, isStderr bool) {
		mu.Lock()
		if truncated {
			mu.Unlock()
			return
		}
		remaining := maxBuffer - outputBytes
		if len(data) <= remaining {
			outputBytes += len(data)
			chunk := data
			if isStderr {
				stderrText += stderrDecoder.Decode(chunk)
			} else {
				stdoutText += stdoutDecoder.Decode(chunk)
			}
			mu.Unlock()
			return
		}
		truncated = true
		if remaining > 0 {
			chunk := data[:remaining]
			if isStderr {
				stderrText += stderrDecoder.Decode(chunk)
			} else {
				stdoutText += stdoutDecoder.Decode(chunk)
			}
		}
		mu.Unlock()

		_ = stdoutPipe.Close()
		_ = stderrPipe.Close()
		killProcess()
	}

	var procState *os.ProcessState
	code, waitErr := utils.WaitForChildStreams(
		func() (int, error) {
			state, err := cmd.Process.Wait()
			procState = state
			if err != nil {
				err = xspawn.VerifyENOENT(cmd, err)
				return -1, err
			}
			return state.ExitCode(), nil
		},
		stdoutPipe,
		stderrPipe,
		func(data []byte) { takeWithinCap(data, false) },
		func(data []byte) { takeWithinCap(data, true) },
	)

	mu.Lock()
	procDone = true
	if forceKillTmr != nil {
		forceKillTmr.Stop()
	}
	if timeoutTmr != nil {
		timeoutTmr.Stop()
	}
	mu.Unlock()
	close(stopWatch)

	mu.Lock()
	defer mu.Unlock()

	if !truncated {
		stdoutText += stdoutDecoder.Flush()
		stderrText += stderrDecoder.Flush()
	}

	exitCode := code
	if waitErr != nil {
		exitCode = 1
		if stderrText == "" {
			stderrText = waitErr.Error()
		}
	} else if exitCode < 0 {
		if procState != nil {
			if ws, ok := procState.Sys().(execSignaledWaitStatus); ok && ws.Signaled() {
				exitCode = 128 + int(ws.Signal())
			} else {
				exitCode = 1
			}
		} else {
			exitCode = 1
		}
	}

	if truncated && exitCode == 0 {
		exitCode = 1
	}

	return ExecResult{
		Stdout:    stdoutText,
		Stderr:    stderrText,
		Code:      exitCode,
		Killed:    killed,
		Truncated: truncated,
	}, nil
}

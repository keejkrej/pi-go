// Ported from packages/coding-agent/src/utils/clipboard-command.ts (pi v1.5.2).

package utils

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	ccDefaultTimeoutMs      = 3000
	ccDefaultMaxBufferBytes = 50 * 1024 * 1024
)

// ClipboardCommandOptions configures RunClipboardCommand.
type ClipboardCommandOptions struct {
	Input          *string
	TimeoutMs      int
	MaxBufferBytes int
}

// RunClipboardCommand executes a clipboard helper detached on POSIX and from filesystem root.
// Returns (nil, false) on error, timeout, overflow, or non-zero exit code; returns (bytes, true)
// on exit status 0 (including an empty slice for no output).
func RunClipboardCommand(command string, args []string, options *ClipboardCommandOptions) ([]byte, bool) {
	timeoutMs := ccDefaultTimeoutMs
	maxBuffer := ccDefaultMaxBufferBytes
	var hasInput bool
	var input string
	if options != nil {
		if options.TimeoutMs > 0 {
			timeoutMs = options.TimeoutMs
		}
		if options.MaxBufferBytes > 0 {
			maxBuffer = options.MaxBufferBytes
		}
		if options.Input != nil {
			hasInput = true
			input = *options.Input
		}
	}

	cmd := exec.Command(command, args...)
	cmd.Dir = ccRootDir()
	ccConfigureCmd(cmd)

	if hasInput {
		cmd.Stdin = strings.NewReader(input)
	} else {
		stdinPipe, err := cmd.StdinPipe()
		if err != nil {
			return nil, false
		}
		_ = stdinPipe.Close()
	}

	var stdoutReader io.ReadCloser
	if !hasInput {
		var err error
		stdoutReader, err = cmd.StdoutPipe()
		if err != nil {
			return nil, false
		}
	} else {
		// Do not give daemonized clipboard writers an output pipe to retain.
		cmd.Stdout = nil
	}
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		if stdoutReader != nil {
			_ = stdoutReader.Close()
		}
		return nil, false
	}

	type waitResult struct {
		err error
	}

	var outBuf bytes.Buffer
	var overflow bool
	var readerWG sync.WaitGroup
	if stdoutReader != nil {
		readerWG.Add(1)
		go func() {
			defer readerWG.Done()
			buf := make([]byte, 32*1024)
			for {
				n, err := stdoutReader.Read(buf)
				if n > 0 {
					if outBuf.Len()+n > maxBuffer {
						overflow = true
						if cmd.Process != nil {
							_ = cmd.Process.Kill()
						}
						_ = stdoutReader.Close()
						return
					}
					outBuf.Write(buf[:n])
				}
				if err != nil {
					return
				}
			}
		}()
	}

	waitCh := make(chan waitResult, 1)
	go func() {
		readerWG.Wait()
		waitCh <- waitResult{err: cmd.Wait()}
	}()

	timer := time.NewTimer(time.Duration(timeoutMs) * time.Millisecond)
	defer timer.Stop()

	select {
	case <-timer.C:
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		if stdoutReader != nil {
			_ = stdoutReader.Close()
		}
		<-waitCh
		return nil, false
	case res := <-waitCh:
		if overflow || res.err != nil {
			return nil, false
		}
		if outBuf.Len() == 0 {
			return []byte{}, true
		}
		return outBuf.Bytes(), true
	}
}

func ccEnsureEnv() {
	_ = os.DevNull
}

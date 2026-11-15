// Ported from packages/coding-agent/src/core/output-guard.ts (pi v1.5.2).

package core

import (
	"errors"
	"io"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/keejkrej/pi-go/codingagent/utils"
)

const ogRawStdoutRetryDelay = 10 * time.Millisecond

type ogTakeoverState struct {
	rawStdout    io.Writer
	rawStderr    io.Writer
	origStdout   *os.File
	customTarget bool
}

var (
	ogStateMu sync.Mutex
	ogState   *ogTakeoverState

	ogQueueMu       sync.Mutex
	ogQueue         []string
	ogWorkerRunning bool
	ogIdleCond      = sync.NewCond(&ogQueueMu)

	// Test hooks allow unit tests to intercept stdout/stderr writers and retry sleep.
	ogCustomRawStdout io.Writer
	ogCustomRawStderr io.Writer
	ogRetrySleep      = time.Sleep
)

func ogGetRawStdoutWriter() io.Writer {
	ogStateMu.Lock()
	defer ogStateMu.Unlock()
	if ogCustomRawStdout != nil {
		return ogCustomRawStdout
	}
	if ogState != nil && ogState.rawStdout != nil {
		return ogState.rawStdout
	}
	return os.Stdout
}

func ogIsTransientWriteErr(err error) bool {
	return errors.Is(err, syscall.ENOBUFS) ||
		errors.Is(err, syscall.EAGAIN) ||
		errors.Is(err, syscall.EWOULDBLOCK)
}

func ogWriteRawStdoutChunk(text string) error {
	for {
		w := ogGetRawStdoutWriter()
		_, err := io.WriteString(w, text)
		if err == nil {
			return nil
		}
		if !ogIsTransientWriteErr(err) {
			return err
		}
		ogRetrySleep(ogRawStdoutRetryDelay)
	}
}

func ogDrainQueue() {
	for {
		ogQueueMu.Lock()
		if len(ogQueue) == 0 {
			ogWorkerRunning = false
			ogIdleCond.Broadcast()
			ogQueueMu.Unlock()
			return
		}
		chunk := ogQueue[0]
		ogQueue = ogQueue[1:]
		ogQueueMu.Unlock()

		if err := ogWriteRawStdoutChunk(chunk); err != nil {
			ogQueueMu.Lock()
			ogQueue = nil
			ogWorkerRunning = false
			ogIdleCond.Broadcast()
			ogQueueMu.Unlock()
			utils.ConfigExit(1)
			return
		}
	}
}

// TakeOverStdout redirects os.Stdout to stderr while preserving the real stdout handle for WriteRawStdout.
func TakeOverStdout() {
	ogStateMu.Lock()
	defer ogStateMu.Unlock()
	if ogState != nil {
		return
	}
	origStdout := os.Stdout
	origStderr := os.Stderr
	rawOut := io.Writer(origStdout)
	rawErr := io.Writer(origStderr)
	custom := false
	if ogCustomRawStdout != nil {
		rawOut = ogCustomRawStdout
		custom = true
	}
	if ogCustomRawStderr != nil {
		rawErr = ogCustomRawStderr
		custom = true
	}
	if !custom && origStderr != nil {
		os.Stdout = origStderr
	}
	ogState = &ogTakeoverState{
		rawStdout:    rawOut,
		rawStderr:    rawErr,
		origStdout:   origStdout,
		customTarget: custom,
	}
}

// RestoreStdout restores os.Stdout after TakeOverStdout.
func RestoreStdout() {
	ogStateMu.Lock()
	defer ogStateMu.Unlock()
	if ogState == nil {
		return
	}
	if !ogState.customTarget && ogState.origStdout != nil {
		os.Stdout = ogState.origStdout
	}
	ogState = nil
}

// IsStdoutTakenOver reports whether stdout has been redirected by TakeOverStdout.
func IsStdoutTakenOver() bool {
	ogStateMu.Lock()
	defer ogStateMu.Unlock()
	return ogState != nil
}

// WriteRawStdout queues text to be written sequentially to the original stdout stream.
func WriteRawStdout(text string) {
	if len(text) == 0 {
		return
	}
	ogQueueMu.Lock()
	ogQueue = append(ogQueue, text)
	if !ogWorkerRunning {
		ogWorkerRunning = true
		go ogDrainQueue()
	}
	ogQueueMu.Unlock()
}

// WaitForRawStdoutBackpressure blocks until all queued WriteRawStdout writes have completed.
func WaitForRawStdoutBackpressure() {
	ogQueueMu.Lock()
	for ogWorkerRunning || len(ogQueue) > 0 {
		ogIdleCond.Wait()
	}
	ogQueueMu.Unlock()
}

// FlushRawStdout waits for pending raw stdout writes and flushes an empty write to ensure delivery.
func FlushRawStdout() error {
	WaitForRawStdoutBackpressure()
	return ogWriteRawStdoutChunk("")
}

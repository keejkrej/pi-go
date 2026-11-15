package core

import (
	"bytes"
	"io"
	"sync"
	"syscall"
	"testing"
	"time"
)

type togFlakyWriter struct {
	mu       sync.Mutex
	failures int
	buf      bytes.Buffer
}

func (w *togFlakyWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failures > 0 {
		w.failures--
		return 0, syscall.EAGAIN
	}
	return w.buf.Write(p)
}

func TestOutputGuardTakeOverWriteAndFlush(t *testing.T) {
	flaky := &togFlakyWriter{failures: 2}
	var errOut bytes.Buffer

	ogStateMu.Lock()
	origOut := ogCustomRawStdout
	origErr := ogCustomRawStderr
	origSleep := ogRetrySleep
	origState := ogState
	ogCustomRawStdout = flaky
	ogCustomRawStderr = &errOut
	ogRetrySleep = func(time.Duration) {}
	ogState = nil
	ogStateMu.Unlock()

	t.Cleanup(func() {
		RestoreStdout()
		ogStateMu.Lock()
		ogCustomRawStdout = origOut
		ogCustomRawStderr = origErr
		ogRetrySleep = origSleep
		ogState = origState
		ogStateMu.Unlock()
	})

	if IsStdoutTakenOver() {
		t.Fatal("expected IsStdoutTakenOver false initially")
	}
	TakeOverStdout()
	if !IsStdoutTakenOver() {
		t.Fatal("expected IsStdoutTakenOver true after TakeOverStdout")
	}

	WriteRawStdout("")
	WriteRawStdout("first\n")
	WriteRawStdout("second\n")
	if err := FlushRawStdout(); err != nil {
		t.Fatalf("FlushRawStdout failed: %v", err)
	}

	flaky.mu.Lock()
	got := flaky.buf.String()
	flaky.mu.Unlock()
	if got != "first\nsecond\n" {
		t.Fatalf("got %q, want %q", got, "first\nsecond\n")
	}

	RestoreStdout()
	if IsStdoutTakenOver() {
		t.Fatal("expected IsStdoutTakenOver false after RestoreStdout")
	}
}

func TestOutputGuardIsTransientErr(t *testing.T) {
	for _, err := range []error{syscall.EAGAIN, syscall.ENOBUFS, syscall.EWOULDBLOCK} {
		if !ogIsTransientWriteErr(err) {
			t.Fatalf("expected transient for %v", err)
		}
	}
	if ogIsTransientWriteErr(io.EOF) {
		t.Fatal("expected io.EOF non-transient")
	}
}

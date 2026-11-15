// Ported from packages/coding-agent/test/auth-terminal.test.ts (pi v1.5.2).

package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/codingagent/utils"
)

func tatIsAbortError(err error) bool {
	var abortErr *utils.AbortError
	return errors.As(err, &abortErr)
}

func TestLoginTerminalIOAndSecretInput(t *testing.T) {
	var outBuf, errBuf bytes.Buffer
	var openedUrls []string
	interrupted := false

	pr, pw := io.Pipe()
	go func() {
		_, _ = io.WriteString(pw, "answer\n")
	}()

	termIO := atNewProcessLoginTerminalWithIO(
		pr,
		&outBuf,
		&errBuf,
		true,
		func(u string) { openedUrls = append(openedUrls, u) },
		func() { interrupted = true },
	)

	if !termIO.Interactive() {
		t.Fatal("expected Interactive() == true")
	}
	termIO.Write("hello out")
	termIO.Error("hello err")
	termIO.OpenUrl("https://example.com/auth")

	ans, err := termIO.Question(t.Context(), "Prompt: ")
	if err != nil || ans != "answer" {
		t.Fatalf("Question = (%q, %v), want (%q, nil)", ans, err, "answer")
	}

	go func() {
		_, _ = io.WriteString(pw, "secret12\x7f\x7f34\n")
		_ = pw.Close()
	}()
	secret, err := termIO.Secret(t.Context(), "Key: ")
	if err != nil || secret != "secret34" {
		t.Fatalf("Secret = (%q, %v), want (%q, nil)", secret, err, "secret34")
	}
	if interrupted {
		t.Fatal("unexpected onInterrupt during normal Question/Secret")
	}
	if !strings.Contains(outBuf.String(), "hello out\n") || !strings.Contains(outBuf.String(), "Key: \n") {
		t.Fatalf("unexpected outBuf: %q", outBuf.String())
	}
	if !strings.Contains(errBuf.String(), "hello err\n") {
		t.Fatalf("unexpected errBuf: %q", errBuf.String())
	}
	if len(openedUrls) != 1 || openedUrls[0] != "https://example.com/auth" {
		t.Fatalf("openedUrls = %v", openedUrls)
	}
	// Verify secret bytes were never echoed to outBuf.
	if strings.Contains(outBuf.String(), "secret") {
		t.Fatalf("secret leaked into outBuf: %q", outBuf.String())
	}
}

func TestLoginTerminalSecretCtrlCEOFAndCancel(t *testing.T) {
	var outBuf bytes.Buffer
	interruptCount := 0
	termCtrlC := atNewProcessLoginTerminalWithIO(
		strings.NewReader("abc\x03"),
		&outBuf,
		io.Discard,
		true,
		nil,
		func() { interruptCount++ },
	)
	_, err := termCtrlC.Secret(t.Context(), "Secret: ")
	if !tatIsAbortError(err) || interruptCount != 1 {
		t.Fatalf("Ctrl+C Secret = (err=%v, interrupts=%d), want AbortError + 1 interrupt", err, interruptCount)
	}

	interruptCount = 0
	termEOF := atNewProcessLoginTerminalWithIO(
		strings.NewReader(""),
		&outBuf,
		io.Discard,
		true,
		nil,
		func() { interruptCount++ },
	)
	_, err = termEOF.Secret(t.Context(), "Secret: ")
	if !tatIsAbortError(err) || interruptCount != 1 {
		t.Fatalf("EOF Secret = (err=%v, interrupts=%d), want AbortError + 1 interrupt", err, interruptCount)
	}

	// Pre-cancelled context aborts immediately without invoking onInterrupt.
	cancelledCtx, cancel := context.WithCancel(t.Context())
	cancel()
	interruptCount = 0
	termCancel := atNewProcessLoginTerminalWithIO(
		strings.NewReader("ignored\n"),
		&outBuf,
		io.Discard,
		false,
		nil,
		func() { interruptCount++ },
	)
	if termCancel.Interactive() {
		t.Fatal("expected Interactive() == false")
	}
	_, err = termCancel.Secret(cancelledCtx, "Secret: ")
	if !tatIsAbortError(err) || interruptCount != 0 {
		t.Fatalf("cancelled Secret = (err=%v, interrupts=%d)", err, interruptCount)
	}
}

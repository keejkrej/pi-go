package utils

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestShellConfigAndSanitize(t *testing.T) {
	origPlat := shellPlatform
	origExists := shellPathExists
	t.Cleanup(func() {
		shellPlatform = origPlat
		shellPathExists = origExists
	})

	// Explicit custom shell path missing -> returns error (#shell-path-exists)
	if _, err := GetShellConfig(filepath.Join(t.TempDir(), "missing-sh")); err == nil {
		t.Fatal("expected error for missing custom shell path")
	}

	// Simulated Windows shell config resolution (windows-shell-config.test.ts & powershell-config.test.ts)
	t.Setenv("SystemRoot", `C:\Windows`)
	shellPlatform = func() string { return "win32" }
	shellPathExists = func(p string) bool {
		return strings.HasSuffix(strings.ToLower(p), "bash.exe") || strings.HasSuffix(strings.ToLower(p), "powershell.exe")
	}
	psCfg, err := GetPowerShellConfig()
	if err != nil || psCfg.Shell == "" {
		t.Fatalf("GetPowerShellConfig failed: %+v err=%v", psCfg, err)
	}

	// SanitizeBinaryOutput strips control characters / null bytes (#5303)
	sanitized := SanitizeBinaryOutput("hello\x00world\x07\n")
	if strings.ContainsRune(sanitized, '\x00') || strings.ContainsRune(sanitized, '\x07') {
		t.Fatalf("control chars remained in SanitizeBinaryOutput: %q", sanitized)
	}
	if !strings.Contains(sanitized, "helloworld\n") {
		t.Fatalf("unexpected sanitized output: %q", sanitized)
	}
}

func TestKillProcessTreeAndTaskkillEnoent(t *testing.T) {
	origPlat := shellPlatform
	origTaskkill := shellSpawnTaskkill
	t.Cleanup(func() {
		shellPlatform = origPlat
		shellSpawnTaskkill = origTaskkill
	})

	// Regression #6596: Windows taskkill missing (ENOENT) must not panic or hang.
	t.Setenv("SystemRoot", `C:\Windows`)
	shellPlatform = func() string { return "win32" }
	taskkillCalled := false
	shellSpawnTaskkill = func(command string, args []string) {
		taskkillCalled = true
		if len(args) != 4 || args[0] != "/F" || args[1] != "/T" || args[2] != "/PID" || args[3] != "999999" {
			t.Fatalf("unexpected taskkill args: %q %v", command, args)
		}
		shellDefaultSpawnTaskkill("nonexistent-taskkill-binary-xyz", args)
	}
	KillProcessTree(999999)
	if !taskkillCalled {
		t.Fatal("expected Windows branch to invoke taskkill")
	}

	// Unix real child tree kill (shell-kill-process-tree.test.ts)
	if runtime.GOOS != "windows" {
		shellPlatform = func() string { return runtime.GOOS }
		cmd := SpawnProcessIn(context.Background(), "", nil, "sh", []string{"-c", "sleep 30"})
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		pid := cmd.Process.Pid
		KillProcessTree(pid)
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = cmd.Process.Kill()
			t.Fatal("child process did not exit after KillProcessTree")
		}
	}
}

func TestFsWatchErrorHandler(t *testing.T) {
	// Issue #2791: fs.watch errors must invoke onError cleanly without panicking.
	tmp := t.TempDir()
	f := filepath.Join(tmp, "watched.txt")
	if err := os.WriteFile(f, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := WatchWithErrorHandler(f, func(eventType, filename string) {}, func() {})
	if w == nil {
		t.Fatal("expected non-nil FSWatcher")
	}
	CloseWatcher(w)
	CloseWatcher(nil)
}

func TestTerminalQuestion(t *testing.T) {
	// Cancelled before start returns empty string / aborts cleanly.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	ans, err := QuestionTerminal(ctx, "Continue? ", QuestionTerminalOptions{
		Input:  strings.NewReader("yes\n"),
		Output: &out,
	})
	var abortErr *AbortError
	if !errors.As(err, &abortErr) {
		t.Fatalf("expected *AbortError on canceled context, got ans=%q err=%v", ans, err)
	}

	// Normal answer trims newline cleanly.
	out.Reset()
	ans, err = QuestionTerminal(context.Background(), "Prompt: ", QuestionTerminalOptions{
		Input:  strings.NewReader("answer\n"),
		Output: &out,
	})
	if err != nil || ans != "answer" || out.String() != "Prompt: " {
		t.Fatalf("QuestionTerminal = (%q, %v), prompt=%q", ans, err, out.String())
	}
}

func TestChildProcessWait(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	res := SpawnProcessSync("sh", []string{"-c", "printf 'out'; printf 'err' >&2"}, nil)
	if res.Error != nil || res.Status != 0 || res.Stdout != "out" || res.Stderr != "err" {
		t.Fatalf("SpawnProcessSync = %+v", res)
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd := SpawnProcess("sh", []string{"-c", "printf 'hello'; printf 'warn' >&2; exit 2"})
	outPipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	errPipe, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	code, err := WaitForChildProcess(cmd, outPipe, errPipe, func(b []byte) { stdoutBuf.Write(b) }, func(b []byte) { stderrBuf.Write(b) })
	if err != nil || code != 2 || stdoutBuf.String() != "hello" || stderrBuf.String() != "warn" {
		t.Fatalf("WaitForChildProcess = (%d, %v) out=%q errOut=%q", code, err, stdoutBuf.String(), stderrBuf.String())
	}
}

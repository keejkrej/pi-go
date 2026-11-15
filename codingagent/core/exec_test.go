package core

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestHelperExecProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	args := os.Args
	sep := -1
	for i, arg := range args {
		if arg == "--" {
			sep = i
			break
		}
	}
	if sep == -1 || sep+1 >= len(args) {
		os.Exit(2)
	}
	mode := args[sep+1]
	switch mode {
	case "empty":
		os.Exit(0)
	case "silent-exit-1":
		os.Exit(1)
	case "write-x":
		_, _ = os.Stdout.WriteString("x")
		os.Exit(0)
	case "mixed-interval":
		_, _ = os.Stderr.WriteString(strings.Repeat("e", 600))
		_, _ = os.Stdout.WriteString(strings.Repeat("o", 600))
		for {
			_, _ = os.Stdout.WriteString(strings.Repeat("x", 1000))
			time.Sleep(10 * time.Millisecond)
		}
	case "write-payload":
		stream := args[sep+2]
		payload := args[sep+3]
		if stream == "stderr" {
			_, _ = os.Stderr.WriteString(payload)
		} else {
			_, _ = os.Stdout.WriteString(payload)
		}
		os.Exit(0)
	case "incomplete-utf8":
		_, _ = os.Stdout.Write([]byte{0xe2})
		_, _ = os.Stderr.Write([]byte{0xf0})
		os.Exit(0)
	case "repeat-a":
		_, _ = os.Stdout.WriteString(strings.Repeat("a", 1000))
		os.Exit(0)
	case "sleep":
		time.Sleep(15 * time.Second)
		os.Exit(0)
	case "sigkill-self":
		_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
		time.Sleep(5 * time.Second)
	case "ignore-sigterm":
		signal.Ignore(syscall.SIGTERM)
		_, _ = os.Stdout.WriteString("xx")
		time.Sleep(15 * time.Second)
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "unknown helper mode %s\n", mode)
		os.Exit(2)
	}
}

func texecHelperArgs(mode string, extra ...string) []string {
	args := []string{"-test.run=TestHelperExecProcess", "--", mode}
	return append(args, extra...)
}

func TestExecCommand(t *testing.T) {
	t.Setenv("GO_WANT_HELPER_PROCESS", "1")
	cwd := t.TempDir()

	t.Run("reports spawn errors in stderr", func(t *testing.T) {
		res, err := ExecCommand(context.Background(), "pi-command-that-does-not-exist-11085", nil, cwd)
		if err != nil {
			t.Fatalf("expected non-error result for ENOENT spawn, got err=%v", err)
		}
		if res.Code != 1 || res.Killed || res.Truncated || res.Stdout != "" || res.Stderr == "" {
			t.Fatalf("unexpected ENOENT result: %+v", res)
		}
	})

	t.Run("keeps stderr empty when command exits silently with 1", func(t *testing.T) {
		res, err := ExecCommand(context.Background(), os.Args[0], texecHelperArgs("silent-exit-1"), cwd)
		if err != nil {
			t.Fatal(err)
		}
		want := ExecResult{Stdout: "", Stderr: "", Code: 1, Killed: false, Truncated: false}
		if res != want {
			t.Fatalf("got %+v, want %+v", res, want)
		}
	})

	t.Run("rejects negative maxBuffer before spawn", func(t *testing.T) {
		neg := -1
		if _, err := ExecCommand(context.Background(), "pi-command-that-does-not-exist-11360", nil, cwd, &ExecOptions{
			MaxBuffer: &neg,
		}); err == nil {
			t.Fatalf("expected error for negative maxBuffer")
		}
	})

	t.Run("truncates all output when maxBuffer is zero", func(t *testing.T) {
		zero := 0
		res, err := ExecCommand(context.Background(), os.Args[0], texecHelperArgs("write-x"), cwd, &ExecOptions{
			MaxBuffer: &zero,
		})
		if err != nil {
			t.Fatal(err)
		}
		if res.Stdout != "" || res.Stderr != "" || !res.Truncated || !res.Killed || res.Code == 0 {
			t.Fatalf("unexpected zero maxBuffer result: %+v", res)
		}
	})

	t.Run("caps combined stdout+stderr at maxBuffer", func(t *testing.T) {
		capBytes := 1000
		res, err := ExecCommand(context.Background(), os.Args[0], texecHelperArgs("mixed-interval"), cwd, &ExecOptions{
			MaxBuffer: &capBytes,
		})
		if err != nil {
			t.Fatal(err)
		}
		if !res.Truncated || !res.Killed || res.Code == 0 {
			t.Fatalf("unexpected capped result: %+v", res)
		}
		if len(res.Stdout)+len(res.Stderr) != capBytes {
			t.Fatalf("combined len=%d, want %d", len(res.Stdout)+len(res.Stderr), capBytes)
		}
	})

	t.Run("keeps complete UTF-8 characters when capping", func(t *testing.T) {
		cases := []struct {
			stream    string
			payload   string
			maxBuffer int
			want      string
		}{
			{"stdout", "€", 1, ""},
			{"stderr", "€", 1, ""},
			{"stdout", "a€🙂", 5, "a€"},
			{"stderr", "a€🙂", 5, "a€"},
		}
		for _, tc := range cases {
			mb := tc.maxBuffer
			res, err := ExecCommand(context.Background(), os.Args[0], texecHelperArgs("write-payload", tc.stream, tc.payload), cwd, &ExecOptions{
				MaxBuffer: &mb,
			})
			if err != nil {
				t.Fatal(err)
			}
			got := res.Stdout
			if tc.stream == "stderr" {
				got = res.Stderr
			}
			if got != tc.want || !res.Truncated || !res.Killed || len(res.Stdout)+len(res.Stderr) > tc.maxBuffer {
				t.Fatalf("%+v -> got %+v", tc, res)
			}
		}
	})

	t.Run("flushes incomplete UTF-8 at normal EOF when untruncated", func(t *testing.T) {
		res, err := ExecCommand(context.Background(), os.Args[0], texecHelperArgs("incomplete-utf8"), cwd)
		if err != nil {
			t.Fatal(err)
		}
		want := ExecResult{Stdout: "\uFFFD", Stderr: "\uFFFD", Code: 0}
		if res != want {
			t.Fatalf("got %+v, want %+v", res, want)
		}
	})

	t.Run("leaves output up to maxBuffer unchanged", func(t *testing.T) {
		mb := 1000
		res, err := ExecCommand(context.Background(), os.Args[0], texecHelperArgs("repeat-a"), cwd, &ExecOptions{
			MaxBuffer: &mb,
		})
		if err != nil {
			t.Fatal(err)
		}
		if res.Truncated || res.Killed || res.Code != 0 || res.Stdout != strings.Repeat("a", 1000) {
			t.Fatalf("unexpected exact cap result: code=%d truncated=%v killed=%v len=%d", res.Code, res.Truncated, res.Killed, len(res.Stdout))
		}
	})

	if runtime.GOOS != "windows" {
		t.Run("reports 128+SIGTERM for timeout", func(t *testing.T) {
			timeout := int64(100)
			res, err := ExecCommand(context.Background(), os.Args[0], texecHelperArgs("sleep"), cwd, &ExecOptions{
				Timeout: &timeout,
			})
			if err != nil {
				t.Fatal(err)
			}
			if res.Code != 143 || !res.Killed || res.Truncated {
				t.Fatalf("got %+v, want code=143 killed=true truncated=false", res)
			}
		})

		t.Run("reports 128+SIGTERM for abort", func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			res, err := ExecCommand(ctx, os.Args[0], texecHelperArgs("sleep"), cwd)
			if err != nil {
				t.Fatal(err)
			}
			if res.Code != 143 || !res.Killed || res.Truncated {
				t.Fatalf("got %+v, want code=143 killed=true truncated=false", res)
			}
		})

		t.Run("reports 128+SIGKILL when killed externally", func(t *testing.T) {
			res, err := ExecCommand(context.Background(), os.Args[0], texecHelperArgs("sigkill-self"), cwd)
			if err != nil {
				t.Fatal(err)
			}
			if res.Code != 137 || res.Killed || res.Truncated {
				t.Fatalf("got %+v, want code=137 killed=false truncated=false", res)
			}
		})

		t.Run("returns when a shell child keeps pipe open after termination", func(t *testing.T) {
			mb := 1000
			res, err := ExecCommand(context.Background(), "sh", []string{"-c", "yes; echo done"}, cwd, &ExecOptions{
				MaxBuffer: &mb,
			})
			if err != nil {
				t.Fatal(err)
			}
			if !res.Killed || !res.Truncated || res.Stdout != strings.Repeat("y\n", 500) {
				t.Fatalf("got killed=%v truncated=%v len=%d", res.Killed, res.Truncated, len(res.Stdout))
			}
		})
	}
}

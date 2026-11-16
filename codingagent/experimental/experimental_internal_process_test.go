// Ported from packages/coding-agent/test/experimental-internal-process.test.ts (pi v1.0.0).

package experimental_test

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/codingagent/experimental"
)

func TestMain(m *testing.M) {
	if handled, err := experimental.MaybeRunInternalProcess(os.Args[1:]); handled {
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func teipShortTempDir(t *testing.T, prefix string) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", prefix)
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
	})
	return dir
}

func teipCanConnect(path string) bool {
	conn, err := net.DialTimeout("unix", path, 100*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func TestExperimentalInternalProcessLauncher(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix sockets require POSIX OS")
	}

	t.Run("starts the coordinator through the current runtime", func(t *testing.T) {
		dir := teipShortTempDir(t, "pi-ip-")
		publicPath := filepath.Join(dir, "p.sock")
		controlPath := filepath.Join(dir, "c.sock")

		child, err := experimental.SpawnInternalProcess(
			experimental.InternalRoleCoordinator,
			[]string{publicPath, controlPath},
			nil,
		)
		if err != nil {
			t.Fatalf("SpawnInternalProcess failed: %v", err)
		}
		t.Cleanup(func() {
			_ = experimental.TerminateInternalProcess(child)
		})

		deadline := time.Now().Add(10 * time.Second)
		for !teipCanConnect(controlPath) {
			if child.Exited() {
				t.Fatalf("coordinator exited early: %v", child.ExitError())
			}
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for coordinator control socket %s", controlPath)
			}
			time.Sleep(20 * time.Millisecond)
		}

		if child.Pid() == os.Getpid() || child.Pid() <= 0 {
			t.Fatalf("expected distinct child PID > 0, got %d (self=%d)", child.Pid(), os.Getpid())
		}
	})

	t.Run("waits for a failed activation child to terminate", func(t *testing.T) {
		dir := teipShortTempDir(t, "pi-ip-stop-")
		publicPath := filepath.Join(dir, "p.sock")
		controlPath := filepath.Join(dir, "c.sock")

		child, err := experimental.SpawnInternalProcess(
			experimental.InternalRoleCoordinator,
			[]string{publicPath, controlPath},
			nil,
		)
		if err != nil {
			t.Fatalf("SpawnInternalProcess failed: %v", err)
		}
		if child.Pid() <= 0 {
			t.Fatalf("expected child PID > 0, got %d", child.Pid())
		}

		if err := experimental.TerminateInternalProcess(child); err != nil {
			t.Fatalf("TerminateInternalProcess failed: %v", err)
		}
		if !child.Exited() {
			t.Fatal("expected child to have exited after TerminateInternalProcess")
		}
		if child.ExitError() == nil {
			t.Fatal("expected terminated/killed child to report non-nil exit error")
		}
	})
}

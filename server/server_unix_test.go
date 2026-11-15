// Ported from packages/server/test/unix.test.ts (pi v1.0.0).

package server_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	sysunix "golang.org/x/sys/unix"

	"github.com/keejkrej/pi-go/protocol"
	"github.com/keejkrej/pi-go/server"
	"github.com/keejkrej/pi-go/server/servertest"
	"github.com/keejkrej/pi-go/server/transports/unix"
)

const tsuServerId = protocol.ServerId("00000000-0000-4000-8000-000000000001")

func TestMain(m *testing.M) {
	if sockPath := os.Getenv("PI_GO_STALE_SOCKET_HELPER"); sockPath != "" {
		ln, err := net.Listen("unix", sockPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "listen failed: %v\n", err)
			os.Exit(1)
		}
		if ul, ok := ln.(*net.UnixListener); ok {
			ul.SetUnlinkOnClose(false)
		}
		fmt.Println("listening")
		_ = os.Stdout.Sync()
		select {}
	}
	os.Exit(m.Run())
}

func tsuCreateStaleSocketProcess(t *testing.T, sockPath string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable failed: %v", err)
	}
	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(), "PI_GO_STALE_SOCKET_HELPER="+sockPath)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe failed: %v", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("cmd.Start failed: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	})

	lineCh := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(stdout).ReadString('\n')
		lineCh <- line
	}()

	select {
	case line := <-lineCh:
		if strings.TrimSpace(line) != "listening" {
			t.Fatalf("unexpected helper output %q (stderr: %s)", line, stderr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for stale socket helper (stderr: %s)", stderr.String())
	}

	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("SIGKILL stale socket helper failed: %v", err)
	}
	_ = cmd.Wait()
}

func tsuMakeSocketPath(t *testing.T, nested bool) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "ps-")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if nested {
		return filepath.Join(dir, "p", "n", "server.sock")
	}
	return filepath.Join(dir, "server.sock")
}

func tsuMakeServer(t *testing.T, sockPath string) *server.Server[server.SessionMetadata] {
	t.Helper()
	srv, err := unix.CreateUnixServer(servertest.NewTestServerHost(), unix.UnixServerOptions{
		Path:     sockPath,
		ServerId: tsuServerId,
	})
	if err != nil {
		t.Fatalf("CreateUnixServer failed: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return srv
}

func TestCreatesInMemoryServerIdAndDerivesExplicitUnixSocketPath(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "pi-server-")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	sockPath, err := unix.GetUnixSocketPath(tsuServerId, dir)
	if err != nil {
		t.Fatalf("GetUnixSocketPath failed: %v", err)
	}
	wantPath := filepath.Join(dir, string(tsuServerId)+".sock")
	if sockPath != wantPath {
		t.Fatalf("expected %q, got %q", wantPath, sockPath)
	}

	first, err := unix.CreateUnixServer(servertest.NewTestServerHost(), unix.UnixServerOptions{
		ServerId: tsuServerId,
		Path:     sockPath,
	})
	if err != nil {
		t.Fatalf("CreateUnixServer 1 failed: %v", err)
	}
	t.Cleanup(func() { _ = first.Close() })
	if err := first.Start(); err != nil {
		t.Fatalf("first.Start failed: %v", err)
	}

	firstClient, err := servertest.ConnectUnixTestClient(sockPath)
	if err != nil {
		t.Fatalf("ConnectUnixTestClient 1 failed: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	msg, err := firstClient.Hello(ctx)
	if err != nil {
		t.Fatalf("firstClient.Hello failed: %v", err)
	}
	sh, ok := msg.(*protocol.ServerHello)
	if !ok || sh.ServerId != tsuServerId {
		t.Fatalf("unexpected hello 1: %#v", msg)
	}
	_ = firstClient.Close()
	if err := first.Close(); err != nil {
		t.Fatalf("first.Close failed: %v", err)
	}

	replacement, err := unix.CreateUnixServer(servertest.NewTestServerHost(), unix.UnixServerOptions{
		ServerId: tsuServerId,
		Path:     sockPath,
	})
	if err != nil {
		t.Fatalf("CreateUnixServer 2 failed: %v", err)
	}
	t.Cleanup(func() { _ = replacement.Close() })
	if err := replacement.Start(); err != nil {
		t.Fatalf("replacement.Start failed: %v", err)
	}

	replacementClient, err := servertest.ConnectUnixTestClient(sockPath)
	if err != nil {
		t.Fatalf("ConnectUnixTestClient 2 failed: %v", err)
	}
	defer func() { _ = replacementClient.Close() }()
	msg2, err := replacementClient.Hello(ctx)
	if err != nil {
		t.Fatalf("replacementClient.Hello failed: %v", err)
	}
	sh2, ok := msg2.(*protocol.ServerHello)
	if !ok || sh2.ServerId != tsuServerId {
		t.Fatalf("unexpected hello 2: %#v", msg2)
	}
}

func TestUnixListenerFilesystemLifecycle(t *testing.T) {
	t.Run("rejects a live listener without unlinking it", func(t *testing.T) {
		sockPath := tsuMakeSocketPath(t, false)
		first := tsuMakeServer(t, sockPath)
		if err := first.Start(); err != nil {
			t.Fatalf("first.Start failed: %v", err)
		}
		var firstStat sysunix.Stat_t
		if err := sysunix.Lstat(sockPath, &firstStat); err != nil {
			t.Fatalf("Lstat failed: %v", err)
		}

		second := tsuMakeServer(t, sockPath)
		if err := second.Start(); err == nil || !strings.Contains(err.Error(), "already running") {
			t.Fatalf("expected 'already running' error, got %v", err)
		}

		var currentStat sysunix.Stat_t
		if err := sysunix.Lstat(sockPath, &currentStat); err != nil {
			t.Fatalf("Lstat after failed start failed: %v", err)
		}
		if (currentStat.Mode & sysunix.S_IFMT) != sysunix.S_IFSOCK {
			t.Fatalf("expected socket mode, got %#o", currentStat.Mode)
		}
		if currentStat.Dev != firstStat.Dev || currentStat.Ino != firstStat.Ino {
			t.Fatalf("socket identity changed: (%d,%d) != (%d,%d)", currentStat.Dev, currentStat.Ino, firstStat.Dev, firstStat.Ino)
		}

		c, err := servertest.ConnectUnixTestClient(sockPath)
		if err != nil {
			t.Fatalf("ConnectUnixTestClient failed: %v", err)
		}
		defer func() { _ = c.Close() }()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		msg, err := c.Hello(ctx)
		if err != nil {
			t.Fatalf("Hello failed: %v", err)
		}
		if _, ok := msg.(*protocol.ServerHello); !ok {
			t.Fatalf("expected ServerHello, got %#v", msg)
		}
	})

	t.Run("never unlinks a regular file at the configured path", func(t *testing.T) {
		sockPath := tsuMakeSocketPath(t, false)
		if err := os.WriteFile(sockPath, []byte("do not remove"), 0o640); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}
		srv := tsuMakeServer(t, sockPath)
		if err := srv.Start(); err == nil || !strings.Contains(err.Error(), "non-socket") {
			t.Fatalf("expected 'non-socket' error, got %v", err)
		}
		data, err := os.ReadFile(sockPath)
		if err != nil || string(data) != "do not remove" {
			t.Fatalf("regular file corrupted or missing: %q (%v)", string(data), err)
		}
	})

	t.Run("creates nested temp parents, restricts permissions, and removes its own socket", func(t *testing.T) {
		sockPath := tsuMakeSocketPath(t, true)
		srv := tsuMakeServer(t, sockPath)
		if err := srv.Start(); err != nil {
			t.Fatalf("Start failed: %v", err)
		}

		var st sysunix.Stat_t
		if err := sysunix.Lstat(sockPath, &st); err != nil {
			t.Fatalf("Lstat failed: %v", err)
		}
		if (st.Mode & sysunix.S_IFMT) != sysunix.S_IFSOCK {
			t.Fatalf("expected S_IFSOCK, got %#o", st.Mode)
		}
		if runtime.GOOS != "windows" && (st.Mode&0o777) != 0o600 {
			t.Fatalf("expected mode 0600, got %#o", st.Mode&0o777)
		}

		entries, err := os.ReadDir(filepath.Dir(sockPath))
		if err != nil {
			t.Fatalf("ReadDir failed: %v", err)
		}
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		if !reflect.DeepEqual(names, []string{"server.sock"}) {
			t.Fatalf("expected only server.sock, got %v", names)
		}

		if err := srv.Close(); err != nil {
			t.Fatalf("Close failed: %v", err)
		}
		if _, err := os.Lstat(sockPath); !errors.Is(err, syscall.ENOENT) {
			t.Fatalf("expected ENOENT after Close, got %v", err)
		}
	})

	t.Run("does not remove a replacement inode during shutdown", func(t *testing.T) {
		sockPath := tsuMakeSocketPath(t, false)
		srv := tsuMakeServer(t, sockPath)
		if err := srv.Start(); err != nil {
			t.Fatalf("Start failed: %v", err)
		}
		if err := os.Remove(sockPath); err != nil {
			t.Fatalf("Remove failed: %v", err)
		}
		if err := os.WriteFile(sockPath, []byte("replacement"), 0o644); err != nil {
			t.Fatalf("WriteFile replacement failed: %v", err)
		}

		if err := srv.Close(); err != nil {
			t.Fatalf("Close failed: %v", err)
		}
		data, err := os.ReadFile(sockPath)
		if err != nil || string(data) != "replacement" {
			t.Fatalf("replacement file missing or changed: %q (%v)", string(data), err)
		}
	})

	t.Run("removes a genuinely stale socket before binding", func(t *testing.T) {
		sockPath := tsuMakeSocketPath(t, false)
		tsuCreateStaleSocketProcess(t, sockPath)

		var staleStat sysunix.Stat_t
		if err := sysunix.Lstat(sockPath, &staleStat); err != nil {
			t.Fatalf("Lstat stale failed: %v", err)
		}
		if (staleStat.Mode & sysunix.S_IFMT) != sysunix.S_IFSOCK {
			t.Fatalf("expected S_IFSOCK for stale socket, got %#o", staleStat.Mode)
		}

		srv := tsuMakeServer(t, sockPath)
		if err := srv.Start(); err != nil {
			t.Fatalf("Start over stale socket failed: %v", err)
		}

		var liveStat sysunix.Stat_t
		if err := sysunix.Lstat(sockPath, &liveStat); err != nil {
			t.Fatalf("Lstat live failed: %v", err)
		}
		if (liveStat.Mode & sysunix.S_IFMT) != sysunix.S_IFSOCK {
			t.Fatalf("expected S_IFSOCK for live socket, got %#o", liveStat.Mode)
		}

		c, err := servertest.ConnectUnixTestClient(sockPath)
		if err != nil {
			t.Fatalf("ConnectUnixTestClient failed: %v", err)
		}
		defer func() { _ = c.Close() }()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		msg, err := c.Hello(ctx)
		if err != nil {
			t.Fatalf("Hello failed: %v", err)
		}
		if _, ok := msg.(*protocol.ServerHello); !ok {
			t.Fatalf("expected ServerHello, got %#v", msg)
		}
	})
}

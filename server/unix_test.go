// Ported from packages/client/test/unix.test.ts (pi v1.0.0).

package server_test

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"syscall"
	"testing"
	"time"

	sysunix "golang.org/x/sys/unix"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/client"
	"github.com/keejkrej/pi-go/protocol"
	"github.com/keejkrej/pi-go/server"
	"github.com/keejkrej/pi-go/server/servertest"
	"github.com/keejkrej/pi-go/server/transports/unix"
)

func tunixMakeDirectory(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "pc-")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func tunixServerId(value int) protocol.ServerId {
	return protocol.ServerId(fmt.Sprintf("00000000-0000-4000-8000-%012x", value))
}

type tunixUnusedHost struct {
	serverServices server.RoutedServerServiceHost
}

func (h *tunixUnusedHost) ServerServices() server.RoutedServerServiceHost { return h.serverServices }
func (h *tunixUnusedHost) ResolveSession(string, chord.Context) (server.SessionMetadata, error) {
	return nil, errors.New("unused")
}
func (h *tunixUnusedHost) OpenSession(server.SessionMetadata, chord.Context) (server.RoutedSessionHandle, error) {
	return nil, errors.New("unused")
}

func tunixStartServer(t *testing.T, dir string, fileServerId protocol.ServerId, reported ...protocol.ServerId) *server.Server[server.SessionMetadata] {
	t.Helper()
	repId := fileServerId
	if len(reported) > 0 && reported[0] != "" {
		repId = reported[0]
	}
	sockPath := filepath.Join(dir, string(fileServerId)+".sock")
	listener, err := unix.CreateUnixListener(unix.UnixListenerOptions{Path: sockPath})
	if err != nil {
		t.Fatalf("CreateUnixListener failed: %v", err)
	}
	srv, err := server.NewServer[server.SessionMetadata](
		&tunixUnusedHost{serverServices: servertest.CreateTestServerServices()},
		server.ServerOptions{
			Listeners: []server.ServerListener{listener},
			ServerId:  repId,
		},
	)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	if err := srv.Start(); err != nil {
		t.Fatalf("srv.Start failed: %v", err)
	}
	return srv
}

type tunixConnStats struct {
	mu      sync.Mutex
	active  int
	maximum int
	total   int
}

func tunixStartSilentSocket(t *testing.T, sockPath string, stats *tunixConnStats) {
	t.Helper()
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("net.Listen unix %s failed: %v", sockPath, err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var conns []net.Conn
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		copied := append([]net.Conn(nil), conns...)
		mu.Unlock()
		for _, c := range copied {
			_ = c.Close()
		}
		wg.Wait()
	})

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, conn)
			mu.Unlock()

			if stats != nil {
				stats.mu.Lock()
				stats.active++
				if stats.active > stats.maximum {
					stats.maximum = stats.active
				}
				stats.total++
				stats.mu.Unlock()
			}

			wg.Add(1)
			go func(c net.Conn) {
				defer wg.Done()
				buf := make([]byte, 256)
				for {
					if _, err := c.Read(buf); err != nil {
						break
					}
				}
				_ = c.Close()
				if stats != nil {
					stats.mu.Lock()
					stats.active--
					stats.mu.Unlock()
				}
			}(conn)
		}
	}()
}

func TestDiscoverUnixServers(t *testing.T) {
	t.Run("returns no routes when the server directory is missing", func(t *testing.T) {
		dir := filepath.Join(tunixMakeDirectory(t), "missing")
		routes, err := client.DiscoverUnixServers(client.DiscoverUnixServersOptions{Directory: dir})
		if err != nil {
			t.Fatalf("DiscoverUnixServers failed: %v", err)
		}
		if len(routes) != 0 {
			t.Fatalf("expected 0 routes, got %+v", routes)
		}
	})

	t.Run("discovers reachable servers in server ID order", func(t *testing.T) {
		dir := tunixMakeDirectory(t)
		first := tunixServerId(1)
		second := tunixServerId(2)
		tunixStartServer(t, dir, second)
		tunixStartServer(t, dir, first)

		routes, err := client.DiscoverUnixServers(client.DiscoverUnixServersOptions{Directory: dir})
		if err != nil {
			t.Fatalf("DiscoverUnixServers failed: %v", err)
		}
		want := []client.UnixServerRoute{
			{ServerId: first, Path: filepath.Join(dir, string(first)+".sock")},
			{ServerId: second, Path: filepath.Join(dir, string(second)+".sock")},
		}
		if !reflect.DeepEqual(routes, want) {
			t.Fatalf("expected routes %+v, got %+v", want, routes)
		}
	})

	t.Run("ignores malformed entries, non-sockets, and mismatched servers", func(t *testing.T) {
		dir := tunixMakeDirectory(t)
		if err := os.WriteFile(filepath.Join(dir, string(tunixServerId(1))+".sock"), []byte("not a socket"), 0o644); err != nil {
			t.Fatalf("WriteFile 1 failed: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "not-a-server.sock"), []byte("ignored"), 0o644); err != nil {
			t.Fatalf("WriteFile 2 failed: %v", err)
		}
		if err := os.Mkdir(filepath.Join(dir, string(tunixServerId(2))+".sock"), 0o755); err != nil {
			t.Fatalf("Mkdir failed: %v", err)
		}
		tunixStartServer(t, dir, tunixServerId(3), tunixServerId(4))

		routes, err := client.DiscoverUnixServers(client.DiscoverUnixServersOptions{Directory: dir})
		if err != nil {
			t.Fatalf("DiscoverUnixServers failed: %v", err)
		}
		if len(routes) != 0 {
			t.Fatalf("expected 0 routes, got %+v", routes)
		}
	})

	t.Run("ignores stale sockets without deleting them", func(t *testing.T) {
		dir := tunixMakeDirectory(t)
		sockPath := filepath.Join(dir, string(tunixServerId(1))+".sock")
		tsuCreateStaleSocketProcess(t, sockPath)

		routes, err := client.DiscoverUnixServers(client.DiscoverUnixServersOptions{Directory: dir})
		if err != nil {
			t.Fatalf("DiscoverUnixServers failed: %v", err)
		}
		if len(routes) != 0 {
			t.Fatalf("expected 0 routes, got %+v", routes)
		}
		var st sysunix.Stat_t
		if err := sysunix.Lstat(sockPath, &st); err != nil || (st.Mode&sysunix.S_IFMT) != sysunix.S_IFSOCK {
			t.Fatalf("expected stale socket preserved, err=%v mode=%#o", err, st.Mode)
		}
	})

	t.Run("times out an unresponsive socket without deleting it", func(t *testing.T) {
		dir := tunixMakeDirectory(t)
		sockPath := filepath.Join(dir, string(tunixServerId(1))+".sock")
		tunixStartSilentSocket(t, sockPath, nil)

		timeout := 20
		routes, err := client.DiscoverUnixServers(client.DiscoverUnixServersOptions{
			Directory: dir,
			TimeoutMs: &timeout,
		})
		if err != nil {
			t.Fatalf("DiscoverUnixServers failed: %v", err)
		}
		if len(routes) != 0 {
			t.Fatalf("expected 0 routes, got %+v", routes)
		}
		var st sysunix.Stat_t
		if err := sysunix.Lstat(sockPath, &st); err != nil || (st.Mode&sysunix.S_IFMT) != sysunix.S_IFSOCK {
			t.Fatalf("expected silent socket preserved, err=%v mode=%#o", err, st.Mode)
		}
	})

	t.Run("limits concurrent probes to 16", func(t *testing.T) {
		dir := tunixMakeDirectory(t)
		stats := &tunixConnStats{}
		for index := 1; index <= 20; index++ {
			tunixStartSilentSocket(t, filepath.Join(dir, string(tunixServerId(index))+".sock"), stats)
		}

		type discRes struct {
			routes []client.UnixServerRoute
			err    error
		}
		ch := make(chan discRes, 1)
		timeout := 150
		go func() {
			r, e := client.DiscoverUnixServers(client.DiscoverUnixServersOptions{
				Directory: dir,
				TimeoutMs: &timeout,
			})
			ch <- discRes{r, e}
		}()

		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			stats.mu.Lock()
			active := stats.active
			stats.mu.Unlock()
			if active == 16 {
				break
			}
			time.Sleep(2 * time.Millisecond)
		}
		stats.mu.Lock()
		activeAtPeak := stats.active
		maxSoFar := stats.maximum
		totalAtPeak := stats.total
		stats.mu.Unlock()
		if activeAtPeak != 16 || maxSoFar != 16 || totalAtPeak != 16 {
			t.Fatalf("expected active==16 max==16 total==16 during discovery, got active=%d max=%d total=%d", activeAtPeak, maxSoFar, totalAtPeak)
		}

		res := <-ch
		if res.err != nil {
			t.Fatalf("DiscoverUnixServers failed: %v", res.err)
		}
		if len(res.routes) != 0 {
			t.Fatalf("expected 0 routes, got %+v", res.routes)
		}
		stats.mu.Lock()
		finalTotal := stats.total
		stats.mu.Unlock()
		if finalTotal != 20 {
			t.Fatalf("expected total=20, got total=%d", finalTotal)
		}
	})

	t.Run("ignores an endpoint that closes before its handshake", func(t *testing.T) {
		dir := tunixMakeDirectory(t)
		sockPath := filepath.Join(dir, string(tunixServerId(1))+".sock")
		ln, err := net.Listen("unix", sockPath)
		if err != nil {
			t.Fatalf("net.Listen failed: %v", err)
		}
		done := make(chan struct{})
		t.Cleanup(func() {
			_ = ln.Close()
			<-done
		})
		go func() {
			defer close(done)
			for {
				conn, err := ln.Accept()
				if err != nil {
					return
				}
				_ = conn.Close()
			}
		}()

		routes, err := client.DiscoverUnixServers(client.DiscoverUnixServersOptions{Directory: dir})
		if err != nil {
			t.Fatalf("DiscoverUnixServers failed: %v", err)
		}
		if len(routes) != 0 {
			t.Fatalf("expected 0 routes, got %+v", routes)
		}
	})

	t.Run("propagates unexpected filesystem errors", func(t *testing.T) {
		dir := tunixMakeDirectory(t)
		file := filepath.Join(dir, "not-a-directory")
		if err := os.WriteFile(file, []byte("content"), 0o644); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}
		_, err := client.DiscoverUnixServers(client.DiscoverUnixServersOptions{Directory: file})
		if !errors.Is(err, syscall.ENOTDIR) {
			t.Fatalf("expected ENOTDIR error, got %T (%v)", err, err)
		}
	})
}

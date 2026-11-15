// Ported from packages/client/test/unix.test.ts (pi v1.0.0).

package server_test

import (
	"context"
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
	"github.com/keejkrej/pi-go/internal/jsonx"
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

type tunixRemoteEndpointAttachment struct {
	endpoint chord.RemoteServiceEndpoint
}

func (a *tunixRemoteEndpointAttachment) InvokeService(call chord.ServiceCall, publish chord.ServiceUpdatePublisher, callContext chord.Context) (chord.JsonValue, bool, error) {
	return a.endpoint.Invoke(call, publish, callContext)
}

func (a *tunixRemoteEndpointAttachment) Release(chord.Context) error {
	a.endpoint.Dispose()
	return nil
}

type tunixRemoteServiceHost struct {
	provider *chord.RemoteServiceProvider
}

func (h *tunixRemoteServiceHost) AttachClient(server.RoutedServerPresentation, chord.Context) (server.RoutedServerServiceAttachment, error) {
	return &tunixRemoteEndpointAttachment{
		endpoint: chord.CreateRemoteServiceEndpoint(h.provider),
	}, nil
}

func TestUnixServerClientServiceCatalogueWithRemoteServiceEndpoint(t *testing.T) {
	tunixRunCatalogueCase := func(t *testing.T, withService bool) {
		t.Helper()
		dir := tunixMakeDirectory(t)
		serverId := tunixServerId(0xca7a109)
		sockPath := filepath.Join(dir, string(serverId)+".sock")

		var allow []chord.RemoteServiceProviderEntry
		type counterSvcIface struct{}
		var counterSvc *chord.Service[counterSvcIface]
		if withService {
			var err error
			counterSvc, err = chord.DefineService[counterSvcIface]("test.counter", nil)
			if err != nil {
				t.Fatalf("DefineService failed: %v", err)
			}
			allow = []chord.RemoteServiceProviderEntry{{Id: counterSvc.Id}}
		}
		provider, err := chord.NewRemoteServiceProvider(allow)
		if err != nil {
			t.Fatalf("NewRemoteServiceProvider failed: %v", err)
		}
		if withService {
			state, err := chord.NewReplicatedState[any](jsonx.ObjectOf("value", float64(42)))
			if err != nil {
				t.Fatalf("NewReplicatedState failed: %v", err)
			}
			if err := provider.Provide(counterSvc, jsonx.ObjectOf(
				"state", state,
				"ping", func(msg any, _ chord.Context) (chord.JsonValue, bool, error) {
					return msg, true, nil
				},
			)); err != nil {
				t.Fatalf("Provide failed: %v", err)
			}
		}

		listener, err := unix.CreateUnixListener(unix.UnixListenerOptions{Path: sockPath})
		if err != nil {
			t.Fatalf("CreateUnixListener failed: %v", err)
		}

		var errMu sync.Mutex
		var serverErrs []error
		srv, err := server.NewServer[server.SessionMetadata](
			&tunixUnusedHost{serverServices: &tunixRemoteServiceHost{provider: provider}},
			server.ServerOptions{
				Listeners: []server.ServerListener{listener},
				ServerId:  serverId,
				OnError: func(err error) {
					errMu.Lock()
					serverErrs = append(serverErrs, err)
					errMu.Unlock()
				},
			},
		)
		if err != nil {
			t.Fatalf("NewServer failed: %v", err)
		}
		t.Cleanup(func() { _ = srv.Close() })
		if err := srv.Start(); err != nil {
			t.Fatalf("srv.Start failed: %v", err)
		}

		factory, err := client.CreateUnixTransportFactory(client.UnixTransportOptions{Path: sockPath})
		if err != nil {
			t.Fatalf("CreateUnixTransportFactory failed: %v", err)
		}
		c, err := client.NewClient(client.ClientOptions{
			ServerId:         serverId,
			TransportFactory: factory,
		})
		if err != nil {
			t.Fatalf("NewClient failed: %v", err)
		}
		t.Cleanup(func() { _ = c.Dispose() })

		hello, err := c.Connect()
		if err != nil || hello.ServerId != serverId {
			t.Fatalf("Connect failed: hello=%+v err=%v", hello, err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		target := &protocol.ServerTarget{ServerId: serverId}

		catalogue, err := c.ServiceCatalogue(ctx, target)
		if err != nil {
			t.Fatalf("ServiceCatalogue failed: %v", err)
		}

		if withService {
			want := []chord.ServiceCatalogueEntry{{ServiceId: counterSvc.Id, Mode: chord.ServiceModeSingleton}}
			if !reflect.DeepEqual(catalogue, want) {
				t.Fatalf("unexpected catalogue: got %#v want %#v", catalogue, want)
			}

			val, ok, err := c.Request(ctx, target, chord.ServiceCall{
				ServiceId: counterSvc.Id,
				Member:    "ping",
				Args:      []chord.JsonValue{"pong"},
			})
			if err != nil || !ok || val != "pong" {
				t.Fatalf("subsequent Request failed after ServiceCatalogue: val=%#v ok=%v err=%v", val, ok, err)
			}

			sub, err := c.SubscribeService(ctx, target, counterSvc.Id, chord.ServiceModeSingleton, nil)
			if err != nil {
				t.Fatalf("SubscribeService failed after ServiceCatalogue: %v", err)
			}
			_ = sub.Dispose()
		} else if catalogue == nil || len(catalogue) != 0 {
			t.Fatalf("expected non-nil empty catalogue, got %#v", catalogue)
		}

		errMu.Lock()
		gotErrs := append([]error(nil), serverErrs...)
		errMu.Unlock()
		if len(gotErrs) != 0 {
			t.Fatalf("expected 0 server OnError callbacks, got %v", gotErrs)
		}
	}

	t.Run("encodes populated catalogue over unix socket", func(t *testing.T) {
		tunixRunCatalogueCase(t, true)
	})
	t.Run("encodes empty catalogue as non-nil JSON array", func(t *testing.T) {
		tunixRunCatalogueCase(t, false)
	})
}

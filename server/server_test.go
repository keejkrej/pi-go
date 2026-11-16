// Ported from packages/server/test/server.test.ts (pi v1.0.0).

package server_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/protocol"
	"github.com/keejkrej/pi-go/server"
	"github.com/keejkrej/pi-go/server/servertest"
	"github.com/keejkrej/pi-go/server/transports/unix"
)

const tserverServerId = protocol.ServerId("00000000-0000-4000-8000-000000000001")

func tserverMakeSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "pss-")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "server.sock")
}

type tserverTimedOutConn struct {
	mu         sync.Mutex
	closed     bool
	finalChunk []byte
	closedCh   chan struct{}
}

func (c *tserverTimedOutConn) Closed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *tserverTimedOutConn) Send([]byte) error {
	return errors.New("handshake timeout must use the terminal close frame")
}

func (c *tserverTimedOutConn) Close(finalChunk []byte) error {
	c.mu.Lock()
	c.finalChunk = append([]byte(nil), finalChunk...)
	c.closed = true
	c.mu.Unlock()
	close(c.closedCh)
	return nil
}

type tserverFailCloseListener struct {
	failure error
}

func (l *tserverFailCloseListener) Start(server.ByteConnectionAcceptor) error { return nil }
func (l *tserverFailCloseListener) Close() error                              { return l.failure }

func TestServerRequiresExplicitListenersAndCanonicalUUIDv4ServerId(t *testing.T) {
	host := servertest.NewTestServerHost()
	if _, err := server.NewServer(host, server.ServerOptions{}); err == nil || !strings.Contains(err.Error(), "listeners") {
		t.Fatalf("expected listeners validation error, got %v", err)
	}
	if _, err := server.NewServer(host, server.ServerOptions{Listeners: []server.ServerListener{}, ServerId: ""}); err == nil || !strings.Contains(err.Error(), "serverId") {
		t.Fatalf("expected empty serverId error, got %v", err)
	}
	if _, err := server.NewServer(host, server.ServerOptions{Listeners: []server.ServerListener{}, ServerId: "invalid-server"}); err == nil || !strings.Contains(err.Error(), "serverId") {
		t.Fatalf("expected invalid serverId error, got %v", err)
	}
}

func TestServerRejectsConcurrentStartCallsWithoutLeakingUnixListener(t *testing.T) {
	host := servertest.NewTestServerHost()
	sockPath := tserverMakeSocketPath(t)
	srv, err := unix.CreateUnixServer(host, unix.UnixServerOptions{
		Path:     sockPath,
		ServerId: tserverServerId,
	})
	if err != nil {
		t.Fatalf("CreateUnixServer failed: %v", err)
	}
	defer func() { _ = srv.Close() }()

	starting := make(chan error, 1)
	go func() {
		starting <- srv.Start()
	}()
	secondErr := srv.Start()
	firstErr := <-starting

	nilCount := 0
	dupErrCount := 0
	for _, err := range []error{firstErr, secondErr} {
		if err == nil {
			nilCount++
		} else if strings.Contains(err.Error(), "starting") || strings.Contains(err.Error(), "started") {
			dupErrCount++
		}
	}
	if nilCount != 1 || dupErrCount != 1 {
		t.Fatalf("expected 1 nil and 1 starting/started error, got first=%v second=%v", firstErr, secondErr)
	}

	if err := srv.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if _, err := os.Lstat(sockPath); !errors.Is(err, syscall.ENOENT) {
		t.Fatalf("expected ENOENT after close, got %v", err)
	}
}

func TestServerConcurrentStartWhileListenerBlocks(t *testing.T) {
	host := servertest.NewTestServerHost()
	entered := make(chan struct{})
	release := make(chan struct{})
	gated := &tserverGatedStartListener{entered: entered, release: release}
	srv, err := server.NewServer(host, server.ServerOptions{
		Listeners: []server.ServerListener{gated},
		ServerId:  tserverServerId,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	defer func() { _ = srv.Close() }()

	firstDone := make(chan error, 1)
	go func() {
		firstDone <- srv.Start()
	}()
	<-entered
	secondErr := srv.Start()
	if secondErr == nil || !strings.Contains(secondErr.Error(), "starting") {
		t.Fatalf("expected error containing 'starting', got %v", secondErr)
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first Start failed: %v", err)
	}
}

type tserverGatedStartListener struct {
	entered chan struct{}
	release chan struct{}
}

func (l *tserverGatedStartListener) Start(server.ByteConnectionAcceptor) error {
	close(l.entered)
	<-l.release
	return nil
}

func (l *tserverGatedStartListener) Close() error { return nil }

func TestServerHandshakeTimeoutClosesWithFinalHelloErrorFrame(t *testing.T) {
	host := servertest.NewTestServerHost()
	maxFrame := 1024
	timeoutMs := 10
	core, err := server.NewServer(host, server.ServerOptions{
		Listeners:          []server.ServerListener{},
		ServerId:           tserverServerId,
		MaxFrameLength:     &maxFrame,
		HandshakeTimeoutMs: &timeoutMs,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	defer func() { _ = core.Close() }()

	conn := &tserverTimedOutConn{closedCh: make(chan struct{})}
	core.Accept(conn)

	select {
	case <-conn.closedCh:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for handshake timeout close")
	}

	conn.mu.Lock()
	closed := conn.closed
	finalChunk := conn.finalChunk
	conn.mu.Unlock()
	if !closed || len(finalChunk) == 0 {
		t.Fatalf("expected closed connection with final frame, closed=%v len=%d", closed, len(finalChunk))
	}

	decoder, err := protocol.NewServerMessageDecoder(nil)
	if err != nil {
		t.Fatalf("NewServerMessageDecoder failed: %v", err)
	}
	msgs, err := decoder.Push(finalChunk)
	if err != nil {
		t.Fatalf("decoder.Push failed: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	he, ok := msgs[0].(*protocol.ServerHelloError)
	if !ok || he.Error.Code != "invalid_request" {
		t.Fatalf("expected ServerHelloError(invalid_request), got %#v", msgs[0])
	}
}

func TestServerRejectsTimeoutValuesAboveMaxTimerDelay(t *testing.T) {
	host := servertest.NewTestServerHost()
	tooBig := 2_147_483_648
	if _, err := unix.CreateUnixServer(host, unix.UnixServerOptions{
		Path:               "/tmp/pi-server-timeout-test.sock",
		ServerId:           tserverServerId,
		HandshakeTimeoutMs: &tooBig,
	}); err == nil || !strings.Contains(err.Error(), "handshakeTimeoutMs") {
		t.Fatalf("expected handshakeTimeoutMs error, got %v", err)
	}
	if _, err := unix.CreateUnixServer(host, unix.UnixServerOptions{
		Path:                   "/tmp/pi-server-timeout-test.sock",
		ServerId:               tserverServerId,
		GracefulCloseTimeoutMs: &tooBig,
	}); err == nil || !strings.Contains(err.Error(), "gracefulCloseTimeoutMs") {
		t.Fatalf("expected gracefulCloseTimeoutMs error, got %v", err)
	}
}

func TestServerRejectsPendingByteLimitsSmallerThanOneMaximumFrame(t *testing.T) {
	host := servertest.NewTestServerHost()
	sockPath := tserverMakeSocketPath(t)
	maxFrame := 128
	maxPending := 131
	if _, err := unix.CreateUnixServer(host, unix.UnixServerOptions{
		Path:            sockPath,
		ServerId:        tserverServerId,
		MaxFrameLength:  &maxFrame,
		MaxPendingBytes: &maxPending,
	}); err == nil || !strings.Contains(err.Error(), "maxPendingBytes") {
		t.Fatalf("expected maxPendingBytes validation error, got %v", err)
	}
}

func TestServerRejectsCloseAndClosedWhenListenerShutdownFails(t *testing.T) {
	host := servertest.NewTestServerHost()
	failure := errors.New("listener close failed")
	core, err := server.NewServer(host, server.ServerOptions{
		Listeners: []server.ServerListener{&tserverFailCloseListener{failure: failure}},
		ServerId:  tserverServerId,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	if err := core.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if err := core.Close(); !errors.Is(err, failure) {
		t.Fatalf("expected Close() to return %v, got %v", failure, err)
	}
	if err := core.Closed(); !errors.Is(err, failure) {
		t.Fatalf("expected Closed() to return %v, got %v", failure, err)
	}
}

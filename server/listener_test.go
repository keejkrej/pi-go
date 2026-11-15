// Ported from packages/server/test/listener.test.ts (pi v1.0.0).

package server_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/keejkrej/pi-go/server"
	"github.com/keejkrej/pi-go/server/servertest"
)

type ltTestListener struct {
	mu         sync.Mutex
	accept     server.ByteConnectionAcceptor
	startCount int
	closeCount int
	startError error
}

func (l *ltTestListener) Start(accept server.ByteConnectionAcceptor) error {
	l.mu.Lock()
	l.startCount++
	l.accept = accept
	err := l.startError
	l.mu.Unlock()
	return err
}

func (l *ltTestListener) Close() error {
	l.mu.Lock()
	l.closeCount++
	l.mu.Unlock()
	return nil
}

func (l *ltTestListener) snapshot() (hasAccept bool, startCount, closeCount int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.accept != nil, l.startCount, l.closeCount
}

func TestServerListenerComposition(t *testing.T) {
	t.Run("starts and closes every configured listener", func(t *testing.T) {
		first := &ltTestListener{}
		second := &ltTestListener{}
		ts, err := servertest.CreateTestServer(servertest.TestServerOptions{
			Listeners: []server.ServerListener{first, second},
		})
		if err != nil {
			t.Fatalf("CreateTestServer failed: %v", err)
		}

		if err := ts.Server.Start(); err != nil {
			t.Fatalf("Start failed: %v", err)
		}
		if hasAccept1, _, _ := first.snapshot(); !hasAccept1 {
			t.Fatal("expected first listener to receive acceptor")
		}
		if hasAccept2, _, _ := second.snapshot(); !hasAccept2 {
			t.Fatal("expected second listener to receive acceptor")
		}

		if err := ts.Server.Close(); err != nil {
			t.Fatalf("Close failed: %v", err)
		}
		_, _, cc1 := first.snapshot()
		_, _, cc2 := second.snapshot()
		if cc1 != 1 || cc2 != 1 {
			t.Fatalf("expected closeCounts (1, 1), got (%d, %d)", cc1, cc2)
		}
	})

	t.Run("closes previously started listeners when startup fails", func(t *testing.T) {
		first := &ltTestListener{}
		failure := errors.New("listener failed")
		second := &ltTestListener{startError: failure}
		ts, err := servertest.CreateTestServer(servertest.TestServerOptions{
			Listeners: []server.ServerListener{first, second},
		})
		if err != nil {
			t.Fatalf("CreateTestServer failed: %v", err)
		}

		if err := ts.Server.Start(); !errors.Is(err, failure) {
			t.Fatalf("expected Start error %v, got %v", failure, err)
		}
		_, _, cc1 := first.snapshot()
		_, _, cc2 := second.snapshot()
		if cc1 != 1 || cc2 != 0 {
			t.Fatalf("expected closeCounts (1, 0), got (%d, %d)", cc1, cc2)
		}
	})
}

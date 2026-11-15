// Ported from packages/server/test/unix-connection.test.ts (pi v1.0.0).

package unix_test

import (
	"bytes"
	"io"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/protocol"
	"github.com/keejkrej/pi-go/server/transports/unix"
)

type controlledSocket struct {
	mu                sync.Mutex
	firstWriteStarted chan struct{}
	firstWriteRelease chan struct{}
	firstWriteOnce    sync.Once
	endedCh           chan struct{}
	endedOnce         sync.Once
	ended             bool
	destroyed         bool
	writtenChunks     [][]byte
	finalChunk        []byte
}

func newControlledSocket() *controlledSocket {
	return &controlledSocket{
		firstWriteStarted: make(chan struct{}),
		firstWriteRelease: make(chan struct{}),
		endedCh:           make(chan struct{}),
	}
}

func (s *controlledSocket) Read([]byte) (int, error) {
	return 0, io.EOF
}

func (s *controlledSocket) Write(p []byte) (int, error) {
	isFirst := false
	s.firstWriteOnce.Do(func() {
		isFirst = true
		close(s.firstWriteStarted)
	})
	if isFirst {
		<-s.firstWriteRelease
	}
	copied := append([]byte(nil), p...)
	s.mu.Lock()
	s.writtenChunks = append(s.writtenChunks, copied)
	if !isFirst {
		s.finalChunk = append(s.finalChunk, copied...)
	}
	s.mu.Unlock()
	return len(p), nil
}

func (s *controlledSocket) CloseWrite() error {
	s.mu.Lock()
	s.ended = true
	s.mu.Unlock()
	s.endedOnce.Do(func() {
		close(s.endedCh)
	})
	return nil
}

func (s *controlledSocket) Close() error {
	s.mu.Lock()
	s.destroyed = true
	s.mu.Unlock()
	return nil
}

func (s *controlledSocket) snapshot() (ended, destroyed bool, finalChunk []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ended, s.destroyed, append([]byte(nil), s.finalChunk...)
}

func TestQueuesFinalProtocolErrorBehindPendingOutputBeforeClosing(t *testing.T) {
	sock := newControlledSocket()
	conn := unix.NewUnixByteConnection(sock, 1_000, 64*1024)

	pendingErrCh := make(chan error, 1)
	go func() {
		pendingErrCh <- conn.Send([]byte{1, 2, 3})
	}()

	select {
	case <-sock.firstWriteStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for first Write to enter socket")
	}

	finalMessage := &protocol.ServerHelloError{
		Type:  "hello_error",
		Error: protocol.ProtocolError{Code: "invalid_request", Message: "Protocol violation"},
	}
	finalFrame, err := protocol.EncodeServerMessage(finalMessage, nil)
	if err != nil {
		t.Fatalf("EncodeServerMessage failed: %v", err)
	}

	closeErrCh := make(chan error, 1)
	go func() {
		closeErrCh <- conn.Close(finalFrame)
	}()

	// Give Close a moment to enqueue behind the blocked write; socket must not end or destroy yet.
	time.Sleep(15 * time.Millisecond)
	ended, destroyed, _ := sock.snapshot()
	if ended || destroyed {
		t.Fatalf("expected ended=false destroyed=false before first write completes, got ended=%v destroyed=%v", ended, destroyed)
	}

	close(sock.firstWriteRelease)
	if err := <-pendingErrCh; err != nil {
		t.Fatalf("pending Send failed: %v", err)
	}

	select {
	case <-sock.endedCh:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for CloseWrite after pending output flushed")
	}

	_, _, gotFinal := sock.snapshot()
	if !bytes.Equal(gotFinal, finalFrame) {
		t.Fatalf("written final chunk %x did not match encoded frame %x", gotFinal, finalFrame)
	}
	decoder, err := protocol.NewServerMessageDecoder(nil)
	if err != nil {
		t.Fatalf("NewServerMessageDecoder failed: %v", err)
	}
	msgs, err := decoder.Push(gotFinal)
	if err != nil {
		t.Fatalf("decoder.Push failed: %v", err)
	}
	if !reflect.DeepEqual(msgs, []protocol.ServerMessage{finalMessage}) {
		t.Fatalf("expected decoded messages [%+v], got %#v", finalMessage, msgs)
	}

	conn.MarkClosed()
	if err := <-closeErrCh; err != nil {
		t.Fatalf("conn.Close failed: %v", err)
	}
}

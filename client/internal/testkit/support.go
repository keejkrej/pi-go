// Ported from packages/client/test/support.ts (pi v1.0.0).

package testkit

import (
	"context"
	"errors"
	"slices"
	"sync"

	"github.com/keejkrej/pi-go/client"
	"github.com/keejkrej/pi-go/protocol"
)

type suppMessageWaiter struct {
	count int
	done  chan struct{}
}

type suppTransport struct {
	server   *MemoryByteServer
	handlers client.ByteTransportHandlers
	decoder  *protocol.ClientMessageDecoder
	mu       sync.Mutex
	closed   bool
}

func (t *suppTransport) Send(chunk []byte) error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return errors.New("transport is closed")
	}
	messages, err := t.decoder.Push(chunk)
	t.mu.Unlock()
	if err != nil {
		return err
	}
	for _, msg := range messages {
		t.server.appendMessage(msg)
		if _, isHello := msg.(*protocol.ClientHello); isHello {
			if err := t.server.Send(&protocol.ServerHello{
				Type:     "hello",
				Version:  protocol.ProtocolVersion,
				ServerId: t.server.serverId,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (t *suppTransport) Close() {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return
	}
	t.closed = true
	t.mu.Unlock()

	t.server.onTransportClosed(t)
}

// MemoryByteServer is an in-memory protocol test server implementing client ByteTransport connections.
type MemoryByteServer struct {
	mu               sync.Mutex
	serverId         protocol.ServerId
	messages         []protocol.ClientMessage
	clientCloseCount int
	handlers         *client.ByteTransportHandlers
	activeTransport  *suppTransport
	messageWaiters   []*suppMessageWaiter
}

// NewMemoryByteServer returns a MemoryByteServer identifying as serverId
// (defaulting to "00000000-0000-4000-8000-000000000001").
func NewMemoryByteServer(serverId ...protocol.ServerId) *MemoryByteServer {
	id := protocol.ServerId("00000000-0000-4000-8000-000000000001")
	if len(serverId) > 0 && serverId[0] != "" {
		id = serverId[0]
	}
	return &MemoryByteServer{
		serverId: id,
	}
}

// ServerId returns the logical server identity advertised in hello responses.
func (s *MemoryByteServer) ServerId() protocol.ServerId {
	return s.serverId
}

// Messages returns a snapshot copy of received client messages.
func (s *MemoryByteServer) Messages() []protocol.ClientMessage {
	s.mu.Lock()
	copied := slices.Clone(s.messages)
	s.mu.Unlock()
	return copied
}

// ClientCloseCount returns the number of times client transports closed.
func (s *MemoryByteServer) ClientCloseCount() int {
	s.mu.Lock()
	count := s.clientCloseCount
	s.mu.Unlock()
	return count
}

// Connect implements client.ByteTransportFactory for in-memory connections.
func (s *MemoryByteServer) Connect(handlers client.ByteTransportHandlers) (client.ByteTransport, error) {
	decoder, err := protocol.NewClientMessageDecoder(nil)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	h := handlers
	t := &suppTransport{
		server:   s,
		handlers: h,
		decoder:  decoder,
	}
	s.handlers = &h
	s.activeTransport = t
	s.mu.Unlock()

	return t, nil
}

// WaitForMessages blocks until at least count client messages have been received or ctx is cancelled.
func (s *MemoryByteServer) WaitForMessages(ctx context.Context, count int) error {
	s.mu.Lock()
	if len(s.messages) >= count {
		s.mu.Unlock()
		return nil
	}
	waiter := &suppMessageWaiter{
		count: count,
		done:  make(chan struct{}),
	}
	s.messageWaiters = append(s.messageWaiters, waiter)
	s.mu.Unlock()

	if ctx == nil {
		<-waiter.done
		return nil
	}
	select {
	case <-waiter.done:
		return nil
	case <-ctx.Done():
		s.mu.Lock()
		for i, w := range s.messageWaiters {
			if w == waiter {
				s.messageWaiters = append(s.messageWaiters[:i], s.messageWaiters[i+1:]...)
				break
			}
		}
		s.mu.Unlock()
		return ctx.Err()
	}
}

// Send encodes and delivers a server message to the currently attached handlers.
func (s *MemoryByteServer) Send(message protocol.ServerMessage) error {
	frame, err := protocol.EncodeServerMessage(message, nil)
	if err != nil {
		return err
	}
	return s.SendRaw(frame)
}

// SendRaw delivers raw wire bytes to the currently attached handlers.
func (s *MemoryByteServer) SendRaw(chunk []byte) error {
	s.mu.Lock()
	handlers := s.handlers
	s.mu.Unlock()
	if handlers == nil {
		return errors.New("No client connection")
	}
	if handlers.OnData != nil {
		handlers.OnData(chunk)
	}
	return nil
}

// Disconnect simulates an orderly transport close from the server side.
func (s *MemoryByteServer) Disconnect() {
	s.mu.Lock()
	handlers := s.handlers
	s.handlers = nil
	s.mu.Unlock()
	if handlers != nil && handlers.OnClose != nil {
		handlers.OnClose()
	}
}

// Error simulates a terminal transport error from the server side.
func (s *MemoryByteServer) Error(err error) {
	s.mu.Lock()
	handlers := s.handlers
	s.handlers = nil
	s.mu.Unlock()
	if handlers != nil && handlers.OnError != nil {
		handlers.OnError(err)
	}
}

func (s *MemoryByteServer) appendMessage(msg protocol.ClientMessage) {
	s.mu.Lock()
	s.messages = append(s.messages, msg)
	msgLen := len(s.messages)
	var ready []*suppMessageWaiter
	remaining := s.messageWaiters[:0]
	for _, w := range s.messageWaiters {
		if msgLen >= w.count {
			ready = append(ready, w)
		} else {
			remaining = append(remaining, w)
		}
	}
	s.messageWaiters = remaining
	s.mu.Unlock()

	for _, w := range ready {
		close(w.done)
	}
}

func (s *MemoryByteServer) onTransportClosed(t *suppTransport) {
	s.mu.Lock()
	s.clientCloseCount++
	if s.activeTransport == t {
		s.handlers = nil
		s.activeTransport = nil
	}
	s.mu.Unlock()
}

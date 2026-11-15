// Ported from packages/server/src/connection.ts (pi v1.0.0).

package server

import (
	"context"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/protocol"
)

// ByteConnection is an established, authorized ordered byte connection.
type ByteConnection interface {
	Closed() bool
	Send(chunk []byte) error
	Close(finalChunk []byte) error
}

// ByteConnectionHandler receives inbound chunks and terminal events from a ByteConnection.
type ByteConnectionHandler struct {
	OnData  func(chunk []byte)
	OnClose func()
	OnError func(err error)
}

// ByteConnectionAcceptor binds an incoming ByteConnection to its server handler.
type ByteConnectionAcceptor func(connection ByteConnection) ByteConnectionHandler

type connStage int

const (
	connStageAwaitingHello connStage = iota
	connStageHandshaking
	connStageReady
	connStageClosing
	connStageClosed
)

type connActiveRequest struct {
	target protocol.RpcTarget
	cancel context.CancelCauseFunc
}

type connOutboundItem struct {
	encode func() ([]byte, error)
	done   chan error
}

type connOutboundQueue struct {
	mu         sync.Mutex
	closed     bool
	items      []*connOutboundItem
	notify     chan struct{}
	connection ByteConnection
	done       chan struct{}
}

func connNewOutboundQueue(connection ByteConnection) *connOutboundQueue {
	q := &connOutboundQueue{
		notify:     make(chan struct{}, 1),
		connection: connection,
		done:       make(chan struct{}),
	}
	go q.run()
	return q
}

func (q *connOutboundQueue) send(encode func() ([]byte, error)) error {
	item := &connOutboundItem{
		encode: encode,
		done:   make(chan error, 1),
	}
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return NewServerError("disconnected", "Connection is closed")
	}
	q.items = append(q.items, item)
	select {
	case q.notify <- struct{}{}:
	default:
	}
	q.mu.Unlock()

	return <-item.done
}

func (q *connOutboundQueue) stop() {
	q.mu.Lock()
	if !q.closed {
		q.closed = true
		select {
		case q.notify <- struct{}{}:
		default:
		}
	}
	q.mu.Unlock()
}

func (q *connOutboundQueue) run() {
	defer close(q.done)
	for {
		q.mu.Lock()
		for len(q.items) == 0 && !q.closed {
			q.mu.Unlock()
			<-q.notify
			q.mu.Lock()
		}
		if q.closed {
			remaining := q.items
			q.items = nil
			q.mu.Unlock()
			closedErr := NewServerError("disconnected", "Connection is closed")
			for _, item := range remaining {
				item.done <- closedErr
			}
			return
		}
		item := q.items[0]
		q.items[0] = nil
		q.items = q.items[1:]
		if len(q.items) == 0 {
			q.items = nil
		}
		q.mu.Unlock()

		frame, err := item.encode()
		if err != nil {
			item.done <- err
			continue
		}
		if q.connection.Closed() {
			item.done <- NewServerError("disconnected", "Connection is closed")
			continue
		}
		item.done <- q.connection.Send(frame)
	}
}

type connState struct {
	mu                   sync.Mutex
	decodeMu             sync.Mutex
	encodeMu             sync.Mutex
	connection           ByteConnection
	decoder              *protocol.ClientMessageDecoder
	serviceStateEncoders map[string]chord.ServiceStateEncoder
	stage                connStage
	disconnected         bool
	handshakeDone        chan struct{}
	pendingPostHello     []protocol.ClientMessage
	handshakeTimer       *time.Timer
	serverServices       RoutedServerServiceAttachment
	activeRequests       map[string]*connActiveRequest
	outbound             *connOutboundQueue
}

func (s *connState) isTerminal() bool {
	s.mu.Lock()
	terminal := s.disconnected || s.stage == connStageClosing || s.stage == connStageClosed
	s.mu.Unlock()
	return terminal
}

func (s *connState) isTerminalLocked() bool {
	return s.disconnected || s.stage == connStageClosing || s.stage == connStageClosed
}

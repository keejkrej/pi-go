// Ported from packages/mcp/src/transports/transport.ts (pi v1.0.0).

package mcp

import (
	"context"
	"sync"

	"github.com/keejkrej/pi-go/mcp/protocol"
)

// DefaultMaxMessageBytes is the default cap on one JSON-RPC message.
const DefaultMaxMessageBytes = 16 * 1024 * 1024

// McpTransportMessageListener receives one decoded JSON-RPC message.
type McpTransportMessageListener func(message protocol.JsonRpcMessage)

// McpTransportErrorListener receives a transport error.
type McpTransportErrorListener func(err error)

// McpTransportCloseListener is called once when the transport closes.
type McpTransportCloseListener func()

// McpTransport is a byte pipe for JSON-RPC messages.
// SetProtocolVersion is optional in TypeScript; the TransportEvents method is a no-op.
type McpTransport interface {
	Start() error
	Send(message protocol.JsonRpcMessage) error
	Close() error
	OnMessage(listener McpTransportMessageListener) (unsubscribe func())
	OnError(listener McpTransportErrorListener) (unsubscribe func())
	OnClose(listener McpTransportCloseListener) (unsubscribe func())
	SetProtocolVersion(version string)
}

// McpTransportSendContext is optionally implemented by transports that support
// per-send context cancellation (TS McpTransport.send(message, { signal })).
type McpTransportSendContext interface {
	SendWithContext(ctx context.Context, message protocol.JsonRpcMessage) error
}

type ttMessageListener struct {
	id uint64
	fn McpTransportMessageListener
}

type ttErrorListener struct {
	id uint64
	fn McpTransportErrorListener
}

type ttCloseListener struct {
	id uint64
	fn McpTransportCloseListener
}

// TransportEvents is the listener bookkeeping shared by transports.
// emitClose fires at most once per transport.
type TransportEvents struct {
	mu               sync.Mutex
	nextId           uint64
	messageListeners []ttMessageListener
	errorListeners   []ttErrorListener
	closeListeners   []ttCloseListener
	closeEmitted     bool
}

// OnMessage registers listener and returns an unsubscribe function.
func (t *TransportEvents) OnMessage(listener McpTransportMessageListener) func() {
	t.mu.Lock()
	defer t.mu.Unlock()
	id := t.nextId
	t.nextId++
	t.messageListeners = append(t.messageListeners, ttMessageListener{id: id, fn: listener})
	return func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		for i, l := range t.messageListeners {
			if l.id == id {
				t.messageListeners = append(t.messageListeners[:i], t.messageListeners[i+1:]...)
				return
			}
		}
	}
}

// OnError registers listener and returns an unsubscribe function.
func (t *TransportEvents) OnError(listener McpTransportErrorListener) func() {
	t.mu.Lock()
	defer t.mu.Unlock()
	id := t.nextId
	t.nextId++
	t.errorListeners = append(t.errorListeners, ttErrorListener{id: id, fn: listener})
	return func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		for i, l := range t.errorListeners {
			if l.id == id {
				t.errorListeners = append(t.errorListeners[:i], t.errorListeners[i+1:]...)
				return
			}
		}
	}
}

// OnClose registers listener and returns an unsubscribe function.
func (t *TransportEvents) OnClose(listener McpTransportCloseListener) func() {
	t.mu.Lock()
	defer t.mu.Unlock()
	id := t.nextId
	t.nextId++
	t.closeListeners = append(t.closeListeners, ttCloseListener{id: id, fn: listener})
	return func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		for i, l := range t.closeListeners {
			if l.id == id {
				t.closeListeners = append(t.closeListeners[:i], t.closeListeners[i+1:]...)
				return
			}
		}
	}
}

// SetProtocolVersion ignores version. Transports that speak a protocol version override it.
func (t *TransportEvents) SetProtocolVersion(version string) {}

func (t *TransportEvents) emitMessage(message protocol.JsonRpcMessage) {
	t.mu.Lock()
	listeners := append([]ttMessageListener(nil), t.messageListeners...)
	t.mu.Unlock()
	for _, l := range listeners {
		l.fn(message)
	}
}

func (t *TransportEvents) emitError(err any) {
	normalized := protocol.ToError(err)
	t.mu.Lock()
	listeners := append([]ttErrorListener(nil), t.errorListeners...)
	t.mu.Unlock()
	for _, l := range listeners {
		l.fn(normalized)
	}
}

func (t *TransportEvents) emitClose() {
	t.mu.Lock()
	if t.closeEmitted {
		t.mu.Unlock()
		return
	}
	t.closeEmitted = true
	listeners := append([]ttCloseListener(nil), t.closeListeners...)
	t.mu.Unlock()
	for _, l := range listeners {
		l.fn()
	}
}

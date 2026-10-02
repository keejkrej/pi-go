// Ported from packages/mcp/src/transports/transport.ts (pi v1.0.0).

package mcp

import (
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

// TransportEvents is the listener bookkeeping shared by transports.
// emitClose fires at most once per transport.
type TransportEvents struct {
	mu               sync.Mutex
	messageListeners []McpTransportMessageListener
	errorListeners   []McpTransportErrorListener
	closeListeners   []McpTransportCloseListener
	closeEmitted     bool
}

// OnMessage registers listener and returns an unsubscribe function.
func (t *TransportEvents) OnMessage(listener McpTransportMessageListener) func() {
	panic("unported: TransportEvents.OnMessage")
}

// OnError registers listener and returns an unsubscribe function.
func (t *TransportEvents) OnError(listener McpTransportErrorListener) func() {
	panic("unported: TransportEvents.OnError")
}

// OnClose registers listener and returns an unsubscribe function.
func (t *TransportEvents) OnClose(listener McpTransportCloseListener) func() {
	panic("unported: TransportEvents.OnClose")
}

// SetProtocolVersion ignores version. Transports that speak a protocol version override it.
func (t *TransportEvents) SetProtocolVersion(version string) {}

func (t *TransportEvents) emitMessage(message protocol.JsonRpcMessage) {
	panic("unported: TransportEvents.emitMessage")
}

func (t *TransportEvents) emitError(err any) {
	panic("unported: TransportEvents.emitError")
}

func (t *TransportEvents) emitClose() {
	panic("unported: TransportEvents.emitClose")
}

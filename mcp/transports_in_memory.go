// Ported from packages/mcp/src/transports/in-memory.ts (pi v1.0.0).

package mcp

import (
	"sync"

	"github.com/keejkrej/pi-go/mcp/protocol"
)

// InMemoryTransport is a paired in-process transport.
type InMemoryTransport struct {
	TransportEvents
	mu      sync.Mutex
	peer    *InMemoryTransport
	started bool
	closed  bool
}

// NewInMemoryTransport returns a transport with no peer.
func NewInMemoryTransport() *InMemoryTransport {
	panic("unported: NewInMemoryTransport")
}

// ConnectPeer attaches peer. It returns an error when this transport already has a peer
// ("In-memory MCP transport already has a peer").
func (t *InMemoryTransport) ConnectPeer(peer *InMemoryTransport) error {
	panic("unported: InMemoryTransport.ConnectPeer")
}

func (t *InMemoryTransport) Start() error {
	panic("unported: InMemoryTransport.Start")
}

func (t *InMemoryTransport) Send(message protocol.JsonRpcMessage) error {
	panic("unported: InMemoryTransport.Send")
}

func (t *InMemoryTransport) Close() error {
	panic("unported: InMemoryTransport.Close")
}

// EmitError reports a transport-level failure. Tests use it.
func (t *InMemoryTransport) EmitError(err any) {
	t.emitError(err)
}

func (t *InMemoryTransport) deliver(message protocol.JsonRpcMessage) {
	panic("unported: InMemoryTransport.deliver")
}

// InMemoryTransportPair is the client and server ends of one pipe.
type InMemoryTransportPair struct {
	Client *InMemoryTransport
	Server *InMemoryTransport
}

// CreateInMemoryTransportPair returns two transports connected to each other.
func CreateInMemoryTransportPair() *InMemoryTransportPair {
	panic("unported: CreateInMemoryTransportPair")
}

var _ McpTransport = (*InMemoryTransport)(nil)

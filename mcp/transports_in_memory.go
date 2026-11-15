// Ported from packages/mcp/src/transports/in-memory.ts (pi v1.0.0).

package mcp

import (
	"errors"
	"sync"

	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/mcp/protocol"
)

type timQueuedMessage struct {
	msg  protocol.JsonRpcMessage
	from *InMemoryTransport
}

// InMemoryTransport is a paired in-process transport.
type InMemoryTransport struct {
	TransportEvents
	mu         sync.Mutex
	peer       *InMemoryTransport
	started    bool
	closed     bool
	queue      []timQueuedMessage
	delivering bool
}

// NewInMemoryTransport returns a transport with no peer.
func NewInMemoryTransport() *InMemoryTransport {
	return &InMemoryTransport{}
}

// ConnectPeer attaches peer. It returns an error when this transport already has a peer
// ("In-memory MCP transport already has a peer").
func (t *InMemoryTransport) ConnectPeer(peer *InMemoryTransport) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.peer != nil {
		return errors.New("In-memory MCP transport already has a peer")
	}
	t.peer = peer
	return nil
}

func (t *InMemoryTransport) Start() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return protocol.NewMcpConnectionClosedError()
	}
	t.started = true
	return nil
}

func (t *InMemoryTransport) Send(message protocol.JsonRpcMessage) error {
	t.mu.Lock()
	if t.closed || !t.started || t.peer == nil {
		t.mu.Unlock()
		return protocol.NewMcpConnectionClosedError()
	}
	peer := t.peer
	t.mu.Unlock()

	raw, err := jsonx.Marshal(message)
	if err != nil {
		return err
	}
	cloned, err := protocol.UnmarshalJsonRpcMessage(raw)
	if err != nil {
		return err
	}
	peer.enqueueDeliver(cloned, t)
	return nil
}

func (t *InMemoryTransport) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	peer := t.peer
	t.peer = nil
	t.mu.Unlock()

	t.emitClose()
	if peer != nil {
		_ = peer.Close()
	}
	return nil
}

// EmitError reports a transport-level failure. Tests use it.
func (t *InMemoryTransport) EmitError(err any) {
	t.emitError(err)
}

func (t *InMemoryTransport) enqueueDeliver(message protocol.JsonRpcMessage, from *InMemoryTransport) {
	t.mu.Lock()
	t.queue = append(t.queue, timQueuedMessage{msg: message, from: from})
	if t.delivering {
		t.mu.Unlock()
		return
	}
	t.delivering = true
	t.mu.Unlock()
	go t.drainQueue()
}

func (t *InMemoryTransport) drainQueue() {
	for {
		t.mu.Lock()
		if len(t.queue) == 0 {
			t.delivering = false
			t.mu.Unlock()
			return
		}
		item := t.queue[0]
		copy(t.queue, t.queue[1:])
		t.queue[len(t.queue)-1] = timQueuedMessage{}
		t.queue = t.queue[:len(t.queue)-1]
		t.mu.Unlock()

		if item.from != nil {
			item.from.mu.Lock()
			senderClosed := item.from.closed
			item.from.mu.Unlock()
			if senderClosed {
				continue
			}
		}
		t.deliver(item.msg)
	}
}

func (t *InMemoryTransport) deliver(message protocol.JsonRpcMessage) {
	t.mu.Lock()
	active := !t.closed && t.started
	t.mu.Unlock()
	if !active {
		return
	}
	t.emitMessage(message)
}

// InMemoryTransportPair is the client and server ends of one pipe.
type InMemoryTransportPair struct {
	Client *InMemoryTransport
	Server *InMemoryTransport
}

// CreateInMemoryTransportPair returns two transports connected to each other.
func CreateInMemoryTransportPair() *InMemoryTransportPair {
	client := NewInMemoryTransport()
	server := NewInMemoryTransport()
	_ = client.ConnectPeer(server)
	_ = server.ConnectPeer(client)
	return &InMemoryTransportPair{
		Client: client,
		Server: server,
	}
}

var _ McpTransport = (*InMemoryTransport)(nil)

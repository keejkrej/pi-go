// Ported from packages/client/src/transport.ts (pi v1.0.0).

package client

// ByteTransport is an ordered byte stream connection to a remote server.
type ByteTransport interface {
	// Send sends one byte chunk. Calls must be delivered in invocation order.
	Send(chunk []byte) error
	// Close closes the transport. Implementations must make repeated calls harmless.
	Close()
}

// ByteTransportHandlers delivers inbound data and terminal lifecycle events from a transport.
type ByteTransportHandlers struct {
	// OnData delivers an arbitrary inbound byte chunk.
	OnData func(chunk []byte)
	// OnClose reports an orderly terminal close.
	OnClose func()
	// OnError reports a terminal transport failure.
	OnError func(err error)
}

// ByteTransportFactory creates a fresh connected, authenticated transport.
// Exactly one terminal handler invocation is expected.
type ByteTransportFactory func(handlers ByteTransportHandlers) (ByteTransport, error)

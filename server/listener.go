// Ported from packages/server/src/listener.ts (pi v1.0.0).

package server

// ServerListener supplies established byte connections after transport authentication.
type ServerListener interface {
	// Start begins listening and passes authorized connections to accept.
	Start(accept ByteConnectionAcceptor) error
	// Close shuts down the listener and releases underlying transport resources.
	Close() error
}

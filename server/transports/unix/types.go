// Ported from packages/server/src/transports/unix/types.ts (pi v1.0.0).

package unix

import "github.com/keejkrej/pi-go/protocol"

// UnixListenerOptions configures a Unix-domain socket ServerListener.
type UnixListenerOptions struct {
	Path string
	// Mode sets the socket filesystem permissions. Defaults to 0o600.
	Mode *int
	// MaxPendingBytes limits queued outbound bytes per connection before closing a slow peer.
	MaxPendingBytes        *int
	GracefulCloseTimeoutMs *int
	// MaxFrameLength validates MaxPendingBytes against the configured server frame limit.
	MaxFrameLength *int
	OnError        func(err error)
}

// UnixServerOptions composes ServerOptions and UnixListenerOptions for createUnixServer.
type UnixServerOptions struct {
	ServerId                 protocol.ServerId
	MaxFrameLength           *int
	HandshakeTimeoutMs       *int
	OnConnectionCountChanged func(count int)
	OnError                  func(err error)
	Path                     string
	Mode                     *int
	MaxPendingBytes          *int
	GracefulCloseTimeoutMs   *int
}

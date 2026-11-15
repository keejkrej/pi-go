// Ported from packages/server/src/transports/unix/preset.ts (pi v1.0.0).

package unix

import "github.com/keejkrej/pi-go/server"

// CreateUnixServer composes a Server with a single Unix-domain socket listener.
func CreateUnixServer[M server.SessionMetadata](host server.ServerHost[M], options UnixServerOptions) (*server.Server[M], error) {
	listener, err := CreateUnixListener(UnixListenerOptions{
		Path:                   options.Path,
		Mode:                   options.Mode,
		MaxFrameLength:         options.MaxFrameLength,
		MaxPendingBytes:        options.MaxPendingBytes,
		GracefulCloseTimeoutMs: options.GracefulCloseTimeoutMs,
		OnError:                options.OnError,
	})
	if err != nil {
		return nil, err
	}
	return server.NewServer(host, server.ServerOptions{
		Listeners:                []server.ServerListener{listener},
		MaxFrameLength:           options.MaxFrameLength,
		HandshakeTimeoutMs:       options.HandshakeTimeoutMs,
		OnConnectionCountChanged: options.OnConnectionCountChanged,
		ServerId:                 options.ServerId,
		OnError:                  options.OnError,
	})
}

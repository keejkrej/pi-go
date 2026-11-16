// Ported from packages/server/src/testing/server.ts (pi v1.0.0).

package servertest

import (
	"github.com/keejkrej/pi-go/protocol"
	"github.com/keejkrej/pi-go/server"
)

// TestServerOptions configures CreateTestServer.
type TestServerOptions struct {
	Listeners                []server.ServerListener
	Host                     server.ServerHost[server.SessionMetadata]
	ServerId                 protocol.ServerId
	MaxFrameLength           *int
	HandshakeTimeoutMs       *int
	OnConnectionCountChanged func(count int)
	OnError                  func(err error)
}

// TestServer bundles a Server and its host for conformance tests.
type TestServer struct {
	Server *server.Server[server.SessionMetadata]
	Host   server.ServerHost[server.SessionMetadata]
}

// CreateTestServer builds an unstarted Server with deterministic defaults for transport conformance tests.
func CreateTestServer(options TestServerOptions) (TestServer, error) {
	host := options.Host
	if host == nil {
		host = NewTestServerHost()
	}
	serverId := options.ServerId
	if serverId == "" {
		serverId = "00000000-0000-4000-8000-000000000001"
	}
	srv, err := server.NewServer(host, server.ServerOptions{
		Listeners:                options.Listeners,
		MaxFrameLength:           options.MaxFrameLength,
		HandshakeTimeoutMs:       options.HandshakeTimeoutMs,
		OnConnectionCountChanged: options.OnConnectionCountChanged,
		ServerId:                 serverId,
		OnError:                  options.OnError,
	})
	if err != nil {
		return TestServer{}, err
	}
	return TestServer{
		Server: srv,
		Host:   host,
	}, nil
}

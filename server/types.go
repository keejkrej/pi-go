// Ported from packages/server/src/types.ts (pi v1.0.0).

package server

import (
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/protocol"
)

// ServerOptions configures a Server instance.
type ServerOptions struct {
	Listeners []ServerListener
	// ServerId is the stable logical server identity supplied by the installation or profile.
	ServerId                 protocol.ServerId
	MaxFrameLength           *int
	HandshakeTimeoutMs       *int
	OnConnectionCountChanged func(count int)
	OnError                  func(err error)
}

// SessionMetadata represents the minimal durable session identity required for routing.
type SessionMetadata interface {
	SessionId() string
}

// BasicSessionMetadata is a minimal SessionMetadata carrying only an ID.
type BasicSessionMetadata struct {
	Id string `json:"id"`
}

// SessionId returns the durable session ID.
func (m BasicSessionMetadata) SessionId() string { return m.Id }

// RoutedSessionAttachment represents one client connection's live capability for a hosted session.
type RoutedSessionAttachment interface {
	// InvokeService routes one contract-agnostic service operation to the attached session endpoint.
	InvokeService(call chord.ServiceCall, publish chord.ServiceUpdatePublisher, callContext chord.Context) (chord.JsonValue, bool, error)
	Release(callContext chord.Context) error
}

// RoutedServerPresentation provides presentation-scoped session routing to server services.
type RoutedServerPresentation interface {
	AttachSession(sessionId string, callContext chord.Context) error
	DetachSession(callContext chord.Context) error
	// PrepareSessionRemoval releases routed attachments and handles before durable metadata deletion.
	PrepareSessionRemoval(sessionId string, callContext chord.Context) error
}

// RoutedServerServiceAttachment represents one connection's server-scoped service endpoint.
type RoutedServerServiceAttachment interface {
	InvokeService(call chord.ServiceCall, publish chord.ServiceUpdatePublisher, callContext chord.Context) (chord.JsonValue, bool, error)
	Release(callContext chord.Context) error
}

// RoutedServerServiceHost attaches server-wide service endpoints to client presentations.
type RoutedServerServiceHost interface {
	AttachClient(presentation RoutedServerPresentation, callContext chord.Context) (RoutedServerServiceAttachment, error)
}

// RoutedSessionHandle acquires presentation-scoped capabilities for one hosted session.
type RoutedSessionHandle interface {
	AttachClient(callContext chord.Context) (RoutedSessionAttachment, error)
	// Terminated optionally returns a channel that emits nil on clean close or an error
	// on unexpected termination. Callers may return nil when not tracked.
	Terminated() <-chan error
	Close(callContext chord.Context) error
}

// ServerHost provides application capabilities for server-wide management and session routing.
type ServerHost[M SessionMetadata] interface {
	ServerServices() RoutedServerServiceHost
	// ResolveSession resolves one durable session ID or returns a bounded routing error.
	ResolveSession(sessionId string, callContext chord.Context) (M, error)
	OpenSession(metadata M, callContext chord.Context) (RoutedSessionHandle, error)
}

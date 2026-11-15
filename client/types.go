// Ported from packages/client/src/types.ts (pi v1.0.0).

package client

import (
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/protocol"
)

// ConnectionState is the lifecycle state of a client connection.
type ConnectionState string

const (
	ConnectionStateDisconnected ConnectionState = "disconnected"
	ConnectionStateConnecting   ConnectionState = "connecting"
	ConnectionStateConnected    ConnectionState = "connected"
)

// ConnectionStateChange describes a transition between client connection states.
type ConnectionStateChange struct {
	State ConnectionState `json:"state"`
	Error error           `json:"error,omitzero"`
}

// Unsubscribe removes a previously registered listener.
type Unsubscribe func()

// ListenerErrorHandler observes exceptions thrown or returned by client listeners.
type ListenerErrorHandler func(err error)

// AttachmentChangeListener observes changes to the active server session attachment.
type AttachmentChangeListener func(attachment *protocol.SessionTarget)

// ServiceSubscription represents an active subscribed service stream on the client.
type ServiceSubscription interface {
	Id() string
	Target() protocol.RpcTarget
	Snapshot() chord.ServiceSubscriptionSnapshot
	// Start begins ordered update delivery after the caller has installed the snapshot.
	Start()
	Dispose() error
}

// ClientOptions configures a Client instance.
type ClientOptions struct {
	TransportFactory ByteTransportFactory
	// ServerId is the logical server identity expected at the physical endpoint.
	ServerId       protocol.ServerId
	MaxFrameLength *int
	// OnListenerError reports subscriber failures without allowing them to corrupt client state.
	OnListenerError ListenerErrorHandler
}

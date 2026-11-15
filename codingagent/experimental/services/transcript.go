// Ported from packages/coding-agent/src/experimental/services/transcript.ts (pi v1.0.0).

package services

import (
	"fmt"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
)

// Transcript exposes the root conversation's durable ConversationView as replicated state.
type Transcript interface {
	State() chord.ReplicatedState[durable.ConversationView]
}

// TranscriptService is the Chord service token for Transcript.
var TranscriptService = connDefineService[Transcript]("pi.transcript")

// ProvideTranscript builds a Chord provider member table for state.
func ProvideTranscript(state any) map[string]any {
	return map[string]any{
		"state": state,
	}
}

// AsTranscript wraps a Chord service handle or concrete implementation as Transcript.
func AsTranscript(raw any) Transcript {
	if raw == nil {
		return nil
	}
	if impl, ok := raw.(Transcript); ok {
		return impl
	}
	if view, ok := raw.(connServiceView); ok {
		return &connTranscriptClient{
			state: connWrapServiceStateMember[durable.ConversationView](view, "state"),
		}
	}
	panic(fmt.Sprintf("services: unsupported Transcript handle %T", raw))
}

type connTranscriptClient struct {
	state chord.ReplicatedState[durable.ConversationView]
}

func (c *connTranscriptClient) State() chord.ReplicatedState[durable.ConversationView] {
	return c.state
}

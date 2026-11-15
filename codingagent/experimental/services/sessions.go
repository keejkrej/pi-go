// Ported from packages/coding-agent/src/experimental/services/sessions.ts (pi v1.0.0).

package services

import (
	"fmt"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/protocol"
)

// SessionAddress identifies one session hosted on a server.
type SessionAddress struct {
	ServerId  protocol.ServerId `json:"serverId"`
	SessionId string            `json:"sessionId"`
}

// SessionSummary describes a persisted session entry in the server directory.
type SessionSummary struct {
	ServerId  protocol.ServerId `json:"serverId"`
	SessionId string            `json:"sessionId"`
	CreatedAt int64             `json:"createdAt"`
}

// SessionCreateOptions configures creation of a durable session.
type SessionCreateOptions struct {
	Id *string `json:"id,omitzero"`
}

// SessionDirectoryState is the replicated session directory document.
type SessionDirectoryState struct {
	Revision int              `json:"revision"`
	Sessions []SessionSummary `json:"sessions"`
}

// SessionDirectory exposes replicated directory state of available sessions.
type SessionDirectory interface {
	State() chord.ReplicatedState[SessionDirectoryState]
}

// SessionDirectoryService is the Chord service token for SessionDirectory.
var SessionDirectoryService = connDefineService[SessionDirectory]("pi.session-directory")

// SessionManagement provides lifecycle and attachment RPCs for sessions on a server.
type SessionManagement interface {
	Create(options SessionCreateOptions, ctx chord.Context) (SessionSummary, error)
	Remove(sessionId string, ctx chord.Context) error
	Attach(sessionId string, ctx chord.Context) error
	Detach(ctx chord.Context) error
}

// SessionManagementService is the Chord service token for SessionManagement.
var SessionManagementService = connDefineService[SessionManagement]("pi.session-management")

// ProvideSessionDirectory builds a Chord provider member table for a replicated directory state.
func ProvideSessionDirectory(state any) map[string]any {
	return map[string]any{
		"state": state,
	}
}

// AsSessionDirectory wraps a Chord service handle or concrete implementation as SessionDirectory.
func AsSessionDirectory(raw any) SessionDirectory {
	if raw == nil {
		return nil
	}
	if impl, ok := raw.(SessionDirectory); ok {
		return impl
	}
	if view, ok := raw.(connServiceView); ok {
		return &sessDirectoryClient{state: connWrapServiceStateMember[SessionDirectoryState](view, "state")}
	}
	panic(fmt.Sprintf("services: unsupported SessionDirectory handle %T", raw))
}

type sessDirectoryClient struct {
	state chord.ReplicatedState[SessionDirectoryState]
}

func (c *sessDirectoryClient) State() chord.ReplicatedState[SessionDirectoryState] {
	return c.state
}

// ProvideSessionManagement builds a Chord provider member table for impl.
func ProvideSessionManagement(impl SessionManagement) map[string]any {
	return map[string]any{
		"create": func(rawOpts any, ctx chord.Context) (any, error) {
			var opts SessionCreateOptions
			if rawOpts != nil {
				var err error
				opts, err = connDecodeJson[SessionCreateOptions](rawOpts)
				if err != nil {
					return nil, err
				}
			}
			summary, err := impl.Create(opts, ctx)
			if err != nil {
				return nil, err
			}
			return connEncodeJsonValue(summary)
		},
		"remove": func(sessionId string, ctx chord.Context) error {
			return impl.Remove(sessionId, ctx)
		},
		"attach": func(sessionId string, ctx chord.Context) error {
			return impl.Attach(sessionId, ctx)
		},
		"detach": func(ctx chord.Context) error {
			return impl.Detach(ctx)
		},
	}
}

// AsSessionManagement wraps a Chord service handle or concrete implementation as SessionManagement.
func AsSessionManagement(raw any) SessionManagement {
	if raw == nil {
		return nil
	}
	if impl, ok := raw.(SessionManagement); ok {
		return impl
	}
	if view, ok := raw.(connServiceView); ok {
		return &sessManagementClient{view: view}
	}
	panic(fmt.Sprintf("services: unsupported SessionManagement handle %T", raw))
}

type sessManagementClient struct {
	view connServiceView
}

func (c *sessManagementClient) Create(options SessionCreateOptions, ctx chord.Context) (SessionSummary, error) {
	var zero SessionSummary
	encoded, err := connEncodeJsonValue(options)
	if err != nil {
		return zero, err
	}
	raw, err := c.view.Call("create", encoded, ctx)
	if err != nil {
		return zero, err
	}
	return connDecodeJson[SessionSummary](raw)
}

func (c *sessManagementClient) Remove(sessionId string, ctx chord.Context) error {
	_, err := c.view.Call("remove", sessionId, ctx)
	return err
}

func (c *sessManagementClient) Attach(sessionId string, ctx chord.Context) error {
	_, err := c.view.Call("attach", sessionId, ctx)
	return err
}

func (c *sessManagementClient) Detach(ctx chord.Context) error {
	_, err := c.view.Call("detach", ctx)
	return err
}

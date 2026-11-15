// Ported from packages/coding-agent/src/experimental/services/plugins.ts (pi v1.0.0).

package services

import (
	"fmt"

	"github.com/keejkrej/pi-go/chord"
)

// PrepareSessionPluginsRequest asks the server to prepare plugin builds for a session.
type PrepareSessionPluginsRequest struct {
	SessionId    string   `json:"sessionId"`
	PackagePaths []string `json:"packagePaths"`
}

// PresentationPlugins manages server-built plugin generations available to presentations.
type PresentationPlugins interface {
	PrepareSession(request PrepareSessionPluginsRequest, ctx chord.Context) (chord.JsonValue, error)
	Reload(ctx chord.Context) (chord.JsonValue, error)
}

// PresentationPluginsService is the Chord service token for PresentationPlugins.
var PresentationPluginsService = connDefineService[PresentationPlugins]("pi.presentation-plugins")

// SessionPlugins manages plugin facets hosted in the currently attached Session worker.
type SessionPlugins interface {
	Reload(ctx chord.Context) error
}

// SessionPluginsService is the Chord service token for SessionPlugins.
var SessionPluginsService = connDefineService[SessionPlugins]("pi.session-plugins")

// ProvidePresentationPlugins builds a Chord provider member table for impl.
func ProvidePresentationPlugins(impl PresentationPlugins) map[string]any {
	return map[string]any{
		"prepareSession": func(rawReq any, ctx chord.Context) (any, error) {
			req, err := connDecodeJson[PrepareSessionPluginsRequest](rawReq)
			if err != nil {
				return nil, err
			}
			res, err := impl.PrepareSession(req, ctx)
			if err != nil {
				return nil, err
			}
			return connEncodeJsonValue(res)
		},
		"reload": func(ctx chord.Context) (any, error) {
			res, err := impl.Reload(ctx)
			if err != nil {
				return nil, err
			}
			return connEncodeJsonValue(res)
		},
	}
}

// AsPresentationPlugins wraps a Chord service handle or concrete implementation as PresentationPlugins.
func AsPresentationPlugins(raw any) PresentationPlugins {
	if raw == nil {
		return nil
	}
	if impl, ok := raw.(PresentationPlugins); ok {
		return impl
	}
	if view, ok := raw.(connServiceView); ok {
		return &plugPresentationClient{view: view}
	}
	panic(fmt.Sprintf("services: unsupported PresentationPlugins handle %T", raw))
}

type plugPresentationClient struct {
	view connServiceView
}

func (c *plugPresentationClient) PrepareSession(request PrepareSessionPluginsRequest, ctx chord.Context) (chord.JsonValue, error) {
	encoded, err := connEncodeJsonValue(request)
	if err != nil {
		return nil, err
	}
	return c.view.Call("prepareSession", encoded, ctx)
}

func (c *plugPresentationClient) Reload(ctx chord.Context) (chord.JsonValue, error) {
	return c.view.Call("reload", ctx)
}

// ProvideSessionPlugins builds a Chord provider member table for impl.
func ProvideSessionPlugins(impl SessionPlugins) map[string]any {
	return map[string]any{
		"reload": func(ctx chord.Context) error {
			return impl.Reload(ctx)
		},
	}
}

// AsSessionPlugins wraps a Chord service handle or concrete implementation as SessionPlugins.
func AsSessionPlugins(raw any) SessionPlugins {
	if raw == nil {
		return nil
	}
	if impl, ok := raw.(SessionPlugins); ok {
		return impl
	}
	if view, ok := raw.(connServiceView); ok {
		return &plugSessionClient{view: view}
	}
	panic(fmt.Sprintf("services: unsupported SessionPlugins handle %T", raw))
}

type plugSessionClient struct {
	view connServiceView
}

func (c *plugSessionClient) Reload(ctx chord.Context) error {
	_, err := c.view.Call("reload", ctx)
	return err
}

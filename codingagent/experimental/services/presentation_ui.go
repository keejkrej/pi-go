// Ported from packages/coding-agent/src/experimental/services/presentation-ui.ts (pi v1.0.0).

package services

import (
	"fmt"

	"github.com/keejkrej/pi-go/chord"
)

// PresentationSelectItem describes one choice presented via PresentationUI.Select.
type PresentationSelectItem struct {
	Value       string `json:"value"`
	Label       string `json:"label"`
	Description string `json:"description,omitzero"`
}

// PresentationUI exposes narrow process-local UI capabilities to presentation facets.
type PresentationUI interface {
	Select(title string, items []PresentationSelectItem, selectedValue *string, ctx chord.Context) (*string, error)
	ShowStatus(message string, ctx chord.Context)
}

// PresentationUIService is the process-local Chord service token for PresentationUI.
var PresentationUIService = connDefineService[PresentationUI]("pi.local.presentation-ui", true)

// ProvidePresentationUI builds a member map or direct provider for PresentationUI.
func ProvidePresentationUI(impl PresentationUI) PresentationUI {
	return impl
}

// AsPresentationUI unwraps a Chord handle or concrete implementation to PresentationUI.
func AsPresentationUI(raw any) PresentationUI {
	if raw == nil {
		return nil
	}
	if impl, ok := raw.(PresentationUI); ok {
		return impl
	}
	if view, ok := raw.(connServiceView); ok {
		return &puClient{view: view}
	}
	panic(fmt.Sprintf("services: unsupported PresentationUI handle %T", raw))
}

type puClient struct {
	view connServiceView
}

func (c *puClient) Select(title string, items []PresentationSelectItem, selectedValue *string, ctx chord.Context) (*string, error) {
	raw, err := c.view.Call("Select", title, items, selectedValue, ctx)
	if err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, nil
	}
	if ptr, ok := raw.(*string); ok {
		return ptr, nil
	}
	if s, ok := raw.(string); ok {
		return &s, nil
	}
	return nil, nil
}

func (c *puClient) ShowStatus(message string, ctx chord.Context) {
	_, _ = c.view.Call("ShowStatus", message, ctx)
}

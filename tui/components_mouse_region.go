// Ported from packages/tui/src/components/mouse-region.ts (pi v1.0.0).

package tui

// MouseRegionHandler handles a mouse event for a MouseRegion.
// Nil means the handler did not claim the event.
type MouseRegionHandler func(event TuiMouseEvent) *TuiMouseEventResult

// MouseRegion adds mouse handling to an existing component without changing its rendering.
type MouseRegion struct {
	child   Component
	onMouse MouseRegionHandler
}

// NewMouseRegion returns a region that forwards render and invalidate to child
// and handles mouse events with onMouse after the child declines them.
func NewMouseRegion(child Component, onMouse MouseRegionHandler) *MouseRegion {
	panic("unported: NewMouseRegion")
}

// Render returns the child render.
func (m *MouseRegion) Render(width int) []string {
	panic("unported: MouseRegion.Render")
}

// HandleMouse offers the event to the child, then to onMouse.
// The TypeScript return may be a TuiMouseDispatchResult; this signature matches the other component skeletons.
func (m *MouseRegion) HandleMouse(event TuiMouseEvent) *TuiMouseEventResult {
	panic("unported: MouseRegion.HandleMouse")
}

// Invalidate invalidates the child.
func (m *MouseRegion) Invalidate() {
	panic("unported: MouseRegion.Invalidate")
}

var _ Component = (*MouseRegion)(nil)

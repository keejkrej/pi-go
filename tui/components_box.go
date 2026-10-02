// Ported from packages/tui/src/components/box.ts (pi v1.0.0).

package tui

// cbMouseChild is one child row in a cached mouse hit layout.
type cbMouseChild struct {
	component Component
	height    int
}

// cbMouseLayout is the last content-width mouse map.
type cbMouseLayout struct {
	width    int
	children []cbMouseChild
}

// cbRenderCache is one cached padded render.
// bgSample nil means no background function was sampled.
type cbRenderCache struct {
	childLines []string
	width      int
	bgSample   *string
	lines      []string
}

// Box is a container that applies padding and background to its children.
// It implements Component. It does not extend Container.
// NewBox must treat the TypeScript defaults as paddingX 1 and paddingY 1.
// Zero padding is not that default. Nil bgFn means no background.
// Children is empty and non-nil after NewBox.
type Box struct {
	Children    []Component
	paddingX    int
	paddingY    int
	bgFn        func(text string) string
	cache       *cbRenderCache
	mouseLayout *cbMouseLayout
}

// NewBox returns a box. bgFn nil means no background.
func NewBox(paddingX int, paddingY int, bgFn func(text string) string) *Box {
	panic("unported: NewBox")
}

// AddChild appends component and drops the render cache.
func (b *Box) AddChild(component Component) {
	panic("unported: Box.AddChild")
}

// RemoveChild removes component when it is present and drops the render cache.
func (b *Box) RemoveChild(component Component) {
	panic("unported: Box.RemoveChild")
}

// Clear removes every child and drops the render cache.
func (b *Box) Clear() {
	panic("unported: Box.Clear")
}

// SetBgFn replaces the background function. It does not drop the cache;
// the next render samples bgFn("test") and misses when the sample changes.
func (b *Box) SetBgFn(bgFn func(text string) string) {
	panic("unported: Box.SetBgFn")
}

func (b *Box) invalidateCache() {
	panic("unported: Box.invalidateCache")
}

func (b *Box) matchCache(width int, childLines []string, bgSample *string) bool {
	panic("unported: Box.matchCache")
}

// Invalidate drops the render cache and invalidates every child.
func (b *Box) Invalidate() {
	panic("unported: Box.Invalidate")
}

// HandleMouse forwards the event into the child under the content cell.
// The TypeScript return is a TuiMouseDispatchResult; this signature matches the other component skeletons.
// Nil means the point is in the padding or misses every child.
func (b *Box) HandleMouse(event TuiMouseEvent) *TuiMouseEventResult {
	panic("unported: Box.HandleMouse")
}

// Render returns the padded, background-painted lines. An empty child list renders no lines.
func (b *Box) Render(width int) []string {
	panic("unported: Box.Render")
}

func (b *Box) applyBg(line string, width int) string {
	panic("unported: Box.applyBg")
}

var _ Component = (*Box)(nil)

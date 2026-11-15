// Ported from packages/tui/src/components/box.ts (pi v1.0.0).

package tui

import "strings"

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
	return &Box{
		Children: []Component{},
		paddingX: paddingX,
		paddingY: paddingY,
		bgFn:     bgFn,
	}
}

// AddChild appends component and drops the render cache.
func (b *Box) AddChild(component Component) {
	b.Children = append(b.Children, component)
	b.invalidateCache()
}

// RemoveChild removes component when it is present and drops the render cache.
func (b *Box) RemoveChild(component Component) {
	for i, child := range b.Children {
		if child == component {
			b.Children = append(b.Children[:i], b.Children[i+1:]...)
			b.invalidateCache()
			return
		}
	}
}

// Clear removes every child and drops the render cache.
func (b *Box) Clear() {
	b.Children = []Component{}
	b.invalidateCache()
}

// SetBgFn replaces the background function. It does not drop the cache;
// the next render samples bgFn("test") and misses when the sample changes.
func (b *Box) SetBgFn(bgFn func(text string) string) {
	b.bgFn = bgFn
}

// SetPaddingX sets the horizontal padding and drops the render cache.
func (b *Box) SetPaddingX(paddingX int) {
	b.paddingX = paddingX
	b.invalidateCache()
}

func (b *Box) invalidateCache() {
	b.cache = nil
}

func (b *Box) matchCache(width int, childLines []string, bgSample *string) bool {
	if b.cache == nil {
		return false
	}
	if b.cache.width != width || len(b.cache.childLines) != len(childLines) {
		return false
	}
	if (b.cache.bgSample == nil) != (bgSample == nil) {
		return false
	}
	if b.cache.bgSample != nil && *b.cache.bgSample != *bgSample {
		return false
	}
	for i, line := range childLines {
		if b.cache.childLines[i] != line {
			return false
		}
	}
	return true
}

// Invalidate drops the render cache and invalidates every child.
func (b *Box) Invalidate() {
	b.invalidateCache()
	for _, child := range b.Children {
		if child != nil {
			child.Invalidate()
		}
	}
}

// HandleMouse forwards the event into the child under the content cell.
// The TypeScript return is a TuiMouseDispatchResult; this signature matches the other component skeletons.
// Nil means the point is in the padding or misses every child.
func (b *Box) HandleMouse(event TuiMouseEvent) *TuiMouseEventResult {
	contentWidth := max(1, event.Width-b.paddingX*2)
	contentY := event.Y - b.paddingY
	contentX := event.X - b.paddingX
	if contentY < 0 || contentX < 0 || contentX >= contentWidth {
		return nil
	}

	var mouseChildren []cbMouseChild
	if b.mouseLayout != nil && b.mouseLayout.width == contentWidth {
		mouseChildren = b.mouseLayout.children
	} else {
		mouseChildren = make([]cbMouseChild, len(b.Children))
		for i, component := range b.Children {
			mouseChildren[i] = cbMouseChild{
				component: component,
				height:    len(component.Render(contentWidth)),
			}
		}
	}

	childY := 0
	for _, mc := range mouseChildren {
		if contentY >= childY && contentY < childY+mc.height {
			childEvent := event
			childEvent.X = contentX
			childEvent.Y = contentY - childY
			childEvent.Width = contentWidth
			childEvent.Height = mc.height
			res := DispatchMouseEvent(mc.component, childEvent)
			if res == nil {
				return nil
			}
			handled := res.Handled
			return &TuiMouseEventResult{
				Handled: &handled,
				Capture: res.Capture,
				Focus:   res.Focus,
				Render:  res.Render,
			}
		}
		childY += mc.height
	}
	return nil
}

// Render returns the padded, background-painted lines. An empty child list renders no lines.
func (b *Box) Render(width int) []string {
	if len(b.Children) == 0 {
		return []string{}
	}

	contentWidth := max(1, width-b.paddingX*2)
	leftPad := strings.Repeat(" ", max(0, b.paddingX))

	childLines := make([]string, 0)
	mouseChildren := make([]cbMouseChild, 0, len(b.Children))
	for _, child := range b.Children {
		lines := child.Render(contentWidth)
		mouseChildren = append(mouseChildren, cbMouseChild{component: child, height: len(lines)})
		childLines = append(childLines, lines...)
	}
	b.mouseLayout = &cbMouseLayout{width: contentWidth, children: mouseChildren}

	if len(childLines) == 0 {
		return []string{}
	}

	var bgSample *string
	if b.bgFn != nil {
		s := b.bgFn("test")
		bgSample = &s
	}

	if b.matchCache(width, childLines, bgSample) {
		return b.cache.lines
	}

	padY := max(0, b.paddingY)
	result := make([]string, 0, padY*2+len(childLines))
	for i := 0; i < padY; i++ {
		result = append(result, b.applyBg("", width))
	}
	for _, line := range childLines {
		result = append(result, b.applyBg(leftPad+line, width))
	}
	for i := 0; i < padY; i++ {
		result = append(result, b.applyBg("", width))
	}

	FlattenLines(result)
	b.cache = &cbRenderCache{
		childLines: childLines,
		width:      width,
		bgSample:   bgSample,
		lines:      result,
	}
	return result
}

func (b *Box) applyBg(line string, width int) string {
	visLen := VisibleWidth(line)
	padNeeded := max(0, width-visLen)
	padded := line + strings.Repeat(" ", padNeeded)
	if b.bgFn != nil {
		return b.bgFn(padded)
	}
	return padded
}

var _ Component = (*Box)(nil)

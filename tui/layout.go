// Ported from packages/tui/src/layout.ts (pi v1.0.0).

package tui

import "regexp"

// layoutOsc133ZonePrefix matches a leading OSC 133 zone marker (A, B, or C).
// Same pattern as layout.ts OSC133_ZONE_PREFIX. Replace is not global.
var layoutOsc133ZonePrefix = regexp.MustCompile("^(?:\x1b]133;[ABC](?:\x07|\x1b\\\\))+")

// LayoutRect is a terminal rectangle in cells. Field order is x, y, width, height.
type LayoutRect struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// LayoutBox is one laid-out component. Children and Parent are pointers so box
// identity and in-place rect updates match the TypeScript objects.
// Lines is nil when the box has no direct render. LineOffset nil means unset (treated as 0 when painting).
// Field order matches the object literals in layoutComponent.
type LayoutBox struct {
	Component          Component    `json:"component"`
	Rect               LayoutRect   `json:"rect"`
	Clip               LayoutRect   `json:"clip"`
	Children           []*LayoutBox `json:"children"`
	Parent             *LayoutBox   `json:"parent,omitzero"`
	Lines              []string     `json:"lines,omitzero"`
	LineOffset         *int         `json:"lineOffset,omitzero"`
	ScrollView         *ScrollView  `json:"scrollView,omitzero"`
	ScrollContentLines []string     `json:"scrollContentLines,omitzero"`
	Layer              int          `json:"layer"`
}

// LayoutFrame is one viewport layout. PrimaryScrollView is nil when no scroll view was seen.
// Field order matches the renderLayoutFrame return literal.
type LayoutFrame struct {
	Root              *LayoutBox  `json:"root"`
	Width             int         `json:"width"`
	Height            int         `json:"height"`
	Lines             []string    `json:"lines"`
	PrimaryScrollView *ScrollView `json:"primaryScrollView,omitzero"`
}

// ScrollbarGeometry is the painted scrollbar track. Field order matches the return literal.
type ScrollbarGeometry struct {
	Column       int `json:"column"`
	TrackTop     int `json:"trackTop"`
	TrackHeight  int `json:"trackHeight"`
	ThumbTop     int `json:"thumbTop"`
	ThumbHeight  int `json:"thumbHeight"`
	MaxScrollTop int `json:"maxScrollTop"`
}

// layoutContext is the state shared by one layout pass.
// renderCache keys are component identities; the dynamic value must be comparable.
type layoutContext struct {
	viewport          LayoutViewport
	renderCache       map[Component]map[int][]string
	requestRender     func()
	primaryScrollView *ScrollView
}

// GetScrollbarGeometry returns the scrollbar track for box.
// Nil means the scrollbar is not drawn. includeHiddenAuto false is the TypeScript default;
// true also returns geometry for an auto scrollbar that is currently hidden but overflowing.
func GetScrollbarGeometry(box *LayoutBox, includeHiddenAuto bool) *ScrollbarGeometry {
	panic("unported: GetScrollbarGeometry")
}

// RenderLayoutFrame lays out root in a width by height viewport and paints it.
// width and height are floored to at least 1. requestRender is stored on scroll views
// and may be called during layout.
func RenderLayoutFrame(root Component, width int, height int, requestRender func()) *LayoutFrame {
	panic("unported: RenderLayoutFrame")
}

// GetLayoutBoxesAt returns the visual hit path from the deepest component to the layout root.
// Order is layer descending, then depth descending. The result is empty when the point misses the root clip.
func GetLayoutBoxesAt(frame *LayoutFrame, x int, y int) []*LayoutBox {
	panic("unported: GetLayoutBoxesAt")
}

// GetScrollViewBox returns the layout box whose ScrollView is scrollView.
// Nil means the frame does not contain that scroll view.
func GetScrollViewBox(frame *LayoutFrame, scrollView *ScrollView) *LayoutBox {
	panic("unported: GetScrollViewBox")
}

// GetScrollViewsAt returns scroll views under the point, deepest first.
func GetScrollViewsAt(frame *LayoutFrame, x int, y int) []*ScrollView {
	panic("unported: GetScrollViewsAt")
}

func layoutIntersect(a LayoutRect, b LayoutRect) LayoutRect {
	panic("unported: layoutIntersect")
}

func layoutRenderCached(context *layoutContext, component Component, width int) []string {
	panic("unported: layoutRenderCached")
}

func layoutMeasureHeight(context *layoutContext, component Component, width int) int {
	panic("unported: layoutMeasureHeight")
}

func layoutMeasureWidth(context *layoutContext, component Component, width int) int {
	panic("unported: layoutMeasureWidth")
}

func layoutWithParent(box *LayoutBox, parent *LayoutBox) *LayoutBox {
	panic("unported: layoutWithParent")
}

func layoutTranslateBox(box *LayoutBox, deltaY int) {
	panic("unported: layoutTranslateBox")
}

func layoutUpdateClips(box *LayoutBox, parentClip LayoutRect) {
	panic("unported: layoutUpdateClips")
}

// height nil means the child's intrinsic height.
func layoutLayoutComponent(context *layoutContext, component Component, x int, y int, width int, height *int, clip LayoutRect) *LayoutBox {
	panic("unported: layoutLayoutComponent")
}

func layoutReplaceScrollbarCell(line string, column int, totalWidth int, replacement string, preserveTargetBackground bool) string {
	panic("unported: layoutReplaceScrollbarCell")
}

func layoutPaintScrollbar(box *LayoutBox, screen []string, totalWidth int) {
	panic("unported: layoutPaintScrollbar")
}

func layoutPaintBox(box *LayoutBox, screen []string, totalWidth int) {
	panic("unported: layoutPaintBox")
}

func layoutContainsPoint(rect LayoutRect, x int, y int) bool {
	panic("unported: layoutContainsPoint")
}

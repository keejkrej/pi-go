// Ported from packages/tui/src/layout.ts (pi v1.5.2).

package tui

import (
	"math"
	"reflect"
	"regexp"
	"slices"
	"strings"
)

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
	if box == nil || box.ScrollView == nil || box.Rect.Width <= 0 || box.Rect.Height <= 0 {
		return nil
	}

	contentHeight := len(box.ScrollContentLines)
	if len(box.Children) > 0 && box.Children[0] != nil {
		contentHeight = box.Children[0].Rect.Height
	}
	trackHeight := box.Rect.Height
	canRevealHiddenAuto := includeHiddenAuto && box.ScrollView.Scrollbar() == ScrollViewScrollbarAuto && contentHeight > trackHeight
	if !box.ScrollView.IsScrollbarVisible() && !canRevealHiddenAuto {
		return nil
	}

	minThumbHeight := min(2, trackHeight)
	var thumbHeight int
	if contentHeight <= 0 {
		thumbHeight = trackHeight
	} else {
		ratio := float64(trackHeight*trackHeight) / float64(contentHeight)
		rounded := int(math.Round(ratio))
		thumbHeight = max(minThumbHeight, min(trackHeight, rounded))
	}

	maxScrollTop := max(0, contentHeight-trackHeight)
	maxThumbTop := trackHeight - thumbHeight
	thumbOffset := 0
	if maxScrollTop != 0 {
		thumbOffset = int(math.Round((float64(box.ScrollView.ScrollTop()) / float64(maxScrollTop)) * float64(maxThumbTop)))
	}
	column := box.rectRightColumn()
	if column < box.Clip.X || column >= box.Clip.X+box.Clip.Width {
		return nil
	}

	return &ScrollbarGeometry{
		Column:       column,
		TrackTop:     box.Rect.Y,
		TrackHeight:  trackHeight,
		ThumbTop:     box.Rect.Y + thumbOffset,
		ThumbHeight:  thumbHeight,
		MaxScrollTop: maxScrollTop,
	}
}

func (b *LayoutBox) rectRightColumn() int {
	return b.Rect.X + b.Rect.Width - 1
}

// RenderLayoutFrame lays out root in a width by height viewport and paints it.
// width and height are floored to at least 1. requestRender is stored on scroll views
// and may be called during layout.
func RenderLayoutFrame(root Component, width int, height int, requestRender func()) *LayoutFrame {
	safeWidth := max(1, width)
	safeHeight := max(1, height)
	context := &layoutContext{
		viewport:    LayoutViewport{Width: safeWidth, Height: safeHeight},
		renderCache: make(map[Component]map[int][]string),
		requestRender: func() {
			if requestRender != nil {
				requestRender()
			}
		},
	}
	fullClip := LayoutRect{X: 0, Y: 0, Width: safeWidth, Height: safeHeight}
	rootBox := layoutLayoutComponent(context, root, 0, 0, safeWidth, &safeHeight, fullClip)
	lines := make([]string, safeHeight)
	layoutPaintBox(rootBox, lines, safeWidth)
	return &LayoutFrame{
		Root:              rootBox,
		Width:             safeWidth,
		Height:            safeHeight,
		Lines:             lines,
		PrimaryScrollView: context.primaryScrollView,
	}
}

// GetLayoutBoxesAt returns the visual hit path from the deepest component to the layout root.
// Order is layer descending, then depth descending. The result is empty when the point misses the root clip.
func GetLayoutBoxesAt(frame *LayoutFrame, x int, y int) []*LayoutBox {
	if frame == nil || frame.Root == nil {
		return []*LayoutBox{}
	}
	type hitEntry struct {
		box   *LayoutBox
		depth int
	}
	var result []hitEntry
	var visit func(box *LayoutBox, depth int)
	visit = func(box *LayoutBox, depth int) {
		if box == nil || !layoutContainsPoint(box.Clip, x, y) {
			return
		}
		result = append(result, hitEntry{box: box, depth: depth})
		for _, child := range box.Children {
			visit(child, depth+1)
		}
	}
	visit(frame.Root, 0)
	slices.SortStableFunc(result, func(a, b hitEntry) int {
		if a.box.Layer != b.box.Layer {
			return b.box.Layer - a.box.Layer
		}
		return b.depth - a.depth
	})
	out := make([]*LayoutBox, len(result))
	for i, entry := range result {
		out[i] = entry.box
	}
	return out
}

// GetScrollViewBox returns the layout box whose ScrollView is scrollView.
// Nil means the frame does not contain that scroll view.
func GetScrollViewBox(frame *LayoutFrame, scrollView *ScrollView) *LayoutBox {
	if frame == nil || frame.Root == nil || scrollView == nil {
		return nil
	}
	var visit func(box *LayoutBox) *LayoutBox
	visit = func(box *LayoutBox) *LayoutBox {
		if box == nil {
			return nil
		}
		if box.ScrollView == scrollView {
			return box
		}
		for _, child := range box.Children {
			if match := visit(child); match != nil {
				return match
			}
		}
		return nil
	}
	return visit(frame.Root)
}

// GetScrollViewsAt returns scroll views under the point, deepest first.
func GetScrollViewsAt(frame *LayoutFrame, x int, y int) []*ScrollView {
	if frame == nil || frame.Root == nil {
		return []*ScrollView{}
	}
	type scrollHit struct {
		scrollView *ScrollView
		depth      int
	}
	var result []scrollHit
	var visit func(box *LayoutBox, depth int)
	visit = func(box *LayoutBox, depth int) {
		if box == nil || !layoutContainsPoint(box.Clip, x, y) {
			return
		}
		if box.ScrollView != nil && layoutContainsPoint(box.Rect, x, y) {
			result = append(result, scrollHit{scrollView: box.ScrollView, depth: depth})
		}
		for _, child := range box.Children {
			visit(child, depth+1)
		}
	}
	visit(frame.Root, 0)
	slices.SortStableFunc(result, func(a, b scrollHit) int {
		return b.depth - a.depth
	})
	out := make([]*ScrollView, len(result))
	for i, entry := range result {
		out[i] = entry.scrollView
	}
	return out
}

func layoutIntersect(a LayoutRect, b LayoutRect) LayoutRect {
	x := max(a.X, b.X)
	y := max(a.Y, b.Y)
	right := min(a.X+a.Width, b.X+b.Width)
	bottom := min(a.Y+a.Height, b.Y+b.Height)
	return LayoutRect{
		X:      x,
		Y:      y,
		Width:  max(0, right-x),
		Height: max(0, bottom-y),
	}
}

func layoutRenderCached(context *layoutContext, component Component, width int) []string {
	safeWidth := max(1, width)
	if component == nil {
		return []string{}
	}
	comparable := reflect.TypeOf(component).Comparable()
	if comparable {
		if widths, ok := context.renderCache[component]; ok {
			if lines, found := widths[safeWidth]; found {
				return lines
			}
		}
	}
	lines := component.Render(safeWidth)
	if lines == nil {
		lines = []string{}
	}
	if comparable {
		widths, ok := context.renderCache[component]
		if !ok {
			widths = make(map[int][]string)
			context.renderCache[component] = widths
		}
		widths[safeWidth] = lines
	}
	return lines
}

func layoutMeasureHeight(context *layoutContext, component Component, width int) int {
	return len(layoutRenderCached(context, component, width))
}

func layoutMeasureWidth(context *layoutContext, component Component, width int) int {
	maxW := 0
	for _, line := range layoutRenderCached(context, component, width) {
		if w := VisibleWidth(line); w > maxW {
			maxW = w
		}
	}
	return maxW
}

func layoutWithParent(box *LayoutBox, parent *LayoutBox) *LayoutBox {
	box.Parent = parent
	return box
}

func layoutTranslateBox(box *LayoutBox, deltaY int) {
	if box == nil || deltaY == 0 {
		return
	}
	box.Rect.Y += deltaY
	for _, child := range box.Children {
		layoutTranslateBox(child, deltaY)
	}
}

func layoutUpdateClips(box *LayoutBox, parentClip LayoutRect) {
	if box == nil {
		return
	}
	box.Clip = layoutIntersect(parentClip, box.Rect)
	for _, child := range box.Children {
		layoutUpdateClips(child, box.Clip)
	}
}

// height nil means the child's intrinsic height.
func layoutLayoutComponent(context *layoutContext, component Component, x int, y int, width int, height *int, clip LayoutRect) *LayoutBox {
	safeWidth := max(1, width)
	node := GetLayoutNode(component)
	if node == nil {
		lines := layoutRenderCached(context, component, safeWidth)
		allocatedHeight := len(lines)
		if height != nil {
			allocatedHeight = max(0, *height)
		}
		lineOffset := 0
		if len(lines) > allocatedHeight && allocatedHeight > 0 {
			for i, line := range lines {
				if strings.Contains(line, CursorMarker) {
					if i >= allocatedHeight {
						lineOffset = i - allocatedHeight + 1
					}
					break
				}
			}
		}
		rect := LayoutRect{X: x, Y: y, Width: safeWidth, Height: allocatedHeight}
		return &LayoutBox{
			Component:  component,
			Rect:       rect,
			Clip:       layoutIntersect(clip, rect),
			Children:   []*LayoutBox{},
			Lines:      lines,
			LineOffset: &lineOffset,
			Layer:      0,
		}
	}

	if scrollNode, ok := node.(*ScrollLayoutNode); ok {
		previousScrollTop := scrollNode.State.ScrollTop()
		contentWidth := scrollNode.State.GetContentWidth(safeWidth)
		childBox := layoutLayoutComponent(
			context,
			scrollNode.Component,
			x,
			y-previousScrollTop,
			contentWidth,
			nil,
			clip,
		)
		contentHeight := childBox.Rect.Height
		viewportHeight := contentHeight
		if height != nil {
			viewportHeight = max(0, *height)
		}
		scrollNode.State.UpdateLayout(contentHeight, viewportHeight, context.requestRender)
		layoutTranslateBox(childBox, previousScrollTop-scrollNode.State.ScrollTop())
		scrollView, _ := scrollNode.State.(*ScrollView)
		if scrollView != nil && (scrollNode.State.Primary() || context.primaryScrollView == nil) {
			context.primaryScrollView = scrollView
		}
		rect := LayoutRect{X: x, Y: y, Width: safeWidth, Height: viewportHeight}
		childClip := layoutIntersect(clip, rect)
		box := &LayoutBox{
			Component:          component,
			Rect:               rect,
			Clip:               childClip,
			Children:           []*LayoutBox{childBox},
			ScrollView:         scrollView,
			ScrollContentLines: layoutRenderCached(context, scrollNode.Component, contentWidth),
			Layer:              0,
		}
		childBox.Parent = box
		layoutUpdateClips(childBox, childClip)
		return box
	}

	stackNode := node.(*StackLayoutNode)
	entries := VisibleStackEntries(stackNode.Entries, context.viewport)
	gapTotal := max(0, len(entries)-1) * stackNode.Gap

	if stackNode.Type == LayoutNodeTypeVstack {
		intrinsicHeights := make([]int, len(entries))
		for i, entry := range entries {
			if b, ok := entry.Basis.(*StackLayoutBasisCells); ok {
				intrinsicHeights[i] = b.Value
			} else {
				intrinsicHeights[i] = layoutMeasureHeight(context, entry.Component, safeWidth)
			}
		}
		sizes := AllocateStackSizes(entries, intrinsicHeights, height, stackNode.Gap)
		naturalHeight := gapTotal
		for _, size := range sizes {
			naturalHeight += size
		}
		allocatedHeight := naturalHeight
		if height != nil {
			allocatedHeight = max(0, *height)
		}
		rect := LayoutRect{X: x, Y: y, Width: safeWidth, Height: allocatedHeight}
		box := &LayoutBox{
			Component: component,
			Rect:      rect,
			Clip:      layoutIntersect(clip, rect),
			Children:  make([]*LayoutBox, 0, len(entries)),
			Layer:     0,
		}
		childY := y
		for i, entry := range entries {
			size := sizes[i]
			box.Children = append(box.Children, layoutWithParent(
				layoutLayoutComponent(context, entry.Component, x, childY, safeWidth, &size, box.Clip),
				box,
			))
			childY += size + stackNode.Gap
		}
		return box
	}

	// hstack
	intrinsicWidths := make([]int, len(entries))
	for i, entry := range entries {
		if b, ok := entry.Basis.(*StackLayoutBasisCells); ok {
			intrinsicWidths[i] = b.Value
		} else {
			intrinsicWidths[i] = layoutMeasureWidth(context, entry.Component, safeWidth)
		}
	}
	avail := safeWidth
	widths := AllocateStackSizes(entries, intrinsicWidths, &avail, stackNode.Gap)
	intrinsicHeights := make([]int, len(entries))
	maxNaturalHeight := 0
	for i, entry := range entries {
		h := layoutMeasureHeight(context, entry.Component, max(1, widths[i]))
		intrinsicHeights[i] = h
		if h > maxNaturalHeight {
			maxNaturalHeight = h
		}
	}
	allocatedHeight := maxNaturalHeight
	if height != nil {
		allocatedHeight = max(0, *height)
	}
	rect := LayoutRect{X: x, Y: y, Width: safeWidth, Height: allocatedHeight}
	box := &LayoutBox{
		Component: component,
		Rect:      rect,
		Clip:      layoutIntersect(clip, rect),
		Children:  make([]*LayoutBox, 0, len(entries)),
		Layer:     0,
	}
	childX := x
	for i, entry := range entries {
		naturalChildHeight := intrinsicHeights[i]
		childHeight := min(allocatedHeight, naturalChildHeight)
		if stackNode.Align == StackLayoutAlignStretch {
			childHeight = allocatedHeight
		}
		childY := y
		switch stackNode.Align {
		case StackLayoutAlignCenter:
			childY += (allocatedHeight - childHeight) / 2
		case StackLayoutAlignEnd:
			childY += allocatedHeight - childHeight
		}
		childWidth := widths[i]
		if childWidth == 0 {
			box.Children = append(box.Children, &LayoutBox{
				Component: entry.Component,
				Rect:      LayoutRect{X: childX, Y: childY, Width: 0, Height: childHeight},
				Clip:      LayoutRect{X: childX, Y: childY, Width: 0, Height: 0},
				Children:  []*LayoutBox{},
				Parent:    box,
				Layer:     0,
			})
		} else {
			box.Children = append(box.Children, layoutWithParent(
				layoutLayoutComponent(context, entry.Component, childX, childY, childWidth, &childHeight, box.Clip),
				box,
			))
		}
		childX += childWidth + stackNode.Gap
	}
	return box
}

func layoutReplaceScrollbarCell(line string, column int, totalWidth int, replacement string, preserveTargetBackground bool) string {
	if IsImageLine(line) {
		return line
	}

	graphemeRange := GetGraphemeCellRange(line, column)
	start := column
	end := column + 1
	if graphemeRange != nil {
		start = graphemeRange.Start
		end = graphemeRange.End
	}
	before := SliceByColumn(line, 0, start, true)
	target := SliceByColumn(line, start, end-start, true)
	after := SliceByColumn(line, end, max(0, totalWidth-end), true)

	var targetPrefix strings.Builder
	for bytePos := 0; bytePos < len(target); {
		code, byteLen := utilsExtractAnsiCodeBytes(target, bytePos)
		if byteLen == 0 {
			break
		}
		targetPrefix.WriteString(code)
		bytePos += byteLen
	}

	beforePadding := strings.Repeat(" ", max(0, start-VisibleWidth(before)))
	cellPaddingBefore := strings.Repeat(" ", max(0, column-start))
	cellPaddingAfter := strings.Repeat(" ", max(0, end-column-1))
	bgAnsi := ""
	if preserveTargetBackground {
		bgAnsi = GetActiveBackgroundAnsi(targetPrefix.String())
	}
	targetStyle := "\x1b[0m\x1b]8;;\x07" + bgAnsi
	return before + beforePadding + targetStyle + cellPaddingBefore + replacement + cellPaddingAfter + after
}

func layoutPaintScrollbar(box *LayoutBox, screen []string, totalWidth int) {
	geometry := GetScrollbarGeometry(box, false)
	if geometry == nil || box.ScrollView == nil {
		return
	}

	preserveTargetBg := box.ScrollView.Scrollbar() != ScrollViewScrollbarAlways
	for offset := 0; offset < geometry.TrackHeight; offset++ {
		row := geometry.TrackTop + offset
		if row < box.Clip.Y || row >= box.Clip.Y+box.Clip.Height || row < 0 || row >= len(screen) {
			continue
		}
		isThumb := row >= geometry.ThumbTop && row < geometry.ThumbTop+geometry.ThumbHeight
		var replacement string
		if isThumb {
			glyph := "┃"
			if box.ScrollView.IsScrollbarActive() {
				glyph = "█"
			}
			replacement = box.ScrollView.ScrollbarThumbStyle(glyph)
		} else {
			replacement = box.ScrollView.ScrollbarTrackStyle("│")
		}
		screen[row] = layoutReplaceScrollbarCell(
			screen[row],
			geometry.Column,
			totalWidth,
			replacement,
			preserveTargetBg,
		)
	}
}

func layoutPaintBox(box *LayoutBox, screen []string, totalWidth int) {
	if box == nil {
		return
	}
	if box.Lines != nil {
		offset := 0
		if box.LineOffset != nil {
			offset = *box.LineOffset
		}
		firstRow := max(box.Rect.Y, box.Clip.Y, 0)
		lastRow := min(box.Rect.Y+box.Rect.Height, box.Clip.Y+box.Clip.Height, len(screen))
		for row := firstRow; row < lastRow; row++ {
			idx := offset + row - box.Rect.Y
			if idx < 0 || idx >= len(box.Lines) {
				continue
			}
			line := box.Lines[idx]
			if loc := layoutOsc133ZonePrefix.FindStringIndex(line); loc != nil {
				line = line[loc[1]:]
			}
			if imageMetadata := GetKittyImageMetadata(line); imageMetadata != nil {
				clipBottom := min(len(screen), box.Clip.Y+box.Clip.Height)
				visibleRows := min(imageMetadata.Rows, clipBottom-row)
				if visibleRows < imageMetadata.Rows {
					line = CropKittyImageLine(line, 0, visibleRows)
				}
			}
			if box.Rect.X == 0 && box.Rect.Width >= totalWidth && (IsImageLine(line) || screen[row] == "") {
				screen[row] = line
			} else {
				screen[row] = CompositeTuiLine(screen[row], line, box.Rect.X, box.Rect.Width, totalWidth)
			}
		}
	}

	for _, child := range box.Children {
		layoutPaintBox(child, screen, totalWidth)
	}

	if box.ScrollView != nil && box.ScrollContentLines != nil && box.ScrollView.ScrollTop() > 0 && box.Rect.Height > 0 {
		scrollTop := box.ScrollView.ScrollTop()
		minRow := max(0, scrollTop-GetMaxKittyImageRows())
		for imageRow := scrollTop - 1; imageRow >= minRow; imageRow-- {
			if imageRow >= len(box.ScrollContentLines) {
				continue
			}
			imageLine := box.ScrollContentLines[imageRow]
			if !IsImageLine(imageLine) {
				continue
			}
			imageMetadata := GetKittyImageMetadata(imageLine)
			if imageMetadata == nil {
				continue
			}
			hiddenRows := scrollTop - imageRow
			if hiddenRows < imageMetadata.Rows {
				visibleRows := min(box.Rect.Height, imageMetadata.Rows-hiddenRows)
				cropped := CropKittyImageLine(imageLine, hiddenRows, visibleRows)
				if box.Rect.X == 0 && box.Rect.Width >= totalWidth && box.Rect.Y >= 0 && box.Rect.Y < len(screen) {
					screen[box.Rect.Y] = cropped
				}
			}
			break
		}
	}

	layoutPaintScrollbar(box, screen, totalWidth)
}

func layoutContainsPoint(rect LayoutRect, x int, y int) bool {
	return x >= rect.X && x < rect.X+rect.Width && y >= rect.Y && y < rect.Y+rect.Height
}

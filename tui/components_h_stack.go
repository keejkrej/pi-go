// Ported from packages/tui/src/components/h-stack.ts (pi v1.0.0).

package tui

// HStack is a horizontal stack. It implements Component and LayoutComponent.
// NewHStack must set layoutType to LayoutNodeTypeHstack.
// Nil children means no children. Nil options means gap 0 and align stretch.
// Zero align is not stretch.
type HStack struct {
	Stack
}

// NewHStack returns a horizontal stack.
func NewHStack(children []StackChild, options *StackOptions) *HStack {
	h := &HStack{}
	cstInitStack(&h.Stack, LayoutNodeTypeHstack, children, options)
	return h
}

// Render draws the visible children left to right at the stack's cross-axis alignment.
func (h *HStack) Render(width int) []string {
	safeWidth := max(1, width)
	viewport := LayoutViewport{Width: safeWidth, Height: cstMaxSafeInteger}
	entries := VisibleStackEntries(h.entries, viewport)
	if len(entries) == 0 {
		return []string{}
	}

	intrinsicWidths := make([]int, len(entries))
	for i, entry := range entries {
		lines := entry.Component.Render(safeWidth)
		maxW := 0
		for _, line := range lines {
			if w := VisibleWidth(line); w > maxW {
				maxW = w
			}
		}
		intrinsicWidths[i] = maxW
	}
	widths := AllocateStackSizes(entries, intrinsicWidths, &safeWidth, h.gap)
	rendered := make([][]string, len(entries))
	height := 0
	for i, entry := range entries {
		if widths[i] == 0 {
			rendered[i] = []string{}
		} else {
			rendered[i] = entry.Component.Render(widths[i])
		}
		if len(rendered[i]) > height {
			height = len(rendered[i])
		}
	}
	result := make([]string, height)
	x := 0
	for i, lines := range rendered {
		childWidth := widths[i]
		offset := 0
		switch h.align {
		case StackLayoutAlignCenter:
			offset = (height - len(lines)) / 2
		case StackLayoutAlignEnd:
			offset = height - len(lines)
		}
		for row, line := range lines {
			target := row + offset
			if target < 0 || target >= len(result) {
				continue
			}
			result[target] = CompositeTuiLine(result[target], line, x, childWidth, safeWidth)
		}
		x += childWidth + h.gap
	}
	return result
}

var (
	_ Component       = (*HStack)(nil)
	_ LayoutComponent = (*HStack)(nil)
)

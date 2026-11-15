// Ported from packages/tui/src/components/v-stack.ts (pi v1.0.0).

package tui

// The TypeScript re-exports of StackChild, StackEntry, StackEntryOptions, and StackOptions
// are not ported. Those types are declared in components_stack.go.

// VStack is a vertical stack. It implements Component and LayoutComponent.
// NewVStack must set layoutType to LayoutNodeTypeVstack.
// Nil children means no children. Nil options means gap 0 and align stretch.
// Zero align is not stretch.
type VStack struct {
	Stack
}

// NewVStack returns a vertical stack.
func NewVStack(children []StackChild, options *StackOptions) *VStack {
	v := &VStack{}
	cstInitStack(&v.Stack, LayoutNodeTypeVstack, children, options)
	return v
}

// Render draws the visible children top to bottom, including gap rows.
func (v *VStack) Render(width int) []string {
	viewport := LayoutViewport{Width: max(1, width), Height: cstMaxSafeInteger}
	entries := VisibleStackEntries(v.entries, viewport)
	rendered := make([][]string, len(entries))
	intrinsic := make([]int, len(entries))
	for i, entry := range entries {
		rendered[i] = entry.Component.Render(viewport.Width)
		intrinsic[i] = len(rendered[i])
	}
	sizes := AllocateStackSizes(entries, intrinsic, nil, v.gap)
	lines := make([]string, 0)
	for i := range entries {
		if i > 0 {
			for g := 0; g < v.gap; g++ {
				lines = append(lines, "")
			}
		}
		limit := min(len(rendered[i]), sizes[i])
		lines = append(lines, rendered[i][:limit]...)
		for padding := limit; padding < sizes[i]; padding++ {
			lines = append(lines, "")
		}
	}
	return lines
}

var (
	_ Component       = (*VStack)(nil)
	_ LayoutComponent = (*VStack)(nil)
)

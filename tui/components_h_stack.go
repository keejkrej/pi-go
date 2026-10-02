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
	panic("unported: NewHStack")
}

// Render draws the visible children left to right at the stack's cross-axis alignment.
func (h *HStack) Render(width int) []string {
	panic("unported: HStack.Render")
}

var (
	_ Component       = (*HStack)(nil)
	_ LayoutComponent = (*HStack)(nil)
)

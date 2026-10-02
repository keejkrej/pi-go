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
	panic("unported: NewVStack")
}

// Render draws the visible children top to bottom, including gap rows.
func (v *VStack) Render(width int) []string {
	panic("unported: VStack.Render")
}

var (
	_ Component       = (*VStack)(nil)
	_ LayoutComponent = (*VStack)(nil)
)

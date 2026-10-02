// Ported from packages/tui/src/components/stack.ts (pi v1.0.0).

package tui

// StackEntryOptions is the flex options for one stack child.
// Nil Basis, Grow, Shrink, MinSize, MaxSize, and Visible mean the field is omitted.
// Field order is basis, grow, shrink, minSize, maxSize, visible.
type StackEntryOptions struct {
	Basis   StackLayoutBasis          `json:"basis,omitzero"`
	Grow    *int                      `json:"grow,omitzero"`
	Shrink  *int                      `json:"shrink,omitzero"`
	MinSize *int                      `json:"minSize,omitzero"`
	MaxSize *int                      `json:"maxSize,omitzero"`
	Visible func(LayoutViewport) bool `json:"-"`
}

// StackEntry is a component plus flex options. It extends StackEntryOptions.
// Field order matches addChild's object literal: component, then the option fields.
type StackEntry struct {
	Component Component `json:"component"`
	StackEntryOptions
}

// StackChild is a bare component or a StackEntry (TS Component | StackEntry).
type StackChild interface{ isStackChild() }

// StackChildComponent is a bare component child.
type StackChildComponent struct {
	Component Component
}

func (*StackChildComponent) isStackChild() {}

// StackChildEntry is a StackEntry child.
type StackChildEntry struct {
	Entry StackEntry
}

func (*StackChildEntry) isStackChild() {}

// StackOptions configures a stack. Nil Gap means 0. Nil Align means stretch.
// The zero StackLayoutAlign is not stretch.
type StackOptions struct {
	Gap   *int              `json:"gap,omitzero"`
	Align *StackLayoutAlign `json:"align,omitzero"`
}

// cstDistributeMode selects grow or shrink distribution.
type cstDistributeMode string

const (
	cstDistributeModeGrow   cstDistributeMode = "grow"
	cstDistributeModeShrink cstDistributeMode = "shrink"
)

// Stack is the shared vertical and horizontal stack. It is abstract in TypeScript;
// construct a VStack or an HStack. It embeds Container and implements LayoutComponent
// once layoutType is set.
// entries is empty and non-nil after construction. Zero align is not stretch.
// Zero layoutType is not vstack or hstack; NewVStack and NewHStack must set it.
type Stack struct {
	Container
	entries    []StackLayoutEntry
	gap        int
	align      StackLayoutAlign
	layoutType LayoutNodeType
}

// AddChild appends component. Nil options means no flex overrides.
func (s *Stack) AddChild(component Component, options *StackEntryOptions) {
	panic("unported: Stack.AddChild")
}

// RemoveChild removes component from the container and from entries.
func (s *Stack) RemoveChild(component Component) {
	panic("unported: Stack.RemoveChild")
}

// Clear removes every child and every entry.
func (s *Stack) Clear() {
	panic("unported: Stack.Clear")
}

// LayoutNode returns the stack's layout description.
func (s *Stack) LayoutNode() LayoutNode {
	panic("unported: Stack.LayoutNode")
}

// VisibleStackEntries keeps entries whose visible predicate passes.
// A nil predicate keeps the entry.
func VisibleStackEntries(entries []StackLayoutEntry, viewport LayoutViewport) []StackLayoutEntry {
	panic("unported: VisibleStackEntries")
}

// AllocateStackSizes returns a main-axis size for each entry.
// availableSize nil means return the clamped intrinsic sizes without grow or shrink.
func AllocateStackSizes(entries []StackLayoutEntry, intrinsicSizes []int, availableSize *int, gap int) []int {
	panic("unported: AllocateStackSizes")
}

func cstIsStackEntry(child StackChild) bool {
	panic("unported: cstIsStackEntry")
}

// value nil means fallback. Non-finite numbers also use fallback.
func cstNormalizeSize(value *int, fallback int) int {
	panic("unported: cstNormalizeSize")
}

func cstClampSize(size int, entry StackLayoutEntry) int {
	panic("unported: cstClampSize")
}

func cstDistribute(sizes []int, entries []StackLayoutEntry, amount int, mode cstDistributeMode) {
	panic("unported: cstDistribute")
}

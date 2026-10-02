// Ported from packages/tui/src/layout-node.ts (pi v1.0.0).

package tui

// LayoutNodeType is the LayoutNode discriminator.
type LayoutNodeType string

const (
	LayoutNodeTypeVstack LayoutNodeType = "vstack"
	LayoutNodeTypeHstack LayoutNodeType = "hstack"
	LayoutNodeTypeScroll LayoutNodeType = "scroll"
)

// LayoutViewport is the size passed to stack visibility predicates.
type LayoutViewport struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// StackLayoutAlign is how a stack places children on the cross axis.
type StackLayoutAlign string

const (
	StackLayoutAlignStretch StackLayoutAlign = "stretch"
	StackLayoutAlignStart   StackLayoutAlign = "start"
	StackLayoutAlignCenter  StackLayoutAlign = "center"
	StackLayoutAlignEnd     StackLayoutAlign = "end"
)

// StackLayoutBasis is a flex basis: a cell count or "auto".
type StackLayoutBasis interface{ isStackLayoutBasis() }

// StackLayoutBasisCells is a numeric flex basis, in cells.
type StackLayoutBasisCells struct {
	Value int
}

func (*StackLayoutBasisCells) isStackLayoutBasis() {}

// StackLayoutBasisAuto is the "auto" flex basis.
type StackLayoutBasisAuto struct{}

func (*StackLayoutBasisAuto) isStackLayoutBasis() {}

// ScrollLayoutOverscroll is what a scroll view does with leftover wheel events.
type ScrollLayoutOverscroll string

const (
	ScrollLayoutOverscrollChain   ScrollLayoutOverscroll = "chain"
	ScrollLayoutOverscrollContain ScrollLayoutOverscroll = "contain"
)

// StackLayoutEntry is one child of a stack layout node.
// Field order matches the StackLayoutEntry interface.
type StackLayoutEntry struct {
	Component Component         `json:"component"`
	Basis     StackLayoutBasis  `json:"basis,omitzero"`
	Grow      *int              `json:"grow,omitzero"`
	Shrink    *int              `json:"shrink,omitzero"`
	MinSize   *int              `json:"minSize,omitzero"`
	MaxSize   *int              `json:"maxSize,omitzero"`
	Visible   func(LayoutViewport) bool `json:"-"`
}

// StackLayoutNode is a vertical or horizontal stack.
type StackLayoutNode struct {
	Type    LayoutNodeType     `json:"type"`
	Entries []StackLayoutEntry `json:"entries"`
	Gap     int                `json:"gap"`
	Align   StackLayoutAlign   `json:"align"`
}

func (*StackLayoutNode) isLayoutNode() {}

// ScrollLayoutState is the live scroll view a scroll layout node reads and updates.
// ScrollTop, Primary, Overscroll, and ViewportHeight are the TypeScript readonly properties.
type ScrollLayoutState interface {
	ScrollTop() int
	Primary() bool
	Overscroll() ScrollLayoutOverscroll
	ViewportHeight() int
	GetContentWidth(width int) int
	UpdateLayout(contentHeight int, viewportHeight int, requestRender func())
}

// ScrollLayoutNode is a scrollable child.
type ScrollLayoutNode struct {
	Type      LayoutNodeType    `json:"type"`
	Component Component         `json:"component"`
	State     ScrollLayoutState `json:"state"`
}

func (*ScrollLayoutNode) isLayoutNode() {}

// LayoutNode is a stack or scroll layout description.
// The TypeScript LAYOUT_NODE symbol is not ported; LayoutComponent.LayoutNode replaces it.
type LayoutNode interface{ isLayoutNode() }

// LayoutComponent is a component that publishes a layout node.
type LayoutComponent interface {
	Component
	LayoutNode() LayoutNode
}

// GetLayoutNode returns the component's layout node, or nil when it has none.
func GetLayoutNode(component Component) LayoutNode {
	panic("unported: GetLayoutNode")
}

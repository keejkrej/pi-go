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

// cstMaxSafeInteger matches JS Number.MAX_SAFE_INTEGER.
const cstMaxSafeInteger = 9007199254740991

// cstInitStack initializes embedded Stack state for NewVStack and NewHStack.
func cstInitStack(s *Stack, layoutType LayoutNodeType, children []StackChild, options *StackOptions) {
	s.layoutType = layoutType
	s.entries = []StackLayoutEntry{}
	s.align = StackLayoutAlignStretch
	if options != nil {
		s.gap = cstNormalizeSize(options.Gap, 0)
		if options.Align != nil {
			s.align = *options.Align
		}
	}
	for _, child := range children {
		if child == nil {
			continue
		}
		if cstIsStackEntry(child) {
			entry := child.(*StackChildEntry).Entry
			opts := entry.StackEntryOptions
			s.AddChild(entry.Component, &opts)
		} else if compChild, ok := child.(*StackChildComponent); ok {
			s.AddChild(compChild.Component, nil)
		}
	}
}

// AddChild appends component. Nil options means no flex overrides.
func (s *Stack) AddChild(component Component, options *StackEntryOptions) {
	s.Container.AddChild(component)
	entry := StackLayoutEntry{Component: component}
	if options != nil {
		if options.Basis != nil {
			entry.Basis = options.Basis
		}
		if options.Grow != nil {
			v := cstNormalizeSize(options.Grow, 0)
			entry.Grow = &v
		}
		if options.Shrink != nil {
			v := cstNormalizeSize(options.Shrink, 1)
			entry.Shrink = &v
		}
		if options.MinSize != nil {
			v := cstNormalizeSize(options.MinSize, 0)
			entry.MinSize = &v
		}
		if options.MaxSize != nil {
			v := cstNormalizeSize(options.MaxSize, cstMaxSafeInteger)
			entry.MaxSize = &v
		}
		if options.Visible != nil {
			entry.Visible = options.Visible
		}
	}
	s.entries = append(s.entries, entry)
}

// RemoveChild removes component from the container and from entries.
func (s *Stack) RemoveChild(component Component) {
	s.Container.RemoveChild(component)
	for i, entry := range s.entries {
		if entry.Component == component {
			s.entries = append(s.entries[:i], s.entries[i+1:]...)
			return
		}
	}
}

// Clear removes every child and every entry.
func (s *Stack) Clear() {
	s.Container.Clear()
	s.entries = []StackLayoutEntry{}
}

// LayoutNode returns the stack's layout description.
func (s *Stack) LayoutNode() LayoutNode {
	return &StackLayoutNode{
		Type:    s.layoutType,
		Entries: s.entries,
		Gap:     s.gap,
		Align:   s.align,
	}
}

// VisibleStackEntries keeps entries whose visible predicate passes.
// A nil predicate keeps the entry.
func VisibleStackEntries(entries []StackLayoutEntry, viewport LayoutViewport) []StackLayoutEntry {
	out := make([]StackLayoutEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Visible == nil || entry.Visible(viewport) {
			out = append(out, entry)
		}
	}
	return out
}

// AllocateStackSizes returns a main-axis size for each entry.
// availableSize nil means return the clamped intrinsic sizes without grow or shrink.
func AllocateStackSizes(entries []StackLayoutEntry, intrinsicSizes []int, availableSize *int, gap int) []int {
	sizes := make([]int, len(entries))
	for i, entry := range entries {
		raw := 0
		switch b := entry.Basis.(type) {
		case nil, *StackLayoutBasisAuto:
			if i < len(intrinsicSizes) {
				raw = intrinsicSizes[i]
			}
		case *StackLayoutBasisCells:
			raw = b.Value
		}
		sizes[i] = cstClampSize(raw, entry)
	}
	if availableSize == nil {
		return sizes
	}

	totalGap := 0
	if len(entries) > 1 {
		totalGap = (len(entries) - 1) * gap
	}
	contentSize := max(0, *availableSize-totalGap)
	total := 0
	for _, size := range sizes {
		total += size
	}
	if total < contentSize {
		cstDistribute(sizes, entries, contentSize-total, cstDistributeModeGrow)
	} else if total > contentSize {
		cstDistribute(sizes, entries, total-contentSize, cstDistributeModeShrink)
	}
	return sizes
}

func cstIsStackEntry(child StackChild) bool {
	_, ok := child.(*StackChildEntry)
	return ok
}

// value nil means fallback. Non-finite numbers also use fallback.
func cstNormalizeSize(value *int, fallback int) int {
	if value == nil {
		return fallback
	}
	return max(0, *value)
}

func cstClampSize(size int, entry StackLayoutEntry) int {
	minSize := 0
	if entry.MinSize != nil {
		minSize = max(0, *entry.MinSize)
	}
	maxSize := cstMaxSafeInteger
	if entry.MaxSize != nil {
		maxSize = *entry.MaxSize
	}
	maxSize = max(minSize, maxSize)
	return max(minSize, min(maxSize, max(0, size)))
}

func cstDistribute(sizes []int, entries []StackLayoutEntry, amount int, mode cstDistributeMode) {
	remaining := amount
	type cstCandidate struct {
		entry StackLayoutEntry
		index int
	}
	for remaining > 0 {
		candidates := make([]cstCandidate, 0, len(entries))
		for i, entry := range entries {
			if mode == cstDistributeModeGrow {
				grow := 0
				if entry.Grow != nil {
					grow = *entry.Grow
				}
				maxSize := cstMaxSafeInteger
				if entry.MaxSize != nil {
					maxSize = *entry.MaxSize
				}
				if grow > 0 && sizes[i] < maxSize {
					candidates = append(candidates, cstCandidate{entry: entry, index: i})
				}
			} else {
				shrink := 1
				if entry.Shrink != nil {
					shrink = *entry.Shrink
				}
				minSize := 0
				if entry.MinSize != nil {
					minSize = *entry.MinSize
				}
				if shrink > 0 && sizes[i] > minSize {
					candidates = append(candidates, cstCandidate{entry: entry, index: i})
				}
			}
		}
		if len(candidates) == 0 {
			return
		}

		totalWeight := 0
		for _, c := range candidates {
			if mode == cstDistributeModeGrow {
				totalWeight += *c.entry.Grow
			} else {
				shrink := 1
				if c.entry.Shrink != nil {
					shrink = *c.entry.Shrink
				}
				totalWeight += shrink * max(1, sizes[c.index])
			}
		}

		distributed := 0
		for _, c := range candidates {
			if remaining <= 0 {
				break
			}
			var weight int
			var capacity int
			if mode == cstDistributeModeGrow {
				weight = *c.entry.Grow
				maxSize := cstMaxSafeInteger
				if c.entry.MaxSize != nil {
					maxSize = *c.entry.MaxSize
				}
				capacity = maxSize - sizes[c.index]
			} else {
				shrink := 1
				if c.entry.Shrink != nil {
					shrink = *c.entry.Shrink
				}
				weight = shrink * max(1, sizes[c.index])
				minSize := 0
				if c.entry.MinSize != nil {
					minSize = *c.entry.MinSize
				}
				capacity = sizes[c.index] - minSize
			}
			proposed := max(1, (remaining*weight)/totalWeight)
			delta := min(remaining, min(proposed, capacity))
			if delta <= 0 {
				continue
			}
			if mode == cstDistributeModeGrow {
				sizes[c.index] += delta
			} else {
				sizes[c.index] -= delta
			}
			remaining -= delta
			distributed += delta
		}
		if distributed == 0 {
			return
		}
	}
}

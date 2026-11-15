// Ported from packages/tui/src/components/spacer.ts (pi v1.0.0).

package tui

// Spacer renders empty lines. It implements Component.
// NewSpacer must treat the TypeScript default as 1 line. Zero is not that default.
type Spacer struct {
	lines int
}

// NewSpacer returns a spacer of lines empty rows.
func NewSpacer(lines int) *Spacer {
	return &Spacer{lines: lines}
}

// SetLines sets how many empty rows Render returns.
func (s *Spacer) SetLines(lines int) {
	s.lines = lines
}

// Invalidate is a no-op. Spacer has no cached render state.
func (s *Spacer) Invalidate() {}

// Render returns lines empty strings. width is ignored.
func (s *Spacer) Render(width int) []string {
	if s.lines <= 0 {
		return []string{}
	}
	return make([]string, s.lines)
}

var _ Component = (*Spacer)(nil)

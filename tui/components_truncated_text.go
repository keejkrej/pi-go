// Ported from packages/tui/src/components/truncated-text.ts (pi v1.0.0).

package tui

// TruncatedText shows one line, truncated to the viewport width. It implements Component.
// The TypeScript padding defaults are 0 and 0, which are the zero values.
type TruncatedText struct {
	text     string
	paddingX int
	paddingY int
}

// NewTruncatedText returns a single-line truncated text component.
func NewTruncatedText(text string, paddingX int, paddingY int) *TruncatedText {
	panic("unported: NewTruncatedText")
}

// Invalidate is a no-op. TruncatedText has no cached render state.
func (t *TruncatedText) Invalidate() {}

// Render returns the first source line, truncated, with padding above and below.
func (t *TruncatedText) Render(width int) []string {
	panic("unported: TruncatedText.Render")
}

var _ Component = (*TruncatedText)(nil)

// Ported from packages/tui/src/components/truncated-text.ts (pi v1.0.0).

package tui

import "strings"

// TruncatedText shows one line, truncated to the viewport width. It implements Component.
// The TypeScript padding defaults are 0 and 0, which are the zero values.
type TruncatedText struct {
	text     string
	paddingX int
	paddingY int
}

// NewTruncatedText returns a single-line truncated text component.
func NewTruncatedText(text string, paddingX int, paddingY int) *TruncatedText {
	return &TruncatedText{
		text:     text,
		paddingX: paddingX,
		paddingY: paddingY,
	}
}

// Invalidate is a no-op. TruncatedText has no cached render state.
func (t *TruncatedText) Invalidate() {}

// Render returns the first source line, truncated, with padding above and below.
func (t *TruncatedText) Render(width int) []string {
	result := make([]string, 0, max(0, t.paddingY)*2+1)

	emptyLine := strings.Repeat(" ", max(0, width))

	for i := 0; i < t.paddingY; i++ {
		result = append(result, emptyLine)
	}

	availableWidth := max(1, width-t.paddingX*2)

	singleLineText := t.text
	if newlineIndex := strings.IndexByte(t.text, '\n'); newlineIndex != -1 {
		singleLineText = t.text[:newlineIndex]
	}

	displayText := TruncateToWidth(singleLineText, availableWidth, "...", false)

	pad := strings.Repeat(" ", max(0, t.paddingX))
	lineWithPadding := pad + displayText + pad

	lineVisibleWidth := VisibleWidth(lineWithPadding)
	paddingNeeded := max(0, width-lineVisibleWidth)
	finalLine := lineWithPadding + strings.Repeat(" ", paddingNeeded)

	result = append(result, finalLine)

	for i := 0; i < t.paddingY; i++ {
		result = append(result, emptyLine)
	}

	return result
}

var _ Component = (*TruncatedText)(nil)

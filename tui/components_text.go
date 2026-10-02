// Ported from packages/tui/src/components/text.ts (pi v1.0.0).

package tui

// Text displays multi-line text with word wrapping. It implements Component.
// NewText must apply the TypeScript defaults text "", paddingX 1, and paddingY 1
// when the caller wants those defaults. Zero padding is not the default.
// Nil customBgFn means no background.
// cachedText nil means the line cache is cold. A cached empty source is a non-nil pointer to "".
// cachedWidth nil means the line cache is cold.
// cachedLines nil means uncached. An empty non-nil slice is a cached empty render.
type Text struct {
	text        string
	paddingX    int
	paddingY    int
	customBgFn  func(text string) string
	cachedText  *string
	cachedWidth *int
	cachedLines []string
}

// NewText returns a text component. customBgFn nil means no background.
func NewText(text string, paddingX int, paddingY int, customBgFn func(text string) string) *Text {
	panic("unported: NewText")
}

// SetText replaces the source and drops the line cache.
func (t *Text) SetText(text string) {
	panic("unported: Text.SetText")
}

// SetCustomBgFn replaces the background function and drops the line cache.
// Nil clears the background.
func (t *Text) SetCustomBgFn(customBgFn func(text string) string) {
	panic("unported: Text.SetCustomBgFn")
}

// Invalidate drops the line cache.
func (t *Text) Invalidate() {
	panic("unported: Text.Invalidate")
}

// Render word-wraps the text into width columns, with padding and an optional background.
func (t *Text) Render(width int) []string {
	panic("unported: Text.Render")
}

var _ Component = (*Text)(nil)

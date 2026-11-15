// Ported from packages/tui/src/components/text.ts (pi v1.0.0).

package tui

import (
	"strings"

	"github.com/keejkrej/pi-go/internal/js"
)

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
	return &Text{
		text:       text,
		paddingX:   paddingX,
		paddingY:   paddingY,
		customBgFn: customBgFn,
	}
}

// SetText replaces the source and drops the line cache when text changed.
func (t *Text) SetText(text string) {
	// Renderers often set the same text on every update; keep the wrapped lines then.
	if text == t.text {
		return
	}
	t.text = text
	t.Invalidate()
}

// SetCustomBgFn replaces the background function and drops the line cache.
// Nil clears the background.
func (t *Text) SetCustomBgFn(customBgFn func(text string) string) {
	t.customBgFn = customBgFn
	t.Invalidate()
}

// SetPaddingX replaces the horizontal padding and drops the line cache.
func (t *Text) SetPaddingX(paddingX int) {
	t.paddingX = paddingX
	t.Invalidate()
}

// Invalidate drops the line cache.
func (t *Text) Invalidate() {
	t.cachedText = nil
	t.cachedWidth = nil
	t.cachedLines = nil
}

// Render word-wraps the text into width columns, with padding and an optional background.
func (t *Text) Render(width int) []string {
	if t.cachedLines != nil && t.cachedText != nil && *t.cachedText == t.text && t.cachedWidth != nil && *t.cachedWidth == width {
		return t.cachedLines
	}

	if t.text == "" || js.Trim(t.text) == "" {
		result := []string{}
		cachedText := t.text
		cachedWidth := width
		t.cachedText = &cachedText
		t.cachedWidth = &cachedWidth
		t.cachedLines = result
		return result
	}

	normalizedText := strings.ReplaceAll(t.text, "\t", "   ")

	// Reduce margins when necessary so content and padding fit within the available width.
	maxMargin := max(0, (width-1)/2)
	if width <= 0 {
		maxMargin = 0
	}
	paddingX := max(0, min(t.paddingX, maxMargin))
	contentWidth := max(1, width-paddingX*2)

	wrappedLines := WrapTextWithAnsi(normalizedText, contentWidth)

	margin := strings.Repeat(" ", paddingX)
	contentLines := make([]string, 0, len(wrappedLines))

	for _, line := range wrappedLines {
		lineWithMargins := margin + line + margin
		if t.customBgFn != nil {
			contentLines = append(contentLines, ApplyBackgroundToLine(lineWithMargins, width, t.customBgFn))
		} else {
			visibleLen := VisibleWidth(lineWithMargins)
			paddingNeeded := max(0, width-visibleLen)
			contentLines = append(contentLines, lineWithMargins+strings.Repeat(" ", paddingNeeded))
		}
	}

	emptyLine := strings.Repeat(" ", max(0, width))
	padY := max(0, t.paddingY)
	emptyLines := make([]string, 0, padY)
	for i := 0; i < padY; i++ {
		if t.customBgFn != nil {
			emptyLines = append(emptyLines, ApplyBackgroundToLine(emptyLine, width, t.customBgFn))
		} else {
			emptyLines = append(emptyLines, emptyLine)
		}
	}

	result := make([]string, 0, len(emptyLines)*2+len(contentLines))
	result = append(result, emptyLines...)
	result = append(result, contentLines...)
	result = append(result, emptyLines...)
	FlattenLines(result)

	cachedText := t.text
	cachedWidth := width
	t.cachedText = &cachedText
	t.cachedWidth = &cachedWidth
	t.cachedLines = result

	if len(result) > 0 {
		return result
	}
	return []string{""}
}

var _ Component = (*Text)(nil)

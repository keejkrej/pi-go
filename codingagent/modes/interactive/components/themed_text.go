// Ported from packages/coding-agent/src/modes/interactive/components/themed-text.ts (pi v1.5.2).

package components

import (
	"sync"

	"github.com/keejkrej/pi-go/tui"
)

// ThemedText displays text whose content rebuilds via build after every Invalidate call so
// theme changes or terminal color updates never leave stale ANSI colors.
type ThemedText struct {
	*tui.Text
	mu    sync.Mutex
	build func() string
	stale bool
}

var _ tui.Component = (*ThemedText)(nil)

// NewThemedText creates a ThemedText with optional paddingX and paddingY (each defaults to 1).
func NewThemedText(build func() string, padding ...int) *ThemedText {
	paddingX := 1
	paddingY := 1
	if len(padding) > 0 {
		paddingX = padding[0]
	}
	if len(padding) > 1 {
		paddingY = padding[1]
	}
	return &ThemedText{
		Text:  tui.NewText("", paddingX, paddingY, nil),
		build: build,
		stale: true,
	}
}

// Invalidate clears cached layout and marks the text to rebuild on the next Render.
func (t *ThemedText) Invalidate() {
	t.Text.Invalidate()
	t.mu.Lock()
	t.stale = true
	t.mu.Unlock()
}

// Render rebuilds the underlying text if stale and returns wrapped ANSI lines.
func (t *ThemedText) Render(width int) []string {
	t.mu.Lock()
	shouldBuild := t.stale
	if shouldBuild {
		t.stale = false
	}
	build := t.build
	t.mu.Unlock()

	if shouldBuild && build != nil {
		t.Text.SetText(build())
	}
	return t.Text.Render(width)
}

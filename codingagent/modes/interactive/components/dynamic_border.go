// Ported from packages/coding-agent/src/modes/interactive/components/dynamic-border.ts (pi v1.5.2).

package components

import (
	"strings"

	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/tui"
)

// DynamicBorder renders a horizontal rule sized to the current viewport width.
type DynamicBorder struct {
	color func(str string) string
}

var _ tui.Component = (*DynamicBorder)(nil)

// NewDynamicBorder creates a DynamicBorder. When color is omitted or nil, it uses the active border theme color.
func NewDynamicBorder(color ...func(str string) string) *DynamicBorder {
	var colorFn func(str string) string
	if len(color) > 0 {
		colorFn = color[0]
	}
	if colorFn == nil {
		colorFn = func(str string) string {
			return theme.CurrentTheme().Fg(theme.FgBorder, str)
		}
	}
	return &DynamicBorder{color: colorFn}
}

// Invalidate satisfies tui.Component (no cached state).
func (b *DynamicBorder) Invalidate() {}

// Render returns a single styled border line spanning at least 1 column.
func (b *DynamicBorder) Render(width int) []string {
	line := strings.Repeat("─", max(1, width))
	return []string{b.color(line)}
}

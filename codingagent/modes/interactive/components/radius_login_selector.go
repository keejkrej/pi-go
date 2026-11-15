// Ported from packages/coding-agent/src/modes/interactive/components/radius-login-selector.ts (pi v1.5.2).

package components

import (
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/tui"
)

// RadiusOption describes the Radius entry in the /login menu: Label is the full row text starting with Text.
type RadiusOption struct {
	Label string
	Text  string
}

// CreateLoginMenuSelector builds the top-level /login selector whose Radius row shimmers while selected.
func CreateLoginMenuSelector(
	ui tui.TUI,
	title string,
	options []string,
	radiusOpt RadiusOption,
	onSelect func(option string),
	onCancel func(),
) *ExtensionSelectorComponent {
	animationStart := time.Now()
	var animating atomic.Bool
	var stopTimer func()

	stopAnimation := func() {
		animating.Store(false)
		if stopTimer != nil {
			stopTimer()
		}
	}

	sel := NewExtensionSelectorComponent(
		title,
		options,
		func(option string) {
			stopAnimation()
			if onSelect != nil {
				onSelect(option)
			}
		},
		func() {
			stopAnimation()
			if onCancel != nil {
				onCancel()
			}
		},
	)

	if ui != nil {
		stopTimer = StartBrandAnimation(ui, func() bool {
			return animating.Load()
		})
	}

	sel.renderOverride = func(width int, base func(int) []string) []string {
		lines := base(width)
		th := theme.CurrentTheme()
		selectedLines := tui.NewText(th.Fg(theme.FgAccent, "→ ")+th.Fg(theme.FgAccent, radiusOpt.Label), 1, 0, nil).Render(width)
		idx := -1
		if len(selectedLines) > 0 {
			idx = slices.Index(lines, selectedLines[0])
		}
		animating.Store(idx >= 0)
		if idx >= 0 {
			elapsedMs := float64(time.Since(animationStart).Nanoseconds()) / 1e6
			shimmer := RadiusShimmer(radiusOpt.Text, elapsedMs)
			suffix := strings.TrimPrefix(radiusOpt.Label, radiusOpt.Text)
			if len(suffix) == len(radiusOpt.Label) && len(radiusOpt.Text) < len(radiusOpt.Label) {
				suffix = radiusOpt.Label[len(radiusOpt.Text):]
			}
			animatedLine := th.Fg(theme.FgAccent, "→ ") + shimmer + suffix
			rendered := tui.NewText(animatedLine, 1, 0, nil).Render(width)
			if len(rendered) > 0 {
				lines[idx] = rendered[0]
			} else {
				lines[idx] = ""
			}
		}
		return lines
	}
	sel.disposeOverride = stopAnimation
	return sel
}

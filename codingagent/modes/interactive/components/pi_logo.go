// Ported from packages/coding-agent/src/modes/interactive/components/pi-logo.ts (pi v1.5.2).

package components

import (
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/tui"
)

const plReset = "\x1b[0m"

func plBrandColors() (*tui.RgbColorValue, *tui.RgbColorValue, *tui.RgbColorValue) {
	coral, _ := tui.NewRgbColor(228, 138, 122)
	blue, _ := tui.NewRgbColor(79, 142, 179)
	yellow, _ := tui.NewRgbColor(234, 182, 93)
	return coral, blue, yellow
}

// PiLogoLines renders the 4-cell wide, 2-line tall pi half-block logo using fixed brand colors
// adapted to the active terminal color mode.
func PiLogoLines() []string {
	coral, blue, yellow := plBrandColors()
	mode := theme.CurrentTheme().GetColorMode()
	fg := func(c *tui.RgbColorValue) string { return tui.ForegroundAnsi(c, mode) }
	// The fourth cell of the top line is empty, so it is padded to the same width as the bottom line.
	top := fg(coral) + tui.BackgroundAnsi(blue, mode) + "▀" + plReset + fg(coral) + "▀█" + plReset + " "
	bottom := fg(blue) + "█▀" + plReset + " " + fg(yellow) + "█" + plReset
	return []string{top, bottom}
}

// SupportsPiLogo reports whether the current terminal reliably aligns half-block logo glyphs.
func SupportsPiLogo() bool {
	return !tui.IsAppleTerminalSession()
}

// PiWordmark returns the fallback "Pi" text logo colored in coral and yellow.
func PiWordmark() string {
	coral, _, yellow := plBrandColors()
	mode := theme.CurrentTheme().GetColorMode()
	return tui.ForegroundAnsi(coral, mode) + "P" + plReset + tui.ForegroundAnsi(yellow, mode) + "i" + plReset
}

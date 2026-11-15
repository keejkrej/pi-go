// Ported from packages/coding-agent/src/modes/interactive/components/easter-egg-3d.lazy.ts (pi v1.5.2).

package components

import "github.com/keejkrej/pi-go/tui"

// a3lScreenProvider matches TUI implementations (such as *tui.TuiAltScreen) that expose captured screen lines.
type a3lScreenProvider interface {
	tui.TUI
	GetScreenLines() []string
}

// PlayPiLogo3d plays the 3D pi logo easter egg, lifting off the header logo whose top-left cell is at (column, row).
// Returns false when the TUI cannot play fullscreen 3D easter eggs.
func PlayPiLogo3d(ui tui.TUI, column, row int) bool {
	return PlayEasterEgg3d(ui, EasterEgg3dOptions{
		Kind:   EasterEggKindPiLogo,
		Column: column,
		Row:    row,
	})
}

// PlayArmin3d plays the 3D Armin easter egg. Returns false when it cannot play so callers can fall back inline.
func PlayArmin3d(ui tui.TUI) bool {
	return PlayEasterEgg3d(ui, EasterEgg3dOptions{
		Kind: EasterEggKindArmin,
	})
}

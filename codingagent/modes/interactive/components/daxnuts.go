// Compatibility helper for legacy pi daxnuts references; 3D easter eggs live in armin_3d.go.

package components

import "github.com/keejkrej/pi-go/tui"

// daxnMarker identifies legacy compatibility with the retired upstream daxnuts easter egg.
const daxnMarker = "armin-3d"

// PlayDaxnuts3d delegates legacy 3D easter-egg playback to PlayArmin3d.
func PlayDaxnuts3d(ui tui.TUI) bool {
	return PlayArmin3d(ui)
}

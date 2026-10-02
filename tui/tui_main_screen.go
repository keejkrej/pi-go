// Ported from packages/tui/src/tui-main-screen.ts (pi v1.0.0).

package tui

import "github.com/keejkrej/pi-go/internal/omap"

const (
	tmsKittySequencePrefix  = "\x1b_G"
	tmsMaxRenderWriteChars  = 1024 * 1024
)

// TuiMainScreenRenderState is a snapshot of the main-screen differential renderer.
// Field order matches captureRenderState.
type TuiMainScreenRenderState struct {
	PreviousLines       []string `json:"previousLines"`
	PreviousWidth       int      `json:"previousWidth"`
	PreviousHeight      int      `json:"previousHeight"`
	CursorRow           int      `json:"cursorRow"`
	HardwareCursorRow   int      `json:"hardwareCursorRow"`
	MaxLinesRendered    int      `json:"maxLinesRendered"`
	PreviousViewportTop int      `json:"previousViewportTop"`
}

// TuiMainScreen renders into the terminal's main screen and scrollback.
type TuiMainScreen struct {
	TuiBase
	previousLines         []string
	previousKittyImageIds *omap.Set[int]
	previousWidth         int
	previousHeight        int
	cursorRow             int
	hardwareCursorRow     int
	maxLinesRendered      int
	previousViewportTop   int
}

// NewTuiMainScreen constructs a regular-mode TUI.
// showHardwareCursor nil leaves the hardware cursor hidden.
// logDirectory nil disables debug logging and sends crash dumps to the OS temp directory.
func NewTuiMainScreen(terminal Terminal, showHardwareCursor *bool, logDirectory *string) *TuiMainScreen {
	panic("unported: NewTuiMainScreen")
}

// CaptureRenderState copies the differential-render cursors and previous frame.
func (s *TuiMainScreen) CaptureRenderState() *TuiMainScreenRenderState {
	panic("unported: TuiMainScreen.CaptureRenderState")
}

// RestoreRenderState installs a previously captured frame. Image lines are blanked.
func (s *TuiMainScreen) RestoreRenderState(state *TuiMainScreenRenderState) {
	panic("unported: TuiMainScreen.RestoreRenderState")
}

func (s *TuiMainScreen) resetRenderState() {
	panic("unported: TuiMainScreen.resetRenderState")
}

func (s *TuiMainScreen) beforeTerminalStop(options *TuiStopOptions) {
	panic("unported: TuiMainScreen.beforeTerminalStop")
}

func (s *TuiMainScreen) doRender() {
	panic("unported: TuiMainScreen.doRender")
}

var (
	_ TUI         = (*TuiMainScreen)(nil)
	_ tuiOverride = (*TuiMainScreen)(nil)
)
